package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestWorktreeJSONFieldSet(t *testing.T) {
	encoded, err := json.Marshal(Worktree{})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, object, []string{
		"id",
		"repository_id",
		"repository",
		"name",
		"path",
		"creation_root",
		"branch",
		"detached_commit",
		"creator_agent",
		"state",
		"created_at",
		"last_grove_activity_at",
		"size_bytes",
		"size_complete",
		"size_measured_at",
		"bootstrap_state",
	})
	for _, field := range []string{"branch", "detached_commit", "size_bytes", "size_measured_at"} {
		if string(object[field]) != "null" {
			t.Errorf("field %q = %s, want null", field, object[field])
		}
	}
}

func TestResultJSONUsesEmptyArrays(t *testing.T) {
	result := NewResult("list", ListData{Worktrees: []Worktree{}})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, object, []string{"schema_version", "command", "data", "warnings", "failures"})
	if string(object["warnings"]) != "[]" {
		t.Errorf("warnings = %s, want []", object["warnings"])
	}
	if string(object["failures"]) != "[]" {
		t.Errorf("failures = %s, want []", object["failures"])
	}
	if result.SchemaVersion != 1 {
		t.Errorf("schema version = %d, want 1", result.SchemaVersion)
	}
}

func TestErrorDocumentJSON(t *testing.T) {
	cause := errors.New("cause")
	domainErr := NewError(ErrorInvalidAgent, ExitInvalidArguments, "The agent ID is not valid.", cause)
	document := NewErrorDocument("create", domainErr)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, object, []string{"schema_version", "command", "error"})
	var errorObject map[string]json.RawMessage
	if err := json.Unmarshal(object["error"], &errorObject); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, errorObject, []string{"code", "message", "details"})
	if string(errorObject["details"]) != "{}" {
		t.Errorf("details = %s, want {}", errorObject["details"])
	}
	if !errors.Is(domainErr, cause) {
		t.Error("domain error does not unwrap its cause")
	}
	if domainErr.Error() != "The agent ID is not valid." {
		t.Errorf("Error() = %q", domainErr.Error())
	}
}

func TestAllJSONTypesHaveExplicitTags(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Repository{}),
		reflect.TypeOf(RepositoryInfo{}),
		reflect.TypeOf(GitDirectoryIdentity{}),
		reflect.TypeOf(GitWorktree{}),
		reflect.TypeOf(WorktreeStatus{}),
		reflect.TypeOf(Worktree{}),
		reflect.TypeOf(Issue{}),
		reflect.TypeOf(Result[struct{}]{}),
		reflect.TypeOf(ErrorDocument{}),
		reflect.TypeOf(Error{}),
		reflect.TypeOf(CreateData{}),
		reflect.TypeOf(BootstrapResult{}),
		reflect.TypeOf(ListData{}),
		reflect.TypeOf(ListSummary{}),
		reflect.TypeOf(TouchData{}),
		reflect.TypeOf(StatsData{}),
		reflect.TypeOf(CleanupData{}),
		reflect.TypeOf(CleanupItem{}),
		reflect.TypeOf(CleanupSummary{}),
		reflect.TypeOf(ConfigShowData{}),
		reflect.TypeOf(ConfigPathData{}),
	}
	for _, typ := range types {
		t.Run(typ.Name(), func(t *testing.T) {
			seen := map[string]string{}
			for index := range typ.NumField() {
				field := typ.Field(index)
				tag, ok := field.Tag.Lookup("json")
				if !ok || tag == "" {
					t.Errorf("field %s has no explicit JSON tag", field.Name)
					continue
				}
				name := tag
				if comma := indexByte(name, ','); comma >= 0 {
					name = name[:comma]
				}
				if name == "-" {
					continue
				}
				if previous, exists := seen[name]; exists {
					t.Errorf("fields %s and %s use JSON name %q", previous, field.Name, name)
				}
				seen[name] = field.Name
			}
		})
	}
}

func TestCommandDataFieldSets(t *testing.T) {
	tests := []struct {
		name string
		data any
		want []string
	}{
		{"create", CreateData{}, []string{"worktree", "bootstrap"}},
		{"bootstrap", BootstrapResult{}, []string{"state", "script", "source", "exit_code", "stdout", "stdout_encoding", "stdout_truncated", "stderr", "stderr_encoding", "stderr_truncated"}},
		{"list", ListData{}, []string{"worktrees", "summary"}},
		{"touch", TouchData{}, []string{"worktree", "previous_activity_at"}},
		{"stats", StatsData{}, []string{"active", "missing", "manual_review", "removed", "create_failed", "repository_count", "size_bytes", "unknown_size_count", "incomplete_size_count", "size_complete", "calculated_at", "oldest_measurement_at", "newest_measurement_at"}},
		{"cleanup", CleanupData{}, []string{"dry_run", "approved", "cutoff_at", "items", "summary"}},
		{"config_show", ConfigShowData{}, []string{"root", "root_source", "bootstrap_script", "bootstrap_script_source", "data_dir", "config_path"}},
		{"config_path", ConfigPathData{}, []string{"config_path"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.data)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &object); err != nil {
				t.Fatal(err)
			}
			assertKeys(t, object, test.want)
		})
	}
}

func TestJSONTimesUseUTC(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	encoded, err := json.Marshal(TouchData{PreviousActivityAt: at})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if string(object["previous_activity_at"]) != `"2026-01-02T03:04:05.000000006Z"` {
		t.Errorf("time = %s", object["previous_activity_at"])
	}
}

func assertKeys(t *testing.T, object map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON fields = %v, want %v", got, want)
	}
}

func indexByte(value string, target byte) int {
	for index := range len(value) {
		if value[index] == target {
			return index
		}
	}
	return -1
}
