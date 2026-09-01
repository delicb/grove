package app

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
	"github.com/del-boy/grove/internal/size"
	"github.com/del-boy/grove/internal/store"
)

var agePattern = regexp.MustCompile(`^([0-9]+)([hdw])$`)

type CleanupOptions struct {
	Repository   string
	OlderThan    string
	AllowIgnored bool
	DryRun       bool
	Approved     bool
}

type cleanupDecision struct {
	safe         bool
	reason       model.RemovalReason
	code         model.IssueCode
	message      string
	gitDirectory model.GitDirectoryIdentity
}

func ParseAge(value string) (time.Duration, error) {
	matches := agePattern.FindStringSubmatch(value)
	if matches == nil {
		return 0, invalidAge(value)
	}
	amount, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || amount == 0 {
		return 0, invalidAge(value)
	}
	unit := time.Hour
	switch matches[2] {
	case "d":
		unit = 24 * time.Hour
	case "w":
		unit = 7 * 24 * time.Hour
	}
	if amount > uint64(math.MaxInt64/int64(unit)) {
		return 0, invalidAge(value)
	}
	return time.Duration(amount) * unit, nil
}

func (service *Service) PlanCleanup(ctx context.Context, options CleanupOptions) (model.Result[model.CleanupData], error) {
	result, _, err := service.planCleanup(ctx, options)
	return result, err
}

func (service *Service) Cleanup(ctx context.Context, options CleanupOptions) (model.Result[model.CleanupData], error) {
	result, _, err := service.planCleanup(ctx, options)
	if err != nil {
		return result, err
	}
	if options.DryRun {
		return result, nil
	}
	if !options.Approved {
		return service.executeCleanup(ctx, options, result, nil)
	}
	warnings, err := service.startup(ctx, true)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	repositories, err := service.repositoriesByID(ctx, "Grove could not read repositories for cleanup.")
	if err != nil {
		return result, err
	}
	return service.executeCleanup(ctx, options, result, repositories)
}

func (service *Service) ExecuteCleanupPlan(
	ctx context.Context,
	options CleanupOptions,
	plan model.CleanupData,
) (model.Result[model.CleanupData], error) {
	result := model.NewResult("cleanup", plan)
	if plan.DryRun || plan.CutoffAt.IsZero() {
		return result, model.NewError(
			model.ErrorInvalidArguments,
			model.ExitInvalidArguments,
			"The approved cleanup plan is not valid.",
			nil,
		)
	}
	if !options.Approved {
		return service.executeCleanup(ctx, options, result, nil)
	}
	warnings, err := service.startup(ctx, true)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	repositories, err := service.repositoriesByID(ctx, "Grove could not read repositories for cleanup.")
	if err != nil {
		return result, err
	}
	return service.executeCleanup(ctx, options, result, repositories)
}

func (service *Service) executeCleanup(
	ctx context.Context,
	options CleanupOptions,
	result model.Result[model.CleanupData],
	repositories map[int64]model.Repository,
) (model.Result[model.CleanupData], error) {
	if !options.Approved {
		domainError := model.NewError(
			model.ErrorConfirmationRequired,
			model.ExitConflict,
			"Cleanup requires approval before deletion.",
			nil,
		)
		domainError.Details["candidate_count"] = result.Data.Summary.Candidate
		return result, domainError
	}
	result.Data.Approved = true
	for index := range result.Data.Items {
		if result.Data.Items[index].Action != model.CleanupActionCandidate {
			continue
		}
		repository, exists := repositories[result.Data.Items[index].Worktree.RepositoryID]
		if !exists {
			return result, internalError("A cleanup repository record is missing.", nil)
		}
		item, warnings, failures, err := service.removeCleanupItem(
			ctx,
			result.Data.Items[index],
			repository,
			result.Data.CutoffAt,
			options.AllowIgnored,
		)
		result.Data.Items[index] = item
		result.Warnings = append(result.Warnings, warnings...)
		result.Failures = append(result.Failures, failures...)
		if err != nil {
			return result, err
		}
	}
	result.Data.Summary = summarizeCleanup(result.Data.Items)
	return result, nil
}

func (service *Service) planCleanup(ctx context.Context, options CleanupOptions) (model.Result[model.CleanupData], map[int64]model.Repository, error) {
	result := model.NewResult("cleanup", model.CleanupData{
		DryRun: options.DryRun,
		Items:  []model.CleanupItem{},
	})
	age, err := ParseAge(options.OlderThan)
	if err != nil {
		return result, nil, err
	}
	if err := service.git.CheckVersion(ctx); err != nil {
		return result, nil, err
	}
	result.Data.CutoffAt = service.now().Add(-age)

	filter := store.Filter{States: []model.WorktreeState{model.WorktreeStateActive}}
	if options.Repository != "" {
		repository, err := service.git.DetectRepository(ctx, options.Repository)
		if err != nil {
			return result, nil, err
		}
		filter.RepositoryCommonDir = &repository.CommonDir
	}
	worktrees, err := service.store.List(ctx, filter)
	if err != nil {
		return result, nil, storeError("Grove could not read worktrees for cleanup.", err)
	}
	repositories, err := service.repositoriesByID(ctx, "Grove could not read repositories for cleanup.")
	if err != nil {
		return result, nil, err
	}

	for _, worktree := range worktrees {
		item := model.CleanupItem{Worktree: worktree}
		if worktree.LastGroveActivityAt.After(result.Data.CutoffAt) {
			item.Action = model.CleanupActionSkipped
			item.Reason = model.RemovalReasonNotOld
			result.Data.Items = append(result.Data.Items, item)
			result.Warnings = append(result.Warnings, cleanupIssue(worktree, cleanupDecision{
				reason:  model.RemovalReasonNotOld,
				code:    model.IssueCleanupRecent,
				message: "The worktree has recent Grove activity.",
			}))
			continue
		}
		repository, exists := repositories[worktree.RepositoryID]
		if !exists {
			return result, nil, internalError("A cleanup repository record is missing.", nil)
		}
		decision := service.inspectCleanup(ctx, worktree, repository, options.AllowIgnored)
		if !decision.safe {
			item.Action = model.CleanupActionSkipped
			item.Reason = decision.reason
			result.Data.Items = append(result.Data.Items, item)
			result.Warnings = append(result.Warnings, cleanupIssue(worktree, decision))
			continue
		}
		gitDirectory := decision.gitDirectory
		item.Worktree.RemovalGitDirectory = &gitDirectory
		measurement := size.Measure(worktree.Path)
		measuredAt := service.now()
		item.Worktree.SizeBytes = int64Pointer(measurement.Bytes)
		item.Worktree.SizeComplete = measurement.Complete
		item.Worktree.SizeMeasuredAt = &measuredAt
		item.FinalSizeBytes = int64Pointer(measurement.Bytes)
		item.Action = model.CleanupActionCandidate
		item.Reason = model.RemovalReasonOldAndClean
		result.Data.Items = append(result.Data.Items, item)
		addIssues(&result.Warnings, &result.Failures, worktree, measurement.Warnings, measurement.Complete)
	}
	result.Data.Summary = summarizeCleanup(result.Data.Items)
	return result, repositories, nil
}

func (service *Service) inspectCleanup(ctx context.Context, worktree model.Worktree, repository model.Repository, allowIgnored bool) cleanupDecision {
	return service.inspectCleanupPath(ctx, worktree, repository, allowIgnored, worktree.Path)
}

func (service *Service) inspectCleanupPath(
	ctx context.Context,
	worktree model.Worktree,
	repository model.Repository,
	allowIgnored bool,
	path string,
) cleanupDecision {
	if !paths.IsChild(worktree.CreationRoot, path) {
		return blockedCleanup(model.RemovalReasonOutsideRoot, model.IssueCleanupOutsideRoot, "The worktree path is outside its creation root.")
	}
	canonical, err := paths.Canonical(path)
	if err != nil || !samePath(canonical, path) || !paths.IsChild(worktree.CreationRoot, canonical) {
		return blockedCleanup(model.RemovalReasonOutsideRoot, model.IssueCleanupOutsideRoot, "Grove could not prove that the worktree is inside its creation root.")
	}
	diskPresent, err := worktreeDirectoryPresent(path)
	if err != nil || !diskPresent {
		return blockedCleanup(model.RemovalReasonStatusError, model.IssueCleanupStatusError, "Grove could not confirm the worktree directory.")
	}
	gitWorktree, gitPresent, err := service.findGitWorktree(ctx, repository, path)
	if err != nil || !gitPresent {
		return blockedCleanup(model.RemovalReasonStatusError, model.IssueCleanupStatusError, "Grove could not confirm the Git worktree.")
	}
	if worktree.RemovalGitDirectory != nil &&
		(!samePath(worktree.RemovalGitDirectory.Path, gitWorktree.GitDirectory.Path) ||
			worktree.RemovalGitDirectory.Token != gitWorktree.GitDirectory.Token) {
		return blockedCleanup(model.RemovalReasonStateChange, model.IssueCleanupStateChanged, "The Git worktree identity changed.")
	}
	if gitWorktree.Main || samePath(repository.MainCheckout, path) {
		return blockedCleanup(model.RemovalReasonMain, model.IssueCleanupStatusError, "Cleanup never removes the main checkout.")
	}
	if gitWorktree.Locked {
		return blockedCleanup(model.RemovalReasonLocked, model.IssueCleanupLocked, "The Git worktree is locked.")
	}
	status, err := service.git.Status(ctx, path)
	if err != nil {
		return blockedCleanup(model.RemovalReasonStatusError, model.IssueCleanupStatusError, "Grove could not read the Git worktree status.")
	}
	if status.Staged || status.Modified || status.Untracked {
		return blockedCleanup(model.RemovalReasonDirty, model.IssueCleanupDirty, "The Git worktree contains changes.")
	}
	if status.Ignored && !allowIgnored {
		return blockedCleanup(model.RemovalReasonIgnored, model.IssueCleanupIgnored, "The Git worktree contains ignored files.")
	}
	return cleanupDecision{safe: true, reason: model.RemovalReasonOldAndClean, gitDirectory: gitWorktree.GitDirectory}
}

func (service *Service) removeCleanupItem(
	ctx context.Context,
	item model.CleanupItem,
	repository model.Repository,
	cutoff time.Time,
	allowIgnored bool,
) (model.CleanupItem, []model.Issue, []model.Issue, error) {
	warnings := []model.Issue{}
	failures := []model.Issue{}
	token, err := service.token()
	if err != nil {
		return item, warnings, failures, internalError("Grove could not create a cleanup operation token.", err)
	}
	operationLock, err := service.locks.AcquireOperation(token)
	if err != nil {
		return item, warnings, failures, internalError("Grove could not acquire the cleanup operation lock.", err)
	}
	operationOwned := true
	defer func() {
		if operationOwned {
			_ = operationLock.Unlock()
		}
	}()

	if service.hooks.BeforeRemovalReserve != nil {
		service.hooks.BeforeRemovalReserve(ctx, item.Worktree)
	}
	currentGitDirectory, identityErr := service.git.WorktreeGitDirectory(ctx, item.Worktree.Path)
	if identityErr != nil || item.Worktree.RemovalGitDirectory == nil ||
		!samePath(currentGitDirectory.Path, item.Worktree.RemovalGitDirectory.Path) ||
		currentGitDirectory.Token != item.Worktree.RemovalGitDirectory.Token {
		item.Action = model.CleanupActionSkipped
		item.Reason = model.RemovalReasonStateChange
		item.FinalSizeBytes = nil
		warnings = append(warnings, cleanupIssue(item.Worktree, blockedCleanup(
			model.RemovalReasonStateChange,
			model.IssueCleanupStateChanged,
			"The Git worktree identity changed before cleanup reserved it.",
		)))
		if unlockErr := operationLock.Unlock(); unlockErr != nil {
			return item, warnings, failures, internalError("Grove could not release the cleanup operation lock.", unlockErr)
		}
		operationOwned = false
		return item, warnings, failures, nil
	}
	reserved, err := service.store.ReserveRemoval(ctx, store.RemoveReservation{
		WorktreeID:         item.Worktree.ID,
		OperationToken:     token,
		OperationStartedAt: service.now(),
		ObservedActivityAt: item.Worktree.LastGroveActivityAt,
		CutoffAt:           cutoff,
		Reason:             model.RemovalReasonOldAndClean,
		GitDirectory:       currentGitDirectory,
	})
	if err != nil {
		if errors.Is(err, store.ErrStateChanged) || errors.Is(err, store.ErrNotActive) || errors.Is(err, store.ErrNotFound) {
			if current, getErr := service.store.Get(ctx, item.Worktree.ID); getErr == nil {
				item.Worktree = current
			}
			item.Action = model.CleanupActionSkipped
			item.Reason = model.RemovalReasonStateChange
			item.FinalSizeBytes = nil
			warnings = append(warnings, cleanupIssue(item.Worktree, blockedCleanup(
				model.RemovalReasonStateChange,
				model.IssueCleanupStateChanged,
				"The worktree state or activity time changed before cleanup reserved it.",
			)))
			if unlockErr := operationLock.Unlock(); unlockErr != nil {
				return item, warnings, failures, internalError("Grove could not release the cleanup operation lock.", unlockErr)
			}
			operationOwned = false
			return item, warnings, failures, nil
		}
		return item, warnings, failures, storeError("Grove could not reserve the worktree removal.", err)
	}
	if service.hooks.AfterRemovalReserved != nil {
		service.hooks.AfterRemovalReserved(ctx, reserved)
	}

	decision := service.inspectCleanup(ctx, reserved, repository, allowIgnored)
	if !decision.safe {
		if err := service.store.CancelRemoval(ctx, reserved.ID, token); err != nil {
			return item, warnings, failures, storeError("Grove could not cancel an unsafe worktree removal.", err)
		}
		item.Action = model.CleanupActionSkipped
		item.Reason = decision.reason
		warnings = append(warnings, cleanupIssue(reserved, decision))
		if err := operationLock.Unlock(); err != nil {
			return item, warnings, failures, internalError("Grove could not release the cleanup operation lock.", err)
		}
		operationOwned = false
		return item, warnings, failures, nil
	}
	quarantinePath, err := cleanupQuarantinePath(reserved, token)
	if err != nil {
		if cancelErr := service.store.CancelRemoval(ctx, reserved.ID, token); cancelErr != nil {
			return item, warnings, failures, storeError("Grove could not cancel a blocked worktree removal.", cancelErr)
		}
		item.Action = model.CleanupActionFailed
		item.Reason = model.RemovalReasonRemoveFail
		failures = append(failures, cleanupIssue(reserved, blockedCleanup(
			model.RemovalReasonRemoveFail,
			model.IssueCleanupRemoveFailed,
			"Grove could not prepare a private cleanup path.",
		)))
		return item, warnings, failures, nil
	}
	if err := service.confirmOwnedOperation(ctx, operationLock, reserved, model.WorktreeStateRemoving, token); err != nil {
		return item, warnings, failures, err
	}
	if err := service.git.MoveWorktree(ctx, repository.MainCheckout, reserved.Path, quarantinePath); err != nil {
		originalGit, originalGitPresent, originalGitErr := service.findGitWorktree(ctx, repository, reserved.Path)
		originalDiskPresent, originalDiskErr := worktreeDirectoryPresent(reserved.Path)
		_, quarantineGitPresent, quarantineGitErr := service.findGitWorktree(ctx, repository, quarantinePath)
		quarantineDiskPresent, quarantineDiskErr := worktreeDirectoryPresent(quarantinePath)
		if originalGitErr == nil && originalDiskErr == nil && quarantineGitErr == nil && quarantineDiskErr == nil &&
			originalGitPresent && originalDiskPresent && !quarantineGitPresent && !quarantineDiskPresent &&
			matchesRemovalIdentity(reserved, originalGit) {
			if cancelErr := service.store.CancelRemoval(ctx, reserved.ID, token); cancelErr != nil {
				return item, warnings, failures, storeError("Grove could not restore the failed worktree move.", cancelErr)
			}
		} else if updateErr := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
			WorktreeID:     reserved.ID,
			FromState:      model.WorktreeStateRemoving,
			State:          model.WorktreeStateManualReview,
			OperationToken: &token,
		}); updateErr != nil {
			return item, warnings, failures, storeError("Grove could not quarantine the uncertain worktree move.", updateErr)
		}
		item.Action = model.CleanupActionFailed
		item.Reason = model.RemovalReasonRemoveFail
		failures = append(failures, cleanupIssueAtPath(reserved, quarantinePath, blockedCleanup(
			model.RemovalReasonRemoveFail,
			model.IssueCleanupRemoveFailed,
			"Grove could not move the worktree to a private cleanup path.",
		)))
		return item, warnings, failures, nil
	}

	quarantined := reserved
	quarantined.Path = quarantinePath
	if service.hooks.AfterWorktreeQuarantined != nil {
		service.hooks.AfterWorktreeQuarantined(ctx, quarantined)
	}
	decision = service.inspectCleanupPath(ctx, reserved, repository, allowIgnored, quarantinePath)
	if !decision.safe {
		if decision.reason == model.RemovalReasonStateChange {
			if updateErr := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
				WorktreeID:     reserved.ID,
				FromState:      model.WorktreeStateRemoving,
				State:          model.WorktreeStateManualReview,
				OperationToken: &token,
			}); updateErr != nil {
				return item, warnings, failures, storeError("Grove could not quarantine the changed cleanup worktree.", updateErr)
			}
			item.Action = model.CleanupActionSkipped
			item.Reason = decision.reason
			warnings = append(warnings, cleanupIssueAtPath(reserved, quarantinePath, decision))
			return item, warnings, failures, nil
		}
		if err := service.git.MoveWorktree(ctx, repository.MainCheckout, quarantinePath, reserved.Path); err != nil {
			if updateErr := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
				WorktreeID:     reserved.ID,
				FromState:      model.WorktreeStateRemoving,
				State:          model.WorktreeStateManualReview,
				OperationToken: &token,
			}); updateErr != nil {
				return item, warnings, failures, storeError("Grove could not quarantine the failed cleanup restore.", updateErr)
			}
			item.Action = model.CleanupActionFailed
			item.Reason = model.RemovalReasonRemoveFail
			failures = append(failures, cleanupIssueAtPath(reserved, quarantinePath, blockedCleanup(
				model.RemovalReasonRemoveFail,
				model.IssueCleanupRemoveFailed,
				"Grove could not restore the worktree from its private cleanup path.",
			)))
			return item, warnings, failures, nil
		}
		if err := service.store.CancelRemoval(ctx, reserved.ID, token); err != nil {
			return item, warnings, failures, storeError("Grove could not cancel an unsafe worktree removal.", err)
		}
		item.Action = model.CleanupActionSkipped
		item.Reason = decision.reason
		warnings = append(warnings, cleanupIssue(reserved, decision))
		return item, warnings, failures, nil
	}

	measurement := size.Measure(quarantinePath)
	measuredAt := service.now()
	item.FinalSizeBytes = int64Pointer(measurement.Bytes)
	addIssues(&warnings, &failures, quarantined, measurement.Warnings, measurement.Complete)
	if service.hooks.BeforeGitRemove != nil {
		service.hooks.BeforeGitRemove(ctx, quarantined)
	}
	if err := service.confirmOwnedOperation(ctx, operationLock, reserved, model.WorktreeStateRemoving, token); err != nil {
		return item, warnings, failures, err
	}
	removeErr := service.git.RemoveWorktree(ctx, repository.MainCheckout, quarantinePath)
	if service.hooks.AfterGitRemove != nil {
		service.hooks.AfterGitRemove(ctx, quarantined, removeErr)
	}

	remainingGitWorktree, gitPresent, gitErr := service.findGitWorktree(ctx, repository, quarantinePath)
	diskPresent, diskErr := worktreeDirectoryPresent(quarantinePath)
	originalPresent, originalErr := worktreeDirectoryPresent(reserved.Path)
	if gitErr == nil && diskErr == nil && originalErr == nil && !gitPresent && !diskPresent && !originalPresent {
		if err := service.store.CompleteRemoval(ctx, reserved.ID, token, store.RemovalResult{
			RemovedAt:      service.now(),
			Reason:         model.RemovalReasonOldAndClean,
			SizeBytes:      int64Pointer(measurement.Bytes),
			SizeComplete:   measurement.Complete,
			SizeMeasuredAt: &measuredAt,
		}); err != nil {
			return item, warnings, failures, storeError("Grove could not complete the worktree removal.", err)
		}
		removed, err := service.store.Get(ctx, reserved.ID)
		if err != nil {
			return item, warnings, failures, storeError("Grove could not read the removed worktree.", err)
		}
		item.Worktree = removed
		item.Action = model.CleanupActionDeleted
		item.Reason = model.RemovalReasonOldAndClean
		if err := operationLock.Unlock(); err != nil {
			return item, warnings, failures, internalError("Grove could not release the cleanup operation lock.", err)
		}
		operationOwned = false
		return item, warnings, failures, nil
	}

	if gitErr == nil && diskErr == nil && originalErr == nil && gitPresent && diskPresent && !originalPresent &&
		matchesRemovalIdentity(reserved, remainingGitWorktree) {
		if err := service.git.MoveWorktree(ctx, repository.MainCheckout, quarantinePath, reserved.Path); err == nil {
			if cancelErr := service.store.CancelRemoval(ctx, reserved.ID, token); cancelErr != nil {
				return item, warnings, failures, storeError("Grove could not restore the failed worktree removal.", cancelErr)
			}
		} else if updateErr := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
			WorktreeID:     reserved.ID,
			FromState:      model.WorktreeStateRemoving,
			State:          model.WorktreeStateManualReview,
			OperationToken: &token,
		}); updateErr != nil {
			return item, warnings, failures, storeError("Grove could not quarantine the uncertain worktree removal.", updateErr)
		}
	} else if err := service.store.UpdateReconciled(ctx, store.ReconcileUpdate{
		WorktreeID:     reserved.ID,
		FromState:      model.WorktreeStateRemoving,
		State:          model.WorktreeStateManualReview,
		OperationToken: &token,
	}); err != nil {
		return item, warnings, failures, storeError("Grove could not quarantine the uncertain worktree removal.", err)
	}
	if current, getErr := service.store.Get(ctx, reserved.ID); getErr == nil {
		item.Worktree = current
	}
	item.Action = model.CleanupActionFailed
	item.Reason = model.RemovalReasonRemoveFail
	failures = append(failures, cleanupIssueAtPath(reserved, quarantinePath, blockedCleanup(
		model.RemovalReasonRemoveFail,
		model.IssueCleanupRemoveFailed,
		"Grove could not remove the Git worktree.",
	)))
	if err := operationLock.Unlock(); err != nil {
		return item, warnings, failures, internalError("Grove could not release the cleanup operation lock.", err)
	}
	operationOwned = false
	return item, warnings, failures, nil
}

func invalidAge(value string) *model.Error {
	domainError := model.NewError(
		model.ErrorInvalidAge,
		model.ExitInvalidArguments,
		"The cleanup age must be a positive number with an h, d, or w suffix.",
		nil,
	)
	domainError.Details["value"] = value
	return domainError
}

func blockedCleanup(reason model.RemovalReason, code model.IssueCode, message string) cleanupDecision {
	return cleanupDecision{reason: reason, code: code, message: message}
}

func cleanupQuarantinePath(worktree model.Worktree, token string) (string, error) {
	path := filepath.Join(filepath.Dir(worktree.Path), ".grove-removing-"+token)
	if !paths.IsChild(worktree.CreationRoot, path) {
		return "", errors.New("cleanup path is outside the creation root")
	}
	present, err := pathPresent(path)
	if err != nil {
		return "", err
	}
	if present {
		return "", errors.New("cleanup path already exists")
	}
	canonical, err := paths.CanonicalForCreation(path)
	if err != nil {
		return "", err
	}
	if !samePath(canonical, path) || !paths.IsChild(worktree.CreationRoot, canonical) {
		return "", errors.New("cleanup path resolves outside the creation root")
	}
	return path, nil
}

func cleanupIssue(worktree model.Worktree, decision cleanupDecision) model.Issue {
	return cleanupIssueAtPath(worktree, worktree.Path, decision)
}

func cleanupIssueAtPath(worktree model.Worktree, path string, decision cleanupDecision) model.Issue {
	id := worktree.ID
	return model.NewIssue(decision.code, decision.message, &path, &id)
}

func summarizeCleanup(items []model.CleanupItem) model.CleanupSummary {
	summary := model.CleanupSummary{}
	for _, item := range items {
		switch item.Action {
		case model.CleanupActionCandidate:
			summary.Candidate++
		case model.CleanupActionDeleted:
			summary.Deleted++
		case model.CleanupActionSkipped:
			summary.Skipped++
		case model.CleanupActionFailed:
			summary.Failed++
		}
	}
	return summary
}

func int64Pointer(value int64) *int64 {
	return &value
}
