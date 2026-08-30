package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Repository struct {
	Path        string
	FirstCommit string
}

func NewRepository(t testing.TB) Repository {
	t.Helper()
	return NewRepositoryNamed(t, "repository with spaces")
}

func NewRepositoryNamed(t testing.TB, name string) Repository {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, name)
	RunGit(t, "", "init", "-b", "main", path)
	RunGit(t, path, "config", "user.name", "Grove Tests")
	RunGit(t, path, "config", "user.email", "grove@example.invalid")
	WriteFile(t, filepath.Join(path, ".gitignore"), "*.log\n")
	WriteFile(t, filepath.Join(path, "README.md"), "first\n")
	RunGit(t, path, "add", ".gitignore", "README.md")
	RunGit(t, path, "commit", "-m", "first")
	first := strings.TrimSpace(RunGit(t, path, "rev-parse", "HEAD"))
	return Repository{Path: path, FirstCommit: first}
}

func RunGit(t testing.TB, directory string, args ...string) string {
	t.Helper()
	output, err := GitCommand(directory, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v returned %v: %s", args, err, output)
	}
	return string(output)
}

func GitCommand(directory string, args ...string) *exec.Cmd {
	if directory != "" {
		args = append([]string{"-C", directory}, args...)
	}
	command := exec.Command("git", args...)
	command.Env = GitEnvironment(os.Environ())
	return command
}

func GitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+3)
	for _, value := range environment {
		if strings.HasPrefix(value, "LC_ALL=") ||
			strings.HasPrefix(value, "GIT_CONFIG_GLOBAL=") ||
			strings.HasPrefix(value, "GIT_CONFIG_NOSYSTEM=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "LC_ALL=C", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

func IsolateGit(t testing.TB) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
