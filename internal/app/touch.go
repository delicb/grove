package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
	"github.com/del-boy/grove/internal/store"
)

type TouchOptions struct {
	Target     string
	Repository string
}

func (service *Service) Touch(ctx context.Context, options TouchOptions) (model.Result[model.TouchData], error) {
	result := model.NewResult("touch", model.TouchData{})
	warnings, err := service.startup(ctx, true)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	if options.Target == "" {
		return result, model.NewError(model.ErrorInvalidArguments, model.ExitInvalidArguments, "A worktree name or absolute path is required.", nil)
	}

	filter := store.Filter{}
	if filepath.IsAbs(options.Target) {
		if err := paths.ValidateUTF8(options.Target); err != nil {
			return result, err
		}
		path := filepath.Clean(options.Target)
		if canonical, err := paths.Canonical(options.Target); err == nil {
			path = canonical
		}
		filter.Path = &path
	} else {
		if err := paths.ValidateWorktreeName(options.Target); err != nil {
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
		filter.Name = &options.Target
		filter.RepositoryCommonDir = &repository.CommonDir
	}

	worktrees, err := service.store.List(ctx, filter)
	if err != nil {
		return result, storeError("Grove could not find the managed worktree.", err)
	}
	worktree, err := selectTouchWorktree(worktrees)
	if err != nil {
		return result, err
	}
	touched, previous, err := service.store.Touch(ctx, worktree.ID, service.now())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return result, worktreeNotFound(options.Target, err)
		case errors.Is(err, store.ErrNotActive), errors.Is(err, store.ErrStateChanged):
			return result, worktreeNotActive(worktree, err)
		default:
			return result, storeError("Grove could not update the worktree activity time.", err)
		}
	}
	result.Data = model.TouchData{Worktree: touched, PreviousActivityAt: previous}
	return result, nil
}

func selectTouchWorktree(worktrees []model.Worktree) (model.Worktree, error) {
	for _, worktree := range worktrees {
		if worktree.State == model.WorktreeStateActive {
			return worktree, nil
		}
	}
	if len(worktrees) == 0 {
		return model.Worktree{}, worktreeNotFound("", nil)
	}
	return model.Worktree{}, worktreeNotActive(worktrees[len(worktrees)-1], nil)
}

func worktreeNotFound(target string, err error) *model.Error {
	domainError := model.NewError(model.ErrorWorktreeNotFound, model.ExitConflict, "The managed worktree was not found.", err)
	if target != "" {
		domainError.Details["target"] = target
	}
	return domainError
}

func worktreeNotActive(worktree model.Worktree, err error) *model.Error {
	domainError := model.NewError(model.ErrorWorktreeNotActive, model.ExitConflict, "The managed worktree is not active.", err)
	domainError.Details["worktree_id"] = worktree.ID
	domainError.Details["state"] = worktree.State
	return domainError
}
