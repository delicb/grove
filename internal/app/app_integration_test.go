package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/config"
	gitadapter "github.com/del-boy/grove/internal/git"
	"github.com/del-boy/grove/internal/lock"
	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
	"github.com/del-boy/grove/internal/store"
	"github.com/del-boy/grove/internal/testutil"
)

var integrationTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

type competingGitClient struct {
	gitadapter.Client
	branch string
}

func (client *competingGitClient) AddWorktree(ctx context.Context, request gitadapter.AddRequest) error {
	competingRequest := request
	competingRequest.Branch = client.branch
	if err := client.Client.AddWorktree(ctx, competingRequest); err != nil {
		return err
	}
	return client.Client.AddWorktree(ctx, request)
}

type removeFailGitClient struct {
	gitadapter.Client
}

func (client *removeFailGitClient) RemoveWorktree(context.Context, string, string) error {
	return model.NewError(model.ErrorGit, model.ExitGit, "Git removal failed for the test.", nil)
}

type addFailGitClient struct {
	gitadapter.Client
}

func (client *addFailGitClient) AddWorktree(context.Context, gitadapter.AddRequest) error {
	return model.NewError(model.ErrorGit, model.ExitGit, "Git creation failed for the test.", nil)
}

type cleanupFaultGitClient struct {
	gitadapter.Client
	operation string
}

type capturedLockManager struct {
	lock.Manager
	operation lock.Lock
}

func (manager *capturedLockManager) AcquireOperation(token string) (lock.Lock, error) {
	operation, err := manager.Manager.AcquireOperation(token)
	manager.operation = operation
	return operation, err
}

func (client *cleanupFaultGitClient) ListWorktrees(ctx context.Context, repository model.RepositoryInfo) ([]model.GitWorktree, error) {
	if client.operation == "list" {
		return nil, model.NewError(model.ErrorGit, model.ExitGit, "Git list failed for the test.", nil)
	}
	return client.Client.ListWorktrees(ctx, repository)
}

func (client *cleanupFaultGitClient) WorktreeGitDirectory(ctx context.Context, path string) (model.GitDirectoryIdentity, error) {
	if client.operation == "identity" {
		return model.GitDirectoryIdentity{}, model.NewError(model.ErrorGit, model.ExitGit, "Git identity failed for the test.", nil)
	}
	return client.Client.WorktreeGitDirectory(ctx, path)
}

func (client *cleanupFaultGitClient) Status(ctx context.Context, path string) (model.WorktreeStatus, error) {
	if client.operation == "status" {
		return model.WorktreeStatus{}, model.NewError(model.ErrorGit, model.ExitGit, "Git status failed for the test.", nil)
	}
	return client.Client.Status(ctx, path)
}

func (client *cleanupFaultGitClient) MoveWorktree(ctx context.Context, repositoryPath, path, target string) error {
	if client.operation == "move" {
		return model.NewError(model.ErrorGit, model.ExitGit, "Git move failed for the test.", nil)
	}
	return client.Client.MoveWorktree(ctx, repositoryPath, path, target)
}

type appFixture struct {
	service *Service
	store   *store.Store
	git     *gitadapter.CommandClient
	locks   *lock.FileManager
	config  config.Config
	now     time.Time
}

func newAppFixture(t *testing.T, repositoryPath string) *appFixture {
	t.Helper()
	dataDirectory := filepath.Join(t.TempDir(), "data with spaces")
	root := filepath.Join(t.TempDir(), "managed root with spaces")
	database, err := store.Open(context.Background(), filepath.Join(dataDirectory, "grove.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() returned %v", err)
		}
	})
	locks, err := lock.NewManager(filepath.Join(dataDirectory, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &appFixture{
		store: database,
		git:   gitadapter.NewClient(),
		locks: locks,
		config: config.Config{
			Root:                  root,
			RootSource:            model.SourceCommand,
			BootstrapScriptSource: model.SourceDisabled,
			DataDir:               dataDirectory,
		},
		now: integrationTime,
	}
	fixture.service, err = New(Options{
		Config:     fixture.config,
		Store:      fixture.store,
		Git:        fixture.git,
		Locks:      fixture.locks,
		Clock:      func() time.Time { return fixture.now },
		WorkingDir: repositoryPath,
		Environment: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func worktreeGitDirectory(t *testing.T, fixture *appFixture, path string) model.GitDirectoryIdentity {
	t.Helper()
	gitDirectory, err := fixture.git.WorktreeGitDirectory(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return gitDirectory
}

func TestCreateRejectsPreexistingTargetWithoutWorktreeRecord(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	target := filepath.Join(fixture.config.Root, paths.Slug(filepath.Base(repository.Path)), "target-exists")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.Create(context.Background(), CreateOptions{Name: "target-exists", Agent: "pi:test"})
	var domainError *model.Error
	if !errors.As(err, &domainError) || domainError.Code != model.ErrorTargetExists {
		t.Fatalf("Create() error = %v", err)
	}
	worktrees, err := fixture.store.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 0 {
		t.Errorf("stored worktrees = %#v", worktrees)
	}
}

func TestCreateRejectsTargetBelowGitMetadata(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	fixture.service.config.Root = filepath.Join(repository.Path, ".git", "managed")

	_, err := fixture.service.Create(context.Background(), CreateOptions{Name: "nested-gitdir", Agent: "pi:test"})
	var domainError *model.Error
	if !errors.As(err, &domainError) || domainError.Code != model.ErrorTargetNestedWorktree {
		t.Fatalf("Create() error = %v", err)
	}
	worktrees, err := fixture.store.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 0 {
		t.Errorf("stored worktrees = %#v", worktrees)
	}
}

func TestCreateRechecksParentAfterDirectoryCreation(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	outside := t.TempDir()
	fixture.service.hooks.BeforeGitAdd = func(_ context.Context, worktree model.Worktree) {
		parent := filepath.Dir(worktree.Path)
		if err := os.Remove(parent); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Error(err)
		}
	}

	_, err := fixture.service.Create(context.Background(), CreateOptions{Name: "parent-race", Agent: "pi:test"})
	var domainError *model.Error
	if !errors.As(err, &domainError) || domainError.Code != model.ErrorTargetOutsideRoot {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "parent-race")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("outside target exists: %v", err)
	}
}

func TestCreateDoesNotAdoptCompetingWorktree(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	fixture.service.git = &competingGitClient{Client: fixture.git, branch: "foreign"}

	_, err := fixture.service.Create(context.Background(), CreateOptions{Name: "claimed", Agent: "pi:test"})
	if err == nil {
		t.Fatal("Create() succeeded for a competing worktree")
	}
	worktrees, listErr := fixture.store.List(context.Background(), store.Filter{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(worktrees) != 1 || worktrees[0].State != model.WorktreeStateManualReview {
		t.Fatalf("stored worktrees = %#v", worktrees)
	}
	repositoryInfo, detectErr := fixture.git.DetectRepository(context.Background(), repository.Path)
	if detectErr != nil {
		t.Fatal(detectErr)
	}
	gitWorktree, present, findErr := fixture.service.findGitWorktreeForInfo(
		context.Background(),
		repositoryInfo,
		worktrees[0].Path,
	)
	if findErr != nil || !present || gitWorktree.Branch == nil || *gitWorktree.Branch != "foreign" {
		t.Errorf("competing Git worktree = %#v, %t, %v", gitWorktree, present, findErr)
	}
}

func TestLiveCreateRecoveryDoesNotTakeOwnership(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)

	reachedGit := make(chan struct{})
	releaseGit := make(chan struct{})
	fixture.service.hooks.BeforeGitAdd = func(context.Context, model.Worktree) {
		close(reachedGit)
		<-releaseGit
	}

	type createResponse struct {
		result model.Result[model.CreateData]
		err    error
	}
	response := make(chan createResponse, 1)
	go func() {
		result, err := fixture.service.Create(context.Background(), CreateOptions{Name: "live-create", Agent: "pi:test"})
		response <- createResponse{result: result, err: err}
	}()
	<-reachedGit

	secondLocks, err := lock.NewManager(fixture.config.LockDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Options{
		Config:     fixture.config,
		Store:      fixture.store,
		Git:        fixture.git,
		Locks:      secondLocks,
		Clock:      func() time.Time { return fixture.now },
		WorkingDir: repository.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recoverable, err := fixture.store.Recoverable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recoverable) != 1 || recoverable[0].State != model.WorktreeStateCreating {
		t.Fatalf("recoverable worktrees = %#v", recoverable)
	}

	close(releaseGit)
	created := <-response
	if created.err != nil {
		t.Fatal(created.err)
	}
	if created.result.Data.Worktree.State != model.WorktreeStateActive {
		t.Errorf("created worktree state = %q", created.result.Data.Worktree.State)
	}
}

func TestFailedCreateBootstrapRecoveryClosesPendingState(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	fixture.service.git = &addFailGitClient{Client: fixture.git}

	if _, err := fixture.service.Create(context.Background(), CreateOptions{Name: "failed-bootstrap", Agent: "pi:test"}); err == nil {
		t.Fatal("Create() succeeded")
	}
	worktrees, err := fixture.store.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 || worktrees[0].State != model.WorktreeStateCreateFailed ||
		worktrees[0].BootstrapState != model.BootstrapStatePending {
		t.Fatalf("failed create worktrees = %#v", worktrees)
	}
	fixture.service.git = fixture.git
	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.store.Get(context.Background(), worktrees[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.WorktreeStateCreateFailed || recovered.BootstrapState != model.BootstrapStateInterrupted {
		t.Errorf("recovered failed create = %#v", recovered)
	}
}

func TestAbandonedCreateRecoveryConfirmsRealGitWorktree(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	if err := os.MkdirAll(fixture.config.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(fixture.config.Root)
	if err != nil {
		t.Fatal(err)
	}
	repositoryInfo, err := fixture.git.DetectRepository(context.Background(), repository.Path)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := fixture.locks.AcquireOperation("abandoned-create")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := fixture.store.ReserveCreate(context.Background(), store.CreateReservation{
		Repository:         repositoryInfo,
		Name:               "recovered",
		CreationRoot:       root,
		RequestedBranch:    "recovered",
		ExpectedCommit:     repository.FirstCommit,
		CreatorAgent:       "pi:test",
		CreatedAt:          fixture.now,
		OperationToken:     "abandoned-create",
		OperationStartedAt: fixture.now,
		BootstrapSource:    model.SourceDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(worktree.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.AddWorktree(context.Background(), gitadapter.AddRequest{
		RepositoryPath: repository.Path,
		Path:           worktree.Path,
		Branch:         "recovered",
		Base:           repository.FirstCommit,
	}); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.store.Get(context.Background(), worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.WorktreeStateActive || recovered.BootstrapState != model.BootstrapStateInterrupted {
		t.Errorf("recovered worktree = %#v", recovered)
	}
}

func TestAbandonedCreateRecoveryRejectsMismatchedWorktree(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	if err := os.MkdirAll(fixture.config.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(fixture.config.Root)
	if err != nil {
		t.Fatal(err)
	}
	repositoryInfo, err := fixture.git.DetectRepository(context.Background(), repository.Path)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := fixture.locks.AcquireOperation("mismatched-create")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := fixture.store.ReserveCreate(context.Background(), store.CreateReservation{
		Repository:         repositoryInfo,
		Name:               "claimed-recovery",
		CreationRoot:       root,
		RequestedBranch:    "claimed-recovery",
		ExpectedCommit:     repository.FirstCommit,
		CreatorAgent:       "pi:test",
		CreatedAt:          fixture.now,
		OperationToken:     "mismatched-create",
		OperationStartedAt: fixture.now,
		BootstrapSource:    model.SourceDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(worktree.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.AddWorktree(context.Background(), gitadapter.AddRequest{
		RepositoryPath: repository.Path,
		Path:           worktree.Path,
		Branch:         "foreign-recovery",
		Base:           repository.FirstCommit,
	}); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.store.Get(context.Background(), worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.WorktreeStateManualReview || recovered.BootstrapState != model.BootstrapStateInterrupted {
		t.Errorf("recovered worktree = %#v", recovered)
	}
}

func TestReconcileKeepsGitMetadataForMissingDiskPath(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "missing-disk", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(created.Data.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateMissing || stored.Branch == nil || *stored.Branch != "missing-disk" {
		t.Errorf("reconciled worktree = %#v", stored)
	}
}

func TestLiveRemovalRecoveryDoesNotTakeOwnership(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "live-removal", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	operation, err := fixture.locks.AcquireOperation("live-removal-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReserveRemoval(context.Background(), store.RemoveReservation{
		WorktreeID:         created.Data.Worktree.ID,
		OperationToken:     "live-removal-token",
		OperationStartedAt: fixture.now,
		ObservedActivityAt: created.Data.Worktree.LastGroveActivityAt,
		CutoffAt:           fixture.now.Add(-30 * 24 * time.Hour),
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       worktreeGitDirectory(t, fixture, created.Data.Worktree.Path),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateRemoving {
		t.Errorf("live removal state = %q", stored.State)
	}
	if err := fixture.store.CancelRemoval(context.Background(), stored.ID, "live-removal-token"); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonedQuarantineRecoveryRestoresWorktree(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "restore-removal", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	operation, err := fixture.locks.AcquireOperation("restore-removal-token")
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := fixture.store.ReserveRemoval(context.Background(), store.RemoveReservation{
		WorktreeID:         created.Data.Worktree.ID,
		OperationToken:     "restore-removal-token",
		OperationStartedAt: fixture.now,
		ObservedActivityAt: created.Data.Worktree.LastGroveActivityAt,
		CutoffAt:           fixture.now.Add(-30 * 24 * time.Hour),
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       worktreeGitDirectory(t, fixture, created.Data.Worktree.Path),
	})
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath, err := cleanupQuarantinePath(reserved, "restore-removal-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.MoveWorktree(context.Background(), repository.Path, reserved.Path, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.Get(context.Background(), reserved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateActive {
		t.Errorf("restored state = %q", stored.State)
	}
	if _, err := os.Stat(reserved.Path); err != nil {
		t.Errorf("restored worktree path error = %v", err)
	}
	if _, err := os.Stat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("private cleanup path still exists: %v", err)
	}
}

func TestAbandonedCompletedRemovalRecoveryMarksRemoved(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "completed-removal", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	operation, err := fixture.locks.AcquireOperation("completed-removal-token")
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := fixture.store.ReserveRemoval(context.Background(), store.RemoveReservation{
		WorktreeID:         created.Data.Worktree.ID,
		OperationToken:     "completed-removal-token",
		OperationStartedAt: fixture.now,
		ObservedActivityAt: created.Data.Worktree.LastGroveActivityAt,
		CutoffAt:           fixture.now.Add(-30 * 24 * time.Hour),
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       worktreeGitDirectory(t, fixture, created.Data.Worktree.Path),
	})
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath, err := cleanupQuarantinePath(reserved, "completed-removal-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.MoveWorktree(context.Background(), repository.Path, reserved.Path, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.RemoveWorktree(context.Background(), repository.Path, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.Get(context.Background(), reserved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateRemoved {
		t.Errorf("recovered removal state = %q", stored.State)
	}
	testutil.RunGit(t, repository.Path, "show-ref", "--verify", "refs/heads/completed-removal")
}

func TestRemovalRecoveryRejectsReplacementWorktrees(t *testing.T) {
	for _, test := range []struct {
		name              string
		replaceQuarantine bool
	}{
		{name: "original"},
		{name: "quarantine", replaceQuarantine: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testutil.IsolateGit(t)
			repository := testutil.NewRepository(t)
			fixture := newAppFixture(t, repository.Path)
			worktreeName := "replace-" + test.name
			created, err := fixture.service.Create(context.Background(), CreateOptions{Name: worktreeName, Agent: "pi:test"})
			if err != nil {
				t.Fatal(err)
			}
			fixture.now = fixture.now.Add(31 * 24 * time.Hour)
			token := "replacement-" + test.name
			operation, err := fixture.locks.AcquireOperation(token)
			if err != nil {
				t.Fatal(err)
			}
			reserved, err := fixture.store.ReserveRemoval(context.Background(), store.RemoveReservation{
				WorktreeID:         created.Data.Worktree.ID,
				OperationToken:     token,
				OperationStartedAt: fixture.now,
				ObservedActivityAt: created.Data.Worktree.LastGroveActivityAt,
				CutoffAt:           fixture.now.Add(-30 * 24 * time.Hour),
				Reason:             model.RemovalReasonOldAndClean,
				GitDirectory:       worktreeGitDirectory(t, fixture, created.Data.Worktree.Path),
			})
			if err != nil {
				t.Fatal(err)
			}
			quarantinePath, err := cleanupQuarantinePath(reserved, token)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.git.MoveWorktree(context.Background(), repository.Path, reserved.Path, quarantinePath); err != nil {
				t.Fatal(err)
			}
			if err := fixture.git.RemoveWorktree(context.Background(), repository.Path, quarantinePath); err != nil {
				t.Fatal(err)
			}
			replacementPath := reserved.Path
			if test.replaceQuarantine {
				replacementPath = quarantinePath
			}
			if err := fixture.git.AddWorktree(context.Background(), gitadapter.AddRequest{
				RepositoryPath: repository.Path,
				Path:           replacementPath,
				Branch:         "foreign-" + test.name,
				Base:           repository.FirstCommit,
			}); err != nil {
				t.Fatal(err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			stored, err := fixture.store.Get(context.Background(), reserved.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != model.WorktreeStateManualReview {
				t.Errorf("recovered replacement state = %q", stored.State)
			}
			if _, err := os.Stat(replacementPath); err != nil {
				t.Errorf("replacement worktree path error = %v", err)
			}
		})
	}
}

func TestBootstrapRecoveryDistinguishesLiveAndAbandonedRuns(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	fixture.config.BootstrapScript = "bootstrap-worktree.sh"
	fixture.config.BootstrapScriptSource = model.SourceBuiltIn
	fixture.service.config = fixture.config

	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "bootstrap-recovery", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	worktree := created.Data.Worktree
	if worktree.BootstrapState != model.BootstrapStateNotPresent {
		t.Fatalf("bootstrap state = %q", worktree.BootstrapState)
	}

	second, err := fixture.service.Create(context.Background(), CreateOptions{Name: "bootstrap-running", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	running := second.Data.Worktree
	if running.BootstrapState != model.BootstrapStateNotPresent {
		t.Fatalf("second bootstrap state = %q", running.BootstrapState)
	}

	pendingOperation, err := fixture.locks.AcquireOperation("pending-bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	repositoryInfo, err := fixture.git.DetectRepository(context.Background(), repository.Path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(fixture.config.Root)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.store.ReserveCreate(context.Background(), store.CreateReservation{
		Repository:         repositoryInfo,
		Name:               "manual-running",
		CreationRoot:       root,
		RequestedBranch:    "manual-running",
		ExpectedCommit:     repository.FirstCommit,
		CreatorAgent:       "pi:test",
		CreatedAt:          fixture.now,
		OperationToken:     "pending-bootstrap",
		OperationStartedAt: fixture.now,
		BootstrapSource:    model.SourceBuiltIn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pending.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.git.AddWorktree(context.Background(), gitadapter.AddRequest{
		RepositoryPath: repository.Path,
		Path:           pending.Path,
		Branch:         "manual-running",
		Base:           repository.FirstCommit,
	}); err != nil {
		t.Fatal(err)
	}
	gitWorktree, present, err := fixture.service.findGitWorktreeForInfo(context.Background(), repositoryInfo, pending.Path)
	if err != nil || !present {
		t.Fatalf("Git worktree = %#v, %t, %v", gitWorktree, present, err)
	}
	bootstrapLock, err := fixture.locks.AcquireBootstrap(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteCreate(context.Background(), pending.ID, "pending-bootstrap", gitWorktree); err != nil {
		t.Fatal(err)
	}
	if err := pendingOperation.Unlock(); err != nil {
		t.Fatal(err)
	}
	startedAt := fixture.now
	script := filepath.Join(pending.Path, "bootstrap-worktree.sh")
	if err := fixture.store.UpdateBootstrap(context.Background(), store.BootstrapUpdate{
		WorktreeID: pending.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateRunning,
		Script:     &script,
		Source:     model.SourceBuiltIn,
		StartedAt:  &startedAt,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	live, err := fixture.store.Get(context.Background(), pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.BootstrapState != model.BootstrapStateRunning {
		t.Errorf("live bootstrap state = %q", live.BootstrapState)
	}
	displacedPath := pending.Path + ".displaced"
	if err := os.Rename(pending.Path, displacedPath); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(displacedPath, pending.Path); err != nil {
			t.Errorf("restore displaced worktree: %v", err)
		}
	}()
	if _, err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapLock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	abandoned, err := fixture.store.Get(context.Background(), pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.State != model.WorktreeStateMissing || abandoned.BootstrapState != model.BootstrapStateInterrupted {
		t.Errorf("abandoned bootstrap worktree = %#v", abandoned)
	}
}

func TestCleanupReservationRejectsTouchAfterPlan(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "touch-before-reserve", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	var touchErr error
	fixture.service.hooks.BeforeRemovalReserve = func(ctx context.Context, worktree model.Worktree) {
		_, touchErr = fixture.service.Touch(ctx, TouchOptions{Target: worktree.Path})
	}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if touchErr != nil {
		t.Fatalf("Touch() returned %v", touchErr)
	}
	if cleanup.Data.Summary.Skipped != 1 || cleanup.Data.Items[0].Reason != model.RemovalReasonStateChange {
		t.Errorf("cleanup result = %#v", cleanup.Data)
	}
	if _, err := os.Stat(created.Data.Worktree.Path); err != nil {
		t.Errorf("worktree path was removed: %v", err)
	}
}

func TestCleanupBlocksDirtyWorktreeStates(t *testing.T) {
	for _, test := range []struct {
		name  string
		dirty func(*testing.T, string)
	}{
		{
			name: "staged",
			dirty: func(t *testing.T, path string) {
				testutil.WriteFile(t, filepath.Join(path, "staged.txt"), "staged\n")
				testutil.RunGit(t, path, "add", "staged.txt")
			},
		},
		{
			name: "modified",
			dirty: func(t *testing.T, path string) {
				testutil.WriteFile(t, filepath.Join(path, "README.md"), "modified\n")
			},
		},
		{
			name: "untracked",
			dirty: func(t *testing.T, path string) {
				testutil.WriteFile(t, filepath.Join(path, "untracked.txt"), "untracked\n")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testutil.IsolateGit(t)
			repository := testutil.NewRepository(t)
			fixture := newAppFixture(t, repository.Path)
			created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "dirty-" + test.name, Agent: "pi:test"})
			if err != nil {
				t.Fatal(err)
			}
			test.dirty(t, created.Data.Worktree.Path)
			testutil.WriteFile(t, filepath.Join(created.Data.Worktree.Path, "cache.log"), "cache\n")
			fixture.now = fixture.now.Add(31 * 24 * time.Hour)
			plan, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{
				OlderThan:    "30d",
				AllowIgnored: true,
				DryRun:       true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Data.Summary.Skipped != 1 || plan.Data.Items[0].Reason != model.RemovalReasonDirty {
				t.Errorf("cleanup plan = %#v", plan.Data)
			}
		})
	}
}

func TestCleanupIgnoredAndLockedPolicies(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "cleanup-policy", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(created.Data.Worktree.Path, "cache.log"), "cache\n")
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	blocked, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Data.Summary.Skipped != 1 || blocked.Data.Items[0].Reason != model.RemovalReasonIgnored {
		t.Errorf("default cleanup plan = %#v", blocked.Data)
	}
	allowed, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", AllowIgnored: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Data.Summary.Candidate != 1 {
		t.Errorf("allow-ignored cleanup plan = %#v", allowed.Data)
	}

	testutil.RunGit(t, repository.Path, "worktree", "lock", created.Data.Worktree.Path)
	locked, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", AllowIgnored: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Data.Summary.Skipped != 1 || locked.Data.Items[0].Reason != model.RemovalReasonLocked {
		t.Errorf("locked cleanup plan = %#v", locked.Data)
	}
	testutil.RunGit(t, repository.Path, "worktree", "unlock", created.Data.Worktree.Path)
}

func TestCleanupFailsClosedOnGitInspectionErrors(t *testing.T) {
	for _, operation := range []string{"list", "identity", "status"} {
		t.Run(operation, func(t *testing.T) {
			testutil.IsolateGit(t)
			repository := testutil.NewRepository(t)
			fixture := newAppFixture(t, repository.Path)
			created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "fault-" + operation, Agent: "pi:test"})
			if err != nil {
				t.Fatal(err)
			}
			fixture.now = fixture.now.Add(31 * 24 * time.Hour)
			fixture.service.git = &cleanupFaultGitClient{Client: fixture.git, operation: operation}
			plan, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Data.Summary.Skipped != 1 || plan.Data.Items[0].Reason != model.RemovalReasonStatusError {
				t.Errorf("cleanup plan = %#v", plan.Data)
			}
			if _, err := os.Stat(created.Data.Worktree.Path); err != nil {
				t.Errorf("worktree path error = %v", err)
			}
		})
	}
}

func TestCleanupFailsClosedWhenPathChangesBeforeReservation(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "path-change", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	displacedPath := created.Data.Worktree.Path + ".displaced"
	fixture.service.hooks.BeforeRemovalReserve = func(context.Context, model.Worktree) {
		if err := os.Rename(created.Data.Worktree.Path, displacedPath); err != nil {
			t.Error(err)
		}
	}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Data.Summary.Skipped != 1 || cleanup.Data.Items[0].Reason != model.RemovalReasonStateChange {
		t.Errorf("cleanup result = %#v", cleanup.Data)
	}
	if err := os.Rename(displacedPath, created.Data.Worktree.Path); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupFailsClosedOnMoveError(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "move-failure", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	fixture.service.git = &cleanupFaultGitClient{Client: fixture.git, operation: "move"}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Data.Summary.Failed != 1 || len(cleanup.Failures) != 1 {
		t.Errorf("cleanup result = %#v", cleanup)
	}
	if _, err := os.Stat(created.Data.Worktree.Path); err != nil {
		t.Errorf("worktree path error = %v", err)
	}
}

func TestCleanupFailsClosedWhenRepositoryRecordIsUnavailable(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "repository-failure", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	plan, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.executeCleanup(
		context.Background(),
		CleanupOptions{OlderThan: "30d", Approved: true},
		plan,
		map[int64]model.Repository{},
	); err == nil {
		t.Fatal("executeCleanup() succeeded without the repository record")
	}
	if _, err := os.Stat(created.Data.Worktree.Path); err != nil {
		t.Errorf("worktree path error = %v", err)
	}
}

func TestCleanupRestoresWorktreeAfterRemovalFailure(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "remove-failure", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.git = &removeFailGitClient{Client: fixture.git}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	result, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.Summary.Failed != 1 || len(result.Failures) != 1 {
		t.Errorf("cleanup result = %#v", result)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateActive {
		t.Errorf("worktree state = %q", stored.State)
	}
	if _, err := os.Stat(created.Data.Worktree.Path); err != nil {
		t.Errorf("restored worktree path error = %v", err)
	}
}

func TestCleanupExecutesOnlyDisplayedCandidates(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	clean, err := fixture.service.Create(context.Background(), CreateOptions{Name: "approved-clean", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := fixture.service.Create(context.Background(), CreateOptions{Name: "blocked-then-clean", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	localData := filepath.Join(blocked.Data.Worktree.Path, "local-data.txt")
	if err := os.WriteFile(localData, []byte("local data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	plan, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Data.Summary.Candidate != 1 || plan.Data.Summary.Skipped != 1 {
		t.Fatalf("cleanup plan = %#v", plan.Data)
	}
	if err := os.Remove(localData); err != nil {
		t.Fatal(err)
	}
	cleanup, err := fixture.service.ExecuteCleanupPlan(
		context.Background(),
		CleanupOptions{OlderThan: "30d", Approved: true},
		plan.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Data.Summary.Deleted != 1 || cleanup.Data.Summary.Skipped != 1 {
		t.Errorf("cleanup result = %#v", cleanup.Data)
	}
	if _, err := os.Stat(clean.Data.Worktree.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("approved worktree was not removed: %v", err)
	}
	if _, err := os.Stat(blocked.Data.Worktree.Path); err != nil {
		t.Errorf("unapproved worktree was removed: %v", err)
	}
}

func TestCleanupDryRunDoesNotUpdateStoredSize(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "dry-run-size", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Data.Worktree.SizeBytes == nil || created.Data.Worktree.SizeMeasuredAt == nil {
		t.Fatalf("created worktree has no size: %#v", created.Data.Worktree)
	}
	if err := os.WriteFile(filepath.Join(created.Data.Worktree.Path, "local.log"), []byte("new local data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	plan, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", AllowIgnored: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Data.Summary.Candidate != 1 || plan.Data.Items[0].Reason != model.RemovalReasonOldAndClean {
		t.Fatalf("cleanup plan = %#v", plan.Data)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SizeBytes == nil || *stored.SizeBytes != *created.Data.Worktree.SizeBytes {
		t.Errorf("stored size changed from %v to %v", created.Data.Worktree.SizeBytes, stored.SizeBytes)
	}
	if stored.SizeMeasuredAt == nil || !stored.SizeMeasuredAt.Equal(*created.Data.Worktree.SizeMeasuredAt) {
		t.Errorf("stored measurement time changed from %v to %v", created.Data.Worktree.SizeMeasuredAt, stored.SizeMeasuredAt)
	}
}

func TestCleanupDryRunDoesNotChangeStoredRows(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "dry-run-rows", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	beforeWorktrees, err := fixture.store.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	beforeRepositories, err := fixture.store.Repositories(context.Background(), store.RepositoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	displacedPath := created.Data.Worktree.Path + ".displaced"
	if err := os.Rename(created.Data.Worktree.Path, displacedPath); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(displacedPath, created.Data.Worktree.Path); err != nil {
			t.Errorf("restore displaced worktree: %v", err)
		}
	}()

	if _, err := fixture.service.PlanCleanup(context.Background(), CleanupOptions{OlderThan: "30d", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	afterWorktrees, err := fixture.store.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	afterRepositories, err := fixture.store.Repositories(context.Background(), store.RepositoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterWorktrees, beforeWorktrees) {
		t.Errorf("dry-run changed worktree rows: before=%#v after=%#v", beforeWorktrees, afterWorktrees)
	}
	if !reflect.DeepEqual(afterRepositories, beforeRepositories) {
		t.Errorf("dry-run changed repository rows: before=%#v after=%#v", beforeRepositories, afterRepositories)
	}
}

func TestCleanupRechecksIgnoredFilesInPrivatePath(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "ignored-after-reserve", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	fixture.service.hooks.AfterWorktreeQuarantined = func(_ context.Context, worktree model.Worktree) {
		if err := os.WriteFile(filepath.Join(worktree.Path, "local.log"), []byte("local data\n"), 0o600); err != nil {
			t.Error(err)
		}
	}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Data.Summary.Skipped != 1 || cleanup.Data.Items[0].Reason != model.RemovalReasonIgnored {
		t.Errorf("cleanup result = %#v", cleanup.Data)
	}
	if _, err := os.Stat(filepath.Join(created.Data.Worktree.Path, "local.log")); err != nil {
		t.Errorf("ignored file was removed: %v", err)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateActive {
		t.Errorf("worktree state = %q", stored.State)
	}
}

func TestCleanupRejectsReplacementAtPrivatePath(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "private-replacement", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	quarantinePath := ""
	fixture.service.hooks.AfterWorktreeQuarantined = func(_ context.Context, worktree model.Worktree) {
		quarantinePath = worktree.Path
		if err := fixture.git.RemoveWorktree(context.Background(), repository.Path, worktree.Path); err != nil {
			t.Fatal(err)
		}
		if err := fixture.git.AddWorktree(context.Background(), gitadapter.AddRequest{
			RepositoryPath: repository.Path,
			Path:           worktree.Path,
			Branch:         "foreign-private-replacement",
			Base:           repository.FirstCommit,
		}); err != nil {
			t.Fatal(err)
		}
	}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Data.Summary.Skipped != 1 || cleanup.Data.Items[0].Reason != model.RemovalReasonStateChange {
		t.Errorf("cleanup result = %#v", cleanup.Data)
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateManualReview {
		t.Errorf("worktree state = %q", stored.State)
	}
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Errorf("replacement worktree path error = %v", err)
	}
	if _, err := os.Stat(created.Data.Worktree.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("original worktree path exists: %v", err)
	}
}

func TestCleanupReservationBlocksTouchAndKeepsBranch(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "touch-after-reserve", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)

	var touchErr error
	var once sync.Once
	fixture.service.hooks.AfterRemovalReserved = func(ctx context.Context, worktree model.Worktree) {
		once.Do(func() {
			_, touchErr = fixture.service.Touch(ctx, TouchOptions{Target: worktree.Path})
		})
	}
	cleanup, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	var domainError *model.Error
	if !errors.As(touchErr, &domainError) || domainError.Code != model.ErrorWorktreeNotActive {
		t.Errorf("Touch() error = %v", touchErr)
	}
	if cleanup.Data.Summary.Deleted != 1 || cleanup.Data.Summary.Failed != 0 {
		t.Errorf("cleanup summary = %#v", cleanup.Data.Summary)
	}
	if _, err := os.Stat(created.Data.Worktree.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed worktree path error = %v", err)
	}
	output, err := testutil.GitCommand(repository.Path, "show-ref", "--verify", "--quiet", "refs/heads/touch-after-reserve").CombinedOutput()
	if err != nil {
		t.Fatalf("cleanup removed the branch: %v: %s", err, output)
	}
}

func TestCleanupChecksOperationTokenImmediatelyBeforeGitRemoval(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "token-check", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	quarantinePath := ""
	fixture.service.hooks.BeforeGitRemove = func(ctx context.Context, worktree model.Worktree) {
		quarantinePath = worktree.Path
		if err := fixture.store.UpdateReconciled(ctx, store.ReconcileUpdate{
			WorktreeID:     worktree.ID,
			FromState:      model.WorktreeStateRemoving,
			State:          model.WorktreeStateManualReview,
			OperationToken: worktree.OperationToken,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true}); err == nil {
		t.Fatal("Cleanup() removed a worktree after the operation token changed")
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateManualReview {
		t.Errorf("worktree state = %q", stored.State)
	}
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Errorf("quarantined worktree path error = %v", err)
	}
}

func TestCleanupChecksOperationLockImmediatelyBeforeGitRemoval(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	created, err := fixture.service.Create(context.Background(), CreateOptions{Name: "lock-check", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	captured := &capturedLockManager{Manager: fixture.locks}
	fixture.service.locks = captured
	quarantinePath := ""
	fixture.service.hooks.BeforeGitRemove = func(_ context.Context, worktree model.Worktree) {
		quarantinePath = worktree.Path
		if err := captured.operation.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.Cleanup(context.Background(), CleanupOptions{OlderThan: "30d", Approved: true}); err == nil {
		t.Fatal("Cleanup() removed a worktree after it lost the operation lock")
	}
	stored, err := fixture.store.Get(context.Background(), created.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateRemoving {
		t.Errorf("worktree state = %q", stored.State)
	}
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Errorf("quarantined worktree path error = %v", err)
	}
}

type sizeRefreshRaceStore struct {
	Store
	worktreeID int64
	flip       func(context.Context) error
	flipped    bool
}

func (racing *sizeRefreshRaceStore) UpdateSize(ctx context.Context, update store.SizeUpdate) error {
	if update.WorktreeID == racing.worktreeID && !racing.flipped {
		racing.flipped = true
		if err := racing.flip(ctx); err != nil {
			return err
		}
	}
	return racing.Store.UpdateSize(ctx, update)
}

type sizeUpdateFailStore struct {
	Store
	worktreeID int64
	failWith   error
}

func (failing *sizeUpdateFailStore) UpdateSize(ctx context.Context, update store.SizeUpdate) error {
	if update.WorktreeID == failing.worktreeID {
		return failing.failWith
	}
	return failing.Store.UpdateSize(ctx, update)
}

func findIssue(issues []model.Issue, code model.IssueCode) (model.Issue, bool) {
	for _, issue := range issues {
		if issue.Code == code {
			return issue, true
		}
	}
	return model.Issue{}, false
}

func TestListRefreshSizeSkipsWorktreeReservedForRemoval(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	skipped, err := fixture.service.Create(context.Background(), CreateOptions{Name: "refresh-skipped", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := fixture.service.Create(context.Background(), CreateOptions{Name: "refresh-kept", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Data.Worktree.SizeMeasuredAt == nil {
		t.Fatalf("created worktree has no size measurement: %#v", skipped.Data.Worktree)
	}
	gitDirectory := worktreeGitDirectory(t, fixture, skipped.Data.Worktree.Path)
	fixture.now = fixture.now.Add(31 * 24 * time.Hour)
	fixture.service.store = &sizeRefreshRaceStore{
		Store:      fixture.store,
		worktreeID: skipped.Data.Worktree.ID,
		flip: func(ctx context.Context) error {
			_, err := fixture.store.ReserveRemoval(ctx, store.RemoveReservation{
				WorktreeID:         skipped.Data.Worktree.ID,
				OperationToken:     "refresh-race-token",
				OperationStartedAt: fixture.now,
				ObservedActivityAt: skipped.Data.Worktree.LastGroveActivityAt,
				CutoffAt:           fixture.now.Add(-30 * 24 * time.Hour),
				Reason:             model.RemovalReasonOldAndClean,
				GitDirectory:       gitDirectory,
			})
			return err
		},
	}

	result, err := fixture.service.List(context.Background(), ListOptions{RefreshSize: true})
	if err != nil {
		t.Fatal(err)
	}
	warning, found := findIssue(result.Warnings, model.IssueSizeRefreshSkipped)
	if !found || warning.Path == nil || *warning.Path != skipped.Data.Worktree.Path ||
		warning.WorktreeID == nil || *warning.WorktreeID != skipped.Data.Worktree.ID {
		t.Errorf("size refresh warning = %#v, %t", warning, found)
	}
	listed := map[int64]model.Worktree{}
	for _, worktree := range result.Data.Worktrees {
		listed[worktree.ID] = worktree
	}
	skippedRow := listed[skipped.Data.Worktree.ID]
	if skippedRow.SizeMeasuredAt == nil || !skippedRow.SizeMeasuredAt.Equal(*skipped.Data.Worktree.SizeMeasuredAt) {
		t.Errorf("skipped measurement time = %v, want %v", skippedRow.SizeMeasuredAt, skipped.Data.Worktree.SizeMeasuredAt)
	}
	keptRow := listed[kept.Data.Worktree.ID]
	if keptRow.SizeMeasuredAt == nil || !keptRow.SizeMeasuredAt.Equal(fixture.now) {
		t.Errorf("kept measurement time = %v, want %v", keptRow.SizeMeasuredAt, fixture.now)
	}
	stored, err := fixture.store.Get(context.Background(), skipped.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.WorktreeStateRemoving {
		t.Errorf("skipped worktree state = %q", stored.State)
	}
	if stored.SizeMeasuredAt == nil || !stored.SizeMeasuredAt.Equal(*skipped.Data.Worktree.SizeMeasuredAt) {
		t.Errorf("stored skipped measurement time = %v, want %v", stored.SizeMeasuredAt, skipped.Data.Worktree.SizeMeasuredAt)
	}
}

func TestStatsRefreshSkipsWorktreeThatLeftActiveState(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	fixture := newAppFixture(t, repository.Path)
	skipped, err := fixture.service.Create(context.Background(), CreateOptions{Name: "stats-skipped", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := fixture.service.Create(context.Background(), CreateOptions{Name: "stats-kept", Agent: "pi:test"})
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Data.Worktree.SizeMeasuredAt == nil {
		t.Fatalf("created worktree has no size measurement: %#v", skipped.Data.Worktree)
	}
	fixture.service.store = &sizeUpdateFailStore{
		Store:      fixture.store,
		worktreeID: skipped.Data.Worktree.ID,
		failWith:   store.ErrNotFound,
	}
	fixture.now = fixture.now.Add(time.Hour)

	result, err := fixture.service.Stats(context.Background(), StatsOptions{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	warning, found := findIssue(result.Warnings, model.IssueSizeRefreshSkipped)
	if !found || warning.Path == nil || *warning.Path != skipped.Data.Worktree.Path ||
		warning.WorktreeID == nil || *warning.WorktreeID != skipped.Data.Worktree.ID {
		t.Errorf("size refresh warning = %#v, %t", warning, found)
	}
	if result.Data.Active != 2 {
		t.Errorf("active count = %d", result.Data.Active)
	}
	storedSkipped, err := fixture.store.Get(context.Background(), skipped.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSkipped.SizeMeasuredAt == nil || !storedSkipped.SizeMeasuredAt.Equal(*skipped.Data.Worktree.SizeMeasuredAt) {
		t.Errorf("skipped measurement time = %v, want %v", storedSkipped.SizeMeasuredAt, skipped.Data.Worktree.SizeMeasuredAt)
	}
	storedKept, err := fixture.store.Get(context.Background(), kept.Data.Worktree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedKept.SizeMeasuredAt == nil || !storedKept.SizeMeasuredAt.Equal(fixture.now) {
		t.Errorf("kept measurement time = %v, want %v", storedKept.SizeMeasuredAt, fixture.now)
	}
}

func TestParseAge(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"1h":  time.Hour,
		"2d":  48 * time.Hour,
		"3w":  21 * 24 * time.Hour,
		"24h": 24 * time.Hour,
	} {
		got, err := ParseAge(value)
		if err != nil || got != want {
			t.Errorf("ParseAge(%q) = %s, %v; want %s", value, got, err, want)
		}
	}
	for _, value := range []string{"", "0h", "-1d", "1m", "1.5h", "9223372036854775807w"} {
		if _, err := ParseAge(value); err == nil {
			t.Errorf("ParseAge(%q) returned nil", value)
		}
	}
}
