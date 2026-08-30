package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/del-boy/grove/internal/model"
)

func (store *Store) UpdateReconciled(ctx context.Context, update ReconcileUpdate) error {
	if err := validateReconcileUpdate(update); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start reconciliation update", err)
	}
	defer tx.Rollback()

	var currentState model.WorktreeState
	var currentToken sql.NullString
	var currentReason sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state, operation_token, removal_reason FROM worktrees WHERE id = ?`,
		update.WorktreeID).Scan(&currentState, &currentToken, &currentReason)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return wrapDatabaseError("read reconciliation state", err)
	}
	if currentState != update.FromState {
		return ErrStateChanged
	}
	if currentState == model.WorktreeStateCreating || currentState == model.WorktreeStateRemoving {
		if update.OperationToken == nil || !currentToken.Valid || *update.OperationToken != currentToken.String {
			return ErrOperationToken
		}
	} else if update.OperationToken != nil || currentToken.Valid {
		return ErrOperationToken
	}

	removedAt, reason, err := reconciledRemovalValues(update, currentReason)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = ?,
		branch = ?,
		detached_commit = ?,
		locked = ?,
		removed_at = ?,
		removal_reason = ?,
		removal_git_dir = NULL,
		removal_git_identity = NULL,
		operation_token = NULL,
		operation_started_at = NULL
	WHERE id = ?`,
		update.State,
		update.Branch,
		update.DetachedCommit,
		boolInteger(update.Locked),
		removedAt,
		reason,
		update.WorktreeID,
	); err != nil {
		return wrapDatabaseError("update reconciled worktree", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit reconciliation update", err)
	}
	return nil
}

func (store *Store) UpdateBootstrap(ctx context.Context, update BootstrapUpdate) error {
	if err := validateBootstrapUpdate(update); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start bootstrap update", err)
	}
	defer tx.Rollback()

	var bootstrapState model.BootstrapState
	err = tx.QueryRowContext(ctx, `SELECT bootstrap_state FROM worktrees WHERE id = ?`, update.WorktreeID).Scan(
		&bootstrapState,
	)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return wrapDatabaseError("read bootstrap state", err)
	}
	if bootstrapState != update.FromState {
		return ErrStateChanged
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		bootstrap_state = ?,
		bootstrap_script = ?,
		bootstrap_source = ?,
		bootstrap_exit_code = ?,
		bootstrap_started_at = ?,
		bootstrap_finished_at = ?
	WHERE id = ?`,
		update.State,
		update.Script,
		update.Source,
		update.ExitCode,
		formatOptionalTime(update.StartedAt),
		formatOptionalTime(update.FinishedAt),
		update.WorktreeID,
	); err != nil {
		return wrapDatabaseError("update bootstrap state", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit bootstrap update", err)
	}
	return nil
}

func (store *Store) UpdateSize(ctx context.Context, update SizeUpdate) error {
	if update.Bytes < 0 {
		return errors.New("size must not be negative")
	}
	if err := validateTime("size measurement time", update.MeasuredAt); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE worktrees SET
		size_bytes = ?,
		size_complete = ?,
		size_measured_at = ?
	WHERE id = ? AND state = 'active'`,
		update.Bytes,
		boolInteger(update.Complete),
		formatTime(update.MeasuredAt),
		update.WorktreeID,
	)
	if err != nil {
		return wrapDatabaseError("update worktree size", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return wrapDatabaseError("read size update result", err)
	}
	if changed == 1 {
		return nil
	}
	return store.notActiveError(ctx, update.WorktreeID)
}

func (store *Store) notActiveError(ctx context.Context, id int64) error {
	var state model.WorktreeState
	err := store.db.QueryRowContext(ctx, `SELECT state FROM worktrees WHERE id = ?`, id).Scan(&state)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return wrapDatabaseError("read worktree state", err)
	}
	return ErrNotActive
}

func validateReconcileUpdate(update ReconcileUpdate) error {
	if err := model.ValidateWorktreeState(update.FromState); err != nil {
		return err
	}
	if err := model.ValidateWorktreeState(update.State); err != nil {
		return err
	}
	if err := validateGitPointers(update.Branch, update.DetachedCommit); err != nil {
		return err
	}
	allowed := false
	switch update.FromState {
	case model.WorktreeStateCreating:
		allowed = update.State == model.WorktreeStateActive ||
			update.State == model.WorktreeStateCreateFailed ||
			update.State == model.WorktreeStateManualReview
	case model.WorktreeStateRemoving:
		allowed = update.State == model.WorktreeStateActive ||
			update.State == model.WorktreeStateRemoved ||
			update.State == model.WorktreeStateManualReview
	case model.WorktreeStateActive, model.WorktreeStateMissing:
		allowed = update.State == model.WorktreeStateActive || update.State == model.WorktreeStateMissing
	}
	if !allowed {
		return fmt.Errorf("reconciliation cannot change state from %q to %q", update.FromState, update.State)
	}
	if update.FromState == model.WorktreeStateCreating || update.FromState == model.WorktreeStateRemoving {
		if update.OperationToken == nil || *update.OperationToken == "" {
			return errors.New("recovery update requires an operation token")
		}
	} else if update.OperationToken != nil {
		return errors.New("generic reconciliation cannot use an operation token")
	}
	if update.RemovalReason != nil {
		if err := model.ValidateRemovalReason(*update.RemovalReason); err != nil {
			return err
		}
	}
	if update.State == model.WorktreeStateRemoved {
		if update.RemovedAt == nil {
			return errors.New("removed reconciliation state requires a removal time")
		}
		if err := validateTime("removal time", *update.RemovedAt); err != nil {
			return err
		}
	} else if update.RemovedAt != nil {
		return errors.New("only removed reconciliation state can set a removal time")
	}
	return nil
}

func reconciledRemovalValues(update ReconcileUpdate, currentReason sql.NullString) (any, any, error) {
	if update.State == model.WorktreeStateActive || update.State == model.WorktreeStateMissing || update.State == model.WorktreeStateCreateFailed {
		return nil, nil, nil
	}
	var reason *model.RemovalReason
	if update.RemovalReason != nil {
		reason = update.RemovalReason
	} else if currentReason.Valid {
		value := model.RemovalReason(currentReason.String)
		if err := model.ValidateRemovalReason(value); err != nil {
			return nil, nil, err
		}
		reason = &value
	}
	if update.State == model.WorktreeStateRemoved && reason == nil {
		return nil, nil, errors.New("removed reconciliation state requires a removal reason")
	}
	return formatOptionalTime(update.RemovedAt), reason, nil
}

func validateBootstrapUpdate(update BootstrapUpdate) error {
	if err := model.ValidateBootstrapState(update.FromState); err != nil {
		return err
	}
	if err := model.ValidateBootstrapState(update.State); err != nil {
		return err
	}
	if err := model.ValidateValueSource(update.Source); err != nil {
		return err
	}
	if err := validateOptionalString("bootstrap script", update.Script); err != nil {
		return err
	}
	allowed := false
	switch update.FromState {
	case model.BootstrapStatePending:
		allowed = update.State == model.BootstrapStateDisabled ||
			update.State == model.BootstrapStateNotPresent ||
			update.State == model.BootstrapStateRunning ||
			update.State == model.BootstrapStateFailed ||
			update.State == model.BootstrapStateInterrupted
	case model.BootstrapStateRunning:
		allowed = update.State == model.BootstrapStateSucceeded ||
			update.State == model.BootstrapStateFailed ||
			update.State == model.BootstrapStateInterrupted
	}
	if !allowed {
		return fmt.Errorf("bootstrap cannot change state from %q to %q", update.FromState, update.State)
	}
	if update.State == model.BootstrapStateRunning && update.StartedAt == nil {
		return errors.New("running bootstrap state requires a start time")
	}
	if update.State == model.BootstrapStateSucceeded || update.State == model.BootstrapStateFailed || update.State == model.BootstrapStateInterrupted {
		if update.FinishedAt == nil {
			return errors.New("terminal bootstrap state requires a finish time")
		}
	}
	if update.StartedAt != nil {
		if err := validateTime("bootstrap start time", *update.StartedAt); err != nil {
			return err
		}
	}
	if update.FinishedAt != nil {
		if err := validateTime("bootstrap finish time", *update.FinishedAt); err != nil {
			return err
		}
	}
	if update.State == model.BootstrapStateSucceeded {
		if update.ExitCode == nil || *update.ExitCode != 0 {
			return errors.New("successful bootstrap state requires exit code zero")
		}
	}
	if update.State == model.BootstrapStateFailed && update.ExitCode != nil && *update.ExitCode == 0 {
		return errors.New("failed bootstrap state cannot use exit code zero")
	}
	if update.State == model.BootstrapStateRunning && (update.ExitCode != nil || update.FinishedAt != nil) {
		return errors.New("running bootstrap state cannot have a result")
	}
	if (update.State == model.BootstrapStateDisabled || update.State == model.BootstrapStateNotPresent) &&
		(update.ExitCode != nil || update.StartedAt != nil || update.FinishedAt != nil) {
		return errors.New("bootstrap state without execution cannot have execution details")
	}
	return nil
}
