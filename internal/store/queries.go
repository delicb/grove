package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/del-boy/grove/internal/model"
)

const worktreeColumns = `
	w.id,
	w.repository_id,
	r.display_name,
	w.name,
	w.path,
	w.creation_root,
	w.branch,
	w.detached_commit,
	w.creator_agent,
	w.state,
	w.created_at,
	w.last_grove_activity_at,
	w.size_bytes,
	w.size_complete,
	w.size_measured_at,
	w.bootstrap_state,
	w.requested_base,
	w.requested_branch,
	w.expected_commit,
	w.locked,
	w.bootstrap_script,
	w.bootstrap_source,
	w.bootstrap_exit_code,
	w.bootstrap_started_at,
	w.bootstrap_finished_at,
	w.removed_at,
	w.removal_reason,
	w.removal_git_dir,
	w.removal_git_identity,
	w.operation_token,
	w.operation_started_at`

const worktreeSelect = `SELECT ` + worktreeColumns + `
FROM worktrees w
JOIN repositories r ON r.id = w.repository_id`

type rowScanner interface {
	Scan(dest ...any) error
}

func (store *Store) Get(ctx context.Context, id int64) (model.Worktree, error) {
	worktree, err := scanWorktree(store.db.QueryRowContext(ctx, worktreeSelect+` WHERE w.id = ?`, id))
	if err == sql.ErrNoRows {
		return model.Worktree{}, ErrNotFound
	}
	if err != nil {
		return model.Worktree{}, wrapDatabaseError("get worktree", err)
	}
	return worktree, nil
}

func (store *Store) Recoverable(ctx context.Context) ([]model.Worktree, error) {
	return store.List(ctx, Filter{States: []model.WorktreeState{
		model.WorktreeStateCreating,
		model.WorktreeStateRemoving,
	}})
}

func (store *Store) List(ctx context.Context, filter Filter) ([]model.Worktree, error) {
	where, arguments, err := worktreeFilterSQL(filter)
	if err != nil {
		return nil, err
	}
	query := worktreeSelect + where + ` ORDER BY r.display_name, r.directory_key, w.name, w.id`
	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, wrapDatabaseError("list worktrees", err)
	}
	defer rows.Close()

	worktrees := make([]model.Worktree, 0)
	for rows.Next() {
		worktree, err := scanWorktree(rows)
		if err != nil {
			return nil, wrapDatabaseError("scan worktree", err)
		}
		worktrees = append(worktrees, worktree)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDatabaseError("list worktrees", err)
	}
	return worktrees, nil
}

func (store *Store) Repositories(ctx context.Context, filter RepositoryFilter) ([]model.Repository, error) {
	query := `SELECT id, common_dir, main_checkout, display_name, directory_key, first_seen_at, last_seen_at
		FROM repositories`
	conditions := make([]string, 0, 2)
	arguments := make([]any, 0, 2)
	if filter.ID != nil {
		conditions = append(conditions, "id = ?")
		arguments = append(arguments, *filter.ID)
	}
	if filter.CommonDir != nil {
		conditions = append(conditions, "common_dir = ?")
		arguments = append(arguments, *filter.CommonDir)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY display_name, directory_key, id"

	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, wrapDatabaseError("list repositories", err)
	}
	defer rows.Close()

	repositories := make([]model.Repository, 0)
	for rows.Next() {
		repository, err := scanRepository(rows)
		if err != nil {
			return nil, wrapDatabaseError("scan repository", err)
		}
		repositories = append(repositories, repository)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDatabaseError("list repositories", err)
	}
	return repositories, nil
}

func (store *Store) Stats(ctx context.Context, request StatsRequest) (Stats, error) {
	conditions := make([]string, 0, 2)
	arguments := make([]any, 0, 2)
	if request.RepositoryID != nil {
		conditions = append(conditions, "w.repository_id = ?")
		arguments = append(arguments, *request.RepositoryID)
	}
	if request.RepositoryCommonDir != nil {
		conditions = append(conditions, "r.common_dir = ?")
		arguments = append(arguments, *request.RepositoryCommonDir)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	query := `SELECT
		COALESCE(SUM(CASE WHEN w.state = 'active' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'missing' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'manual_review' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'removed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'create_failed' THEN 1 ELSE 0 END), 0),
		COUNT(DISTINCT CASE WHEN w.state NOT IN ('removed', 'create_failed') THEN w.repository_id END),
		COALESCE(SUM(CASE WHEN w.state = 'active' AND w.size_bytes IS NOT NULL THEN w.size_bytes ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'active' AND w.size_bytes IS NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN w.state = 'active' AND w.size_bytes IS NOT NULL AND w.size_complete = 0 THEN 1 ELSE 0 END), 0),
		MIN(CASE WHEN w.state = 'active' THEN w.size_measured_at END),
		MAX(CASE WHEN w.state = 'active' THEN w.size_measured_at END)
	FROM worktrees w
	JOIN repositories r ON r.id = w.repository_id` + where

	var result Stats
	var removed int
	var createFailed int
	var oldest sql.NullString
	var newest sql.NullString
	err := store.db.QueryRowContext(ctx, query, arguments...).Scan(
		&result.Active,
		&result.Missing,
		&result.ManualReview,
		&removed,
		&createFailed,
		&result.RepositoryCount,
		&result.SizeBytes,
		&result.UnknownSizeCount,
		&result.IncompleteSizeCount,
		&oldest,
		&newest,
	)
	if err != nil {
		return Stats{}, wrapDatabaseError("calculate stats", err)
	}
	if request.IncludeFinal {
		result.Removed = &removed
		result.CreateFailed = &createFailed
	}
	result.SizeComplete = result.UnknownSizeCount == 0 && result.IncompleteSizeCount == 0
	result.OldestMeasurementAt, err = parseOptionalTime(oldest)
	if err != nil {
		return Stats{}, err
	}
	result.NewestMeasurementAt, err = parseOptionalTime(newest)
	if err != nil {
		return Stats{}, err
	}
	return result, nil
}

func worktreeFilterSQL(filter Filter) (string, []any, error) {
	conditions := make([]string, 0, 5)
	arguments := make([]any, 0, 5+len(filter.States))
	if filter.RepositoryID != nil {
		conditions = append(conditions, "w.repository_id = ?")
		arguments = append(arguments, *filter.RepositoryID)
	}
	if filter.RepositoryCommonDir != nil {
		conditions = append(conditions, "r.common_dir = ?")
		arguments = append(arguments, *filter.RepositoryCommonDir)
	}
	if filter.Name != nil {
		conditions = append(conditions, "w.name = ?")
		arguments = append(arguments, *filter.Name)
	}
	if filter.Path != nil {
		conditions = append(conditions, "w.path = ?")
		arguments = append(arguments, *filter.Path)
	}
	if len(filter.States) > 0 {
		for _, state := range filter.States {
			if err := model.ValidateWorktreeState(state); err != nil {
				return "", nil, err
			}
		}
		conditions = append(conditions, "w.state IN ("+placeholders(len(filter.States))+")")
		for _, state := range filter.States {
			arguments = append(arguments, state)
		}
	}
	if len(conditions) == 0 {
		return "", arguments, nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), arguments, nil
}

func scanRepository(scanner rowScanner) (model.Repository, error) {
	var repository model.Repository
	var firstSeen string
	var lastSeen string
	if err := scanner.Scan(
		&repository.ID,
		&repository.CommonDir,
		&repository.MainCheckout,
		&repository.DisplayName,
		&repository.DirectoryKey,
		&firstSeen,
		&lastSeen,
	); err != nil {
		return model.Repository{}, err
	}
	var err error
	repository.FirstSeenAt, err = parseTime(firstSeen)
	if err != nil {
		return model.Repository{}, err
	}
	repository.LastSeenAt, err = parseTime(lastSeen)
	if err != nil {
		return model.Repository{}, err
	}
	return repository, nil
}

func scanWorktree(scanner rowScanner) (model.Worktree, error) {
	var worktree model.Worktree
	var branch sql.NullString
	var detachedCommit sql.NullString
	var state string
	var createdAt string
	var lastActivityAt string
	var sizeBytes sql.NullInt64
	var sizeComplete int
	var sizeMeasuredAt sql.NullString
	var bootstrapState string
	var requestedBase sql.NullString
	var requestedBranch string
	var expectedCommit string
	var locked int
	var bootstrapScript sql.NullString
	var bootstrapSource string
	var bootstrapExitCode sql.NullInt64
	var bootstrapStartedAt sql.NullString
	var bootstrapFinishedAt sql.NullString
	var removedAt sql.NullString
	var removalReason sql.NullString
	var removalGitDir sql.NullString
	var removalGitIdentity sql.NullString
	var operationToken sql.NullString
	var operationStartedAt sql.NullString

	if err := scanner.Scan(
		&worktree.ID,
		&worktree.RepositoryID,
		&worktree.Repository,
		&worktree.Name,
		&worktree.Path,
		&worktree.CreationRoot,
		&branch,
		&detachedCommit,
		&worktree.CreatorAgent,
		&state,
		&createdAt,
		&lastActivityAt,
		&sizeBytes,
		&sizeComplete,
		&sizeMeasuredAt,
		&bootstrapState,
		&requestedBase,
		&requestedBranch,
		&expectedCommit,
		&locked,
		&bootstrapScript,
		&bootstrapSource,
		&bootstrapExitCode,
		&bootstrapStartedAt,
		&bootstrapFinishedAt,
		&removedAt,
		&removalReason,
		&removalGitDir,
		&removalGitIdentity,
		&operationToken,
		&operationStartedAt,
	); err != nil {
		return model.Worktree{}, err
	}

	worktree.State = model.WorktreeState(state)
	if err := model.ValidateWorktreeState(worktree.State); err != nil {
		return model.Worktree{}, err
	}
	worktree.BootstrapState = model.BootstrapState(bootstrapState)
	if err := model.ValidateBootstrapState(worktree.BootstrapState); err != nil {
		return model.Worktree{}, err
	}
	worktree.BootstrapSource = model.ValueSource(bootstrapSource)
	if err := model.ValidateValueSource(worktree.BootstrapSource); err != nil {
		return model.Worktree{}, err
	}
	if sizeComplete != 0 && sizeComplete != 1 || locked != 0 && locked != 1 {
		return model.Worktree{}, fmt.Errorf("database contains an invalid Boolean value")
	}

	worktree.Branch = optionalString(branch)
	worktree.DetachedCommit = optionalString(detachedCommit)
	worktree.RequestedBase = optionalString(requestedBase)
	worktree.RequestedBranch = requestedBranch
	worktree.ExpectedCommit = expectedCommit
	worktree.Locked = locked == 1
	worktree.BootstrapScript = optionalString(bootstrapScript)
	if removalGitDir.Valid != removalGitIdentity.Valid {
		return model.Worktree{}, fmt.Errorf("database contains an incomplete removal Git identity")
	}
	if removalGitDir.Valid {
		worktree.RemovalGitDirectory = &model.GitDirectoryIdentity{
			Path:  removalGitDir.String,
			Token: removalGitIdentity.String,
		}
	}
	worktree.OperationToken = optionalString(operationToken)
	if sizeBytes.Valid {
		value := sizeBytes.Int64
		worktree.SizeBytes = &value
	}
	if bootstrapExitCode.Valid {
		value := int(bootstrapExitCode.Int64)
		if int64(value) != bootstrapExitCode.Int64 {
			return model.Worktree{}, fmt.Errorf("bootstrap exit code is outside the int range")
		}
		worktree.BootstrapExitCode = &value
	}
	worktree.SizeComplete = sizeComplete == 1

	var err error
	worktree.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.LastGroveActivityAt, err = parseTime(lastActivityAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.SizeMeasuredAt, err = parseOptionalTime(sizeMeasuredAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.BootstrapStartedAt, err = parseOptionalTime(bootstrapStartedAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.BootstrapFinishedAt, err = parseOptionalTime(bootstrapFinishedAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.RemovedAt, err = parseOptionalTime(removedAt)
	if err != nil {
		return model.Worktree{}, err
	}
	worktree.OperationStartedAt, err = parseOptionalTime(operationStartedAt)
	if err != nil {
		return model.Worktree{}, err
	}
	if removalReason.Valid {
		reason := model.RemovalReason(removalReason.String)
		if err := model.ValidateRemovalReason(reason); err != nil {
			return model.Worktree{}, err
		}
		worktree.RemovalReason = &reason
	}
	return worktree, nil
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
