package app

import (
	"context"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/size"
	"github.com/del-boy/grove/internal/store"
)

type ListOptions struct {
	Repository  string
	All         bool
	RefreshSize bool
}

func (service *Service) List(ctx context.Context, options ListOptions) (model.Result[model.ListData], error) {
	result := model.NewResult("list", model.ListData{Worktrees: []model.Worktree{}})
	warnings, err := service.startup(ctx, true)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	filter := store.Filter{}
	if !options.All {
		filter.States = []model.WorktreeState{model.WorktreeStateActive}
	}
	if options.Repository != "" {
		repository, err := service.git.DetectRepository(ctx, options.Repository)
		if err != nil {
			return result, err
		}
		filter.RepositoryCommonDir = &repository.CommonDir
	}
	worktrees, err := service.store.List(ctx, filter)
	if err != nil {
		return result, storeError("Grove could not list managed worktrees.", err)
	}
	if options.RefreshSize {
		for index := range worktrees {
			if worktrees[index].State != model.WorktreeStateActive {
				continue
			}
			measurement := size.Measure(worktrees[index].Path)
			measuredAt := service.now()
			if err := service.store.UpdateSize(ctx, store.SizeUpdate{
				WorktreeID: worktrees[index].ID,
				Bytes:      measurement.Bytes,
				Complete:   measurement.Complete,
				MeasuredAt: measuredAt,
			}); err != nil {
				return result, storeError("Grove could not store a worktree size.", err)
			}
			bytes := measurement.Bytes
			worktrees[index].SizeBytes = &bytes
			worktrees[index].SizeComplete = measurement.Complete
			worktrees[index].SizeMeasuredAt = &measuredAt
			addIssues(&result.Warnings, &result.Failures, worktrees[index], measurement.Warnings, measurement.Complete)
		}
	}
	result.Data.Worktrees = worktrees
	result.Data.Summary = summarizeList(worktrees)
	return result, nil
}

func summarizeList(worktrees []model.Worktree) model.ListSummary {
	summary := model.ListSummary{SizeComplete: true}
	for _, worktree := range worktrees {
		switch worktree.State {
		case model.WorktreeStateActive:
			summary.Active++
			if worktree.SizeBytes == nil {
				summary.UnknownSizeCount++
				summary.SizeComplete = false
			} else {
				summary.SizeBytes += *worktree.SizeBytes
				if !worktree.SizeComplete {
					summary.SizeComplete = false
				}
			}
		case model.WorktreeStateCreating:
			summary.Creating++
		case model.WorktreeStateRemoving:
			summary.Removing++
		case model.WorktreeStateMissing:
			summary.Missing++
		case model.WorktreeStateRemoved:
			summary.Removed++
		case model.WorktreeStateCreateFailed:
			summary.CreateFailed++
		case model.WorktreeStateManualReview:
			summary.ManualReview++
		}
	}
	return summary
}
