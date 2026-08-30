package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type resultEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Command       string            `json:"command"`
	Data          json.RawMessage   `json:"data"`
	Warnings      []json.RawMessage `json:"warnings"`
	Failures      []json.RawMessage `json:"failures"`
}

func TestBlackBoxCLIWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Grove does not support Windows")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "grove")
	build := exec.Command("go", "build", "-o", binary, "./cmd/grove")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	home := filepath.Join(temporary, "home")
	configHome := filepath.Join(temporary, "config")
	dataHome := filepath.Join(temporary, "data")
	managedRoot := filepath.Join(temporary, "managed root")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	environment := isolatedEnvironment(home, configHome, dataHome, managedRoot)

	configResult := runCommand(t, binary, environment, "config", "show", "--json")
	assertExitAndStreams(t, configResult, 0, true)
	configDocument := decodeResult(t, configResult.stdout, "config show")
	var configData struct {
		Root       string  `json:"root"`
		DataDir    string  `json:"data_dir"`
		ConfigPath *string `json:"config_path"`
	}
	decodeData(t, configDocument, &configData)
	if configData.Root != managedRoot || configData.DataDir != filepath.Join(dataHome, "grove") || configData.ConfigPath != nil {
		t.Errorf("config data = %#v", configData)
	}
	if _, err := os.Stat(filepath.Join(dataHome, "grove")); !os.IsNotExist(err) {
		t.Fatalf("config show created the data directory: %v", err)
	}

	versionResult := runCommand(t, binary, environment, "version")
	assertExitAndStreams(t, versionResult, 0, false)
	if string(versionResult.stdout) != "grove dev\n" {
		t.Errorf("version stdout = %q", versionResult.stdout)
	}

	repositoryOne := newRepository(t, temporary, environment, "repository one")
	repositoryTwo := newRepository(t, temporary, environment, "repository two")
	writeBootstrap(t, repositoryOne, environment)
	writeBootstrap(t, repositoryTwo, environment)

	createOne := runCommand(
		t,
		binary,
		environment,
		"create", "alpha", "--repo", repositoryOne, "--agent", "pi:black-box", "--bootstrap-script", "bootstrap-worktree.sh", "--json",
	)
	assertExitAndStreams(t, createOne, 0, true)
	createOneDocument := decodeResult(t, createOne.stdout, "create")
	var createOneData struct {
		Worktree struct {
			Path         string `json:"path"`
			CreatorAgent string `json:"creator_agent"`
		} `json:"worktree"`
		Bootstrap struct {
			State  string `json:"state"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		} `json:"bootstrap"`
	}
	decodeData(t, createOneDocument, &createOneData)
	if createOneData.Worktree.CreatorAgent != "pi:black-box" || createOneData.Bootstrap.State != "succeeded" {
		t.Errorf("create data = %#v", createOneData)
	}
	if createOneData.Bootstrap.Stdout != "bootstrap stdout\n" || createOneData.Bootstrap.Stderr != "bootstrap stderr\n" {
		t.Errorf("captured bootstrap output = %#v", createOneData.Bootstrap)
	}

	createTwo := runCommand(
		t,
		binary,
		environment,
		"create", "beta", "--repo", repositoryTwo, "--agent", "human:test", "--bootstrap-script", "bootstrap-worktree.sh",
	)
	assertExitAndStreams(t, createTwo, 0, false)
	if !strings.Contains(string(createTwo.stdout), "bootstrap stdout\n") || !strings.Contains(string(createTwo.stdout), "Created beta") {
		t.Errorf("human create stdout = %q", createTwo.stdout)
	}
	if !strings.Contains(string(createTwo.stderr), "bootstrap stderr\n") {
		t.Errorf("human create stderr = %q", createTwo.stderr)
	}

	failedBootstrap := runCommand(
		t,
		binary,
		environment,
		"create", "failed-bootstrap", "--repo", repositoryOne, "--bootstrap-script", "bootstrap-worktree.sh", "--json",
	)
	assertExitAndStreams(t, failedBootstrap, 6, true)
	failedDocument := decodeResult(t, failedBootstrap.stdout, "create")
	var failedData struct {
		Bootstrap struct {
			State    string `json:"state"`
			ExitCode *int   `json:"exit_code"`
		} `json:"bootstrap"`
	}
	decodeData(t, failedDocument, &failedData)
	if failedData.Bootstrap.State != "failed" || failedData.Bootstrap.ExitCode == nil || *failedData.Bootstrap.ExitCode != 23 {
		t.Errorf("failed bootstrap result = %#v", failedData.Bootstrap)
	}

	listResult := runCommand(t, binary, environment, "list", "--json")
	assertExitAndStreams(t, listResult, 0, true)
	listDocument := decodeResult(t, listResult.stdout, "list")
	var listData struct {
		Worktrees []struct {
			Name         string `json:"name"`
			Path         string `json:"path"`
			CreatorAgent string `json:"creator_agent"`
		} `json:"worktrees"`
		Summary struct {
			Active int `json:"active"`
		} `json:"summary"`
	}
	decodeData(t, listDocument, &listData)
	if listData.Summary.Active != 3 || len(listData.Worktrees) != 3 {
		t.Fatalf("list data = %#v", listData)
	}
	worktreePaths := make(map[string]string, len(listData.Worktrees))
	creators := make(map[string]string, len(listData.Worktrees))
	for _, worktree := range listData.Worktrees {
		worktreePaths[worktree.Name] = worktree.Path
		creators[worktree.Name] = worktree.CreatorAgent
		if !filepath.IsAbs(worktree.Path) {
			t.Errorf("worktree path is not absolute: %q", worktree.Path)
		}
	}
	if creators["alpha"] != "pi:black-box" || creators["beta"] != "human:test" || creators["failed-bootstrap"] != "pi:environment" {
		t.Errorf("creators = %#v", creators)
	}

	touchResult := runCommand(t, binary, environment, "touch", worktreePaths["alpha"], "--json")
	assertExitAndStreams(t, touchResult, 0, true)
	decodeResult(t, touchResult.stdout, "touch")

	statsResult := runCommand(t, binary, environment, "stats", "--all", "--json")
	assertExitAndStreams(t, statsResult, 0, true)
	statsDocument := decodeResult(t, statsResult.stdout, "stats")
	var statsData struct {
		Active          int  `json:"active"`
		RepositoryCount int  `json:"repository_count"`
		Removed         *int `json:"removed"`
		CreateFailed    *int `json:"create_failed"`
	}
	decodeData(t, statsDocument, &statsData)
	if statsData.Active != 3 || statsData.RepositoryCount != 2 || statsData.Removed == nil || statsData.CreateFailed == nil {
		t.Errorf("stats data = %#v", statsData)
	}

	unreadable := filepath.Join(worktreePaths["alpha"], "unreadable")
	if err := os.Mkdir(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	partialStats := runCommand(t, binary, environment, "stats", "--refresh", "--json")
	if partialStats.exitCode != 7 {
		t.Logf("permission-based partial scan did not fail on this file system: exit=%d stderr=%s", partialStats.exitCode, partialStats.stderr)
	} else {
		assertExitAndStreams(t, partialStats, 7, true)
		partialDocument := decodeResult(t, partialStats.stdout, "stats")
		if len(partialDocument.Failures) == 0 {
			t.Error("partial stats has no failures")
		}
	}
	if err := os.Chmod(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}

	unapprovedCleanup := runCommand(t, binary, environment, "cleanup", "--older-than", "1h", "--json")
	if unapprovedCleanup.exitCode != 5 || len(unapprovedCleanup.stdout) != 0 {
		t.Errorf("unapproved cleanup = %#v", unapprovedCleanup)
	}
	var errorDocument struct {
		Command string `json:"command"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(unapprovedCleanup.stderr, &errorDocument); err != nil {
		t.Fatal(err)
	}
	if errorDocument.Command != "cleanup" || errorDocument.Error.Code != "confirmation_required" {
		t.Errorf("cleanup error = %#v", errorDocument)
	}

	setAllActivityOld(t, filepath.Join(dataHome, "grove", "grove.db"))
	dirtyPath := filepath.Join(worktreePaths["alpha"], "local-data.txt")
	if err := os.WriteFile(dirtyPath, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	humanPlan := runCommand(t, binary, environment, "cleanup", "--older-than", "1h", "--dry-run")
	assertExitAndStreams(t, humanPlan, 0, false)
	for _, path := range worktreePaths {
		if !strings.Contains(string(humanPlan.stdout), path) {
			t.Errorf("human cleanup does not show %q:\n%s", path, humanPlan.stdout)
		}
	}
	if !strings.Contains(string(humanPlan.stdout), "2000-01-02T03:04:05Z") || !strings.Contains(string(humanPlan.stdout), "CUTOFF") {
		t.Errorf("human cleanup does not show activity and cutoff:\n%s", humanPlan.stdout)
	}

	dryRun := runCommand(t, binary, environment, "cleanup", "--older-than", "1h", "--dry-run", "--json")
	assertExitAndStreams(t, dryRun, 0, true)
	dryRunDocument := decodeResult(t, dryRun.stdout, "cleanup")
	var dryRunData struct {
		DryRun   bool `json:"dry_run"`
		Approved bool `json:"approved"`
		Summary  struct {
			Candidate int `json:"candidate"`
			Skipped   int `json:"skipped"`
		} `json:"summary"`
	}
	decodeData(t, dryRunDocument, &dryRunData)
	if !dryRunData.DryRun || dryRunData.Approved || dryRunData.Summary.Candidate != 2 || dryRunData.Summary.Skipped != 1 {
		t.Errorf("dry-run data = %#v", dryRunData)
	}

	cleanupResult := runCommand(t, binary, environment, "cleanup", "--older-than", "1h", "--yes", "--json")
	assertExitAndStreams(t, cleanupResult, 0, true)
	cleanupDocument := decodeResult(t, cleanupResult.stdout, "cleanup")
	var cleanupData struct {
		DryRun   bool `json:"dry_run"`
		Approved bool `json:"approved"`
		Summary  struct {
			Deleted int `json:"deleted"`
			Failed  int `json:"failed"`
		} `json:"summary"`
	}
	decodeData(t, cleanupDocument, &cleanupData)
	if cleanupData.DryRun || !cleanupData.Approved || cleanupData.Summary.Deleted != 2 || cleanupData.Summary.Failed != 0 {
		t.Errorf("cleanup data = %#v\n%s", cleanupData, cleanupResult.stdout)
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Errorf("cleanup removed dirty worktree data: %v", err)
	}
	for _, name := range []string{"beta", "failed-bootstrap"} {
		path := worktreePaths[name]
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("cleanup did not remove %q: %v", path, err)
		}
	}
	assertBranchExists(t, repositoryOne, environment, "alpha")
	assertBranchExists(t, repositoryOne, environment, "failed-bootstrap")
	assertBranchExists(t, repositoryTwo, environment, "beta")

	finalStats := runCommand(t, binary, environment, "stats", "--all", "--json")
	assertExitAndStreams(t, finalStats, 0, true)
	finalStatsDocument := decodeResult(t, finalStats.stdout, "stats")
	var finalStatsData struct {
		Active  int  `json:"active"`
		Removed *int `json:"removed"`
	}
	decodeData(t, finalStatsDocument, &finalStatsData)
	if finalStatsData.Active != 1 || finalStatsData.Removed == nil || *finalStatsData.Removed != 2 {
		t.Errorf("final stats data = %#v", finalStatsData)
	}
}

func isolatedEnvironment(home, configHome, dataHome, managedRoot string) []string {
	blocked := []string{
		"HOME=", "XDG_CONFIG_HOME=", "XDG_DATA_HOME=", "GROVE_CONFIG=", "GROVE_ROOT=", "GROVE_BOOTSTRAP_SCRIPT=",
		"GROVE_AGENT=", "GROVE_DATA_DIR=", "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=",
	}
	environment := make([]string, 0, len(os.Environ())+9)
	for _, value := range os.Environ() {
		skip := false
		for _, prefix := range blocked {
			if strings.HasPrefix(value, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			environment = append(environment, value)
		}
	}
	return append(environment,
		"HOME="+home,
		"XDG_CONFIG_HOME="+configHome,
		"XDG_DATA_HOME="+dataHome,
		"GROVE_ROOT="+managedRoot,
		"GROVE_AGENT=pi:environment",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func newRepository(t *testing.T, root string, environment []string, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	runGit(t, environment, "init", "-b", "main", path)
	runGit(t, environment, "-C", path, "config", "user.name", "Grove Tests")
	runGit(t, environment, "-C", path, "config", "user.email", "grove@example.invalid")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, environment, "-C", path, "add", "README.md")
	runGit(t, environment, "-C", path, "commit", "-m", "first")
	return path
}

func writeBootstrap(t *testing.T, repository string, environment []string) {
	t.Helper()
	contents := "printf 'bootstrap stdout\\n'\nprintf 'bootstrap stderr\\n' >&2\nif [ \"$GROVE_WORKTREE_NAME\" = failed-bootstrap ]; then exit 23; fi\n"
	if err := os.WriteFile(filepath.Join(repository, "bootstrap-worktree.sh"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, environment, "-C", repository, "add", "bootstrap-worktree.sh")
	runGit(t, environment, "-C", repository, "commit", "-m", "add bootstrap")
}

func assertBranchExists(t *testing.T, repository string, environment []string, branch string) {
	t.Helper()
	command := exec.Command("git", "-C", repository, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Errorf("branch %q is missing: %v\n%s", branch, err, output)
	}
}

func runGit(t *testing.T, environment []string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func runCommand(t *testing.T, binary string, environment []string, args ...string) commandResult {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = environment
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("grove %v failed to run: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return commandResult{stdout: []byte(stdout.String()), stderr: []byte(stderr.String()), exitCode: exitCode}
}

func assertExitAndStreams(t *testing.T, result commandResult, exitCode int, jsonMode bool) {
	t.Helper()
	if result.exitCode != exitCode {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", result.exitCode, exitCode, result.stdout, result.stderr)
	}
	if jsonMode && len(result.stderr) != 0 {
		t.Errorf("JSON result wrote stderr: %s", result.stderr)
	}
}

func decodeResult(t *testing.T, encoded []byte, command string) resultEnvelope {
	t.Helper()
	var document resultEnvelope
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, encoded)
	}
	if document.SchemaVersion != 1 || document.Command != command || document.Warnings == nil || document.Failures == nil {
		t.Errorf("result envelope = %#v", document)
	}
	return document
}

func decodeData(t *testing.T, document resultEnvelope, target any) {
	t.Helper()
	if err := json.Unmarshal(document.Data, target); err != nil {
		t.Fatalf("could not decode result data: %v\n%s", err, document.Data)
	}
}

func setAllActivityOld(t *testing.T, databasePath string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC).Format("2006-01-02T15:04:05.000000000Z")
	if _, err := database.Exec(`UPDATE worktrees SET last_grove_activity_at = ? WHERE state = 'active'`, old); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}
