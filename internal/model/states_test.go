package model

import (
	"reflect"
	"testing"
)

func TestWorktreeStates(t *testing.T) {
	want := []WorktreeState{
		"creating",
		"active",
		"removing",
		"missing",
		"removed",
		"create_failed",
		"manual_review",
	}
	got := WorktreeStates()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorktreeStates() = %v, want %v", got, want)
	}
	for _, state := range got {
		if !state.Valid() {
			t.Errorf("state %q is not valid", state)
		}
		if err := ValidateWorktreeState(state); err != nil {
			t.Errorf("ValidateWorktreeState(%q) returned %v", state, err)
		}
	}
	if WorktreeState("unknown").Valid() {
		t.Error("unknown worktree state is valid")
	}
	if err := ValidateWorktreeState("unknown"); err == nil {
		t.Error("ValidateWorktreeState accepted an unknown state")
	}
	got[0] = "changed"
	if WorktreeStates()[0] != WorktreeStateCreating {
		t.Error("WorktreeStates returned shared storage")
	}
}

func TestWorktreeFinalStates(t *testing.T) {
	for _, state := range WorktreeStates() {
		want := state == WorktreeStateRemoved || state == WorktreeStateCreateFailed
		if state.Final() != want {
			t.Errorf("%q Final() = %t, want %t", state, state.Final(), want)
		}
	}
}

func TestBootstrapStates(t *testing.T) {
	want := []BootstrapState{
		"pending",
		"disabled",
		"not_present",
		"running",
		"succeeded",
		"failed",
		"interrupted",
	}
	got := BootstrapStates()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BootstrapStates() = %v, want %v", got, want)
	}
	terminal := map[BootstrapState]bool{
		BootstrapStateDisabled:    true,
		BootstrapStateNotPresent:  true,
		BootstrapStateSucceeded:   true,
		BootstrapStateFailed:      true,
		BootstrapStateInterrupted: true,
	}
	for _, state := range got {
		if !state.Valid() {
			t.Errorf("state %q is not valid", state)
		}
		if state.Terminal() != terminal[state] {
			t.Errorf("%q Terminal() = %t, want %t", state, state.Terminal(), terminal[state])
		}
		if err := ValidateBootstrapState(state); err != nil {
			t.Errorf("ValidateBootstrapState(%q) returned %v", state, err)
		}
	}
	if BootstrapState("unknown").Valid() {
		t.Error("unknown bootstrap state is valid")
	}
	if BootstrapStatePending.Terminal() || BootstrapStateRunning.Terminal() {
		t.Error("a nonterminal bootstrap state is terminal")
	}
	if err := ValidateBootstrapState("unknown"); err == nil {
		t.Error("ValidateBootstrapState accepted an unknown state")
	}
}

func TestOtherEnums(t *testing.T) {
	wantSources := []ValueSource{"built-in", "config", "environment", "command", "disabled"}
	if got := ValueSources(); !reflect.DeepEqual(got, wantSources) {
		t.Errorf("ValueSources() = %v, want %v", got, wantSources)
	}
	for _, source := range ValueSources() {
		if !source.Valid() {
			t.Errorf("source %q is not valid", source)
		}
		if err := ValidateValueSource(source); err != nil {
			t.Errorf("ValidateValueSource(%q) returned %v", source, err)
		}
	}
	if ValueSource("unknown").Valid() {
		t.Error("unknown source is valid")
	}
	if err := ValidateValueSource("unknown"); err == nil {
		t.Error("ValidateValueSource accepted an unknown source")
	}

	for _, encoding := range []StreamEncoding{"utf-8", "base64"} {
		if !encoding.Valid() {
			t.Errorf("encoding %q is not valid", encoding)
		}
		if err := ValidateStreamEncoding(encoding); err != nil {
			t.Errorf("ValidateStreamEncoding(%q) returned %v", encoding, err)
		}
	}
	if StreamEncoding("unknown").Valid() {
		t.Error("unknown stream encoding is valid")
	}
	if err := ValidateStreamEncoding("unknown"); err == nil {
		t.Error("ValidateStreamEncoding accepted an unknown encoding")
	}

	wantReasons := []RemovalReason{
		"old_and_clean",
		"not_old",
		"dirty",
		"ignored_files",
		"locked",
		"main_checkout",
		"outside_root",
		"state_changed",
		"status_error",
		"remove_failed",
	}
	if got := RemovalReasons(); !reflect.DeepEqual(got, wantReasons) {
		t.Errorf("RemovalReasons() = %v, want %v", got, wantReasons)
	}
	for _, reason := range RemovalReasons() {
		if !reason.Valid() {
			t.Errorf("reason %q is not valid", reason)
		}
		if err := ValidateRemovalReason(reason); err != nil {
			t.Errorf("ValidateRemovalReason(%q) returned %v", reason, err)
		}
	}
	if RemovalReason("unknown").Valid() {
		t.Error("unknown removal reason is valid")
	}
	if err := ValidateRemovalReason("unknown"); err == nil {
		t.Error("ValidateRemovalReason accepted an unknown reason")
	}

	for _, action := range []CleanupAction{"candidate", "deleted", "skipped", "failed"} {
		if !action.Valid() {
			t.Errorf("action %q is not valid", action)
		}
		if err := ValidateCleanupAction(action); err != nil {
			t.Errorf("ValidateCleanupAction(%q) returned %v", action, err)
		}
	}
	if CleanupAction("unknown").Valid() {
		t.Error("unknown cleanup action is valid")
	}
	if err := ValidateCleanupAction("unknown"); err == nil {
		t.Error("ValidateCleanupAction accepted an unknown action")
	}
}

func TestStableCodes(t *testing.T) {
	wantErrors := []ErrorCode{
		"invalid_arguments",
		"invalid_age",
		"config_not_found",
		"config_invalid",
		"git_version_unsupported",
		"not_repository",
		"bare_repository",
		"invalid_path",
		"invalid_name",
		"invalid_agent",
		"invalid_branch",
		"invalid_base",
		"branch_exists",
		"branch_in_use",
		"target_exists",
		"target_outside_root",
		"target_nested_in_worktree",
		"worktree_not_found",
		"worktree_not_active",
		"worktree_conflict",
		"confirmation_required",
		"unsafe_cleanup",
		"bootstrap_missing",
		"bootstrap_failed",
		"git_error",
		"database_busy",
		"database_error",
		"internal_error",
	}
	if got := ErrorCodes(); !reflect.DeepEqual(got, wantErrors) {
		t.Fatalf("ErrorCodes() = %v, want %v", got, wantErrors)
	}
	for _, code := range ErrorCodes() {
		if !code.Valid() {
			t.Errorf("error code %q is not valid", code)
		}
		if err := ValidateErrorCode(code); err != nil {
			t.Errorf("ValidateErrorCode(%q) returned %v", code, err)
		}
	}
	if ErrorCode("unknown").Valid() {
		t.Error("unknown error code is valid")
	}
	if err := ValidateErrorCode("unknown"); err == nil {
		t.Error("ValidateErrorCode accepted an unknown code")
	}

	wantIssues := []IssueCode{
		"repository_unreadable",
		"worktree_missing",
		"recovery_manual_review",
		"size_incomplete",
		"file_disappeared",
		"permission_denied",
		"cleanup_recent",
		"cleanup_dirty",
		"cleanup_ignored",
		"cleanup_locked",
		"cleanup_outside_root",
		"cleanup_status_error",
		"cleanup_state_changed",
		"cleanup_remove_failed",
	}
	if got := IssueCodes(); !reflect.DeepEqual(got, wantIssues) {
		t.Fatalf("IssueCodes() = %v, want %v", got, wantIssues)
	}
	for _, code := range IssueCodes() {
		if !code.Valid() {
			t.Errorf("issue code %q is not valid", code)
		}
		if err := ValidateIssueCode(code); err != nil {
			t.Errorf("ValidateIssueCode(%q) returned %v", code, err)
		}
	}
	if IssueCode("unknown").Valid() {
		t.Error("unknown issue code is valid")
	}
	if err := ValidateIssueCode("unknown"); err == nil {
		t.Error("ValidateIssueCode accepted an unknown code")
	}
}
