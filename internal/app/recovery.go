package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
	"github.com/del-boy/grove/internal/store"
)

func (service *Service) Recover(ctx context.Context) ([]model.Issue, error) {
	warnings := []model.Issue{}
	recoverable, err := service.store.Recoverable(ctx)
	if err != nil {
		return nil, storeError("Grove could not read incomplete operations.", err)
	}
	repositories, err := service.store.Repositories(ctx, store.RepositoryFilter{})
	if err != nil {
		return nil, storeError("Grove could not read repositories for recovery.", err)
	}
	repositoryByID := make(map[int64]model.Repository, len(repositories))
	for _, repository := range repositories {
		repositoryByID[repository.ID] = repository
	}
	tokensInUse := make(map[string]struct{}, len(recoverable))
	for _, worktree := range recoverable {
		if worktree.OperationToken != nil {
			tokensInUse[*worktree.OperationToken] = struct{}{}
		}
	}

	for _, worktree := range recoverable {
		if worktree.OperationToken == nil {
			return nil, internalError("An incomplete operation has no operation token.", nil)
		}
		operationLock, acquired, err := service.locks.TryOperation(*worktree.OperationToken)
		if err != nil {
			return nil, internalError("Grove could not inspect an operation lock.", err)
		}
		if !acquired {
			continue
		}
		recoveryWarnings, recoverErr := service.recoverOperation(ctx, worktree, repositoryByID[worktree.RepositoryID])
		unlockErr := operationLock.Unlock()
		if recoverErr != nil {
			return nil, recoverErr
		}
		if unlockErr != nil {
			return nil, internalError("Grove could not release an operation lock.", unlockErr)
		}
		warnings = append(warnings, recoveryWarnings...)
	}

	service.locks.SweepOperations(func(token string) bool {
		_, used := tokensInUse[token]
		return used
	})

	bootstrapWarnings, err := service.recoverBootstrap(ctx)
	if err != nil {
		return nil, err
	}
	return append(warnings, bootstrapWarnings...), nil
}

func (service *Service) recoverOperation(ctx context.Context, worktree model.Worktree, repository model.Repository) ([]model.Issue, error) {
	token := worktree.OperationToken
	if token == nil {
		return nil, internalError("An incomplete operation has no operation token.", nil)
	}
	gitWorktree, gitPresent, gitErr := service.findGitWorktree(ctx, repository, worktree.Path)
	diskPresent, diskErr := worktreeDirectoryPresent(worktree.Path)

	state := model.WorktreeStateManualReview
	var branch *string
	var detachedCommit *string
	locked := false
	if gitErr == nil && diskErr == nil {
		switch worktree.State {
		case model.WorktreeStateCreating:
			switch {
			case gitPresent && diskPresent && matchesCreateRequest(worktree, gitWorktree):
				state = model.WorktreeStateActive
				branch = gitWorktree.Branch
				detachedCommit = gitWorktree.DetachedCommit
				locked = gitWorktree.Locked
			case !gitPresent && !diskPresent:
				state = model.WorktreeStateCreateFailed
			}
		case model.WorktreeStateRemoving:
			quarantinePath := filepath.Join(filepath.Dir(worktree.Path), ".grove-removing-"+*token)
			quarantineGit, quarantineGitPresent, quarantineGitErr := service.findGitWorktree(ctx, repository, quarantinePath)
			quarantineDiskPresent, quarantineDiskErr := worktreeDirectoryPresent(quarantinePath)
			if quarantineGitErr == nil && quarantineDiskErr == nil {
				switch {
				case gitPresent && diskPresent && !quarantineGitPresent && !quarantineDiskPresent &&
					matchesRemovalIdentity(worktree, gitWorktree):
					state = model.WorktreeStateActive
					branch = gitWorktree.Branch
					detachedCommit = gitWorktree.DetachedCommit
					locked = gitWorktree.Locked
				case !gitPresent && !diskPresent && !quarantineGitPresent && !quarantineDiskPresent:
					state = model.WorktreeStateRemoved
				case !gitPresent && !diskPresent && quarantineGitPresent && quarantineDiskPresent &&
					matchesRemovalIdentity(worktree, quarantineGit):
					canonicalTarget, canonicalErr := paths.CanonicalForCreation(worktree.Path)
					if canonicalErr == nil && samePath(canonicalTarget, worktree.Path) &&
						paths.IsChild(worktree.CreationRoot, canonicalTarget) {
						if err := service.git.MoveWorktree(ctx, repository.MainCheckout, quarantinePath, worktree.Path); err == nil {
							restoredGit, restoredGitPresent, restoredGitErr := service.findGitWorktree(ctx, repository, worktree.Path)
							restoredDiskPresent, restoredDiskErr := worktreeDirectoryPresent(worktree.Path)
							if restoredGitErr == nil && restoredDiskErr == nil && restoredGitPresent && restoredDiskPresent &&
								matchesRemovalIdentity(worktree, restoredGit) {
								state = model.WorktreeStateActive
								branch = restoredGit.Branch
								detachedCommit = restoredGit.DetachedCommit
								locked = restoredGit.Locked
							}
						}
					}
				}
			}
		default:
			return nil, internalError("Recovery selected a complete operation.", nil)
		}
	}

	update := store.ReconcileUpdate{
		WorktreeID:     worktree.ID,
		FromState:      worktree.State,
		State:          state,
		OperationToken: token,
		Branch:         branch,
		DetachedCommit: detachedCommit,
		Locked:         locked,
	}
	if state == model.WorktreeStateRemoved {
		removedAt := service.now()
		update.RemovedAt = &removedAt
		update.RemovalReason = worktree.RemovalReason
	}
	if err := service.store.UpdateReconciled(ctx, update); err != nil {
		return nil, storeError("Grove could not recover an incomplete operation.", err)
	}
	if state != model.WorktreeStateManualReview {
		return nil, nil
	}
	path := worktree.Path
	id := worktree.ID
	return []model.Issue{model.NewIssue(
		model.IssueRecoveryManualReview,
		"Grove could not prove the state of an incomplete operation.",
		&path,
		&id,
	)}, nil
}

func (service *Service) recoverBootstrap(ctx context.Context) ([]model.Issue, error) {
	worktrees, err := service.store.List(ctx, store.Filter{})
	if err != nil {
		return nil, storeError("Grove could not read bootstrap states.", err)
	}
	for _, worktree := range worktrees {
		if worktree.BootstrapState != model.BootstrapStatePending && worktree.BootstrapState != model.BootstrapStateRunning {
			continue
		}
		if worktree.State == model.WorktreeStateCreating || worktree.State == model.WorktreeStateRemoving {
			continue
		}
		bootstrapLock, acquired, err := service.locks.TryBootstrap(worktree.ID)
		if err != nil {
			return nil, internalError("Grove could not inspect a bootstrap lock.", err)
		}
		if !acquired {
			continue
		}
		finishedAt := service.now()
		updateErr := service.store.UpdateBootstrap(ctx, store.BootstrapUpdate{
			WorktreeID: worktree.ID,
			FromState:  worktree.BootstrapState,
			State:      model.BootstrapStateInterrupted,
			Script:     worktree.BootstrapScript,
			Source:     worktree.BootstrapSource,
			StartedAt:  worktree.BootstrapStartedAt,
			FinishedAt: &finishedAt,
		})
		unlockErr := bootstrapLock.Unlock()
		if updateErr != nil {
			return nil, storeError("Grove could not recover an abandoned bootstrap run.", updateErr)
		}
		if unlockErr != nil {
			return nil, internalError("Grove could not release a bootstrap lock.", unlockErr)
		}
	}
	return []model.Issue{}, nil
}

func (service *Service) findGitWorktree(ctx context.Context, repository model.Repository, target string) (model.GitWorktree, bool, error) {
	if repository.ID == 0 || repository.CommonDir == "" {
		return model.GitWorktree{}, false, errors.New("repository record is missing")
	}
	worktrees, err := service.git.ListWorktrees(ctx, model.RepositoryInfo{
		CommonDir:    repository.CommonDir,
		MainCheckout: repository.MainCheckout,
		DisplayName:  repository.DisplayName,
	})
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

func worktreeDirectoryPresent(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return true, errors.New("the worktree path is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true, err
	}
	if !samePath(resolved, path) {
		return true, errors.New("the worktree path resolves to a different path")
	}
	return true, nil
}

func matchesRemovalIdentity(worktree model.Worktree, gitWorktree model.GitWorktree) bool {
	return worktree.RemovalGitDirectory != nil &&
		samePath(worktree.RemovalGitDirectory.Path, gitWorktree.GitDirectory.Path) &&
		worktree.RemovalGitDirectory.Token == gitWorktree.GitDirectory.Token
}

func samePath(first, second string) bool {
	return filepath.Clean(first) == filepath.Clean(second)
}
