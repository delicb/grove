package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/paths"
)

const (
	MinimumVersionMajor = 2
	MinimumVersionMinor = 36
)

var versionPattern = regexp.MustCompile(`^git version ([0-9]+)\.([0-9]+)(?:\.([0-9]+))?(?:[ .-].*)?$`)

type Client interface {
	CheckVersion(ctx context.Context) error
	DetectRepository(ctx context.Context, path string) (model.RepositoryInfo, error)
	ListWorktrees(ctx context.Context, repo model.RepositoryInfo) ([]model.GitWorktree, error)
	ResolveCommit(ctx context.Context, repoPath, ref string) (string, error)
	BranchExists(ctx context.Context, repoPath, branch string) (bool, error)
	ValidateBranch(ctx context.Context, branch string) error
	AddWorktree(ctx context.Context, request AddRequest) error
	WorktreeGitDirectory(ctx context.Context, path string) (model.GitDirectoryIdentity, error)
	Status(ctx context.Context, path string) (model.WorktreeStatus, error)
	MoveWorktree(ctx context.Context, repoPath, path, target string) error
	RemoveWorktree(ctx context.Context, repoPath, path string) error
}

type AddRequest struct {
	RepositoryPath string
	Path           string
	Branch         string
	Base           string
	UseExisting    bool
}

type CommandClient struct {
	executable string
}

var _ Client = (*CommandClient)(nil)

func NewClient() *CommandClient {
	return NewClientWithExecutable("git")
}

func NewClientWithExecutable(executable string) *CommandClient {
	if executable == "" {
		executable = "git"
	}
	return &CommandClient{executable: executable}
}

func (client *CommandClient) CheckVersion(ctx context.Context) error {
	stdout, stderr, err := client.run(ctx, "--version")
	if err != nil {
		return newGitError("Grove could not get the Git version.", err, stderr)
	}

	matches := versionPattern.FindSubmatch(bytes.TrimSpace(stdout))
	if matches == nil {
		return newGitError("Grove could not read the Git version.", fmt.Errorf("unexpected version output %q", stdout), stderr)
	}
	major, majorErr := strconv.Atoi(string(matches[1]))
	minor, minorErr := strconv.Atoi(string(matches[2]))
	if majorErr != nil || minorErr != nil {
		return newGitError("Grove could not read the Git version.", errors.Join(majorErr, minorErr), stderr)
	}
	if major > MinimumVersionMajor || major == MinimumVersionMajor && minor >= MinimumVersionMinor {
		return nil
	}

	detected := string(matches[1]) + "." + string(matches[2])
	if len(matches[3]) > 0 {
		detected += "." + string(matches[3])
	}
	domainErr := model.NewError(
		model.ErrorGitVersionUnsupported,
		model.ExitGit,
		"Git 2.36 or later is required.",
		nil,
	)
	domainErr.Details["detected"] = detected
	domainErr.Details["minimum"] = "2.36"
	return domainErr
}

func (client *CommandClient) DetectRepository(ctx context.Context, path string) (model.RepositoryInfo, error) {
	selectedPath, err := paths.CanonicalDirectory(path)
	if err != nil {
		return model.RepositoryInfo{}, err
	}

	stdout, stderr, err := client.run(ctx, "-C", selectedPath, "rev-parse", "--is-inside-work-tree", "--is-bare-repository")
	if err != nil {
		var exitErr *exec.ExitError
		if ctx.Err() != nil || !errors.As(err, &exitErr) {
			return model.RepositoryInfo{}, newGitError("Grove could not inspect the Git repository.", err, stderr)
		}
		return model.RepositoryInfo{}, repositoryError(model.ErrorNotRepository, "The path is not inside a Git working tree.", selectedPath, err, stderr)
	}
	flags := splitOutputLines(stdout)
	if len(flags) != 2 {
		return model.RepositoryInfo{}, newGitError("Grove could not read the Git repository type.", fmt.Errorf("unexpected rev-parse output %q", stdout), stderr)
	}
	if flags[1] == "true" {
		return model.RepositoryInfo{}, repositoryError(model.ErrorBareRepository, "Bare Git repositories are not supported.", selectedPath, nil, nil)
	}
	if flags[0] != "true" || flags[1] != "false" {
		return model.RepositoryInfo{}, repositoryError(model.ErrorNotRepository, "The path is not inside a Git working tree.", selectedPath, nil, nil)
	}

	commonOutput, commonStderr, err := client.run(ctx, "-C", selectedPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return model.RepositoryInfo{}, newGitError("Grove could not get the common Git directory.", err, commonStderr)
	}
	commonDir, err := paths.CanonicalDirectory(trimGitValue(commonOutput))
	if err != nil {
		return model.RepositoryInfo{}, newGitError("Grove could not resolve the common Git directory.", err, commonStderr)
	}

	checkoutOutput, checkoutStderr, err := client.run(ctx, "-C", selectedPath, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return model.RepositoryInfo{}, newGitError("Grove could not get the selected Git checkout.", err, checkoutStderr)
	}
	selectedCheckout, err := paths.CanonicalDirectory(trimGitValue(checkoutOutput))
	if err != nil {
		return model.RepositoryInfo{}, newGitError("Grove could not resolve the selected Git checkout.", err, checkoutStderr)
	}

	repo := model.RepositoryInfo{CommonDir: commonDir, SelectedCheckout: selectedCheckout}
	worktrees, err := client.ListWorktrees(ctx, repo)
	if err != nil {
		return model.RepositoryInfo{}, err
	}
	if len(worktrees) == 0 {
		return model.RepositoryInfo{}, newGitError("The Git repository has no working tree.", errors.New("empty worktree list"), nil)
	}
	mainCheckout, err := paths.CanonicalDirectory(worktrees[0].Path)
	if err != nil {
		return model.RepositoryInfo{}, newGitError("Grove could not resolve the main Git checkout.", err, nil)
	}

	repo.MainCheckout = mainCheckout
	repo.DisplayName = filepath.Base(mainCheckout)
	return repo, nil
}

func (client *CommandClient) ListWorktrees(ctx context.Context, repo model.RepositoryInfo) ([]model.GitWorktree, error) {
	if err := validatePathValue(repo.CommonDir); err != nil {
		return nil, err
	}
	stdout, stderr, err := client.run(ctx, "--git-dir="+repo.CommonDir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, newGitError("Grove could not list the Git worktrees.", err, stderr)
	}
	worktrees, err := ParseWorktreePorcelain(stdout)
	if err != nil {
		return nil, newGitError("Grove could not read the Git worktree list.", err, stderr)
	}
	return worktrees, nil
}

func (client *CommandClient) ResolveCommit(ctx context.Context, repoPath, ref string) (string, error) {
	if err := validatePathValue(repoPath); err != nil {
		return "", err
	}
	if err := validateRefInput(ref, model.ErrorInvalidBase, "The base reference is not valid."); err != nil {
		return "", err
	}

	stdout, stderr, err := client.run(ctx, "-C", repoPath, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			domainErr := model.NewError(model.ErrorInvalidBase, model.ExitGit, "The base reference does not resolve to a commit.", err)
			domainErr.Details["ref"] = ref
			addStderrDetail(domainErr, stderr)
			return "", domainErr
		}
		return "", newGitError("Grove could not resolve the base reference.", err, stderr)
	}

	commit := trimGitValue(stdout)
	if !isObjectID(commit) {
		return "", newGitError("Grove could not read the resolved Git commit.", fmt.Errorf("unexpected object ID %q", commit), stderr)
	}
	return commit, nil
}

func (client *CommandClient) BranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	if err := validatePathValue(repoPath); err != nil {
		return false, err
	}
	if err := validateRefInput(branch, model.ErrorInvalidBranch, "The branch name is not valid."); err != nil {
		return false, err
	}

	_, stderr, err := client.run(ctx, "-C", repoPath, "show-ref", "--verify", "--quiet", "--", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, newGitError("Grove could not check the Git branch.", err, stderr)
}

func (client *CommandClient) ValidateBranch(ctx context.Context, branch string) error {
	if err := validateRefInput(branch, model.ErrorInvalidBranch, "The branch name is not valid."); err != nil {
		return err
	}
	_, stderr, err := client.run(ctx, "check-ref-format", "--branch", branch)
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		domainErr := model.NewError(model.ErrorInvalidBranch, model.ExitInvalidArguments, "The branch name is not valid.", err)
		domainErr.Details["branch"] = branch
		addStderrDetail(domainErr, stderr)
		return domainErr
	}
	return newGitError("Grove could not validate the Git branch.", err, stderr)
}

func (client *CommandClient) AddWorktree(ctx context.Context, request AddRequest) error {
	if err := validatePathValue(request.RepositoryPath); err != nil {
		return err
	}
	if err := validatePathValue(request.Path); err != nil {
		return err
	}
	if err := validateRefInput(request.Branch, model.ErrorInvalidBranch, "The branch name is not valid."); err != nil {
		return err
	}

	args := []string{"-C", request.RepositoryPath, "worktree", "add"}
	if request.UseExisting {
		if request.Base != "" {
			return model.NewError(model.ErrorInvalidArguments, model.ExitInvalidArguments, "--base cannot be used with --use-existing.", nil)
		}
		args = append(args, "--", request.Path, request.Branch)
	} else {
		if err := validateRefInput(request.Base, model.ErrorInvalidBase, "The base reference is not valid."); err != nil {
			return err
		}
		args = append(args, "-b", request.Branch, "--", request.Path, request.Base)
	}

	_, stderr, err := client.run(ctx, args...)
	if err == nil {
		return nil
	}
	return classifyAddError(request, err, stderr)
}

func (client *CommandClient) WorktreeGitDirectory(ctx context.Context, path string) (model.GitDirectoryIdentity, error) {
	if err := validatePathValue(path); err != nil {
		return model.GitDirectoryIdentity{}, err
	}
	stdout, stderr, err := client.run(ctx, "-C", path, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return model.GitDirectoryIdentity{}, newGitError("Grove could not get the worktree Git directory.", err, stderr)
	}
	gitDir, err := paths.CanonicalDirectory(trimGitValue(stdout))
	if err != nil {
		return model.GitDirectoryIdentity{}, newGitError("Grove could not resolve the worktree Git directory.", err, stderr)
	}
	info, err := os.Stat(gitDir)
	if err != nil {
		return model.GitDirectoryIdentity{}, newGitError("Grove could not inspect the worktree Git directory.", err, stderr)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return model.GitDirectoryIdentity{}, newGitError("Grove could not identify the worktree Git directory.", errors.New("file identity is unavailable"), stderr)
	}
	value := fmt.Sprintf("%s\x00%d\x00%d", gitDir, stat.Dev, stat.Ino)
	sum := sha256.Sum256([]byte(value))
	return model.GitDirectoryIdentity{Path: gitDir, Token: hex.EncodeToString(sum[:])}, nil
}

func (client *CommandClient) Status(ctx context.Context, path string) (model.WorktreeStatus, error) {
	if err := validatePathValue(path); err != nil {
		return model.WorktreeStatus{}, err
	}
	stdout, stderr, err := client.run(
		ctx,
		"--no-optional-locks",
		"-C", path,
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--ignored=matching",
	)
	if err != nil {
		return model.WorktreeStatus{}, newGitError("Grove could not get the Git worktree status.", err, stderr)
	}
	status, err := ParseStatusPorcelain(stdout)
	if err != nil {
		return model.WorktreeStatus{}, newGitError("Grove could not read the Git worktree status.", err, stderr)
	}
	return status, nil
}

func (client *CommandClient) MoveWorktree(ctx context.Context, repoPath, path, target string) error {
	if err := validatePathValue(repoPath); err != nil {
		return err
	}
	if err := validatePathValue(path); err != nil {
		return err
	}
	if err := validatePathValue(target); err != nil {
		return err
	}
	_, stderr, err := client.run(ctx, "-C", repoPath, "worktree", "move", "--", path, target)
	if err != nil {
		return newGitError("Git could not move the worktree.", err, stderr)
	}
	return nil
}

func (client *CommandClient) RemoveWorktree(ctx context.Context, repoPath, path string) error {
	if err := validatePathValue(repoPath); err != nil {
		return err
	}
	if err := validatePathValue(path); err != nil {
		return err
	}
	_, stderr, err := client.run(ctx, "-C", repoPath, "worktree", "remove", "--", path)
	if err != nil {
		return newGitError("Git could not remove the worktree.", err, stderr)
	}
	return nil
}

func (client *CommandClient) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, client.executable, args...)
	command.Env = localeEnvironment(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func splitOutputLines(output []byte) []string {
	value := bytes.TrimSuffix(output, []byte("\n"))
	if len(value) == 0 {
		return nil
	}
	parts := bytes.Split(value, []byte("\n"))
	lines := make([]string, len(parts))
	for index, part := range parts {
		lines[index] = string(part)
	}
	return lines
}

func trimGitValue(output []byte) string {
	return string(bytes.TrimSuffix(output, []byte("\n")))
}

func validatePathValue(path string) error {
	if err := paths.ValidateUTF8(path); err != nil {
		return err
	}
	if path == "" {
		return model.NewError(model.ErrorInvalidPath, model.ExitInvalidArguments, "The path must not be empty.", nil)
	}
	return nil
}

func validateRefInput(value string, code model.ErrorCode, message string) error {
	if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "-") {
		exitCode := model.ExitInvalidArguments
		if code == model.ErrorInvalidBase {
			exitCode = model.ExitGit
		}
		return model.NewError(code, exitCode, message, nil)
	}
	return nil
}

func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func classifyAddError(request AddRequest, cause error, stderr []byte) error {
	message := strings.ToLower(string(stderr))
	var code model.ErrorCode
	var text string
	switch {
	case strings.Contains(message, "is already checked out at") || strings.Contains(message, "is already used by worktree at"):
		code = model.ErrorBranchInUse
		text = "The branch is already in use by another worktree."
	case !request.UseExisting && strings.Contains(message, "a branch named") && strings.Contains(message, "already exists"):
		code = model.ErrorBranchExists
		text = "The branch exists. Use --use-existing to attach it."
	case strings.Contains(message, "already exists"):
		code = model.ErrorTargetExists
		text = "The target path already exists."
	case !request.UseExisting && (strings.Contains(message, "not a valid object name") || strings.Contains(message, "invalid reference") || strings.Contains(message, "unknown revision")):
		code = model.ErrorInvalidBase
		text = "The base reference does not resolve to a commit."
	default:
		return newGitError("Git could not create the worktree.", cause, stderr)
	}

	exitCode := model.ExitConflict
	if code == model.ErrorInvalidBase {
		exitCode = model.ExitGit
	}
	domainErr := model.NewError(code, exitCode, text, cause)
	domainErr.Details["branch"] = request.Branch
	domainErr.Details["path"] = request.Path
	addStderrDetail(domainErr, stderr)
	return domainErr
}

func repositoryError(code model.ErrorCode, message, path string, cause error, stderr []byte) *model.Error {
	domainErr := model.NewError(code, model.ExitGit, message, cause)
	domainErr.Details["path"] = path
	addStderrDetail(domainErr, stderr)
	return domainErr
}

func newGitError(message string, cause error, stderr []byte) *model.Error {
	domainErr := model.NewError(model.ErrorGit, model.ExitGit, message, cause)
	addStderrDetail(domainErr, stderr)
	return domainErr
}

func addStderrDetail(domainErr *model.Error, stderr []byte) {
	if value := strings.TrimSpace(string(stderr)); value != "" {
		domainErr.Details["stderr"] = value
	}
}

func localeEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		if strings.HasPrefix(value, "LC_ALL=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "LC_ALL=C")
}
