package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

const databaseTimeLayout = "2006-01-02T15:04:05.000000000Z"

// SQLite primary result codes: SQLITE_BUSY, SQLITE_LOCKED, and SQLITE_CONSTRAINT.
const (
	sqliteBusy       = 5
	sqliteLocked     = 6
	sqliteConstraint = 19
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("make database path absolute: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	dsn := databaseDSN(absolute)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, wrapDatabaseError("open database", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)

	store := &Store{db: db}
	if err := pingDatabase(ctx, db); err != nil {
		db.Close()
		return nil, wrapDatabaseError("connect to database", err)
	}
	if err := enableWAL(ctx, db); err != nil {
		db.Close()
		return nil, wrapDatabaseError("enable write-ahead logging", err)
	}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func databaseDSN(path string) string {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := uri.Query()
	query.Set("_foreign_keys", "1")
	query.Set("_busy_timeout", "5000")
	query.Set("_txlock", "immediate")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func formatTime(value time.Time) string {
	return value.UTC().Format(databaseTimeLayout)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validateTime(name string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s must not be zero", name)
	}
	return nil
}

func validateOptionalString(name string, value *string) error {
	if value != nil && *value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	return nil
}

func pingDatabase(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := db.PingContext(ctx)
		if err == nil || !isBusyError(err) || time.Now().After(deadline) {
			return err
		}
		if err := waitForRetry(ctx); err != nil {
			return err
		}
	}
}

func enableWAL(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var mode string
		err := db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&mode)
		if err == nil && strings.EqualFold(mode, "wal") {
			return nil
		}
		if err == nil {
			return fmt.Errorf("SQLite selected journal mode %q", mode)
		}
		if !isBusyError(err) || time.Now().After(deadline) {
			return err
		}
		if err := waitForRetry(ctx); err != nil {
			return err
		}
	}
}

func waitForRetry(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func wrapDatabaseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if isBusyError(err) {
		return fmt.Errorf("%s: %w: %v", operation, ErrBusy, err)
	}
	if isConstraintError(err) {
		return fmt.Errorf("%s: %w: %v", operation, ErrConflict, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func isBusyError(err error) bool {
	var sqliteError *sqlite.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	code := sqliteError.Code() & 0xff
	return code == sqliteBusy || code == sqliteLocked
}

func isConstraintError(err error) bool {
	var sqliteError *sqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == sqliteConstraint
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
