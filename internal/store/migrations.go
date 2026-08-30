package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

type migration struct {
	version    int
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE repositories (
				id INTEGER PRIMARY KEY,
				common_dir TEXT NOT NULL UNIQUE CHECK (common_dir <> ''),
				main_checkout TEXT NOT NULL CHECK (main_checkout <> ''),
				display_name TEXT NOT NULL CHECK (display_name <> ''),
				directory_key TEXT NOT NULL UNIQUE CHECK (directory_key <> ''),
				first_seen_at TEXT NOT NULL CHECK (first_seen_at <> ''),
				last_seen_at TEXT NOT NULL CHECK (last_seen_at <> '')
			)`,
			`CREATE TABLE worktrees (
				id INTEGER PRIMARY KEY,
				repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
				name TEXT NOT NULL CHECK (
					length(name) BETWEEN 1 AND 100 AND
					substr(name, 1, 1) GLOB '[A-Za-z0-9_]' AND
					name NOT GLOB '*[^A-Za-z0-9._-]*'
				),
				creation_root TEXT NOT NULL CHECK (creation_root <> ''),
				path TEXT NOT NULL CHECK (path <> ''),
				branch TEXT CHECK (branch IS NULL OR branch <> ''),
				detached_commit TEXT CHECK (detached_commit IS NULL OR detached_commit <> ''),
				requested_base TEXT CHECK (requested_base IS NULL OR requested_base <> ''),
				requested_branch TEXT NOT NULL CHECK (requested_branch <> ''),
				expected_commit TEXT NOT NULL CHECK (expected_commit <> ''),
				creator_agent TEXT NOT NULL CHECK (length(creator_agent) BETWEEN 1 AND 200),
				created_at TEXT NOT NULL CHECK (created_at <> ''),
				last_grove_activity_at TEXT NOT NULL CHECK (last_grove_activity_at <> ''),
				state TEXT NOT NULL CHECK (state IN (
					'creating', 'active', 'removing', 'missing', 'removed', 'create_failed', 'manual_review'
				)),
				locked INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
				bootstrap_state TEXT NOT NULL CHECK (bootstrap_state IN (
					'pending', 'disabled', 'not_present', 'running', 'succeeded', 'failed', 'interrupted'
				)),
				bootstrap_script TEXT CHECK (bootstrap_script IS NULL OR bootstrap_script <> ''),
				bootstrap_source TEXT NOT NULL CHECK (bootstrap_source IN (
					'built-in', 'config', 'environment', 'command', 'disabled'
				)),
				bootstrap_exit_code INTEGER,
				bootstrap_started_at TEXT CHECK (bootstrap_started_at IS NULL OR bootstrap_started_at <> ''),
				bootstrap_finished_at TEXT CHECK (bootstrap_finished_at IS NULL OR bootstrap_finished_at <> ''),
				size_bytes INTEGER CHECK (size_bytes IS NULL OR size_bytes >= 0),
				size_complete INTEGER NOT NULL DEFAULT 0 CHECK (size_complete IN (0, 1)),
				size_measured_at TEXT CHECK (size_measured_at IS NULL OR size_measured_at <> ''),
				removed_at TEXT CHECK (removed_at IS NULL OR removed_at <> ''),
				removal_reason TEXT CHECK (removal_reason IS NULL OR removal_reason IN (
					'old_and_clean', 'not_old', 'dirty', 'ignored_files', 'locked', 'main_checkout',
					'outside_root', 'state_changed', 'status_error', 'remove_failed'
				)),
				operation_token TEXT CHECK (operation_token IS NULL OR operation_token <> ''),
				operation_started_at TEXT CHECK (operation_started_at IS NULL OR operation_started_at <> ''),
				CHECK (branch IS NULL OR detached_commit IS NULL),
				CHECK (
					(size_bytes IS NULL AND size_measured_at IS NULL AND size_complete = 0) OR
					(size_bytes IS NOT NULL AND size_measured_at IS NOT NULL)
				),
				CHECK ((state IN ('creating', 'removing')) = (operation_token IS NOT NULL)),
				CHECK ((state IN ('creating', 'removing')) = (operation_started_at IS NOT NULL)),
				CHECK (removed_at IS NULL OR state = 'removed'),
				CHECK (state <> 'removed' OR (removed_at IS NOT NULL AND removal_reason IS NOT NULL))
			)`,
			`CREATE UNIQUE INDEX worktrees_live_path
			ON worktrees(path)
			WHERE state NOT IN ('removed', 'create_failed')`,
			`CREATE UNIQUE INDEX worktrees_live_name
			ON worktrees(repository_id, name)
			WHERE state NOT IN ('removed', 'create_failed')`,
			`CREATE UNIQUE INDEX worktrees_operation_token
			ON worktrees(operation_token)
			WHERE operation_token IS NOT NULL`,
			`CREATE INDEX worktrees_state ON worktrees(state)`,
			`CREATE INDEX worktrees_repository_state_activity
			ON worktrees(repository_id, state, last_grove_activity_at)`,
		},
	},
	{
		version: 2,
		statements: []string{
			`ALTER TABLE worktrees ADD COLUMN removal_git_dir TEXT
			CHECK (removal_git_dir IS NULL OR removal_git_dir <> '')`,
			`ALTER TABLE worktrees ADD COLUMN removal_git_identity TEXT
			CHECK (removal_git_identity IS NULL OR removal_git_identity <> '')`,
		},
	},
}

func (store *Store) migrate(ctx context.Context) error {
	if err := store.ensureMigrationTable(ctx); err != nil {
		return err
	}

	applied, err := store.appliedMigrations(ctx)
	if err != nil {
		return err
	}
	latest := migrations[len(migrations)-1].version
	for version := range applied {
		if version > latest {
			return fmt.Errorf("database schema version %d is newer than supported version %d", version, latest)
		}
	}

	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		if err := store.applyMigration(ctx, migration); err != nil {
			return fmt.Errorf("apply database migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func (store *Store) ensureMigrationTable(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start migration table transaction", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY CHECK (version > 0),
		applied_at TEXT NOT NULL CHECK (applied_at <> '')
	)`); err != nil {
		return wrapDatabaseError("create migration table", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit migration table transaction", err)
	}
	return nil
}

func (store *Store) appliedMigrations(ctx context.Context) (map[int]bool, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, wrapDatabaseError("read database migrations", err)
	}
	defer rows.Close()

	versions := make([]int, 0, len(migrations))
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, wrapDatabaseError("scan database migration", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDatabaseError("read database migrations", err)
	}
	sort.Ints(versions)
	applied := make(map[int]bool, len(versions))
	for _, version := range versions {
		applied[version] = true
	}
	return applied, nil
}

func (store *Store) applyMigration(ctx context.Context, migration migration) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapDatabaseError("start database migration", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, migration.version).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return wrapDatabaseError("check database migration", err)
	}
	if err == nil {
		return tx.Commit()
	}

	for _, statement := range migration.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return wrapDatabaseError("run database migration statement", err)
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		migration.version,
		formatTime(time.Now()),
	); err != nil {
		return wrapDatabaseError("record database migration", err)
	}
	if err := tx.Commit(); err != nil {
		return wrapDatabaseError("commit database migration", err)
	}
	return nil
}
