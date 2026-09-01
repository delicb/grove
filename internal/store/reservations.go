package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
)

func (store *Store) ReserveRepository(
	ctx context.Context,
	repository model.RepositoryInfo,
	seenAt time.Time,
) (model.Repository, error) {
	if repository.CommonDir == "" || repository.MainCheckout == "" || repository.DisplayName == "" {
		return model.Repository{}, errors.New("repository fields must not be empty")
	}
	if err := validateTime("repository seen time", seenAt); err != nil {
		return model.Repository{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Repository{}, wrapDatabaseError("start repository reservation", err)
	}
	defer tx.Rollback()
	reserved, err := allocateRepository(ctx, tx, CreateReservation{Repository: repository, CreatedAt: seenAt})
	if err != nil {
		return model.Repository{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Repository{}, wrapDatabaseError("commit repository reservation", err)
	}
	return reserved, nil
}

func (store *Store) ReserveCreate(ctx context.Context, request CreateReservation) (model.Worktree, error) {
	if err := validateCreateReservation(request); err != nil {
		return model.Worktree{}, err
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("start create reservation", err)
	}
	defer tx.Rollback()

	repository, err := allocateRepository(ctx, tx, request)
	if err != nil {
		return model.Worktree{}, err
	}
	path := filepath.Join(request.CreationRoot, repository.DirectoryKey, request.Name)
	result, err := tx.ExecContext(ctx, `INSERT INTO worktrees (
		repository_id,
		name,
		creation_root,
		path,
		requested_base,
		requested_branch,
		expected_commit,
		creator_agent,
		created_at,
		last_grove_activity_at,
		state,
		bootstrap_state,
		bootstrap_script,
		bootstrap_source,
		operation_token,
		operation_started_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'creating', 'pending', ?, ?, ?, ?)`,
		repository.ID,
		request.Name,
		request.CreationRoot,
		path,
		request.RequestedBase,
		request.RequestedBranch,
		request.ExpectedCommit,
		request.CreatorAgent,
		formatTime(request.CreatedAt),
		formatTime(request.CreatedAt),
		request.BootstrapScript,
		request.BootstrapSource,
		request.OperationToken,
		formatTime(request.OperationStartedAt),
	)
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("reserve worktree creation", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("read created worktree ID", err)
	}
	worktree, err := scanWorktree(tx.QueryRowContext(ctx, worktreeSelect+` WHERE w.id = ?`, id))
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("read create reservation", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Worktree{}, wrapDatabaseError("commit create reservation", err)
	}
	return worktree, nil
}

func (store *Store) CompleteCreate(ctx context.Context, id int64, token string, gitWorktree model.GitWorktree) error {
	if token == "" {
		return errors.New("operation token must not be empty")
	}
	if gitWorktree.Path == "" {
		return errors.New("git worktree path must not be empty")
	}
	if err := validateGitPointers(gitWorktree.Branch, gitWorktree.DetachedCommit); err != nil {
		return err
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start create completion", err)
	}
	defer tx.Rollback()
	path, err := requireOperation(ctx, tx, id, model.WorktreeStateCreating, token)
	if err != nil {
		return err
	}
	if path != gitWorktree.Path {
		return fmt.Errorf("%w: Git worktree path does not match the reservation", ErrStateChanged)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = 'active',
		branch = ?,
		detached_commit = ?,
		locked = ?,
		operation_token = NULL,
		operation_started_at = NULL
	WHERE id = ?`,
		gitWorktree.Branch,
		gitWorktree.DetachedCommit,
		boolInteger(gitWorktree.Locked),
		id,
	); err != nil {
		return wrapDatabaseError("complete worktree creation", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit create completion", err)
	}
	return nil
}

func (store *Store) FailCreate(ctx context.Context, id int64, token string, state model.WorktreeState) error {
	if state != model.WorktreeStateCreateFailed && state != model.WorktreeStateManualReview {
		return fmt.Errorf("invalid create failure state %q", state)
	}
	if token == "" {
		return errors.New("operation token must not be empty")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start create failure update", err)
	}
	defer tx.Rollback()
	if _, err := requireOperation(ctx, tx, id, model.WorktreeStateCreating, token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = ?,
		locked = 0,
		operation_token = NULL,
		operation_started_at = NULL
	WHERE id = ?`, state, id); err != nil {
		return wrapDatabaseError("record create failure", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit create failure", err)
	}
	return nil
}

func (store *Store) ConfirmOperation(ctx context.Context, id int64, state model.WorktreeState, token string) error {
	if state != model.WorktreeStateCreating && state != model.WorktreeStateRemoving {
		return fmt.Errorf("state %q does not own an operation token", state)
	}
	if token == "" {
		return errors.New("operation token must not be empty")
	}
	var currentState model.WorktreeState
	var currentToken sql.NullString
	err := store.db.QueryRowContext(ctx, `SELECT state, operation_token FROM worktrees WHERE id = ?`, id).Scan(
		&currentState,
		&currentToken,
	)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return wrapDatabaseError("confirm worktree operation", err)
	}
	if currentState != state {
		return ErrStateChanged
	}
	if !currentToken.Valid || currentToken.String != token {
		return ErrOperationToken
	}
	return nil
}

func (store *Store) ReserveRemoval(ctx context.Context, request RemoveReservation) (model.Worktree, error) {
	if request.OperationToken == "" {
		return model.Worktree{}, errors.New("operation token must not be empty")
	}
	if err := validateTime("operation start time", request.OperationStartedAt); err != nil {
		return model.Worktree{}, err
	}
	if err := validateTime("observed activity time", request.ObservedActivityAt); err != nil {
		return model.Worktree{}, err
	}
	if err := validateTime("cleanup cutoff time", request.CutoffAt); err != nil {
		return model.Worktree{}, err
	}
	if err := model.ValidateRemovalReason(request.Reason); err != nil {
		return model.Worktree{}, err
	}
	if request.GitDirectory.Path == "" || request.GitDirectory.Token == "" {
		return model.Worktree{}, errors.New("removal Git directory identity must not be empty")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("start removal reservation", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = 'removing',
		removal_reason = ?,
		removal_git_dir = ?,
		removal_git_identity = ?,
		operation_token = ?,
		operation_started_at = ?
	WHERE id = ?
		AND state = 'active'
		AND operation_token IS NULL
		AND last_grove_activity_at = ?
		AND last_grove_activity_at <= ?`,
		request.Reason,
		request.GitDirectory.Path,
		request.GitDirectory.Token,
		request.OperationToken,
		formatTime(request.OperationStartedAt),
		request.WorktreeID,
		formatTime(request.ObservedActivityAt),
		formatTime(request.CutoffAt),
	)
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("reserve worktree removal", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("read removal reservation result", err)
	}
	if changed != 1 {
		return model.Worktree{}, diagnoseRemovalReservation(ctx, tx, request)
	}
	worktree, err := scanWorktree(tx.QueryRowContext(ctx, worktreeSelect+` WHERE w.id = ?`, request.WorktreeID))
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("read removal reservation", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Worktree{}, wrapDatabaseError("commit removal reservation", err)
	}
	return worktree, nil
}

func (store *Store) CompleteRemoval(ctx context.Context, id int64, token string, result RemovalResult) error {
	if token == "" {
		return errors.New("operation token must not be empty")
	}
	if err := validateTime("removal time", result.RemovedAt); err != nil {
		return err
	}
	if err := model.ValidateRemovalReason(result.Reason); err != nil {
		return err
	}
	if err := validateRemovalSize(result); err != nil {
		return err
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start removal completion", err)
	}
	defer tx.Rollback()
	if _, err := requireOperation(ctx, tx, id, model.WorktreeStateRemoving, token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = 'removed',
		locked = 0,
		removed_at = ?,
		removal_reason = ?,
		removal_git_dir = NULL,
		removal_git_identity = NULL,
		size_bytes = ?,
		size_complete = ?,
		size_measured_at = ?,
		operation_token = NULL,
		operation_started_at = NULL
	WHERE id = ?`,
		formatTime(result.RemovedAt),
		result.Reason,
		result.SizeBytes,
		boolInteger(result.SizeComplete),
		formatOptionalTime(result.SizeMeasuredAt),
		id,
	); err != nil {
		return wrapDatabaseError("complete worktree removal", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit removal completion", err)
	}
	return nil
}

func (store *Store) CancelRemoval(ctx context.Context, id int64, token string) error {
	if token == "" {
		return errors.New("operation token must not be empty")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start removal cancellation", err)
	}
	defer tx.Rollback()
	if _, err := requireOperation(ctx, tx, id, model.WorktreeStateRemoving, token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET
		state = 'active',
		removal_reason = NULL,
		removal_git_dir = NULL,
		removal_git_identity = NULL,
		operation_token = NULL,
		operation_started_at = NULL
	WHERE id = ?`, id); err != nil {
		return wrapDatabaseError("cancel worktree removal", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit removal cancellation", err)
	}
	return nil
}

func (store *Store) Touch(ctx context.Context, id int64, at time.Time) (model.Worktree, time.Time, error) {
	if err := validateTime("activity time", at); err != nil {
		return model.Worktree{}, time.Time{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Worktree{}, time.Time{}, wrapDatabaseError("start worktree touch", err)
	}
	defer tx.Rollback()

	var state model.WorktreeState
	var previousValue string
	err = tx.QueryRowContext(ctx, `SELECT state, last_grove_activity_at FROM worktrees WHERE id = ?`, id).Scan(
		&state,
		&previousValue,
	)
	if err == sql.ErrNoRows {
		return model.Worktree{}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return model.Worktree{}, time.Time{}, wrapDatabaseError("read worktree activity", err)
	}
	if state != model.WorktreeStateActive {
		return model.Worktree{}, time.Time{}, ErrNotActive
	}
	previous, err := parseTime(previousValue)
	if err != nil {
		return model.Worktree{}, time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET last_grove_activity_at = ? WHERE id = ? AND state = 'active'`,
		formatTime(at), id); err != nil {
		return model.Worktree{}, time.Time{}, wrapDatabaseError("touch worktree", err)
	}
	worktree, err := scanWorktree(tx.QueryRowContext(ctx, worktreeSelect+` WHERE w.id = ?`, id))
	if err != nil {
		return model.Worktree{}, time.Time{}, wrapDatabaseError("read touched worktree", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Worktree{}, time.Time{}, wrapDatabaseError("commit worktree touch", err)
	}
	return worktree, previous, nil
}

func (store *Store) UpdateRepository(ctx context.Context, id int64, update RepositoryUpdate) error {
	if update.MainCheckout == "" {
		return errors.New("main checkout path must not be empty")
	}
	if err := validateTime("repository seen time", update.SeenAt); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE repositories SET main_checkout = ?, last_seen_at = ? WHERE id = ?`,
		update.MainCheckout, formatTime(update.SeenAt), id)
	if err != nil {
		return wrapDatabaseError("update repository", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return wrapDatabaseError("read repository update result", err)
	}
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

func allocateRepository(ctx context.Context, tx *sql.Tx, request CreateReservation) (model.Repository, error) {
	repository, err := scanRepository(tx.QueryRowContext(ctx, `SELECT
		id, common_dir, main_checkout, display_name, directory_key, first_seen_at, last_seen_at
		FROM repositories WHERE common_dir = ?`, request.Repository.CommonDir))
	if err == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE repositories SET
			main_checkout = ?, display_name = ?, last_seen_at = ? WHERE id = ?`,
			request.Repository.MainCheckout,
			request.Repository.DisplayName,
			formatTime(request.CreatedAt),
			repository.ID,
		); err != nil {
			return model.Repository{}, wrapDatabaseError("update repository reservation", err)
		}
		repository.MainCheckout = request.Repository.MainCheckout
		repository.DisplayName = request.Repository.DisplayName
		repository.LastSeenAt = request.CreatedAt.UTC()
		return repository, nil
	}
	if err != sql.ErrNoRows {
		return model.Repository{}, wrapDatabaseError("find repository reservation", err)
	}

	for _, key := range paths.RepositoryKeyCandidates(request.Repository.DisplayName, request.Repository.CommonDir) {
		result, err := tx.ExecContext(ctx, `INSERT INTO repositories (
			common_dir, main_checkout, display_name, directory_key, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
			request.Repository.CommonDir,
			request.Repository.MainCheckout,
			request.Repository.DisplayName,
			key,
			formatTime(request.CreatedAt),
			formatTime(request.CreatedAt),
		)
		if err != nil {
			if isConstraintError(err) {
				continue
			}
			return model.Repository{}, wrapDatabaseError("allocate repository key", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return model.Repository{}, wrapDatabaseError("read repository ID", err)
		}
		return model.Repository{
			ID:           id,
			CommonDir:    request.Repository.CommonDir,
			MainCheckout: request.Repository.MainCheckout,
			DisplayName:  request.Repository.DisplayName,
			DirectoryKey: key,
			FirstSeenAt:  request.CreatedAt.UTC(),
			LastSeenAt:   request.CreatedAt.UTC(),
		}, nil
	}
	return model.Repository{}, ErrConflict
}

func requireOperation(ctx context.Context, tx *sql.Tx, id int64, state model.WorktreeState, token string) (string, error) {
	var currentState model.WorktreeState
	var currentToken sql.NullString
	var path string
	err := tx.QueryRowContext(ctx, `SELECT state, operation_token, path FROM worktrees WHERE id = ?`, id).Scan(
		&currentState,
		&currentToken,
		&path,
	)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", wrapDatabaseError("read worktree operation", err)
	}
	if currentState != state {
		return "", ErrStateChanged
	}
	if !currentToken.Valid || currentToken.String != token {
		return "", ErrOperationToken
	}
	return path, nil
}

func diagnoseRemovalReservation(ctx context.Context, tx *sql.Tx, request RemoveReservation) error {
	var state model.WorktreeState
	var activity string
	err := tx.QueryRowContext(ctx, `SELECT state, last_grove_activity_at FROM worktrees WHERE id = ?`, request.WorktreeID).Scan(
		&state,
		&activity,
	)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return wrapDatabaseError("diagnose removal reservation", err)
	}
	if state != model.WorktreeStateActive {
		return fmt.Errorf("%w: %w", ErrStateChanged, ErrNotActive)
	}
	return ErrStateChanged
}

func validateCreateReservation(request CreateReservation) error {
	if request.Repository.CommonDir == "" || request.Repository.MainCheckout == "" || request.Repository.DisplayName == "" {
		return errors.New("repository fields must not be empty")
	}
	for name, value := range map[string]string{
		"repository common directory": request.Repository.CommonDir,
		"repository main checkout":    request.Repository.MainCheckout,
		"repository display name":     request.Repository.DisplayName,
		"creation root":               request.CreationRoot,
		"requested branch":            request.RequestedBranch,
		"expected commit":             request.ExpectedCommit,
		"creator agent":               request.CreatorAgent,
		"operation token":             request.OperationToken,
	} {
		if !utf8.ValidString(value) {
			return fmt.Errorf("%s must use valid UTF-8", name)
		}
		if value == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
	}
	if !filepath.IsAbs(request.CreationRoot) {
		return errors.New("creation root must be absolute")
	}
	if err := paths.ValidateWorktreeName(request.Name); err != nil {
		return err
	}
	if err := validateOptionalString("requested base", request.RequestedBase); err != nil {
		return err
	}
	if err := validateOptionalString("bootstrap script", request.BootstrapScript); err != nil {
		return err
	}
	if err := model.ValidateValueSource(request.BootstrapSource); err != nil {
		return err
	}
	if utf8.RuneCountInString(request.CreatorAgent) > 200 {
		return errors.New("creator agent must contain at most 200 characters")
	}
	if err := validateTime("creation time", request.CreatedAt); err != nil {
		return err
	}
	return validateTime("operation start time", request.OperationStartedAt)
}

func validateGitPointers(branch, detachedCommit *string) error {
	if err := validateOptionalString("branch", branch); err != nil {
		return err
	}
	if err := validateOptionalString("detached commit", detachedCommit); err != nil {
		return err
	}
	if branch != nil && detachedCommit != nil {
		return errors.New("branch and detached commit cannot both be set")
	}
	return nil
}

func validateRemovalSize(result RemovalResult) error {
	if result.SizeBytes == nil {
		if result.SizeComplete || result.SizeMeasuredAt != nil {
			return errors.New("a missing removal size cannot be complete or measured")
		}
		return nil
	}
	if *result.SizeBytes < 0 {
		return errors.New("removal size must not be negative")
	}
	if result.SizeMeasuredAt == nil {
		return errors.New("removal size measurement time must be set")
	}
	return validateTime("removal size measurement time", *result.SizeMeasuredAt)
}
