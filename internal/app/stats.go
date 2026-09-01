package app

import (
	"context"
	"errors"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/size"
	"github.com/del-boy/grove/internal/store"
)

type StatsOptions struct {
	Repository string
	All        bool
	Refresh    bool
}

func (service *Service) Stats(ctx context.Context, options StatsOptions) (model.Result[model.StatsData], error) {
	result := model.NewResult("stats", model.StatsData{})
	warnings, err := service.startup(ctx, true)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	request := store.StatsRequest{IncludeFinal: options.All}
	filter := store.Filter{States: []model.WorktreeState{model.WorktreeStateActive}}
	if options.Repository != "" {
		repository, err := service.git.DetectRepository(ctx, options.Repository)
		if err != nil {
			return result, err
		}
		request.RepositoryCommonDir = &repository.CommonDir
		filter.RepositoryCommonDir = &repository.CommonDir
	}
	if options.Refresh {
		worktrees, err := service.store.List(ctx, filter)
		if err != nil {
			return result, storeError("Grove could not read worktrees for the size refresh.", err)
		}
		for _, worktree := range worktrees {
			measurement := size.Measure(worktree.Path)
			measuredAt := service.now()
			if err := service.store.UpdateSize(ctx, store.SizeUpdate{
				WorktreeID: worktree.ID,
				Bytes:      measurement.Bytes,
				Complete:   measurement.Complete,
				MeasuredAt: measuredAt,
			}); err != nil {
				if errors.Is(err, store.ErrNotActive) || errors.Is(err, store.ErrNotFound) {
					result.Warnings = append(result.Warnings, sizeRefreshSkippedIssue(worktree))
					continue
				}
				return result, storeError("Grove could not store a worktree size.", err)
			}
			addIssues(&result.Warnings, &result.Failures, worktree, measurement.Warnings, measurement.Complete)
		}
	}
	stats, err := service.store.Stats(ctx, request)
	if err != nil {
		return result, storeError("Grove could not calculate worktree statistics.", err)
	}
	result.Data = model.StatsData{
		Active:              stats.Active,
		Missing:             stats.Missing,
		ManualReview:        stats.ManualReview,
		Removed:             stats.Removed,
		CreateFailed:        stats.CreateFailed,
		RepositoryCount:     stats.RepositoryCount,
		SizeBytes:           stats.SizeBytes,
		UnknownSizeCount:    stats.UnknownSizeCount,
		IncompleteSizeCount: stats.IncompleteSizeCount,
		SizeComplete:        stats.SizeComplete,
		CalculatedAt:        service.now(),
		OldestMeasurementAt: stats.OldestMeasurementAt,
		NewestMeasurementAt: stats.NewestMeasurementAt,
	}
	return result, nil
}
