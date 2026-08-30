package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/model"
)

func TestReconciliationTransitionsAndRepositoryUpdate(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	create := createRequest(database, "repo", "Repo", "recover-create", "create-token", testTime)
	creating, err := database.store.ReserveCreate(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	wrongToken := "wrong"
	branch := "refs/heads/recover-create"
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     creating.ID,
		FromState:      model.WorktreeStateCreating,
		State:          model.WorktreeStateActive,
		OperationToken: &wrongToken,
		Branch:         &branch,
	}); !errors.Is(err, ErrOperationToken) {
		t.Errorf("wrong recovery token error = %v, want ErrOperationToken", err)
	}
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     creating.ID,
		FromState:      model.WorktreeStateCreating,
		State:          model.WorktreeStateActive,
		OperationToken: &create.OperationToken,
		Branch:         &branch,
		Locked:         true,
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.store.Get(ctx, creating.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.WorktreeStateActive || recovered.OperationToken != nil || !recovered.Locked {
		t.Errorf("recovered create = %#v", recovered)
	}

	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID: creating.ID,
		FromState:  model.WorktreeStateActive,
		State:      model.WorktreeStateMissing,
	}); err != nil {
		t.Fatal(err)
	}
	missing, err := database.store.Get(ctx, creating.ID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.State != model.WorktreeStateMissing || missing.Branch != nil || missing.Locked {
		t.Errorf("missing worktree = %#v", missing)
	}
	detached := "abcdef"
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     creating.ID,
		FromState:      model.WorktreeStateMissing,
		State:          model.WorktreeStateActive,
		DetachedCommit: &detached,
	}); err != nil {
		t.Fatal(err)
	}

	failedRequest := createRequest(database, "repo", "Repo", "recover-failed", "failed-token", testTime.Add(time.Minute))
	failed, err := database.store.ReserveCreate(ctx, failedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     failed.ID,
		FromState:      model.WorktreeStateCreating,
		State:          model.WorktreeStateCreateFailed,
		OperationToken: &failedRequest.OperationToken,
	}); err != nil {
		t.Fatal(err)
	}

	manualRequest := createRequest(database, "repo", "Repo", "recover-manual", "manual-token", testTime.Add(2*time.Minute))
	manual, err := database.store.ReserveCreate(ctx, manualRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     manual.ID,
		FromState:      model.WorktreeStateCreating,
		State:          model.WorktreeStateManualReview,
		OperationToken: &manualRequest.OperationToken,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID: manual.ID,
		FromState:  model.WorktreeStateManualReview,
		State:      model.WorktreeStateActive,
	}); err == nil {
		t.Error("UpdateReconciled() changed a manual-review record")
	}

	removeTarget := reserveActive(t, database, "repo", "Repo", "recover-remove", "remove-create", testTime.Add(3*time.Minute))
	removeRequest := RemoveReservation{
		WorktreeID:         removeTarget.ID,
		OperationToken:     "remove-token",
		OperationStartedAt: testTime.Add(4 * time.Minute),
		ObservedActivityAt: removeTarget.LastGroveActivityAt,
		CutoffAt:           removeTarget.LastGroveActivityAt,
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       model.GitDirectoryIdentity{Path: "/repo/.git/worktrees/recover-remove", Token: "recover-remove-token"},
	}
	reservedRemoval, err := database.store.ReserveRemoval(ctx, removeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reservedRemoval.RemovalGitDirectory == nil ||
		*reservedRemoval.RemovalGitDirectory != removeRequest.GitDirectory {
		t.Errorf("reserved removal identity = %#v", reservedRemoval.RemovalGitDirectory)
	}
	removedAt := testTime.Add(5 * time.Minute)
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID:     removeTarget.ID,
		FromState:      model.WorktreeStateRemoving,
		State:          model.WorktreeStateRemoved,
		OperationToken: &removeRequest.OperationToken,
		RemovedAt:      &removedAt,
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := database.store.Get(ctx, removeTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.State != model.WorktreeStateRemoved || removed.RemovalReason == nil ||
		removed.RemovalGitDirectory != nil || removed.OperationToken != nil {
		t.Errorf("recovered removal = %#v", removed)
	}

	repositoryUpdateAt := testTime.Add(6 * time.Minute)
	newMain := filepath.Join(filepath.Dir(database.root), "refreshed main")
	if err := database.store.UpdateRepository(ctx, creating.RepositoryID, RepositoryUpdate{
		MainCheckout: newMain,
		SeenAt:       repositoryUpdateAt,
	}); err != nil {
		t.Fatal(err)
	}
	repositories, err := database.store.Repositories(ctx, RepositoryFilter{ID: &creating.RepositoryID})
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].MainCheckout != newMain || !repositories[0].LastSeenAt.Equal(repositoryUpdateAt) {
		t.Errorf("updated repository = %#v", repositories)
	}
}

func TestBootstrapTransitions(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	active := reserveActive(t, database, "repo", "Repo", "bootstrap", "create", testTime)
	script := filepath.Join(active.Path, "bootstrap-worktree.sh")
	startedAt := testTime.Add(time.Minute)

	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: active.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateRunning,
		Script:     &script,
		Source:     model.SourceBuiltIn,
		StartedAt:  &startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	running, err := database.store.Get(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.BootstrapState != model.BootstrapStateRunning || running.BootstrapStartedAt == nil || !running.BootstrapStartedAt.Equal(startedAt) {
		t.Errorf("running bootstrap = %#v", running)
	}

	finishedAt := startedAt.Add(time.Minute)
	exitCode := 0
	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: active.ID,
		FromState:  model.BootstrapStateRunning,
		State:      model.BootstrapStateSucceeded,
		Script:     &script,
		Source:     model.SourceBuiltIn,
		ExitCode:   &exitCode,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatal(err)
	}
	succeeded, err := database.store.Get(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.BootstrapState != model.BootstrapStateSucceeded || succeeded.BootstrapExitCode == nil || *succeeded.BootstrapExitCode != 0 {
		t.Errorf("successful bootstrap = %#v", succeeded)
	}
	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: active.ID,
		FromState:  model.BootstrapStateSucceeded,
		State:      model.BootstrapStateFailed,
		Source:     model.SourceBuiltIn,
		FinishedAt: &finishedAt,
	}); err == nil {
		t.Error("UpdateBootstrap() changed a terminal state")
	}

	interrupted := reserveActive(t, database, "repo", "Repo", "interrupted", "create-interrupted", testTime)
	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: interrupted.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateInterrupted,
		Source:     model.SourceBuiltIn,
		FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatal(err)
	}

	disabled := reserveActive(t, database, "repo", "Repo", "disabled", "create-disabled", testTime)
	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: disabled.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateDisabled,
		Source:     model.SourceDisabled,
	}); err != nil {
		t.Fatal(err)
	}

	missing := reserveActive(t, database, "repo", "Repo", "not-present", "create-not-present", testTime)
	if err := database.store.UpdateBootstrap(ctx, BootstrapUpdate{
		WorktreeID: missing.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateNotPresent,
		Script:     &script,
		Source:     model.SourceBuiltIn,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestListFiltersSizeAndStats(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	first := reserveActive(t, database, "repo-a", "Alpha", "complete", "create-complete", testTime)
	second := reserveActive(t, database, "repo-a", "Alpha", "partial", "create-partial", testTime.Add(time.Minute))
	third := reserveActive(t, database, "repo-b", "Beta", "unknown", "create-unknown", testTime.Add(2*time.Minute))

	firstMeasured := testTime.Add(10 * time.Minute)
	secondMeasured := firstMeasured.Add(time.Minute)
	if err := database.store.UpdateSize(ctx, SizeUpdate{WorktreeID: first.ID, Bytes: 100, Complete: true, MeasuredAt: firstMeasured}); err != nil {
		t.Fatal(err)
	}
	if err := database.store.UpdateSize(ctx, SizeUpdate{WorktreeID: second.ID, Bytes: 40, Complete: false, MeasuredAt: secondMeasured}); err != nil {
		t.Fatal(err)
	}

	missing := reserveActive(t, database, "repo-a", "Alpha", "missing", "create-missing", testTime.Add(3*time.Minute))
	if err := database.store.UpdateReconciled(ctx, ReconcileUpdate{
		WorktreeID: missing.ID,
		FromState:  model.WorktreeStateActive,
		State:      model.WorktreeStateMissing,
	}); err != nil {
		t.Fatal(err)
	}
	manualRequest := createRequest(database, "repo-b", "Beta", "manual", "create-manual", testTime.Add(4*time.Minute))
	manual, err := database.store.ReserveCreate(ctx, manualRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.store.FailCreate(ctx, manual.ID, manualRequest.OperationToken, model.WorktreeStateManualReview); err != nil {
		t.Fatal(err)
	}
	failedRequest := createRequest(database, "repo-b", "Beta", "failed", "create-failed", testTime.Add(5*time.Minute))
	failed, err := database.store.ReserveCreate(ctx, failedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.store.FailCreate(ctx, failed.ID, failedRequest.OperationToken, model.WorktreeStateCreateFailed); err != nil {
		t.Fatal(err)
	}
	removed := reserveActive(t, database, "repo-b", "Beta", "removed", "create-removed", testTime.Add(6*time.Minute))
	removeRequest := RemoveReservation{
		WorktreeID:         removed.ID,
		OperationToken:     "remove",
		OperationStartedAt: testTime.Add(7 * time.Minute),
		ObservedActivityAt: removed.LastGroveActivityAt,
		CutoffAt:           removed.LastGroveActivityAt,
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       model.GitDirectoryIdentity{Path: "/repo/.git/worktrees/removed", Token: "removed-token"},
	}
	if _, err := database.store.ReserveRemoval(ctx, removeRequest); err != nil {
		t.Fatal(err)
	}
	if err := database.store.CompleteRemoval(ctx, removed.ID, removeRequest.OperationToken, RemovalResult{
		RemovedAt: testTime.Add(8 * time.Minute),
		Reason:    model.RemovalReasonOldAndClean,
	}); err != nil {
		t.Fatal(err)
	}

	activeState := model.WorktreeStateActive
	active, err := database.store.List(ctx, Filter{States: []model.WorktreeState{activeState}})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Errorf("active list count = %d, want 3", len(active))
	}
	commonDir := createRequest(database, "repo-a", "Alpha", "unused", "unused", testTime).Repository.CommonDir
	alpha, err := database.store.List(ctx, Filter{RepositoryCommonDir: &commonDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 3 {
		t.Errorf("Alpha list count = %d, want 3", len(alpha))
	}
	path := third.Path
	byPath, err := database.store.List(ctx, Filter{Path: &path})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPath) != 1 || byPath[0].ID != third.ID {
		t.Errorf("path filter result = %#v", byPath)
	}
	name := "partial"
	byName, err := database.store.List(ctx, Filter{RepositoryID: &second.RepositoryID, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) != 1 || byName[0].ID != second.ID {
		t.Errorf("name filter result = %#v", byName)
	}

	stats, err := database.store.Stats(ctx, StatsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Active != 3 || stats.Missing != 1 || stats.ManualReview != 1 {
		t.Errorf("stats state counts = active:%d missing:%d manual:%d", stats.Active, stats.Missing, stats.ManualReview)
	}
	if stats.Removed != nil || stats.CreateFailed != nil {
		t.Errorf("default final counts = %v, %v", stats.Removed, stats.CreateFailed)
	}
	if stats.RepositoryCount != 2 || stats.SizeBytes != 140 || stats.UnknownSizeCount != 1 || stats.IncompleteSizeCount != 1 || stats.SizeComplete {
		t.Errorf("stats totals = %#v", stats)
	}
	if stats.OldestMeasurementAt == nil || !stats.OldestMeasurementAt.Equal(firstMeasured) ||
		stats.NewestMeasurementAt == nil || !stats.NewestMeasurementAt.Equal(secondMeasured) {
		t.Errorf("stats measurement range = %v through %v", stats.OldestMeasurementAt, stats.NewestMeasurementAt)
	}

	allStats, err := database.store.Stats(ctx, StatsRequest{IncludeFinal: true})
	if err != nil {
		t.Fatal(err)
	}
	if allStats.Removed == nil || *allStats.Removed != 1 || allStats.CreateFailed == nil || *allStats.CreateFailed != 1 {
		t.Errorf("all final counts = %v, %v", allStats.Removed, allStats.CreateFailed)
	}
	alphaStats, err := database.store.Stats(ctx, StatsRequest{RepositoryID: &first.RepositoryID, IncludeFinal: true})
	if err != nil {
		t.Fatal(err)
	}
	if alphaStats.Active != 2 || alphaStats.Missing != 1 || alphaStats.RepositoryCount != 1 || alphaStats.SizeBytes != 140 {
		t.Errorf("Alpha stats = %#v", alphaStats)
	}

	if err := database.store.UpdateSize(ctx, SizeUpdate{WorktreeID: missing.ID, Bytes: 1, Complete: true, MeasuredAt: secondMeasured}); !errors.Is(err, ErrNotActive) {
		t.Errorf("missing size update error = %v, want ErrNotActive", err)
	}
}

func TestConcurrentTouchAndRemovalReservation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	for index := range 30 {
		active := reserveActive(
			t,
			database,
			"repo",
			"Repo",
			fmt.Sprintf("race-%02d", index),
			fmt.Sprintf("create-%02d", index),
			testTime,
		)
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var touchErr error
		var removeErr error
		removeToken := fmt.Sprintf("remove-%02d", index)
		go func() {
			defer wait.Done()
			<-start
			_, _, touchErr = database.store.Touch(ctx, active.ID, testTime.Add(time.Hour))
		}()
		go func() {
			defer wait.Done()
			<-start
			_, removeErr = database.store.ReserveRemoval(ctx, RemoveReservation{
				WorktreeID:         active.ID,
				OperationToken:     removeToken,
				OperationStartedAt: testTime.Add(time.Minute),
				ObservedActivityAt: active.LastGroveActivityAt,
				CutoffAt:           active.LastGroveActivityAt,
				Reason:             model.RemovalReasonOldAndClean,
				GitDirectory:       model.GitDirectoryIdentity{Path: "/repo/.git/worktrees/race", Token: "race-token"},
			})
		}()
		close(start)
		wait.Wait()

		switch {
		case touchErr == nil && errors.Is(removeErr, ErrStateChanged):
		case removeErr == nil && errors.Is(touchErr, ErrNotActive):
			if err := database.store.CancelRemoval(ctx, active.ID, removeToken); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("race %d errors = touch:%v remove:%v", index, touchErr, removeErr)
		}
	}
}

func TestConcurrentOpenRunsMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared", "grove.db")
	const count = 8
	start := make(chan struct{})
	stores := make(chan *Store, count)
	errorsChannel := make(chan error, count)
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store, err := Open(context.Background(), path)
			stores <- store
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(stores)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Errorf("Open() returned %v", err)
		}
	}
	var opened []*Store
	for store := range stores {
		if store != nil {
			opened = append(opened, store)
		}
	}
	defer func() {
		for _, store := range opened {
			store.Close()
		}
	}()
	if len(opened) == 0 {
		t.Fatal("no store opened")
	}
	var migrationCount int
	if err := opened[0].db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(migrations) {
		t.Errorf("migration count = %d, want %d", migrationCount, len(migrations))
	}
}
