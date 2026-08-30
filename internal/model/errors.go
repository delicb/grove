package model

import (
	"encoding/json"
	"fmt"
)

const (
	ExitSuccess          = 0
	ExitInvalidArguments = 2
	ExitConfiguration    = 3
	ExitGit              = 4
	ExitConflict         = 5
	ExitBootstrap        = 6
	ExitPartial          = 7
	ExitDatabase         = 8
	ExitInternal         = 10
)

type ErrorCode string

const (
	ErrorInvalidArguments      ErrorCode = "invalid_arguments"
	ErrorInvalidAge            ErrorCode = "invalid_age"
	ErrorConfigNotFound        ErrorCode = "config_not_found"
	ErrorConfigInvalid         ErrorCode = "config_invalid"
	ErrorGitVersionUnsupported ErrorCode = "git_version_unsupported"
	ErrorNotRepository         ErrorCode = "not_repository"
	ErrorBareRepository        ErrorCode = "bare_repository"
	ErrorInvalidPath           ErrorCode = "invalid_path"
	ErrorInvalidName           ErrorCode = "invalid_name"
	ErrorInvalidAgent          ErrorCode = "invalid_agent"
	ErrorInvalidBranch         ErrorCode = "invalid_branch"
	ErrorInvalidBase           ErrorCode = "invalid_base"
	ErrorBranchExists          ErrorCode = "branch_exists"
	ErrorBranchInUse           ErrorCode = "branch_in_use"
	ErrorTargetExists          ErrorCode = "target_exists"
	ErrorTargetOutsideRoot     ErrorCode = "target_outside_root"
	ErrorTargetNestedWorktree  ErrorCode = "target_nested_in_worktree"
	ErrorWorktreeNotFound      ErrorCode = "worktree_not_found"
	ErrorWorktreeNotActive     ErrorCode = "worktree_not_active"
	ErrorWorktreeConflict      ErrorCode = "worktree_conflict"
	ErrorConfirmationRequired  ErrorCode = "confirmation_required"
	ErrorUnsafeCleanup         ErrorCode = "unsafe_cleanup"
	ErrorBootstrapMissing      ErrorCode = "bootstrap_missing"
	ErrorBootstrapFailed       ErrorCode = "bootstrap_failed"
	ErrorGit                   ErrorCode = "git_error"
	ErrorDatabaseBusy          ErrorCode = "database_busy"
	ErrorDatabase              ErrorCode = "database_error"
	ErrorInternal              ErrorCode = "internal_error"
)

var errorCodes = []ErrorCode{
	ErrorInvalidArguments,
	ErrorInvalidAge,
	ErrorConfigNotFound,
	ErrorConfigInvalid,
	ErrorGitVersionUnsupported,
	ErrorNotRepository,
	ErrorBareRepository,
	ErrorInvalidPath,
	ErrorInvalidName,
	ErrorInvalidAgent,
	ErrorInvalidBranch,
	ErrorInvalidBase,
	ErrorBranchExists,
	ErrorBranchInUse,
	ErrorTargetExists,
	ErrorTargetOutsideRoot,
	ErrorTargetNestedWorktree,
	ErrorWorktreeNotFound,
	ErrorWorktreeNotActive,
	ErrorWorktreeConflict,
	ErrorConfirmationRequired,
	ErrorUnsafeCleanup,
	ErrorBootstrapMissing,
	ErrorBootstrapFailed,
	ErrorGit,
	ErrorDatabaseBusy,
	ErrorDatabase,
	ErrorInternal,
}

func ErrorCodes() []ErrorCode {
	return append([]ErrorCode(nil), errorCodes...)
}

func (code ErrorCode) Valid() bool {
	for _, candidate := range errorCodes {
		if code == candidate {
			return true
		}
	}
	return false
}

type IssueCode string

const (
	IssueRepositoryUnreadable IssueCode = "repository_unreadable"
	IssueWorktreeMissing      IssueCode = "worktree_missing"
	IssueRecoveryManualReview IssueCode = "recovery_manual_review"
	IssueSizeIncomplete       IssueCode = "size_incomplete"
	IssueSizeRefreshSkipped   IssueCode = "size_refresh_skipped"
	IssueFileDisappeared      IssueCode = "file_disappeared"
	IssuePermissionDenied     IssueCode = "permission_denied"
	IssueCleanupRecent        IssueCode = "cleanup_recent"
	IssueCleanupDirty         IssueCode = "cleanup_dirty"
	IssueCleanupIgnored       IssueCode = "cleanup_ignored"
	IssueCleanupLocked        IssueCode = "cleanup_locked"
	IssueCleanupOutsideRoot   IssueCode = "cleanup_outside_root"
	IssueCleanupStatusError   IssueCode = "cleanup_status_error"
	IssueCleanupStateChanged  IssueCode = "cleanup_state_changed"
	IssueCleanupRemoveFailed  IssueCode = "cleanup_remove_failed"
)

var issueCodes = []IssueCode{
	IssueRepositoryUnreadable,
	IssueWorktreeMissing,
	IssueRecoveryManualReview,
	IssueSizeIncomplete,
	IssueSizeRefreshSkipped,
	IssueFileDisappeared,
	IssuePermissionDenied,
	IssueCleanupRecent,
	IssueCleanupDirty,
	IssueCleanupIgnored,
	IssueCleanupLocked,
	IssueCleanupOutsideRoot,
	IssueCleanupStatusError,
	IssueCleanupStateChanged,
	IssueCleanupRemoveFailed,
}

func IssueCodes() []IssueCode {
	return append([]IssueCode(nil), issueCodes...)
}

func (code IssueCode) Valid() bool {
	for _, candidate := range issueCodes {
		if code == candidate {
			return true
		}
	}
	return false
}

type Error struct {
	Code     ErrorCode      `json:"code"`
	ExitCode int            `json:"-"`
	Message  string         `json:"message"`
	Details  map[string]any `json:"details"`
	Err      error          `json:"-"`
}

var _ error = (*Error)(nil)

func NewError(code ErrorCode, exitCode int, message string, err error) *Error {
	return &Error{
		Code:     code,
		ExitCode: exitCode,
		Message:  message,
		Details:  map[string]any{},
		Err:      err,
	}
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	if err.Message != "" {
		return err.Message
	}
	if err.Err != nil {
		return err.Err.Error()
	}
	return string(err.Code)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err Error) MarshalJSON() ([]byte, error) {
	details := err.Details
	if details == nil {
		details = map[string]any{}
	}
	return json.Marshal(struct {
		Code    ErrorCode      `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}{
		Code:    err.Code,
		Message: err.Message,
		Details: details,
	})
}

func ValidateErrorCode(code ErrorCode) error {
	if !code.Valid() {
		return fmt.Errorf("invalid error code %q", code)
	}
	return nil
}

func ValidateIssueCode(code IssueCode) error {
	if !code.Valid() {
		return fmt.Errorf("invalid issue code %q", code)
	}
	return nil
}
