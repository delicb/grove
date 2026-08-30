package model

import "fmt"

type WorktreeState string

const (
	WorktreeStateCreating     WorktreeState = "creating"
	WorktreeStateActive       WorktreeState = "active"
	WorktreeStateRemoving     WorktreeState = "removing"
	WorktreeStateMissing      WorktreeState = "missing"
	WorktreeStateRemoved      WorktreeState = "removed"
	WorktreeStateCreateFailed WorktreeState = "create_failed"
	WorktreeStateManualReview WorktreeState = "manual_review"
)

var worktreeStates = []WorktreeState{
	WorktreeStateCreating,
	WorktreeStateActive,
	WorktreeStateRemoving,
	WorktreeStateMissing,
	WorktreeStateRemoved,
	WorktreeStateCreateFailed,
	WorktreeStateManualReview,
}

func WorktreeStates() []WorktreeState {
	return append([]WorktreeState(nil), worktreeStates...)
}

func (state WorktreeState) Valid() bool {
	switch state {
	case WorktreeStateCreating,
		WorktreeStateActive,
		WorktreeStateRemoving,
		WorktreeStateMissing,
		WorktreeStateRemoved,
		WorktreeStateCreateFailed,
		WorktreeStateManualReview:
		return true
	default:
		return false
	}
}

func (state WorktreeState) Final() bool {
	return state == WorktreeStateRemoved || state == WorktreeStateCreateFailed
}

func ValidateWorktreeState(state WorktreeState) error {
	if !state.Valid() {
		return fmt.Errorf("invalid worktree state %q", state)
	}
	return nil
}

type BootstrapState string

const (
	BootstrapStatePending     BootstrapState = "pending"
	BootstrapStateDisabled    BootstrapState = "disabled"
	BootstrapStateNotPresent  BootstrapState = "not_present"
	BootstrapStateRunning     BootstrapState = "running"
	BootstrapStateSucceeded   BootstrapState = "succeeded"
	BootstrapStateFailed      BootstrapState = "failed"
	BootstrapStateInterrupted BootstrapState = "interrupted"
)

var bootstrapStates = []BootstrapState{
	BootstrapStatePending,
	BootstrapStateDisabled,
	BootstrapStateNotPresent,
	BootstrapStateRunning,
	BootstrapStateSucceeded,
	BootstrapStateFailed,
	BootstrapStateInterrupted,
}

func BootstrapStates() []BootstrapState {
	return append([]BootstrapState(nil), bootstrapStates...)
}

func (state BootstrapState) Valid() bool {
	switch state {
	case BootstrapStatePending,
		BootstrapStateDisabled,
		BootstrapStateNotPresent,
		BootstrapStateRunning,
		BootstrapStateSucceeded,
		BootstrapStateFailed,
		BootstrapStateInterrupted:
		return true
	default:
		return false
	}
}

func (state BootstrapState) Terminal() bool {
	switch state {
	case BootstrapStateDisabled,
		BootstrapStateNotPresent,
		BootstrapStateSucceeded,
		BootstrapStateFailed,
		BootstrapStateInterrupted:
		return true
	default:
		return false
	}
}

func ValidateBootstrapState(state BootstrapState) error {
	if !state.Valid() {
		return fmt.Errorf("invalid bootstrap state %q", state)
	}
	return nil
}

type ValueSource string

const (
	SourceBuiltIn     ValueSource = "built-in"
	SourceConfig      ValueSource = "config"
	SourceEnvironment ValueSource = "environment"
	SourceCommand     ValueSource = "command"
	SourceDisabled    ValueSource = "disabled"
)

var valueSources = []ValueSource{
	SourceBuiltIn,
	SourceConfig,
	SourceEnvironment,
	SourceCommand,
	SourceDisabled,
}

func ValueSources() []ValueSource {
	return append([]ValueSource(nil), valueSources...)
}

func (source ValueSource) Valid() bool {
	switch source {
	case SourceBuiltIn, SourceConfig, SourceEnvironment, SourceCommand, SourceDisabled:
		return true
	default:
		return false
	}
}

func ValidateValueSource(source ValueSource) error {
	if !source.Valid() {
		return fmt.Errorf("invalid value source %q", source)
	}
	return nil
}

type StreamEncoding string

const (
	StreamEncodingUTF8   StreamEncoding = "utf-8"
	StreamEncodingBase64 StreamEncoding = "base64"
)

func (encoding StreamEncoding) Valid() bool {
	return encoding == StreamEncodingUTF8 || encoding == StreamEncodingBase64
}

func ValidateStreamEncoding(encoding StreamEncoding) error {
	if !encoding.Valid() {
		return fmt.Errorf("invalid stream encoding %q", encoding)
	}
	return nil
}

type RemovalReason string

const (
	RemovalReasonOldAndClean RemovalReason = "old_and_clean"
	RemovalReasonNotOld      RemovalReason = "not_old"
	RemovalReasonDirty       RemovalReason = "dirty"
	RemovalReasonIgnored     RemovalReason = "ignored_files"
	RemovalReasonLocked      RemovalReason = "locked"
	RemovalReasonMain        RemovalReason = "main_checkout"
	RemovalReasonOutsideRoot RemovalReason = "outside_root"
	RemovalReasonStateChange RemovalReason = "state_changed"
	RemovalReasonStatusError RemovalReason = "status_error"
	RemovalReasonRemoveFail  RemovalReason = "remove_failed"
)

var removalReasons = []RemovalReason{
	RemovalReasonOldAndClean,
	RemovalReasonNotOld,
	RemovalReasonDirty,
	RemovalReasonIgnored,
	RemovalReasonLocked,
	RemovalReasonMain,
	RemovalReasonOutsideRoot,
	RemovalReasonStateChange,
	RemovalReasonStatusError,
	RemovalReasonRemoveFail,
}

func RemovalReasons() []RemovalReason {
	return append([]RemovalReason(nil), removalReasons...)
}

func (reason RemovalReason) Valid() bool {
	switch reason {
	case RemovalReasonOldAndClean,
		RemovalReasonNotOld,
		RemovalReasonDirty,
		RemovalReasonIgnored,
		RemovalReasonLocked,
		RemovalReasonMain,
		RemovalReasonOutsideRoot,
		RemovalReasonStateChange,
		RemovalReasonStatusError,
		RemovalReasonRemoveFail:
		return true
	default:
		return false
	}
}

func ValidateRemovalReason(reason RemovalReason) error {
	if !reason.Valid() {
		return fmt.Errorf("invalid removal reason %q", reason)
	}
	return nil
}

type CleanupAction string

const (
	CleanupActionCandidate CleanupAction = "candidate"
	CleanupActionDeleted   CleanupAction = "deleted"
	CleanupActionSkipped   CleanupAction = "skipped"
	CleanupActionFailed    CleanupAction = "failed"
)

func (action CleanupAction) Valid() bool {
	switch action {
	case CleanupActionCandidate, CleanupActionDeleted, CleanupActionSkipped, CleanupActionFailed:
		return true
	default:
		return false
	}
}

func ValidateCleanupAction(action CleanupAction) error {
	if !action.Valid() {
		return fmt.Errorf("invalid cleanup action %q", action)
	}
	return nil
}
