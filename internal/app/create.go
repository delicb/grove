package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/del-boy/grove/internal/bootstrap"
	gitadapter "github.com/del-boy/grove/internal/git"
	"github.com/del-boy/grove/internal/identity"
	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
	"github.com/del-boy/grove/internal/size"
	"github.com/del-boy/grove/internal/store"
)

type CreateOptions struct {
	Name        string
	Repository  string
	Branch      string
	Base        string
	UseExisting bool
	Agent       string
}

func (service *Service) Create(ctx context.Context, options CreateOptions) (model.Result[model.CreateData], error) {
	result := model.NewResult("create", model.CreateData{})
	warnings, err := service.startup(ctx, false)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	if err := paths.ValidateWorktreeName(options.Name); err != nil {
		return result, err
	}
	repositoryPath := options.Repository
	if repositoryPath == "" {
		repositoryPath = service.workingDir
	}
	repository, err := service.git.DetectRepository(ctx, repositoryPath)
	if err != nil {
		return result, err
	}
	branch := options.Branch
	if branch == "" {
		branch = options.Name
	}
	if err := service.git.ValidateBranch(ctx, branch); err != nil {
		return result, err
	}
	branchExists, err := service.git.BranchExists(ctx, repository.SelectedCheckout, branch)
	if err != nil {
		return result, err
	}

	var requestedBase *string
	resolvedBase := ""
	expectedCommit := ""
	if options.UseExisting {
		if options.Base != "" {
			return result, model.NewError(
				model.ErrorInvalidArguments,
				model.ExitInvalidArguments,
				"--base cannot be used with --use-existing.",
				nil,
			)
		}
		if !branchExists {
			domainError := model.NewError(
				model.ErrorInvalidBranch,
				model.ExitGit,
				"The existing branch does not exist.",
				nil,
			)
			domainError.Details["branch"] = branch
			return result, domainError
		}
		expectedCommit, err = service.git.ResolveCommit(ctx, repository.SelectedCheckout, "refs/heads/"+branch)
		if err != nil {
			return result, err
		}
	} else {
		if branchExists {
			domainError := model.NewError(
				model.ErrorBranchExists,
				model.ExitConflict,
				"The branch exists. Use --use-existing to attach it.",
				nil,
			)
			domainError.Details["branch"] = branch
			return result, domainError
		}
		base := options.Base
		if base == "" {
			base = "HEAD"
		}
		resolvedBase, err = service.git.ResolveCommit(ctx, repository.SelectedCheckout, base)
		if err != nil {
			return result, err
		}
		requestedBase = &base
		expectedCommit = resolvedBase
	}

	agent, err := identity.Resolve(options.Agent, environmentLookup(service.environment))
	if err != nil {
		return result, err
	}
	root, err := service.prepareCreationRoot()
	if err != nil {
		return result, err
	}
	startedAt := service.now()
	repositoryRecord, err := service.store.ReserveRepository(ctx, repository, startedAt)
	if err != nil {
		return result, storeError("Grove could not reserve the repository directory.", err)
	}
	target := filepath.Join(root, repositoryRecord.DirectoryKey, options.Name)
	if err := service.validateReservedTarget(ctx, root, target); err != nil {
		return result, err
	}

	token, err := service.token()
	if err != nil {
		return result, internalError("Grove could not create an operation token.", err)
	}
	operationLock, err := service.locks.AcquireOperation(token)
	if err != nil {
		return result, internalError("Grove could not acquire the create operation lock.", err)
	}
	operationOwned := true
	defer func() {
		if operationOwned {
			_ = operationLock.Unlock()
		}
	}()

	bootstrapScript := optionalBootstrapScript(service.config.BootstrapScript)
	bootstrapSource := service.config.BootstrapScriptSource
	if bootstrapScript == nil {
		bootstrapSource = model.SourceDisabled
	}
	worktree, err := service.store.ReserveCreate(ctx, store.CreateReservation{
		Repository:         repository,
		Name:               options.Name,
		CreationRoot:       root,
		RequestedBase:      requestedBase,
		RequestedBranch:    branch,
		ExpectedCommit:     expectedCommit,
		CreatorAgent:       agent,
		CreatedAt:          startedAt,
		OperationToken:     token,
		OperationStartedAt: startedAt,
		BootstrapScript:    bootstrapScript,
		BootstrapSource:    bootstrapSource,
	})
	if err != nil {
		return result, storeError("Grove could not reserve the worktree creation.", err)
	}
	if !samePath(worktree.Path, target) {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateManualReview); updateErr != nil {
			return result, storeError("Grove could not quarantine an invalid create reservation.", updateErr)
		}
		return result, internalError("The reserved worktree path changed.", nil)
	}

	if err := service.validateReservedTarget(ctx, root, worktree.Path); err != nil {
		state := model.WorktreeStateCreateFailed
		if present, _ := pathPresent(worktree.Path); present {
			state = model.WorktreeStateManualReview
		}
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, state); updateErr != nil {
			return result, storeError("Grove could not close the failed create reservation.", updateErr)
		}
		return result, err
	}
	worktreeParent := filepath.Dir(worktree.Path)
	if err := os.MkdirAll(worktreeParent, 0o700); err != nil {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateCreateFailed); updateErr != nil {
			return result, storeError("Grove could not close the failed create reservation.", updateErr)
		}
		return result, model.NewError(model.ErrorInvalidPath, model.ExitConflict, "Grove could not create the worktree parent directory.", err)
	}
	if err := os.Chmod(worktreeParent, 0o700); err != nil {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateCreateFailed); updateErr != nil {
			return result, storeError("Grove could not close the unprotected create reservation.", updateErr)
		}
		return result, model.NewError(model.ErrorInvalidPath, model.ExitConflict, "Grove could not protect the worktree parent directory.", err)
	}
	if err := service.confirmOwnedOperation(ctx, operationLock, worktree, model.WorktreeStateCreating, token); err != nil {
		return result, err
	}
	if service.hooks.BeforeGitAdd != nil {
		service.hooks.BeforeGitAdd(ctx, worktree)
	}
	if err := service.validateReservedTarget(ctx, root, worktree.Path); err != nil {
		state := model.WorktreeStateCreateFailed
		if present, _ := pathPresent(worktree.Path); present {
			state = model.WorktreeStateManualReview
		}
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, state); updateErr != nil {
			return result, storeError("Grove could not close the unsafe create reservation.", updateErr)
		}
		return result, err
	}
	if err := service.confirmOwnedOperation(ctx, operationLock, worktree, model.WorktreeStateCreating, token); err != nil {
		return result, err
	}
	addErr := service.git.AddWorktree(ctx, gitadapter.AddRequest{
		RepositoryPath: repository.SelectedCheckout,
		Path:           worktree.Path,
		Branch:         branch,
		Base:           resolvedBase,
		UseExisting:    options.UseExisting,
	})
	if service.hooks.AfterGitAdd != nil {
		service.hooks.AfterGitAdd(ctx, worktree, addErr)
	}

	gitWorktree, gitPresent, gitStateErr := service.findGitWorktreeForInfo(ctx, repository, worktree.Path)
	diskPresent, diskStateErr := worktreeDirectoryPresent(worktree.Path)
	if gitStateErr != nil || diskStateErr != nil || gitPresent != diskPresent {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateManualReview); updateErr != nil {
			return result, storeError("Grove could not quarantine the uncertain worktree creation.", updateErr)
		}
		if addErr != nil {
			return result, addErr
		}
		return result, model.NewError(
			model.ErrorGit,
			model.ExitGit,
			"Grove could not confirm the created worktree.",
			errors.Join(gitStateErr, diskStateErr),
		)
	}
	if !gitPresent {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateCreateFailed); updateErr != nil {
			return result, storeError("Grove could not close the failed create reservation.", updateErr)
		}
		removeEmptyParent(worktree.Path, root)
		if addErr != nil {
			return result, addErr
		}
		return result, model.NewError(model.ErrorGit, model.ExitGit, "Git did not create the worktree.", nil)
	}
	if addErr != nil {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateManualReview); updateErr != nil {
			return result, storeError("Grove could not quarantine the competing worktree creation.", updateErr)
		}
		return result, addErr
	}
	if !matchesCreateRequest(worktree, gitWorktree) {
		if updateErr := service.store.FailCreate(ctx, worktree.ID, token, model.WorktreeStateManualReview); updateErr != nil {
			return result, storeError("Grove could not quarantine the mismatched worktree creation.", updateErr)
		}
		return result, model.NewError(
			model.ErrorGit,
			model.ExitGit,
			"The created Git worktree does not match the create request.",
			nil,
		)
	}

	bootstrapLock, err := service.locks.AcquireBootstrap(worktree.ID)
	if err != nil {
		return result, internalError("Grove could not acquire the bootstrap lock.", err)
	}
	bootstrapOwned := true
	defer func() {
		if bootstrapOwned {
			_ = bootstrapLock.Unlock()
		}
	}()
	if err := service.store.CompleteCreate(ctx, worktree.ID, token, gitWorktree); err != nil {
		return result, storeError("Grove could not complete the worktree creation.", err)
	}
	if err := operationLock.Unlock(); err != nil {
		return result, internalError("Grove could not release the create operation lock.", err)
	}
	operationOwned = false

	bootstrapResult, err := service.runBootstrap(ctx, worktree, repository, branch, agent)
	if err != nil {
		return result, err
	}
	if err := bootstrapLock.Unlock(); err != nil {
		return result, internalError("Grove could not release the bootstrap lock.", err)
	}
	bootstrapOwned = false

	durableContext := ctx
	if ctx.Err() != nil {
		durableContext = context.WithoutCancel(ctx)
	}
	measurement := size.Measure(worktree.Path)
	measuredAt := service.now()
	if err := service.store.UpdateSize(durableContext, store.SizeUpdate{
		WorktreeID: worktree.ID,
		Bytes:      measurement.Bytes,
		Complete:   measurement.Complete,
		MeasuredAt: measuredAt,
	}); err != nil {
		return result, storeError("Grove could not store the worktree size.", err)
	}
	addIssues(&result.Warnings, &result.Failures, worktree, measurement.Warnings, measurement.Complete)
	worktree, err = service.store.Get(durableContext, worktree.ID)
	if err != nil {
		return result, storeError("Grove could not read the created worktree.", err)
	}
	result.Data = model.CreateData{Worktree: worktree, Bootstrap: bootstrapResult}
	return result, nil
}

func (service *Service) prepareCreationRoot() (string, error) {
	if service.config.Root == "" {
		return "", model.NewError(model.ErrorConfigInvalid, model.ExitConfiguration, "The managed root must not be empty.", nil)
	}
	if err := os.MkdirAll(service.config.Root, 0o700); err != nil {
		return "", model.NewError(model.ErrorInvalidPath, model.ExitConflict, "Grove could not create the managed root.", err)
	}
	root, err := paths.CanonicalDirectory(service.config.Root)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", model.NewError(model.ErrorInvalidPath, model.ExitConflict, "Grove could not protect the managed root.", err)
	}
	return root, nil
}

func (service *Service) validateReservedTarget(ctx context.Context, root, target string) error {
	if !paths.IsChild(root, target) {
		return model.NewError(model.ErrorTargetOutsideRoot, model.ExitConflict, "The target path is outside the managed root.", nil)
	}
	if present, err := pathPresent(target); err != nil {
		return model.NewError(model.ErrorInvalidPath, model.ExitConflict, "Grove could not inspect the target path.", err)
	} else if present {
		return model.NewError(model.ErrorTargetExists, model.ExitConflict, "The target path already exists.", nil)
	}
	canonicalTarget, err := paths.CanonicalForCreation(target)
	if err != nil {
		return err
	}
	if !samePath(canonicalTarget, target) || !paths.IsChild(root, canonicalTarget) {
		return model.NewError(model.ErrorTargetOutsideRoot, model.ExitConflict, "The target path resolves outside the managed root.", nil)
	}
	ancestor, err := paths.NearestExistingAncestor(target)
	if err != nil {
		return err
	}
	for {
		_, detectErr := service.git.DetectRepository(ctx, ancestor)
		if detectErr == nil {
			return model.NewError(
				model.ErrorTargetNestedWorktree,
				model.ExitConflict,
				"The target path is below a Git worktree.",
				nil,
			)
		}
		var domainError *model.Error
		if !errors.As(detectErr, &domainError) ||
			(domainError.Code != model.ErrorNotRepository && domainError.Code != model.ErrorBareRepository) {
			return detectErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil
		}
		ancestor = parent
	}
}

func (service *Service) confirmOwnedOperation(ctx context.Context, operationLock interface{ Owned() bool }, worktree model.Worktree, state model.WorktreeState, token string) error {
	if !operationLock.Owned() {
		return internalError("Grove does not own the operation lock.", nil)
	}
	if err := service.store.ConfirmOperation(ctx, worktree.ID, state, token); err != nil {
		return storeError("Grove could not confirm the operation owner.", err)
	}
	if !operationLock.Owned() {
		return internalError("Grove lost the operation lock.", nil)
	}
	return nil
}

func (service *Service) findGitWorktreeForInfo(ctx context.Context, repository model.RepositoryInfo, target string) (model.GitWorktree, bool, error) {
	worktrees, err := service.git.ListWorktrees(ctx, repository)
	if err != nil {
		return model.GitWorktree{}, false, err
	}
	for _, worktree := range worktrees {
		if samePath(worktree.Path, target) {
			gitDirectory, err := service.git.WorktreeGitDirectory(ctx, target)
			if err != nil {
				return model.GitWorktree{}, false, err
			}
			worktree.GitDirectory = gitDirectory
			return worktree, true, nil
		}
	}
	return model.GitWorktree{}, false, nil
}

func (service *Service) runBootstrap(ctx context.Context, worktree model.Worktree, repository model.RepositoryInfo, branch, agent string) (model.BootstrapResult, error) {
	plan, err := bootstrap.Select(worktree.Path, service.config.BootstrapScript, service.config.BootstrapScriptSource)
	if err != nil {
		return model.BootstrapResult{}, internalError("Grove could not select the bootstrap script.", err)
	}
	if plan.State != model.BootstrapStatePending {
		finishedAt := service.now()
		update := store.BootstrapUpdate{
			WorktreeID: worktree.ID,
			FromState:  model.BootstrapStatePending,
			State:      plan.State,
			Script:     plan.Script,
			Source:     plan.Source,
		}
		if plan.State == model.BootstrapStateFailed {
			update.FinishedAt = &finishedAt
		}
		if err := service.store.UpdateBootstrap(ctx, update); err != nil {
			return model.BootstrapResult{}, storeError("Grove could not store the bootstrap selection.", err)
		}
		return bootstrap.Execute(ctx, bootstrap.ExecuteOptions{Plan: plan}).Result, nil
	}

	startedAt := service.now()
	if err := service.store.UpdateBootstrap(ctx, store.BootstrapUpdate{
		WorktreeID: worktree.ID,
		FromState:  model.BootstrapStatePending,
		State:      model.BootstrapStateRunning,
		Script:     plan.Script,
		Source:     plan.Source,
		StartedAt:  &startedAt,
	}); err != nil {
		return model.BootstrapResult{}, storeError("Grove could not start the bootstrap state.", err)
	}
	execution := bootstrap.Execute(ctx, bootstrap.ExecuteOptions{
		Plan: plan,
		Context: bootstrap.Context{
			WorktreePath:   worktree.Path,
			WorktreeName:   worktree.Name,
			RepositoryPath: repository.MainCheckout,
			Branch:         branch,
			Agent:          agent,
		},
		Environment: service.environment,
		Mode:        service.bootstrapMode,
		Stdin:       service.bootstrapStdin,
		Stdout:      service.bootstrapStdout,
		Stderr:      service.bootstrapStderr,
	})
	finishedAt := service.now()
	durableContext := ctx
	if ctx.Err() != nil {
		durableContext = context.WithoutCancel(ctx)
	}
	if err := service.store.UpdateBootstrap(durableContext, store.BootstrapUpdate{
		WorktreeID: worktree.ID,
		FromState:  model.BootstrapStateRunning,
		State:      execution.Result.State,
		Script:     plan.Script,
		Source:     plan.Source,
		ExitCode:   execution.Result.ExitCode,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}); err != nil {
		return model.BootstrapResult{}, storeError("Grove could not store the bootstrap result.", err)
	}
	return execution.Result, nil
}

func matchesCreateRequest(worktree model.Worktree, gitWorktree model.GitWorktree) bool {
	return gitWorktree.Branch != nil &&
		*gitWorktree.Branch == worktree.RequestedBranch &&
		gitWorktree.HEAD == worktree.ExpectedCommit
}

func optionalBootstrapScript(script string) *string {
	if script == "" {
		return nil
	}
	return &script
}

func pathPresent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func removeEmptyParent(path, root string) {
	parent := filepath.Dir(path)
	for paths.IsChild(root, parent) {
		if err := os.Remove(parent); err != nil {
			return
		}
		parent = filepath.Dir(parent)
	}
}

func environmentLookup(environment []string) identity.LookupEnv {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}
