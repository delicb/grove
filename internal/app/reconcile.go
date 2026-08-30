package app

import (
	"context"
	"errors"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/store"
)

func (service *Service) Reconcile(ctx context.Context) ([]model.Issue, error) {
	worktrees, err := service.store.List(ctx, store.Filter{States: []model.WorktreeState{
		model.WorktreeStateActive,
		model.WorktreeStateMissing,
	}})
	if err != nil {
		return nil, storeError("Grove could not read worktrees for reconciliation.", err)
	}
	repositories, err := service.store.Repositories(ctx, store.RepositoryFilter{})
	if err != nil {
		return nil, storeError("Grove could not read repositories for reconciliation.", err)
	}
	repositoryByID := make(map[int64]model.Repository, len(repositories))
	for _, repository := range repositories {
		repositoryByID[repository.ID] = repository
	}
	grouped := make(map[int64][]model.Worktree)
	for _, worktree := range worktrees {
		grouped[worktree.RepositoryID] = append(grouped[worktree.RepositoryID], worktree)
	}

	warnings := []model.Issue{}
	for repositoryID, records := range grouped {
		repository, exists := repositoryByID[repositoryID]
		if !exists {
			return nil, internalError("A worktree repository record is missing.", nil)
		}
		gitWorktrees, err := service.git.ListWorktrees(ctx, model.RepositoryInfo{
			CommonDir:    repository.CommonDir,
			MainCheckout: repository.MainCheckout,
			DisplayName:  repository.DisplayName,
		})
		if err != nil {
			path := repository.MainCheckout
			warnings = append(warnings, model.NewIssue(
				model.IssueRepositoryUnreadable,
				"Grove could not read the repository during reconciliation.",
				&path,
				nil,
			))
			continue
		}
		if len(gitWorktrees) > 0 {
			if err := service.store.UpdateRepository(ctx, repository.ID, store.RepositoryUpdate{
				MainCheckout: gitWorktrees[0].Path,
				SeenAt:       service.now(),
			}); err != nil {
				return nil, storeError("Grove could not refresh the repository record.", err)
			}
			repository.MainCheckout = gitWorktrees[0].Path
		}
		gitByPath := make(map[string]model.GitWorktree, len(gitWorktrees))
		for _, gitWorktree := range gitWorktrees {
			gitByPath[gitWorktree.Path] = gitWorktree
		}
		for _, worktree := range records {
			gitWorktree, gitPresent := gitByPath[worktree.Path]
			if !gitPresent {
				for path, candidate := range gitByPath {
					if samePath(path, worktree.Path) {
						gitWorktree = candidate
						gitPresent = true
						break
					}
				}
			}
			diskPresent, diskErr := worktreeDirectoryPresent(worktree.Path)
			if diskErr != nil {
				path := worktree.Path
				id := worktree.ID
				warnings = append(warnings, model.NewIssue(
					model.IssueRepositoryUnreadable,
					"Grove could not inspect the worktree path during reconciliation.",
					&path,
					&id,
				))
				continue
			}

			desiredState := model.WorktreeStateMissing
			var branch *string
			var detachedCommit *string
			locked := false
			if gitPresent {
				branch = gitWorktree.Branch
				detachedCommit = gitWorktree.DetachedCommit
				locked = gitWorktree.Locked
				if diskPresent {
					desiredState = model.WorktreeStateActive
				}
			}
			if worktree.State == desiredState && equalStringPointers(worktree.Branch, branch) &&
				equalStringPointers(worktree.DetachedCommit, detachedCommit) && worktree.Locked == locked {
				continue
			}
			if err := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
				WorktreeID:     worktree.ID,
				FromState:      worktree.State,
				State:          desiredState,
				Branch:         branch,
				DetachedCommit: detachedCommit,
				Locked:         locked,
			}); err != nil {
				if errors.Is(err, store.ErrStateChanged) {
					continue
				}
				return nil, storeError("Grove could not update a reconciled worktree.", err)
			}
			if desiredState == model.WorktreeStateMissing {
				path := worktree.Path
				id := worktree.ID
				warnings = append(warnings, model.NewIssue(
					model.IssueWorktreeMissing,
					"Git or the file system does not contain the managed worktree.",
					&path,
					&id,
				))
			}
		}
	}
	return warnings, nil
}

func equalStringPointers(first, second *string) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}
