package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/del-boy/grove/internal/bootstrap"
	"github.com/del-boy/grove/internal/config"
	gitadapter "github.com/del-boy/grove/internal/git"
	"github.com/del-boy/grove/internal/lock"
	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/store"
)

type Clock func() time.Time

type TokenGenerator func() (string, error)

type Store interface {
	Recoverable(context.Context) ([]model.Worktree, error)
	ReserveRepository(context.Context, model.RepositoryInfo, time.Time) (model.Repository, error)
	ReserveCreate(context.Context, store.CreateReservation) (model.Worktree, error)
	CompleteCreate(context.Context, int64, string, model.GitWorktree) error
	FailCreate(context.Context, int64, string, model.WorktreeState) error
	ConfirmOperation(context.Context, int64, model.WorktreeState, string) error
	ReserveRemoval(context.Context, store.RemoveReservation) (model.Worktree, error)
	CompleteRemoval(context.Context, int64, string, store.RemovalResult) error
	CancelRemoval(context.Context, int64, string) error
	Get(context.Context, int64) (model.Worktree, error)
	List(context.Context, store.Filter) ([]model.Worktree, error)
	Touch(context.Context, int64, time.Time) (model.Worktree, time.Time, error)
	Repositories(context.Context, store.RepositoryFilter) ([]model.Repository, error)
	UpdateRepository(context.Context, int64, store.RepositoryUpdate) error
	UpdateReconciled(context.Context, store.ReconcileUpdate) error
	UpdateBootstrap(context.Context, store.BootstrapUpdate) error
	UpdateSize(context.Context, store.SizeUpdate) error
	Stats(context.Context, store.StatsRequest) (store.Stats, error)
}

type Hooks struct {
	BeforeGitAdd             func(context.Context, model.Worktree)
	AfterGitAdd              func(context.Context, model.Worktree, error)
	BeforeRemovalReserve     func(context.Context, model.Worktree)
	AfterRemovalReserved     func(context.Context, model.Worktree)
	BeforeGitRemove          func(context.Context, model.Worktree)
	AfterWorktreeQuarantined func(context.Context, model.Worktree)
	AfterGitRemove           func(context.Context, model.Worktree, error)
}

type Options struct {
	Config          config.Config
	Store           Store
	Git             gitadapter.Client
	Locks           lock.Manager
	Clock           Clock
	Token           TokenGenerator
	WorkingDir      string
	Environment     []string
	BootstrapMode   bootstrap.OutputMode
	BootstrapStdin  io.Reader
	BootstrapStdout io.Writer
	BootstrapStderr io.Writer
	Hooks           Hooks
}

type OpenOptions struct {
	Config          config.Options
	Git             gitadapter.Client
	Clock           Clock
	Token           TokenGenerator
	Environment     []string
	BootstrapMode   bootstrap.OutputMode
	BootstrapStdin  io.Reader
	BootstrapStdout io.Writer
	BootstrapStderr io.Writer
	Hooks           Hooks
}

type Service struct {
	config          config.Config
	store           Store
	git             gitadapter.Client
	locks           lock.Manager
	clock           Clock
	token           TokenGenerator
	workingDir      string
	environment     []string
	bootstrapMode   bootstrap.OutputMode
	bootstrapStdin  io.Reader
	bootstrapStdout io.Writer
	bootstrapStderr io.Writer
	hooks           Hooks
	close           func() error
}

func Open(ctx context.Context, options OpenOptions) (*Service, error) {
	loaded, err := config.Load(options.Config)
	if err != nil {
		return nil, err
	}
	if err := loaded.EnsureDataDirs(); err != nil {
		return nil, err
	}
	database, err := store.Open(ctx, loaded.DatabasePath())
	if err != nil {
		return nil, databaseError("Grove could not open the database.", err)
	}
	locks, err := lock.NewManager(loaded.LockDir())
	if err != nil {
		_ = database.Close()
		return nil, internalError("Grove could not open the lock directory.", err)
	}
	workingDirectory := options.Config.WorkingDir
	if workingDirectory == "" {
		workingDirectory, err = os.Getwd()
		if err != nil {
			_ = database.Close()
			return nil, internalError("Grove could not read the current directory.", err)
		}
	}
	service, err := New(Options{
		Config:          loaded,
		Store:           database,
		Git:             options.Git,
		Locks:           locks,
		Clock:           options.Clock,
		Token:           options.Token,
		WorkingDir:      workingDirectory,
		Environment:     options.Environment,
		BootstrapMode:   options.BootstrapMode,
		BootstrapStdin:  options.BootstrapStdin,
		BootstrapStdout: options.BootstrapStdout,
		BootstrapStderr: options.BootstrapStderr,
		Hooks:           options.Hooks,
	})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	service.close = database.Close
	return service, nil
}

func New(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, errors.New("app store must not be nil")
	}
	if options.Git == nil {
		options.Git = gitadapter.NewClient()
	}
	if options.Locks == nil {
		return nil, errors.New("app lock manager must not be nil")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Token == nil {
		options.Token = randomToken
	}
	if options.WorkingDir == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		options.WorkingDir = workingDirectory
	}
	workingDirectory, err := filepath.Abs(options.WorkingDir)
	if err != nil {
		return nil, err
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	return &Service{
		config:          options.Config,
		store:           options.Store,
		git:             options.Git,
		locks:           options.Locks,
		clock:           options.Clock,
		token:           options.Token,
		workingDir:      filepath.Clean(workingDirectory),
		environment:     append([]string(nil), environment...),
		bootstrapMode:   options.BootstrapMode,
		bootstrapStdin:  options.BootstrapStdin,
		bootstrapStdout: options.BootstrapStdout,
		bootstrapStderr: options.BootstrapStderr,
		hooks:           options.Hooks,
	}, nil
}

func (service *Service) Close() error {
	if service == nil || service.close == nil {
		return nil
	}
	closeStore := service.close
	service.close = nil
	return closeStore()
}

func (service *Service) startup(ctx context.Context, reconcile bool) ([]model.Issue, error) {
	if err := service.git.CheckVersion(ctx); err != nil {
		return nil, err
	}
	warnings, err := service.Recover(ctx)
	if err != nil {
		return nil, err
	}
	if !reconcile {
		return warnings, nil
	}
	reconcileWarnings, err := service.Reconcile(ctx)
	if err != nil {
		return nil, err
	}
	return append(warnings, reconcileWarnings...), nil
}

func (service *Service) now() time.Time {
	return service.clock().UTC()
}

func randomToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func databaseError(message string, err error) *model.Error {
	code := model.ErrorDatabase
	if errors.Is(err, store.ErrBusy) {
		code = model.ErrorDatabaseBusy
	}
	return model.NewError(code, model.ExitDatabase, message, err)
}

func internalError(message string, err error) *model.Error {
	return model.NewError(model.ErrorInternal, model.ExitInternal, message, err)
}

func storeError(message string, err error) error {
	var domainError *model.Error
	if errors.As(err, &domainError) {
		return err
	}
	if errors.Is(err, store.ErrConflict) {
		return model.NewError(model.ErrorWorktreeConflict, model.ExitConflict, message, err)
	}
	return databaseError(message, err)
}

func sizeRefreshSkippedIssue(worktree model.Worktree) model.Issue {
	path := worktree.Path
	id := worktree.ID
	return model.NewIssue(
		model.IssueSizeRefreshSkipped,
		"The worktree changed state during the size refresh.",
		&path,
		&id,
	)
}

func addIssues(resultWarnings *[]model.Issue, resultFailures *[]model.Issue, worktree model.Worktree, warnings []model.Issue, complete bool) {
	for _, warning := range warnings {
		id := worktree.ID
		warning.WorktreeID = &id
		*resultWarnings = append(*resultWarnings, warning)
	}
	if !complete {
		path := worktree.Path
		id := worktree.ID
		*resultFailures = append(*resultFailures, model.NewIssue(
			model.IssueSizeIncomplete,
			"Grove could not complete the worktree size scan.",
			&path,
			&id,
		))
	}
}
