package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/model"
)

func TestSelect(t *testing.T) {
	worktree := t.TempDir()

	disabled, err := Select(worktree, "", model.SourceEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.State != model.BootstrapStateDisabled || disabled.Source != model.SourceDisabled || disabled.Script != nil {
		t.Errorf("disabled plan = %#v", disabled)
	}

	missingDefault, err := Select(worktree, "bootstrap-worktree.sh", model.SourceBuiltIn)
	if err != nil {
		t.Fatal(err)
	}
	if missingDefault.State != model.BootstrapStateNotPresent || missingDefault.Script == nil || !filepath.IsAbs(*missingDefault.Script) {
		t.Errorf("missing default plan = %#v", missingDefault)
	}

	missingExplicit, err := Select(worktree, "bootstrap-worktree.sh", model.SourceConfig)
	if err != nil {
		t.Fatal(err)
	}
	if missingExplicit.State != model.BootstrapStateFailed || missingExplicit.Err == nil {
		t.Errorf("missing explicit plan = %#v", missingExplicit)
	}

	outside := filepath.Join(filepath.Dir(worktree), "outside.sh")
	if err := os.WriteFile(outside, []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absolutePlan, err := Select(worktree, outside, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	if absolutePlan.State != model.BootstrapStatePending || absolutePlan.Err != nil {
		t.Errorf("absolute plan = %#v", absolutePlan)
	}

	linkedScript := filepath.Join(worktree, "linked.sh")
	if err := os.Symlink(outside, linkedScript); err != nil {
		t.Fatal(err)
	}
	linkedPlan, err := Select(worktree, "linked.sh", model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	if linkedPlan.State != model.BootstrapStateFailed || linkedPlan.Err == nil {
		t.Errorf("linked plan = %#v", linkedPlan)
	}
}

func TestExecuteCapturesBoundedOutputAndClosesInput(t *testing.T) {
	worktree := t.TempDir()
	script := filepath.Join(worktree, "bootstrap.sh")
	contents := `
if read value; then
  echo "stdin was open" >&2
  exit 9
fi
printf '\377'
head -c 1048584 /dev/zero | tr '\000' 'x'
printf 'error' >&2
head -c 1048584 /dev/zero | tr '\000' 'y' >&2
printf '%s' "$GROVE_WORKTREE_NAME|$GROVE_AGENT|${GROVE_OLD-unset}" >&2
`
	if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Select(worktree, script, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	execution := Execute(context.Background(), ExecuteOptions{
		Plan: plan,
		Context: Context{
			WorktreePath:   worktree,
			WorktreeName:   "feature",
			RepositoryPath: "/repo",
			Branch:         "feature",
			Agent:          "pi:test",
		},
		Environment: []string{"PATH=" + os.Getenv("PATH"), "GROVE_OLD=unsafe"},
	})
	if execution.Result.State != model.BootstrapStateSucceeded || execution.Err != nil {
		t.Fatalf("Execute() = %#v, %v", execution.Result, execution.Err)
	}
	if execution.Result.StdoutEncoding != model.StreamEncodingBase64 {
		t.Errorf("stdout encoding = %q", execution.Result.StdoutEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(execution.Result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != CaptureLimit || decoded[0] != 0xff || !execution.Result.StdoutTruncated {
		t.Errorf("stdout length = %d, first byte = %x, truncated = %t", len(decoded), decoded[0], execution.Result.StdoutTruncated)
	}
	if execution.Result.StderrEncoding != model.StreamEncodingUTF8 || len(execution.Result.Stderr) != CaptureLimit || !execution.Result.StderrTruncated {
		t.Errorf("stderr length = %d, encoding = %q, truncated = %t", len(execution.Result.Stderr), execution.Result.StderrEncoding, execution.Result.StderrTruncated)
	}
	if strings.Contains(execution.Result.Stderr, "stdin was open") || strings.Contains(execution.Result.Stderr, "unsafe") {
		t.Errorf("stderr contains unsafe data: %q", execution.Result.Stderr)
	}
}

func TestExecuteReplacesManagedGroveEnvironment(t *testing.T) {
	worktree := t.TempDir()
	script := filepath.Join(worktree, "environment.sh")
	contents := `printf '%s' "$GROVE_WORKTREE_PATH|$GROVE_WORKTREE_NAME|$GROVE_REPOSITORY_PATH|$GROVE_BRANCH|$GROVE_AGENT|${GROVE_OLD-unset}"`
	if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Select(worktree, script, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	execution := Execute(context.Background(), ExecuteOptions{
		Plan: plan,
		Context: Context{
			WorktreePath:   worktree,
			WorktreeName:   "feature",
			RepositoryPath: "/repo",
			Branch:         "branch",
			Agent:          "pi:test",
		},
		Environment: []string{"PATH=" + os.Getenv("PATH"), "GROVE_OLD=unsafe", "GROVE_AGENT=wrong"},
	})
	want := worktree + "|feature|/repo|branch|pi:test|unsafe"
	if execution.Err != nil || execution.Result.Stdout != want {
		t.Errorf("Execute() = %q, %v; want %q", execution.Result.Stdout, execution.Err, want)
	}
}

func TestExecuteStopsProcessGroupAfterCanceledContext(t *testing.T) {
	worktree := t.TempDir()
	script := filepath.Join(worktree, "ignore-interrupt.sh")
	if err := os.WriteFile(script, []byte("trap '' INT TERM\nsleep 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Select(worktree, script, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	execution := Execute(ctx, ExecuteOptions{Plan: plan, Context: Context{WorktreePath: worktree}})
	if elapsed := time.Since(started); elapsed > interruptGracePeriod+2*time.Second {
		t.Fatalf("Execute() took %s after cancellation", elapsed)
	}
	if execution.Result.State != model.BootstrapStateInterrupted {
		t.Errorf("bootstrap state = %q", execution.Result.State)
	}
}

func TestExecuteKillsChildAfterInterruptedShellExits(t *testing.T) {
	worktree := t.TempDir()
	pidFile := filepath.Join(worktree, "child.pid")
	script := filepath.Join(worktree, "orphan-child.sh")
	contents := "trap 'exit 0' INT\nsh -c 'trap \"\" INT TERM; while :; do sleep 1; done' &\nprintf '%s' \"$!\" > \"" + pidFile + "\"\nwait\n"
	if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Select(worktree, script, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	execution := Execute(ctx, ExecuteOptions{Plan: plan, Context: Context{WorktreePath: worktree}})
	if execution.Result.State != model.BootstrapStateInterrupted {
		t.Fatalf("bootstrap state = %q", execution.Result.State)
	}
	encodedPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(encodedPID))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bootstrap child process %d is still alive", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecuteReportsNonzeroExit(t *testing.T) {
	worktree := t.TempDir()
	script := filepath.Join(worktree, "fail.sh")
	if err := os.WriteFile(script, []byte("printf 'no' >&2\nexit 23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Select(worktree, script, model.SourceCommand)
	if err != nil {
		t.Fatal(err)
	}
	execution := Execute(context.Background(), ExecuteOptions{Plan: plan, Context: Context{WorktreePath: worktree}})
	if execution.Result.State != model.BootstrapStateFailed || execution.Result.ExitCode == nil || *execution.Result.ExitCode != 23 {
		t.Errorf("Execute() = %#v", execution.Result)
	}
	if execution.Err == nil || execution.Result.Stderr != "no" {
		t.Errorf("Execute() error = %v, stderr = %q", execution.Err, execution.Result.Stderr)
	}
}
