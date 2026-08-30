package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/model"
)

func TestWriteJSONResult(t *testing.T) {
	result := model.NewResult("list", model.ListData{Worktrees: []model.Worktree{}})
	var buffer bytes.Buffer
	if err := WriteJSON(&buffer, result); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(buffer.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	assertJSONFields(t, document, "schema_version", "command", "data", "warnings", "failures")
	if string(document["warnings"]) != "[]" || string(document["failures"]) != "[]" {
		t.Fatalf("JSON arrays are not empty: %s", buffer.String())
	}
	if strings.Contains(buffer.String(), "\\u003c") {
		t.Fatalf("JSON uses HTML escaping: %s", buffer.String())
	}
}

func TestWriteJSONError(t *testing.T) {
	domainErr := model.NewError(model.ErrorBranchExists, model.ExitConflict, "The branch exists.", errors.New("cause"))
	domainErr.Details["branch"] = "feature"
	var buffer bytes.Buffer
	if err := WriteJSONError(&buffer, "create", domainErr); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(buffer.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	assertJSONFields(t, document, "schema_version", "command", "error")
	var errorObject map[string]json.RawMessage
	if err := json.Unmarshal(document["error"], &errorObject); err != nil {
		t.Fatal(err)
	}
	assertJSONFields(t, errorObject, "code", "message", "details")
	if string(errorObject["code"]) != `"branch_exists"` {
		t.Errorf("code = %s", errorObject["code"])
	}
}

func TestResultExitCodePriority(t *testing.T) {
	failure := model.NewIssue(model.IssueSizeIncomplete, "incomplete", nil, nil)
	bootstrap := model.BootstrapResult{State: model.BootstrapStateFailed}
	if got := ResultExitCode([]model.Issue{failure}, &bootstrap); got != model.ExitBootstrap {
		t.Errorf("bootstrap and partial exit = %d, want %d", got, model.ExitBootstrap)
	}
	if got := ResultExitCode([]model.Issue{failure}, nil); got != model.ExitPartial {
		t.Errorf("partial exit = %d, want %d", got, model.ExitPartial)
	}
	if got := ResultExitCode(nil, nil); got != model.ExitSuccess {
		t.Errorf("success exit = %d, want %d", got, model.ExitSuccess)
	}
}

func TestFormatSizeUsesBinaryUnits(t *testing.T) {
	tests := map[int64]string{
		0:               "0 B",
		1023:            "1023 B",
		1024:            "1 KiB",
		1536:            "1.5 KiB",
		1024 * 1024:     "1 MiB",
		5 * 1024 * 1024: "5 MiB",
	}
	for value, want := range tests {
		if got := FormatSize(value); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestWriteCleanupShowsSafetyData(t *testing.T) {
	activity := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cutoff := activity.Add(24 * time.Hour)
	path := filepath.Join(string(filepath.Separator), "tmp", "managed", "worktree")
	size := int64(1536)
	data := model.CleanupData{
		DryRun:   true,
		CutoffAt: cutoff,
		Items: []model.CleanupItem{{
			Worktree: model.Worktree{
				Path:                path,
				LastGroveActivityAt: activity,
			},
			Action:         model.CleanupActionCandidate,
			Reason:         model.RemovalReasonOldAndClean,
			FinalSizeBytes: &size,
		}},
		Summary: model.CleanupSummary{Candidate: 1},
	}
	var buffer bytes.Buffer
	if err := WriteCleanup(&buffer, data); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		path,
		activity.Format(time.RFC3339Nano),
		cutoff.Format(time.RFC3339Nano),
		"candidate",
		"old_and_clean",
		"1.5 KiB",
		"grove touch",
	} {
		if !strings.Contains(buffer.String(), value) {
			t.Errorf("cleanup output does not contain %q:\n%s", value, buffer.String())
		}
	}
}

func TestWriteListUsesRequiredColumns(t *testing.T) {
	branch := "feature"
	size := int64(1024)
	data := model.ListData{
		Worktrees: []model.Worktree{{
			Repository:          "api",
			Name:                "feature",
			Branch:              &branch,
			CreatorAgent:        "pi:test",
			LastGroveActivityAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			State:               model.WorktreeStateActive,
			SizeBytes:           &size,
			SizeComplete:        true,
			Path:                "/tmp/api/feature",
		}},
		Summary: model.ListSummary{Active: 1, SizeBytes: size, SizeComplete: true},
	}
	var buffer bytes.Buffer
	if err := WriteList(&buffer, data); err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"REPOSITORY", "NAME", "BRANCH", "CREATOR", "ACTIVITY", "STATE", "SIZE", "SIZE STATUS", "PATH"} {
		if !strings.Contains(buffer.String(), heading) {
			t.Errorf("list output does not contain heading %q:\n%s", heading, buffer.String())
		}
	}
}

func assertJSONFields(t *testing.T, object map[string]json.RawMessage, fields ...string) {
	t.Helper()
	if len(object) != len(fields) {
		t.Fatalf("field count = %d, want %d: %v", len(object), len(fields), object)
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			t.Errorf("field %q is missing", field)
		}
	}
}
