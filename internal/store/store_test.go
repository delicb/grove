package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/model"
	pathutil "github.com/del-boy/grove/internal/paths"
	_ "modernc.org/sqlite"
)

var testTime = time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("test", 2*60*60))

type testDatabase struct {
	store *Store
	path  string
	root  string
}

func openTestDatabase(t *testing.T) testDatabase {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "data with spaces", "grove.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() returned %v", err)
		}
	})
	return testDatabase{
		store: store,
		path:  path,
		root:  filepath.Join(directory, "managed root"),
	}
}

func createRequest(database testDatabase, identity, displayName, name, token string, at time.Time) CreateReservation {
	commonDir := filepath.Join(filepath.Dir(database.root), "repositories", identity, ".git")
	return CreateReservation{
		Repository: model.RepositoryInfo{
			CommonDir:    commonDir,
			MainCheckout: filepath.Dir(commonDir),
			DisplayName:  displayName,
		},
		Name:               name,
		CreationRoot:       database.root,
		RequestedBranch:    name,
		ExpectedCommit:     "0123456789abcdef",
		CreatorAgent:       "pi:test-session",
		CreatedAt:          at,
		OperationToken:     token,
		OperationStartedAt: at.Add(time.Second),
		BootstrapSource:    model.SourceBuiltIn,
	}
}

func reserveActive(t *testing.T, database testDatabase, identity, displayName, name, token string, at time.Time) model.Worktree {
	t.Helper()
	worktree, err := database.store.ReserveCreate(
		context.Background(),
		createRequest(database, identity, displayName, name, token, at),
	)
	if err != nil {
		t.Fatal(err)
	}
	branch := "refs/heads/" + name
	if err := database.store.CompleteCreate(context.Background(), worktree.ID, token, model.GitWorktree{
		Path:   worktree.Path,
		HEAD:   "0123456789abcdef",
		Branch: &branch,
	}); err != nil {
		t.Fatal(err)
	}
	active, err := database.store.Get(context.Background(), worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	return active
}

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	var migrationCount int
	if err := database.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(migrations) {
		t.Fatalf("migration count = %d, want %d", migrationCount, len(migrations))
	}

	connections := make([]*sql.Conn, 0, 2)
	for range 2 {
		connection, err := database.store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		defer connection.Close()
	}
	for index, connection := range connections {
		var foreignKeys int
		var busyTimeout int
		var journalMode string
		if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 || journalMode != "wal" {
			t.Errorf("connection %d pragmas = foreign_keys:%d busy_timeout:%d journal_mode:%q", index, foreignKeys, busyTimeout, journalMode)
		}
	}

	var indexCount int
	if err := database.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name IN (
			'worktrees_live_path',
			'worktrees_live_name',
			'worktrees_operation_token',
			'worktrees_state',
			'worktrees_repository_state_activity'
		)`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 5 {
		t.Errorf("store index count = %d, want 5", indexCount)
	}

	_, err := database.store.db.ExecContext(ctx, `INSERT INTO worktrees (
		repository_id, name, creation_root, path, creator_agent, created_at,
		last_grove_activity_at, state, bootstrap_state, bootstrap_source
	) VALUES (999, 'test', '/root', '/root/test', 'human', 'time', 'time', 'active', 'pending', 'built-in')`)
	if !isConstraintError(err) {
		t.Fatalf("foreign key insert error = %v, want constraint error", err)
	}
}

func TestOpenRejectsNewerMigration(t *testing.T) {
	database := openTestDatabase(t)
	if _, err := database.store.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (99, ?)`, formatTime(testTime)); err != nil {
		t.Fatal(err)
	}
	if err := database.store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), database.path)
	if err == nil {
		t.Fatal("Open() accepted a newer schema")
	}
}

func TestCreateCompletionFailureAndFinalRecordReuse(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	request := createRequest(database, "one", "My API", "feature", "create-one", testTime)
	base := "main"
	script := "bootstrap-worktree.sh"
	request.RequestedBase = &base
	request.BootstrapScript = &script

	creating, err := database.store.ReserveCreate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if creating.State != model.WorktreeStateCreating || creating.BootstrapState != model.BootstrapStatePending {
		t.Fatalf("reservation states = %q, %q", creating.State, creating.BootstrapState)
	}
	if creating.OperationToken == nil || *creating.OperationToken != request.OperationToken {
		t.Errorf("reservation token = %v", creating.OperationToken)
	}
	if creating.RequestedBase == nil || *creating.RequestedBase != base {
		t.Errorf("requested base = %v", creating.RequestedBase)
	}
	if err := database.store.ConfirmOperation(ctx, creating.ID, model.WorktreeStateCreating, "wrong"); !errors.Is(err, ErrOperationToken) {
		t.Errorf("ConfirmOperation() error = %v, want ErrOperationToken", err)
	}
	if err := database.store.CompleteCreate(ctx, creating.ID, "wrong", model.GitWorktree{Path: creating.Path}); !errors.Is(err, ErrOperationToken) {
		t.Errorf("CompleteCreate() error = %v, want ErrOperationToken", err)
	}
	if err := database.store.CompleteCreate(ctx, creating.ID, request.OperationToken, model.GitWorktree{Path: creating.Path + "-wrong"}); !errors.Is(err, ErrStateChanged) {
		t.Errorf("CompleteCreate() path error = %v, want ErrStateChanged", err)
	}

	branch := "refs/heads/feature"
	if err := database.store.CompleteCreate(ctx, creating.ID, request.OperationToken, model.GitWorktree{
		Path:   creating.Path,
		HEAD:   "abc",
		Branch: &branch,
		Locked: true,
	}); err != nil {
		t.Fatal(err)
	}
	active, err := database.store.Get(ctx, creating.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.State != model.WorktreeStateActive || active.OperationToken != nil || !active.Locked {
		t.Errorf("completed worktree = %#v", active)
	}
	if err := database.store.FailCreate(ctx, creating.ID, request.OperationToken, model.WorktreeStateCreateFailed); !errors.Is(err, ErrStateChanged) {
		t.Errorf("FailCreate() after completion error = %v, want ErrStateChanged", err)
	}

	failedRequest := createRequest(database, "one", "My API", "failed", "create-failed", testTime.Add(time.Minute))
	failed, err := database.store.ReserveCreate(ctx, failedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.store.FailCreate(ctx, failed.ID, failedRequest.OperationToken, model.WorktreeStateCreateFailed); err != nil {
		t.Fatal(err)
	}
	failedRecord, err := database.store.Get(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedRecord.State != model.WorktreeStateCreateFailed || failedRecord.OperationToken != nil {
		t.Errorf("failed record = %#v", failedRecord)
	}

	reusedRequest := createRequest(database, "one", "My API", "failed", "create-reused", testTime.Add(2*time.Minute))
	reused, err := database.store.ReserveCreate(ctx, reusedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID == failed.ID || reused.Path != failed.Path {
		t.Errorf("reused reservation = id:%d path:%q, prior id:%d path:%q", reused.ID, reused.Path, failed.ID, failed.Path)
	}
	duplicateToken := createRequest(database, "one", "My API", "other", reusedRequest.OperationToken, testTime.Add(3*time.Minute))
	if _, err := database.store.ReserveCreate(ctx, duplicateToken); !errors.Is(err, ErrConflict) {
		t.Errorf("operation token conflict error = %v, want ErrConflict", err)
	}

	conflictRequest := createRequest(database, "one", "My API", "feature", "create-conflict", testTime.Add(4*time.Minute))
	if _, err := database.store.ReserveCreate(ctx, conflictRequest); !errors.Is(err, ErrConflict) {
		t.Errorf("live name conflict error = %v, want ErrConflict", err)
	}
}

func TestRepositoryKeyAllocationAndRefresh(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	first := reserveActive(t, database, "identity-a", "API", "one", "token-a", testTime)
	second := reserveActive(t, database, "identity-b", "API", "two", "token-b", testTime.Add(time.Minute))

	repositories, err := database.store.Repositories(ctx, RepositoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 2 {
		t.Fatalf("repository count = %d, want 2", len(repositories))
	}
	keys := []string{repositories[0].DirectoryKey, repositories[1].DirectoryKey}
	sort.Strings(keys)
	if keys[0] != "API" && keys[1] != "API" {
		t.Errorf("repository keys = %v, want one plain slug", keys)
	}
	if repositories[0].DirectoryKey == repositories[1].DirectoryKey {
		t.Errorf("repository keys are equal: %q", repositories[0].DirectoryKey)
	}
	secondCommonDir := createRequest(database, "identity-b", "API", "unused", "unused", testTime).Repository.CommonDir
	secondRepository, err := database.store.Repositories(ctx, RepositoryFilter{CommonDir: &secondCommonDir})
	if err != nil {
		t.Fatal(err)
	}
	expectedSecondKey := pathutil.RepositoryKeyCandidates("API", secondCommonDir)[1]
	if len(secondRepository) != 1 || secondRepository[0].DirectoryKey != expectedSecondKey {
		t.Errorf("second repository key = %#v, want %q", secondRepository, expectedSecondKey)
	}
	if first.Path == second.Path {
		t.Errorf("worktree paths are equal: %q", first.Path)
	}

	request := createRequest(database, "identity-a", "API Renamed", "three", "token-c", testTime.Add(2*time.Minute))
	request.Repository.MainCheckout = filepath.Join(filepath.Dir(database.root), "new main checkout")
	if _, err := database.store.ReserveCreate(ctx, request); err != nil {
		t.Fatal(err)
	}
	commonDir := request.Repository.CommonDir
	refreshed, err := database.store.Repositories(ctx, RepositoryFilter{CommonDir: &commonDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed) != 1 {
		t.Fatalf("refreshed repository count = %d, want 1", len(refreshed))
	}
	if refreshed[0].DisplayName != "API Renamed" || refreshed[0].MainCheckout != request.Repository.MainCheckout {
		t.Errorf("refreshed repository = %#v", refreshed[0])
	}
	if !refreshed[0].FirstSeenAt.Equal(testTime) || !refreshed[0].LastSeenAt.Equal(request.CreatedAt) {
		t.Errorf("repository times = %v, %v", refreshed[0].FirstSeenAt, refreshed[0].LastSeenAt)
	}
}

func TestRemovalReservationTouchCompletionAndReuse(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	active := reserveActive(t, database, "repo", "Repo", "old", "create", testTime)

	touchedAt := testTime.Add(time.Hour)
	touched, previous, err := database.store.Touch(ctx, active.ID, touchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !previous.Equal(testTime) || !touched.LastGroveActivityAt.Equal(touchedAt) {
		t.Errorf("touch times = previous:%v current:%v", previous, touched.LastGroveActivityAt)
	}
	stale := RemoveReservation{
		WorktreeID:         active.ID,
		OperationToken:     "remove-stale",
		OperationStartedAt: touchedAt.Add(time.Minute),
		ObservedActivityAt: previous,
		CutoffAt:           touchedAt,
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       model.GitDirectoryIdentity{Path: "/repo/.git/worktrees/old", Token: "old-token"},
	}
	if _, err := database.store.ReserveRemoval(ctx, stale); !errors.Is(err, ErrStateChanged) {
		t.Errorf("stale removal error = %v, want ErrStateChanged", err)
	}

	request := stale
	request.OperationToken = "remove-current"
	request.ObservedActivityAt = touchedAt
	removing, err := database.store.ReserveRemoval(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if removing.State != model.WorktreeStateRemoving || removing.RemovalReason == nil {
		t.Errorf("removal reservation = %#v", removing)
	}
	if _, _, err := database.store.Touch(ctx, active.ID, touchedAt.Add(time.Minute)); !errors.Is(err, ErrNotActive) {
		t.Errorf("Touch() while removing error = %v, want ErrNotActive", err)
	}
	if err := database.store.CancelRemoval(ctx, active.ID, "wrong"); !errors.Is(err, ErrOperationToken) {
		t.Errorf("CancelRemoval() error = %v, want ErrOperationToken", err)
	}
	if err := database.store.CancelRemoval(ctx, active.ID, request.OperationToken); err != nil {
		t.Fatal(err)
	}
	cancelled, err := database.store.Get(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != model.WorktreeStateActive || cancelled.RemovalReason != nil || cancelled.OperationToken != nil {
		t.Errorf("cancelled removal = %#v", cancelled)
	}

	request.OperationToken = "remove-final"
	request.OperationStartedAt = touchedAt.Add(2 * time.Minute)
	if _, err := database.store.ReserveRemoval(ctx, request); err != nil {
		t.Fatal(err)
	}
	size := int64(1234)
	measuredAt := touchedAt.Add(3 * time.Minute)
	removedAt := touchedAt.Add(4 * time.Minute)
	if err := database.store.CompleteRemoval(ctx, active.ID, request.OperationToken, RemovalResult{
		RemovedAt:      removedAt,
		Reason:         model.RemovalReasonOldAndClean,
		SizeBytes:      &size,
		SizeComplete:   true,
		SizeMeasuredAt: &measuredAt,
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := database.store.Get(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.State != model.WorktreeStateRemoved || removed.RemovedAt == nil || !removed.RemovedAt.Equal(removedAt) {
		t.Errorf("removed record = %#v", removed)
	}
	if removed.SizeBytes == nil || *removed.SizeBytes != size || !removed.SizeComplete {
		t.Errorf("removed size = %v complete:%t", removed.SizeBytes, removed.SizeComplete)
	}

	reused := createRequest(database, "repo", "Repo", "old", "create-after-remove", removedAt.Add(time.Minute))
	newRecord, err := database.store.ReserveCreate(ctx, reused)
	if err != nil {
		t.Fatal(err)
	}
	if newRecord.Path != removed.Path || newRecord.ID == removed.ID {
		t.Errorf("record after removal = %#v", newRecord)
	}
}

func TestSchemaRejectsInvalidStatesAndBooleans(t *testing.T) {
	database := openTestDatabase(t)
	active := reserveActive(t, database, "repo", "Repo", "one", "token", testTime)
	for name, statement := range map[string]string{
		"state":            `UPDATE worktrees SET state = 'invalid' WHERE id = ?`,
		"locked":           `UPDATE worktrees SET locked = 2 WHERE id = ?`,
		"bootstrap state":  `UPDATE worktrees SET bootstrap_state = 'invalid' WHERE id = ?`,
		"bootstrap source": `UPDATE worktrees SET bootstrap_source = 'invalid' WHERE id = ?`,
		"negative size":    `UPDATE worktrees SET size_bytes = -1, size_measured_at = 'time' WHERE id = ?`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.store.db.Exec(statement, active.ID); !isConstraintError(err) {
				t.Errorf("constraint error = %v", err)
			}
		})
	}
}

func TestConcurrentRepositoryAndWorktreeReservations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	const count = 12
	start := make(chan struct{})
	type result struct {
		worktree model.Worktree
		err      error
	}
	results := make(chan result, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request := createRequest(
				database,
				fmt.Sprintf("identity-%02d", index),
				"Shared Name",
				fmt.Sprintf("worktree-%02d", index),
				fmt.Sprintf("token-%02d", index),
				testTime.Add(time.Duration(index)*time.Nanosecond),
			)
			worktree, err := database.store.ReserveCreate(ctx, request)
			results <- result{worktree: worktree, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	paths := map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Errorf("ReserveCreate() returned %v", result.err)
			continue
		}
		if paths[result.worktree.Path] {
			t.Errorf("duplicate path %q", result.worktree.Path)
		}
		paths[result.worktree.Path] = true
	}
	if len(paths) != count {
		t.Fatalf("unique path count = %d, want %d", len(paths), count)
	}
	repositories, err := database.store.Repositories(ctx, RepositoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != count {
		t.Fatalf("repository count = %d, want %d", len(repositories), count)
	}
	keys := map[string]bool{}
	for _, repository := range repositories {
		if keys[repository.DirectoryKey] {
			t.Errorf("duplicate repository key %q", repository.DirectoryKey)
		}
		keys[repository.DirectoryKey] = true
	}
}

func TestConcurrentLiveNameReservationHasOneOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	const count = 10
	start := make(chan struct{})
	errorsChannel := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request := createRequest(database, "same-repository", "Repo", "same-name", fmt.Sprintf("token-%d", index), testTime)
			_, err := database.store.ReserveCreate(ctx, request)
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)

	successes := 0
	conflicts := 0
	for err := range errorsChannel {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Errorf("reservation error = %v", err)
		}
	}
	if successes != 1 || conflicts != count-1 {
		t.Errorf("successes = %d, conflicts = %d", successes, conflicts)
	}
}
