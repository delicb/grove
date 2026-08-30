package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/del-boy/grove/internal/model"
)

func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr model.ErrorCode
	}{
		{name: "minimum", output: "git version 2.36.0\n"},
		{name: "newer major", output: "git version 3.0.0\n"},
		{name: "vendor suffix", output: "git version 2.39.3 (Apple Git-146)\n"},
		{name: "windows suffix", output: "git version 2.40.1.windows.1\n"},
		{name: "old", output: "git version 2.35.9\n", wantErr: model.ErrorGitVersionUnsupported},
		{name: "malformed", output: "not git\n", wantErr: model.ErrorGit},
		{name: "invalid suffix", output: "git version 2.36invalid\n", wantErr: model.ErrorGit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := writeVersionCommand(t, test.output)
			err := NewClientWithExecutable(executable).CheckVersion(context.Background())
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckVersion() returned %v", err)
				}
				return
			}
			assertErrorCode(t, err, test.wantErr)
		})
	}
}

func TestParseWorktreePorcelain(t *testing.T) {
	data := strings.Join([]string{
		"worktree /repo with spaces",
		"HEAD 1111111111111111111111111111111111111111",
		"branch refs/heads/main",
		"",
		"worktree /linked\ncheckout",
		"HEAD 2222222222222222222222222222222222222222",
		"detached",
		"locked cleanup in progress",
		"future-field value",
		"",
	}, "\x00") + "\x00"

	worktrees, err := ParseWorktreePorcelain([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("worktree count = %d, want 2", len(worktrees))
	}
	if !worktrees[0].Main || worktrees[0].Path != "/repo with spaces" || pointerValue(worktrees[0].Branch) != "main" {
		t.Errorf("main worktree = %#v", worktrees[0])
	}
	if worktrees[0].DetachedCommit != nil || worktrees[0].Locked {
		t.Errorf("main worktree state = %#v", worktrees[0])
	}
	if worktrees[1].Main || worktrees[1].Path != "/linked\ncheckout" || worktrees[1].Branch != nil || !worktrees[1].Locked {
		t.Errorf("linked worktree = %#v", worktrees[1])
	}
	if pointerValue(worktrees[1].DetachedCommit) != worktrees[1].HEAD {
		t.Errorf("detached commit = %v, HEAD = %q", worktrees[1].DetachedCommit, worktrees[1].HEAD)
	}
}

func TestParseWorktreePorcelainRejectsMalformedData(t *testing.T) {
	tests := map[string][]byte{
		"relative path":  []byte("worktree relative\x00HEAD 1111111111111111111111111111111111111111\x00branch refs/heads/main\x00\x00"),
		"invalid HEAD":   []byte("worktree /one\x00HEAD short\x00branch refs/heads/main\x00\x00"),
		"invalid branch": []byte("worktree /one\x00HEAD 1111111111111111111111111111111111111111\x00branch main\x00\x00"),
		"no separator":   []byte("worktree /one\x00HEAD 1111111111111111111111111111111111111111\x00branch refs/heads/main\x00worktree /two\x00"),
		"no state":       []byte("worktree /one\x00HEAD 1111111111111111111111111111111111111111\x00\x00"),
		"not delimited":  []byte("worktree /one\x00HEAD 1111111111111111111111111111111111111111\x00branch refs/heads/main"),
		"invalid UTF-8":  append([]byte("worktree /"), 0xff, 0, 0),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWorktreePorcelain(data); err == nil {
				t.Error("ParseWorktreePorcelain() returned nil")
			}
		})
	}
}

func TestParseStatusPorcelain(t *testing.T) {
	data := []byte("M  staged.txt\x00 M modified.txt\x00?? untracked file\x00!! ignored.log\x00R  renamed.txt\x00old.txt\x00")
	status, err := ParseStatusPorcelain(data)
	if err != nil {
		t.Fatal(err)
	}
	want := model.WorktreeStatus{Staged: true, Modified: true, Untracked: true, Ignored: true}
	if !reflect.DeepEqual(status, want) {
		t.Errorf("status = %#v, want %#v", status, want)
	}
	if _, err := ParseStatusPorcelain([]byte("R  renamed.txt\x00")); err == nil {
		t.Error("ParseStatusPorcelain accepted a rename without a source path")
	}
	if _, err := ParseStatusPorcelain([]byte("bad\x00")); err == nil {
		t.Error("ParseStatusPorcelain accepted malformed output")
	}
}

func TestDetectRepositoryFromMainAndLinkedCheckout(t *testing.T) {
	isolateGit(t)
	repo, _ := createRepository(t, "repository with spaces")
	client := NewClient()
	ctx := context.Background()
	if err := client.CheckVersion(ctx); err != nil {
		t.Fatal(err)
	}

	subdirectory := filepath.Join(repo, "directory with spaces")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	mainInfo, err := client.DetectRepository(ctx, subdirectory)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	canonicalCommon, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if mainInfo.CommonDir != canonicalCommon || mainInfo.MainCheckout != canonicalRepo || mainInfo.SelectedCheckout != canonicalRepo {
		t.Errorf("main repository info = %#v", mainInfo)
	}
	if mainInfo.DisplayName != filepath.Base(repo) {
		t.Errorf("display name = %q", mainInfo.DisplayName)
	}

	linked := filepath.Join(filepath.Dir(repo), "linked checkout with spaces")
	runGit(t, repo, "worktree", "add", "-b", "linked-branch", "--", linked, "HEAD")
	canonicalLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	linkedSubdirectory := filepath.Join(linked, "nested")
	if err := os.Mkdir(linkedSubdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedInfo, err := client.DetectRepository(ctx, linkedSubdirectory)
	if err != nil {
		t.Fatal(err)
	}
	if linkedInfo.CommonDir != mainInfo.CommonDir || linkedInfo.MainCheckout != mainInfo.MainCheckout || linkedInfo.SelectedCheckout != canonicalLinked {
		t.Errorf("linked repository info = %#v", linkedInfo)
	}

	runGit(t, repo, "worktree", "lock", "--reason", "test lock", linked)
	worktrees, err := client.ListWorktrees(ctx, linkedInfo)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("worktree count = %d, want 2", len(worktrees))
	}
	if !worktrees[0].Main || worktrees[0].Path != canonicalRepo || pointerValue(worktrees[0].Branch) != "main" {
		t.Errorf("main worktree = %#v", worktrees[0])
	}
	if worktrees[1].Main || worktrees[1].Path != canonicalLinked || pointerValue(worktrees[1].Branch) != "linked-branch" || !worktrees[1].Locked {
		t.Errorf("linked worktree = %#v", worktrees[1])
	}
}

func TestDetectRepositoryResolvesSymlinkAndRejectsInvalidRepositories(t *testing.T) {
	isolateGit(t)
	repo, _ := createRepository(t, "repo")
	client := NewClient()
	ctx := context.Background()

	link := filepath.Join(filepath.Dir(repo), "repository-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	info, err := client.DetectRepository(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if info.SelectedCheckout != canonicalRepo {
		t.Errorf("selected checkout = %q, want %q", info.SelectedCheckout, canonicalRepo)
	}

	nonRepository := t.TempDir()
	_, err = client.DetectRepository(ctx, nonRepository)
	assertErrorCode(t, err, model.ErrorNotRepository)

	bare := filepath.Join(t.TempDir(), "bare.git")
	runGit(t, "", "init", "--bare", bare)
	_, err = client.DetectRepository(ctx, bare)
	assertErrorCode(t, err, model.ErrorBareRepository)

	_, err = client.DetectRepository(ctx, filepath.Join(repo, ".git"))
	assertErrorCode(t, err, model.ErrorNotRepository)

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = client.DetectRepository(ctx, file)
	assertErrorCode(t, err, model.ErrorInvalidPath)

	_, err = client.DetectRepository(ctx, string([]byte{0xff}))
	assertErrorCode(t, err, model.ErrorInvalidPath)
}

func TestRefBranchAddStatusAndRemove(t *testing.T) {
	isolateGit(t)
	repo, firstCommit := createRepository(t, "repository")
	client := NewClient()
	ctx := context.Background()

	if err := client.ValidateBranch(ctx, "feature/nested"); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"", "-option", "bad branch", "ends.", string([]byte{0xff})} {
		err := client.ValidateBranch(ctx, branch)
		assertErrorCode(t, err, model.ErrorInvalidBranch)
	}

	head, err := client.ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head == firstCommit {
		t.Fatal("repository setup did not create a second commit")
	}
	resolvedFirst, err := client.ResolveCommit(ctx, repo, firstCommit)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedFirst != firstCommit {
		t.Errorf("resolved commit = %q, want %q", resolvedFirst, firstCommit)
	}
	_, err = client.ResolveCommit(ctx, repo, "missing-reference")
	assertErrorCode(t, err, model.ErrorInvalidBase)
	_, err = client.ResolveCommit(ctx, repo, "-option")
	assertErrorCode(t, err, model.ErrorInvalidBase)

	exists, err := client.BranchExists(ctx, repo, "feature/nested")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("feature branch exists before creation")
	}

	target := filepath.Join(filepath.Dir(repo), "new worktree with spaces")
	request := AddRequest{
		RepositoryPath: repo,
		Path:           target,
		Branch:         "feature/nested",
		Base:           firstCommit,
	}
	if err := client.AddWorktree(ctx, request); err != nil {
		t.Fatal(err)
	}
	gitDirectory, err := client.WorktreeGitDirectory(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(gitDirectory.Path); err != nil || !info.IsDir() || gitDirectory.Token == "" {
		t.Errorf("worktree Git directory = %#v, %v", gitDirectory, err)
	}
	resolvedFeature, err := client.ResolveCommit(ctx, target, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedFeature != firstCommit {
		t.Errorf("feature HEAD = %q, want %q", resolvedFeature, firstCommit)
	}
	exists, err = client.BranchExists(ctx, repo, "feature/nested")
	if err != nil || !exists {
		t.Errorf("BranchExists() = %t, %v", exists, err)
	}

	status, err := client.Status(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Clean(false) {
		t.Errorf("new worktree status = %#v", status)
	}
	writeFile(t, filepath.Join(target, "staged.txt"), "staged\n")
	runGit(t, target, "add", "staged.txt")
	writeFile(t, filepath.Join(target, "README.md"), "modified\n")
	writeFile(t, filepath.Join(target, "untracked file"), "untracked\n")
	writeFile(t, filepath.Join(target, "ignored.log"), "ignored\n")
	status, err = client.Status(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus := model.WorktreeStatus{Staged: true, Modified: true, Untracked: true, Ignored: true}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Errorf("dirty status = %#v, want %#v", status, wantStatus)
	}

	if err := client.RemoveWorktree(ctx, repo, target); err == nil {
		t.Fatal("RemoveWorktree removed a dirty worktree")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dirty worktree no longer exists: %v", err)
	}
	runGit(t, target, "reset", "--hard")
	runGit(t, target, "clean", "-fdx")
	if err := client.RemoveWorktree(ctx, repo, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed worktree stat error = %v", err)
	}
	exists, err = client.BranchExists(ctx, repo, "feature/nested")
	if err != nil || !exists {
		t.Errorf("removed worktree branch exists = %t, error = %v", exists, err)
	}
}

func TestAddExistingBranchAndGitConflictErrors(t *testing.T) {
	isolateGit(t)
	repo, _ := createRepository(t, "repository")
	client := NewClient()
	ctx := context.Background()
	runGit(t, repo, "branch", "existing", "HEAD")

	target := filepath.Join(filepath.Dir(repo), "existing branch checkout")
	request := AddRequest{RepositoryPath: repo, Path: target, Branch: "existing", UseExisting: true}
	if err := client.AddWorktree(ctx, request); err != nil {
		t.Fatal(err)
	}

	secondTarget := filepath.Join(filepath.Dir(repo), "second existing checkout")
	err := client.AddWorktree(ctx, AddRequest{RepositoryPath: repo, Path: secondTarget, Branch: "existing", UseExisting: true})
	assertErrorCode(t, err, model.ErrorBranchInUse)

	err = client.AddWorktree(ctx, AddRequest{RepositoryPath: repo, Path: secondTarget, Branch: "other", Base: "HEAD", UseExisting: true})
	assertErrorCode(t, err, model.ErrorInvalidArguments)

	if err := client.RemoveWorktree(ctx, repo, target); err != nil {
		t.Fatal(err)
	}
	err = client.AddWorktree(ctx, AddRequest{RepositoryPath: repo, Path: secondTarget, Branch: "existing", Base: "HEAD"})
	assertErrorCode(t, err, model.ErrorBranchExists)
}

func TestListWorktreesReportsDetachedCheckout(t *testing.T) {
	isolateGit(t)
	repo, _ := createRepository(t, "repository")
	detachedPath := filepath.Join(filepath.Dir(repo), "detached checkout")
	runGit(t, repo, "worktree", "add", "--detach", "--", detachedPath, "HEAD")

	client := NewClient()
	info, err := client.DetectRepository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := client.ListWorktrees(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("worktree count = %d, want 2", len(worktrees))
	}
	detached := worktrees[1]
	if detached.Branch != nil || pointerValue(detached.DetachedCommit) != detached.HEAD || detached.Main {
		t.Errorf("detached worktree = %#v", detached)
	}
}

func TestResolveCommitRejectsUnbornHead(t *testing.T) {
	isolateGit(t)
	repo := filepath.Join(t.TempDir(), "unborn")
	runGit(t, "", "init", "-b", "main", repo)
	client := NewClient()
	if _, err := client.DetectRepository(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	_, err := client.ResolveCommit(context.Background(), repo, "HEAD")
	assertErrorCode(t, err, model.ErrorInvalidBase)
}

func writeVersionCommand(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git-version")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$LC_ALL\" = C ] || exit 9\nprintf '%%s' %s\n", shellQuote(output))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func createRepository(t *testing.T, name string) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	runGit(t, "", "init", "-b", "main", repo)
	runGit(t, repo, "config", "user.name", "Grove Tests")
	runGit(t, repo, "config", "user.email", "grove@example.invalid")
	writeFile(t, filepath.Join(repo, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(repo, "README.md"), "first\n")
	runGit(t, repo, "add", ".gitignore", "README.md")
	runGit(t, repo, "commit", "-m", "first")
	first := runGit(t, repo, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(repo, "README.md"), "second\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "second")
	return repo, strings.TrimSpace(first)
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	if directory != "" {
		args = append([]string{"-C", directory}, args...)
	}
	command := exec.Command("git", args...)
	command.Env = testGitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

func testGitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+3)
	for _, value := range environment {
		if strings.HasPrefix(value, "LC_ALL=") || strings.HasPrefix(value, "GIT_CONFIG_GLOBAL=") || strings.HasPrefix(value, "GIT_CONFIG_NOSYSTEM=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "LC_ALL=C", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func assertErrorCode(t *testing.T, err error, code model.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %T %v, want *model.Error", err, err)
	}
	if domainErr.Code != code {
		t.Fatalf("error code = %s, want %s: %v", domainErr.Code, code, err)
	}
}
