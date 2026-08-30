package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/testutil"
)

func TestVersionDoesNotLoadConfiguration(t *testing.T) {
	home := t.TempDir()
	dataHome := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{"version"}, Options{
		Stdin:   strings.NewReader(""),
		Stdout:  stdout,
		Stderr:  stderr,
		Version: "0.5.0-test",
	})
	if exitCode != model.ExitSuccess {
		t.Fatalf("exit code = %d", exitCode)
	}
	if stdout.String() != "grove 0.5.0-test\n" {
		t.Errorf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dataHome, "grove", "grove.db")); !os.IsNotExist(err) {
		t.Errorf("version created a database: %v", err)
	}
}

func TestConfigCommandsDoNotCreateDataDirectory(t *testing.T) {
	home := t.TempDir()
	dataHome := filepath.Join(home, "xdg-data")
	configHome := filepath.Join(home, "xdg-config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("PATH", t.TempDir())

	for _, command := range [][]string{{"config", "show", "--json"}, {"config", "path", "--json"}} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		exitCode := Run(context.Background(), command, Options{
			Stdin:  strings.NewReader(""),
			Stdout: stdout,
			Stderr: stderr,
		})
		if exitCode != model.ExitSuccess {
			t.Fatalf("%v exit code = %d, stderr = %s", command, exitCode, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("%v stderr = %q", command, stderr.String())
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
			t.Fatalf("%v output is not JSON: %v", command, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataHome, "grove")); !os.IsNotExist(err) {
		t.Errorf("config command created the data directory: %v", err)
	}
}

func TestJSONCleanupRequiresApprovalModeWithoutOpeningDatabase(t *testing.T) {
	home := t.TempDir()
	dataHome := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{"cleanup", "--older-than", "30d", "--json"}, Options{
		Stdin:      strings.NewReader("yes\n"),
		Stdout:     stdout,
		Stderr:     stderr,
		IsTerminal: func() bool { return true },
	})
	if exitCode != model.ExitConflict {
		t.Fatalf("exit code = %d, want %d", exitCode, model.ExitConflict)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	assertErrorDocument(t, stderr.Bytes(), "cleanup", model.ErrorConfirmationRequired)
	if strings.Contains(stderr.String(), "Delete") {
		t.Errorf("JSON cleanup wrote a prompt: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dataHome, "grove")); !os.IsNotExist(err) {
		t.Errorf("cleanup validation created the data directory: %v", err)
	}
}

func TestInvalidCleanupAgePrecedesJSONApproval(t *testing.T) {
	home := t.TempDir()
	dataHome := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{"cleanup", "--older-than", "0d", "--json"}, Options{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
	})
	if exitCode != model.ExitInvalidArguments {
		t.Fatalf("exit code = %d, want %d", exitCode, model.ExitInvalidArguments)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	assertErrorDocument(t, stderr.Bytes(), "cleanup", model.ErrorInvalidAge)
	if _, err := os.Stat(filepath.Join(dataHome, "grove")); !os.IsNotExist(err) {
		t.Errorf("age validation created the data directory: %v", err)
	}
}

func TestJSONParseErrorUsesStandardError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{"create", "--json"}, Options{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
	})
	if exitCode != model.ExitInvalidArguments {
		t.Fatalf("exit code = %d, want %d", exitCode, model.ExitInvalidArguments)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	assertErrorDocument(t, stderr.Bytes(), "create", model.ErrorInvalidArguments)
}

func TestMissingExplicitBootstrapReturnsCreateResult(t *testing.T) {
	testutil.IsolateGit(t)
	repository := testutil.NewRepository(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("GROVE_ROOT", filepath.Join(home, "worktrees"))
	t.Setenv("GROVE_AGENT", "pi:test")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{
		"create", "missing-bootstrap",
		"--repo", repository.Path,
		"--bootstrap-script", "does-not-exist.sh",
		"--json",
	}, Options{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
	})
	if exitCode != model.ExitBootstrap {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", exitCode, model.ExitBootstrap, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var result model.Result[model.CreateData]
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not a create result: %v\n%s", err, stdout.String())
	}
	if result.Command != "create" || result.Data.Worktree.Path == "" {
		t.Errorf("create result = %#v", result)
	}
	if result.Data.Bootstrap.State != model.BootstrapStateFailed || result.Data.Bootstrap.ExitCode != nil {
		t.Errorf("bootstrap result = %#v", result.Data.Bootstrap)
	}
}

func TestHelpListsCommandsAndTouchGuidance(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exitCode := Run(context.Background(), []string{"--help"}, Options{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
	})
	if exitCode != model.ExitSuccess {
		t.Fatalf("exit code = %d", exitCode)
	}
	for _, text := range []string{"create", "list", "touch", "stats", "cleanup", "config show", "config path", "version", "grove touch"} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("help does not contain %q:\n%s", text, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestCommandHelpListsSpecifiedOptions(t *testing.T) {
	tests := []struct {
		args    []string
		options []string
	}{
		{[]string{"create", "--help"}, []string{"--repo", "--branch", "--base", "--use-existing", "--agent", "--bootstrap-script", "--no-bootstrap", "--json"}},
		{[]string{"list", "--help"}, []string{"--repo", "--all", "--refresh-size", "--json"}},
		{[]string{"touch", "--help"}, []string{"--repo", "--json"}},
		{[]string{"stats", "--help"}, []string{"--repo", "--all", "--refresh", "--json"}},
		{[]string{"cleanup", "--help"}, []string{"--repo", "--older-than", "--allow-ignored", "--dry-run", "--yes", "--json"}},
		{[]string{"config", "show", "--help"}, []string{"--json"}},
		{[]string{"config", "path", "--help"}, []string{"--json"}},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			exitCode := Run(context.Background(), test.args, Options{
				Stdin:  strings.NewReader(""),
				Stdout: stdout,
				Stderr: stderr,
			})
			if exitCode != model.ExitSuccess {
				t.Fatalf("exit code = %d", exitCode)
			}
			for _, option := range test.options {
				if !strings.Contains(stdout.String(), option) {
					t.Errorf("help does not contain %q:\n%s", option, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestConfirmCleanupReadsOneAnswer(t *testing.T) {
	stdin := strings.NewReader("yes\nno\n")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	runner := runner{options: Options{Stdin: stdin, Stdout: stdout, Stderr: stderr}}
	approved, err := runner.confirmCleanup(2)
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Error("confirmation was not approved")
	}
	if strings.Count(stderr.String(), "Delete") != 1 {
		t.Errorf("prompt count is not one: %q", stderr.String())
	}
}

func assertErrorDocument(t *testing.T, encoded []byte, command string, code model.ErrorCode) {
	t.Helper()
	var document struct {
		SchemaVersion int    `json:"schema_version"`
		Command       string `json:"command"`
		Error         struct {
			Code model.ErrorCode `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("error output is not JSON: %v\n%s", err, encoded)
	}
	if document.SchemaVersion != model.SchemaVersion || document.Command != command || document.Error.Code != code {
		t.Errorf("error document = %#v", document)
	}
}
