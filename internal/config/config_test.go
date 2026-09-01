package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/del-boy/grove/internal/model"
)

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	config, err := Load(testOptions(home, workingDirectory, nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(home, "worktrees") {
		t.Errorf("Root = %q", config.Root)
	}
	if config.RootSource != model.SourceBuiltIn {
		t.Errorf("RootSource = %q", config.RootSource)
	}
	if config.BootstrapScript != DefaultBootstrapScript {
		t.Errorf("BootstrapScript = %q", config.BootstrapScript)
	}
	if config.BootstrapScriptSource != model.SourceBuiltIn {
		t.Errorf("BootstrapScriptSource = %q", config.BootstrapScriptSource)
	}
	if config.DataDir != filepath.Join(home, ".local", "share", "grove") {
		t.Errorf("DataDir = %q", config.DataDir)
	}
	if config.DataDirSource != model.SourceBuiltIn {
		t.Errorf("DataDirSource = %q", config.DataDirSource)
	}
	if config.ConfigPath != nil {
		t.Errorf("ConfigPath = %q, want nil", *config.ConfigPath)
	}
	if config.DatabasePath() != filepath.Join(config.DataDir, DatabaseFilename) {
		t.Errorf("DatabasePath() = %q", config.DatabasePath())
	}
	if config.LockDir() != filepath.Join(config.DataDir, LocksDirectoryName) {
		t.Errorf("LockDir() = %q", config.LockDir())
	}
	if config.BootstrapScript == "" {
		t.Error("default bootstrap is disabled")
	}
}

func TestLoadExplicitConfig(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	writeTestFile(t, filepath.Join(workingDirectory, "grove.toml"), `
root = "~/managed"
bootstrap_script = "scripts/../setup.sh"
`)
	options := testOptions(home, workingDirectory, nil)
	options.ConfigPath = "grove.toml"
	config, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConfigPath == nil || *config.ConfigPath != filepath.Join(workingDirectory, "grove.toml") {
		t.Fatalf("ConfigPath = %v", config.ConfigPath)
	}
	if config.Root != filepath.Join(home, "managed") || config.RootSource != model.SourceConfig {
		t.Errorf("root = %q from %q", config.Root, config.RootSource)
	}
	if config.BootstrapScript != "setup.sh" || config.BootstrapScriptSource != model.SourceConfig {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}
}

func TestConfigPathPriority(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	explicitPath := filepath.Join(workingDirectory, "explicit.toml")
	environmentPath := filepath.Join(workingDirectory, "environment.toml")
	xdgHome := filepath.Join(workingDirectory, "xdg")
	fallbackPath := filepath.Join(home, ".config", "grove", "config.toml")
	xdgPath := filepath.Join(xdgHome, "grove", "config.toml")
	writeTestFile(t, explicitPath, `root = "explicit"`)
	writeTestFile(t, environmentPath, `root = "environment"`)
	writeTestFile(t, xdgPath, `root = "xdg"`)
	writeTestFile(t, fallbackPath, `root = "fallback"`)

	environment := map[string]string{
		"GROVE_CONFIG":    environmentPath,
		"XDG_CONFIG_HOME": xdgHome,
	}
	options := testOptions(home, workingDirectory, environment)
	options.ConfigPath = explicitPath
	config, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "explicit") {
		t.Errorf("explicit root = %q", config.Root)
	}

	options.ConfigPath = ""
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "environment") {
		t.Errorf("environment root = %q", config.Root)
	}

	delete(environment, "GROVE_CONFIG")
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "xdg") {
		t.Errorf("XDG root = %q", config.Root)
	}

	if err := os.Remove(xdgPath); err != nil {
		t.Fatal(err)
	}
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "fallback") {
		t.Errorf("fallback root = %q", config.Root)
	}
}

func TestMissingExplicitConfig(t *testing.T) {
	tests := []struct {
		name        string
		configPath  string
		environment map[string]string
	}{
		{"command", "missing.toml", nil},
		{"environment", "", map[string]string{"GROVE_CONFIG": "missing.toml"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			workingDirectory := t.TempDir()
			options := testOptions(home, workingDirectory, test.environment)
			options.ConfigPath = test.configPath
			_, err := Load(options)
			assertConfigError(t, err, model.ErrorConfigNotFound)
		})
	}
}

func TestMissingImplicitConfigUsesDefaults(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	config, err := Load(testOptions(home, workingDirectory, map[string]string{
		"GROVE_CONFIG":    "",
		"XDG_CONFIG_HOME": filepath.Join(workingDirectory, "missing"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.ConfigPath != nil || config.RootSource != model.SourceBuiltIn {
		t.Errorf("config = %#v", config)
	}
}

func TestInvalidConfigFile(t *testing.T) {
	tests := map[string]string{
		"invalid TOML": `root = [`,
		"unknown key":  `unknown = "value"`,
		"wrong type":   `root = 12`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			workingDirectory := t.TempDir()
			path := filepath.Join(workingDirectory, "config.toml")
			writeTestFile(t, path, contents)
			options := testOptions(home, workingDirectory, nil)
			options.ConfigPath = path
			_, err := Load(options)
			assertConfigError(t, err, model.ErrorConfigInvalid)
		})
	}
}

func TestValuePriority(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	path := filepath.Join(workingDirectory, "config.toml")
	writeTestFile(t, path, `
root = "file-root"
bootstrap_script = "file-script"
`)
	environment := map[string]string{
		"GROVE_ROOT":             "environment-root",
		"GROVE_BOOTSTRAP_SCRIPT": "environment-script",
	}
	commandRoot := "command-root"
	commandScript := "command-script"
	options := testOptions(home, workingDirectory, environment)
	options.ConfigPath = path
	options.Root = &commandRoot
	options.BootstrapScript = &commandScript
	config, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, commandRoot) || config.RootSource != model.SourceCommand {
		t.Errorf("root = %q from %q", config.Root, config.RootSource)
	}
	if config.BootstrapScript != commandScript || config.BootstrapScriptSource != model.SourceCommand {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}

	options.Root = nil
	options.BootstrapScript = nil
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "environment-root") || config.RootSource != model.SourceEnvironment {
		t.Errorf("root = %q from %q", config.Root, config.RootSource)
	}
	if config.BootstrapScript != "environment-script" || config.BootstrapScriptSource != model.SourceEnvironment {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}

	delete(environment, "GROVE_ROOT")
	delete(environment, "GROVE_BOOTSTRAP_SCRIPT")
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "file-root") || config.RootSource != model.SourceConfig {
		t.Errorf("root = %q from %q", config.Root, config.RootSource)
	}
	if config.BootstrapScript != "file-script" || config.BootstrapScriptSource != model.SourceConfig {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}
}

func TestEmptyValues(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	path := filepath.Join(workingDirectory, "config.toml")
	writeTestFile(t, path, `
root = "file-root"
bootstrap_script = "file-script"
`)
	environment := map[string]string{
		"GROVE_ROOT":             "",
		"GROVE_BOOTSTRAP_SCRIPT": "",
		"GROVE_DATA_DIR":         "",
	}
	options := testOptions(home, workingDirectory, environment)
	options.ConfigPath = path
	config, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "file-root") || config.RootSource != model.SourceConfig {
		t.Errorf("root = %q from %q", config.Root, config.RootSource)
	}
	if config.BootstrapScript != "" || config.BootstrapScriptSource != model.SourceEnvironment {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}
	if config.DataDir != filepath.Join(home, ".local", "share", "grove") {
		t.Errorf("DataDir = %q", config.DataDir)
	}

	writeTestFile(t, path, `bootstrap_script = ""`)
	delete(environment, "GROVE_BOOTSTRAP_SCRIPT")
	config, err = Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.BootstrapScript != "" || config.BootstrapScriptSource != model.SourceConfig {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}

	writeTestFile(t, path, `root = ""`)
	_, err = Load(options)
	assertConfigError(t, err, model.ErrorConfigInvalid)
}

func TestNoBootstrap(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	options := testOptions(home, workingDirectory, nil)
	options.NoBootstrap = true
	config, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.BootstrapScript != "" || config.BootstrapScriptSource != model.SourceDisabled {
		t.Errorf("bootstrap = %q from %q", config.BootstrapScript, config.BootstrapScriptSource)
	}

	script := "script.sh"
	options.BootstrapScript = &script
	_, err = Load(options)
	assertConfigError(t, err, model.ErrorInvalidArguments)
}

func TestPathExpansion(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	environment := map[string]string{
		"GROVE_ROOT":             "relative/root",
		"GROVE_BOOTSTRAP_SCRIPT": "~/scripts/setup.sh",
		"GROVE_DATA_DIR":         "relative/data",
	}
	config, err := Load(testOptions(home, workingDirectory, environment))
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != filepath.Join(workingDirectory, "relative", "root") {
		t.Errorf("Root = %q", config.Root)
	}
	if config.BootstrapScript != filepath.Join(home, "scripts", "setup.sh") {
		t.Errorf("BootstrapScript = %q", config.BootstrapScript)
	}
	if config.DataDir != filepath.Join(workingDirectory, "relative", "data") {
		t.Errorf("DataDir = %q", config.DataDir)
	}

	environment["GROVE_ROOT"] = "~someone/root"
	_, err = Load(testOptions(home, workingDirectory, environment))
	assertConfigError(t, err, model.ErrorConfigInvalid)

	environment["GROVE_ROOT"] = string([]byte{0xff})
	_, err = Load(testOptions(home, workingDirectory, environment))
	assertConfigError(t, err, model.ErrorConfigInvalid)
}

func TestXDGDataHome(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	config, err := Load(testOptions(home, workingDirectory, map[string]string{
		"XDG_DATA_HOME": "xdg-data",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.DataDir != filepath.Join(workingDirectory, "xdg-data", "grove") {
		t.Errorf("DataDir = %q", config.DataDir)
	}
}

func TestEnsureDataDirs(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data", "grove")
	config := Config{DataDir: dataDir}
	if err := config.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dataDir, filepath.Join(dataDir, LocksDirectoryName)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", path)
		}
	}
	if err := config.EnsureDataDirs(); err != nil {
		t.Errorf("second EnsureDataDirs returned %v", err)
	}
}

func TestEnsureDataDirsRejectsSymlinkLockDirectory(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "other-locks")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, LocksDirectoryName)); err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: dataDir}
	assertConfigError(t, config.EnsureDataDirs(), model.ErrorConfigInvalid)
}

func TestEnsureDataDirsRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	writeTestFile(t, path, "data")
	config := Config{DataDir: path}
	err := config.EnsureDataDirs()
	assertConfigError(t, err, model.ErrorConfigInvalid)
}

func TestShowAndPathData(t *testing.T) {
	path := "/tmp/config.toml"
	config := Config{
		Root:                  "/tmp/worktrees",
		RootSource:            model.SourceConfig,
		BootstrapScript:       "setup.sh",
		BootstrapScriptSource: model.SourceEnvironment,
		DataDir:               "/tmp/data",
		ConfigPath:            &path,
	}
	show := config.ShowData()
	if show.Root != config.Root || show.ConfigPath != config.ConfigPath || show.DataDir != config.DataDir {
		t.Errorf("ShowData() = %#v", show)
	}
	pathData := config.PathData()
	if pathData.ConfigPath != config.ConfigPath {
		t.Errorf("PathData() = %#v", pathData)
	}
}

func testOptions(home, workingDirectory string, environment map[string]string) Options {
	return Options{
		HomeDir:    home,
		WorkingDir: workingDirectory,
		LookupEnv: func(name string) (string, bool) {
			value, exists := environment[name]
			return value, exists
		},
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertConfigError(t *testing.T, err error, code model.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %#v, want *model.Error", err)
	}
	if domainErr.Code != code {
		t.Errorf("error code = %q, want %q", domainErr.Code, code)
	}
}
