package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
)

const (
	CaptureLimit         = 1 << 20
	interruptGracePeriod = 2 * time.Second
)

type OutputMode int

const (
	CaptureOutput OutputMode = iota
	PassthroughOutput
)

type Plan struct {
	State  model.BootstrapState
	Script *string
	Source model.ValueSource
	Err    error
}

type Context struct {
	WorktreePath   string
	WorktreeName   string
	RepositoryPath string
	Branch         string
	Agent          string
}

type ExecuteOptions struct {
	Plan        Plan
	Context     Context
	Environment []string
	Mode        OutputMode
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type Execution struct {
	Result model.BootstrapResult
	Err    error
}

func Select(worktreePath, script string, source model.ValueSource) (Plan, error) {
	if script == "" {
		return Plan{State: model.BootstrapStateDisabled, Source: model.SourceDisabled}, nil
	}
	if err := model.ValidateValueSource(source); err != nil {
		return Plan{}, err
	}
	root, err := paths.CanonicalDirectory(worktreePath)
	if err != nil {
		return Plan{}, err
	}
	requireWorktreeChild := !filepath.IsAbs(script)
	candidate := script
	if requireWorktreeChild {
		candidate = filepath.Join(root, candidate)
	}
	resolved, resolveErr := resolveScript(root, candidate, requireWorktreeChild)
	if resolved == "" {
		resolved = filepath.Clean(candidate)
	}
	plan := Plan{
		State:  model.BootstrapStatePending,
		Script: stringPointer(resolved),
		Source: source,
	}
	if resolveErr != nil {
		plan.State = model.BootstrapStateFailed
		plan.Err = resolveErr
		return plan, nil
	}
	info, err := os.Stat(resolved)
	if err == nil && info.IsDir() {
		plan.State = model.BootstrapStateFailed
		plan.Err = errors.New("the bootstrap script path names a directory")
		return plan, nil
	}
	if err == nil {
		return plan, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		plan.State = model.BootstrapStateFailed
		plan.Err = err
		return plan, nil
	}
	if source == model.SourceBuiltIn {
		plan.State = model.BootstrapStateNotPresent
		return plan, nil
	}
	plan.State = model.BootstrapStateFailed
	plan.Err = err
	return plan, nil
}

func Execute(ctx context.Context, options ExecuteOptions) Execution {
	result := emptyResult(options.Plan)
	if options.Plan.State != model.BootstrapStatePending || options.Plan.Script == nil {
		return Execution{Result: result, Err: options.Plan.Err}
	}

	command := exec.Command("/bin/sh", *options.Plan.Script)
	command.Dir = options.Context.WorktreePath
	command.Env = environment(options.Environment, options.Context)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout *boundedBuffer
	var stderr *boundedBuffer
	if options.Mode == PassthroughOutput {
		command.Stdin = options.Stdin
		if command.Stdin == nil {
			command.Stdin = os.Stdin
		}
		command.Stdout = options.Stdout
		if command.Stdout == nil {
			command.Stdout = os.Stdout
		}
		command.Stderr = options.Stderr
		if command.Stderr == nil {
			command.Stderr = os.Stderr
		}
	} else {
		stdout = newBoundedBuffer(CaptureLimit)
		stderr = newBoundedBuffer(CaptureLimit)
		command.Stdin = bytes.NewReader(nil)
		command.Stdout = stdout
		command.Stderr = stderr
	}

	if err := command.Start(); err != nil {
		result.State = model.BootstrapStateFailed
		setCapturedOutput(&result, stdout, stderr)
		return Execution{Result: result, Err: err}
	}

	wait := make(chan error, 1)
	go func() {
		wait <- command.Wait()
	}()

	interrupted := false
	var runErr error
	select {
	case runErr = <-wait:
	case <-ctx.Done():
		interrupted = true
		if signalErr := signalProcessGroup(command.Process.Pid, syscall.SIGINT); signalErr != nil {
			_ = signalProcessGroup(command.Process.Pid, syscall.SIGKILL)
		}
		timer := time.NewTimer(interruptGracePeriod)
		select {
		case runErr = <-wait:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			_ = signalProcessGroup(command.Process.Pid, syscall.SIGKILL)
		case <-timer.C:
			_ = signalProcessGroup(command.Process.Pid, syscall.SIGKILL)
			runErr = <-wait
		}
	}

	setCapturedOutput(&result, stdout, stderr)
	if interrupted || command.ProcessState != nil && command.ProcessState.ExitCode() < 0 {
		result.State = model.BootstrapStateInterrupted
		return Execution{Result: result, Err: runErr}
	}
	if runErr == nil {
		exitCode := 0
		result.State = model.BootstrapStateSucceeded
		result.ExitCode = &exitCode
		return Execution{Result: result}
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode := exitErr.ExitCode()
		result.State = model.BootstrapStateFailed
		result.ExitCode = &exitCode
		return Execution{Result: result, Err: runErr}
	}
	result.State = model.BootstrapStateFailed
	return Execution{Result: result, Err: runErr}
}

func emptyResult(plan Plan) model.BootstrapResult {
	return model.BootstrapResult{
		State:          plan.State,
		Script:         plan.Script,
		Source:         plan.Source,
		StdoutEncoding: model.StreamEncodingUTF8,
		StderrEncoding: model.StreamEncodingUTF8,
	}
}

func resolveScript(root, candidate string, requireWorktreeChild bool) (string, error) {
	resolved, err := paths.CanonicalForCreation(candidate)
	if err != nil {
		return "", err
	}
	if requireWorktreeChild && !paths.IsChild(root, resolved) {
		return resolved, errors.New("the bootstrap script is outside the worktree")
	}
	return resolved, nil
}

func environment(base []string, values Context) []string {
	replaced := map[string]struct{}{
		"GROVE_WORKTREE_PATH":   {},
		"GROVE_WORKTREE_NAME":   {},
		"GROVE_REPOSITORY_PATH": {},
		"GROVE_BRANCH":          {},
		"GROVE_AGENT":           {},
	}
	result := make([]string, 0, len(base)+5)
	for _, value := range base {
		name, _, found := strings.Cut(value, "=")
		if found {
			if _, exists := replaced[name]; exists {
				continue
			}
		}
		result = append(result, value)
	}
	return append(result,
		"GROVE_WORKTREE_PATH="+values.WorktreePath,
		"GROVE_WORKTREE_NAME="+values.WorktreeName,
		"GROVE_REPOSITORY_PATH="+values.RepositoryPath,
		"GROVE_BRANCH="+values.Branch,
		"GROVE_AGENT="+values.Agent,
	)
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining < len(data) {
		buffer.truncated = true
		if remaining < 0 {
			remaining = 0
		}
		data = data[:remaining]
	}
	if len(data) > 0 {
		_, _ = buffer.buffer.Write(data)
	}
	return originalLength, nil
}

func setCapturedOutput(result *model.BootstrapResult, stdout, stderr *boundedBuffer) {
	if stdout == nil || stderr == nil {
		return
	}
	result.Stdout, result.StdoutEncoding = encode(stdout.buffer.Bytes())
	result.StdoutTruncated = stdout.truncated
	result.Stderr, result.StderrEncoding = encode(stderr.buffer.Bytes())
	result.StderrTruncated = stderr.truncated
}

func encode(data []byte) (string, model.StreamEncoding) {
	if utf8.Valid(data) {
		return string(data), model.StreamEncodingUTF8
	}
	return base64.StdEncoding.EncodeToString(data), model.StreamEncodingBase64
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func stringPointer(value string) *string {
	return &value
}
