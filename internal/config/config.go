package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultRoot            = "~/worktrees"
	DefaultBootstrapScript = "bootstrap-worktree.sh"
	DatabaseFilename       = "grove.db"
	LocksDirectoryName     = "locks"
)

type LookupEnv func(string) (string, bool)

type Options struct {
	ConfigPath      string
	Root            *string
	BootstrapScript *string
	NoBootstrap     bool
	LookupEnv       LookupEnv
	HomeDir         string
	WorkingDir      string
}

type Config struct {
	Root                  string
	RootSource            model.ValueSource
	BootstrapScript       string
	BootstrapScriptSource model.ValueSource
	DataDir               string
	DataDirSource         model.ValueSource
	ConfigPath            *string
}

type fileConfig struct {
	Root            *string `toml:"root"`
	BootstrapScript *string `toml:"bootstrap_script"`
}

func Load(options Options) (Config, error) {
	lookup := options.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	home, err := resolveHome(options.HomeDir)
	if err != nil {
		return Config{}, err
	}
	workingDirectory, err := resolveWorkingDirectory(options.WorkingDir)
	if err != nil {
		return Config{}, err
	}

	selectedPath, explicit, err := selectConfigPath(options.ConfigPath, lookup, home, workingDirectory)
	if err != nil {
		return Config{}, err
	}
	stored := fileConfig{}
	if selectedPath != nil {
		stored, err = readConfigFile(*selectedPath, explicit)
		if err != nil {
			return Config{}, err
		}
	}

	root := DefaultRoot
	rootSource := model.SourceBuiltIn
	bootstrapScript := DefaultBootstrapScript
	bootstrapSource := model.SourceBuiltIn
	if stored.Root != nil {
		root = *stored.Root
		rootSource = model.SourceConfig
	}
	if stored.BootstrapScript != nil {
		bootstrapScript = *stored.BootstrapScript
		bootstrapSource = model.SourceConfig
	}
	if value, exists := lookup("GROVE_ROOT"); exists && value != "" {
		root = value
		rootSource = model.SourceEnvironment
	}
	if value, exists := lookup("GROVE_BOOTSTRAP_SCRIPT"); exists {
		bootstrapScript = value
		bootstrapSource = model.SourceEnvironment
	}
	if options.Root != nil {
		root = *options.Root
		rootSource = model.SourceCommand
	}
	if options.BootstrapScript != nil {
		bootstrapScript = *options.BootstrapScript
		bootstrapSource = model.SourceCommand
	}
	if options.NoBootstrap {
		if options.BootstrapScript != nil {
			return Config{}, model.NewError(
				model.ErrorInvalidArguments,
				model.ExitInvalidArguments,
				"--bootstrap-script and --no-bootstrap cannot be used together.",
				nil,
			)
		}
		bootstrapScript = ""
		bootstrapSource = model.SourceDisabled
	}

	if root == "" {
		return Config{}, invalidConfig("The managed root must not be empty.", nil, selectedPath)
	}
	root, err = resolveConfiguredPath(root, home, workingDirectory, true)
	if err != nil {
		return Config{}, invalidConfig("The managed root is not valid.", err, selectedPath)
	}
	bootstrapScript, err = resolveConfiguredPath(bootstrapScript, home, workingDirectory, false)
	if err != nil {
		return Config{}, invalidConfig("The bootstrap script path is not valid.", err, selectedPath)
	}

	dataDir, dataSource, err := resolveDataDir(lookup, home, workingDirectory)
	if err != nil {
		return Config{}, invalidConfig("The data directory is not valid.", err, selectedPath)
	}

	return Config{
		Root:                  root,
		RootSource:            rootSource,
		BootstrapScript:       bootstrapScript,
		BootstrapScriptSource: bootstrapSource,
		DataDir:               dataDir,
		DataDirSource:         dataSource,
		ConfigPath:            selectedPath,
	}, nil
}

func (config Config) DatabasePath() string {
	return filepath.Join(config.DataDir, DatabaseFilename)
}

func (config Config) LockDir() string {
	return filepath.Join(config.DataDir, LocksDirectoryName)
}

func (config *Config) EnsureDataDirs() error {
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return invalidConfig("Grove could not create the data directory.", err, config.ConfigPath)
	}
	canonicalDataDir, err := filepath.EvalSymlinks(config.DataDir)
	if err != nil {
		return invalidConfig("Grove could not resolve the data directory.", err, config.ConfigPath)
	}
	canonicalDataDir, err = filepath.Abs(canonicalDataDir)
	if err != nil {
		return invalidConfig("Grove could not make the data directory absolute.", err, config.ConfigPath)
	}
	config.DataDir = filepath.Clean(canonicalDataDir)
	if err := os.Chmod(config.DataDir, 0o700); err != nil {
		return invalidConfig("Grove could not protect the data directory.", err, config.ConfigPath)
	}

	lockDir := config.LockDir()
	if info, err := os.Lstat(lockDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return invalidConfig("The lock path must be a real directory.", nil, config.ConfigPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return invalidConfig("Grove could not inspect the lock directory.", err, config.ConfigPath)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return invalidConfig("Grove could not create the lock directory.", err, config.ConfigPath)
	}
	if err := os.Chmod(lockDir, 0o700); err != nil {
		return invalidConfig("Grove could not protect the lock directory.", err, config.ConfigPath)
	}
	return nil
}

func (config Config) ShowData() model.ConfigShowData {
	return model.ConfigShowData{
		Root:                  config.Root,
		RootSource:            config.RootSource,
		BootstrapScript:       config.BootstrapScript,
		BootstrapScriptSource: config.BootstrapScriptSource,
		DataDir:               config.DataDir,
		ConfigPath:            config.ConfigPath,
	}
}

func (config Config) PathData() model.ConfigPathData {
	return model.ConfigPathData{ConfigPath: config.ConfigPath}
}

func selectConfigPath(explicitPath string, lookup LookupEnv, home, workingDirectory string) (*string, bool, error) {
	if explicitPath != "" {
		path, err := resolveConfiguredPath(explicitPath, home, workingDirectory, true)
		if err != nil {
			return nil, true, invalidConfig("The configuration path is not valid.", err, nil)
		}
		return &path, true, nil
	}
	if environmentPath, exists := lookup("GROVE_CONFIG"); exists && environmentPath != "" {
		path, err := resolveConfiguredPath(environmentPath, home, workingDirectory, true)
		if err != nil {
			return nil, true, invalidConfig("The configuration path is not valid.", err, nil)
		}
		return &path, true, nil
	}

	candidates := []string{}
	if xdgHome, exists := lookup("XDG_CONFIG_HOME"); exists && xdgHome != "" {
		resolved, err := resolveConfiguredPath(xdgHome, home, workingDirectory, true)
		if err != nil {
			return nil, false, invalidConfig("XDG_CONFIG_HOME is not valid.", err, nil)
		}
		candidates = append(candidates, filepath.Join(resolved, "grove", "config.toml"))
	}
	candidates = append(candidates, filepath.Join(home, ".config", "grove", "config.toml"))

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		_, err := os.Stat(candidate)
		if err == nil {
			path := candidate
			return &path, false, nil
		}
		if !os.IsNotExist(err) {
			return nil, false, invalidConfig("Grove could not inspect the configuration path.", err, &candidate)
		}
	}
	return nil, false, nil
}

func readConfigFile(path string, explicit bool) (fileConfig, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if explicit && errors.Is(err, os.ErrNotExist) {
			domainErr := model.NewError(
				model.ErrorConfigNotFound,
				model.ExitConfiguration,
				"The configuration file does not exist.",
				err,
			)
			domainErr.Details["path"] = path
			return fileConfig{}, domainErr
		}
		return fileConfig{}, invalidConfig("Grove could not read the configuration file.", err, &path)
	}

	var stored fileConfig
	decoder := toml.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return fileConfig{}, invalidConfig("The configuration file contains invalid TOML.", err, &path)
	}
	return stored, nil
}

func resolveDataDir(lookup LookupEnv, home, workingDirectory string) (string, model.ValueSource, error) {
	value := ""
	if xdgHome, exists := lookup("XDG_DATA_HOME"); exists && xdgHome != "" {
		resolved, err := resolveConfiguredPath(xdgHome, home, workingDirectory, true)
		if err != nil {
			return "", "", err
		}
		value = filepath.Join(resolved, "grove")
	} else {
		value = filepath.Join(home, ".local", "share", "grove")
	}
	source := model.SourceBuiltIn
	if environmentValue, exists := lookup("GROVE_DATA_DIR"); exists && environmentValue != "" {
		value = environmentValue
		source = model.SourceEnvironment
	}
	resolved, err := resolveConfiguredPath(value, home, workingDirectory, true)
	if err != nil {
		return "", "", err
	}
	return resolved, source, nil
}

func resolveHome(value string) (string, error) {
	if value == "" {
		var err error
		value, err = os.UserHomeDir()
		if err != nil {
			return "", invalidConfig("Grove could not find the home directory.", err, nil)
		}
	}
	if !validPathString(value) {
		return "", invalidConfig("The home directory is not valid.", nil, nil)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", invalidConfig("Grove could not make the home directory absolute.", err, nil)
	}
	return filepath.Clean(absolute), nil
}

func resolveWorkingDirectory(value string) (string, error) {
	if value == "" {
		var err error
		value, err = os.Getwd()
		if err != nil {
			return "", invalidConfig("Grove could not read the current directory.", err, nil)
		}
	}
	if !validPathString(value) {
		return "", invalidConfig("The current directory is not valid.", nil, nil)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", invalidConfig("Grove could not make the current directory absolute.", err, nil)
	}
	return filepath.Clean(absolute), nil
}

func resolveConfiguredPath(value, home, workingDirectory string, absolute bool) (string, error) {
	if value == "" {
		return "", nil
	}
	if !validPathString(value) {
		return "", fmt.Errorf("path must use valid UTF-8 and must not contain a null byte")
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~"+string(filepath.Separator)) {
		value = filepath.Join(home, strings.TrimPrefix(value, "~"+string(filepath.Separator)))
	} else if strings.HasPrefix(value, "~") {
		return "", fmt.Errorf("path uses an unsupported home directory form")
	}
	if absolute && !filepath.IsAbs(value) {
		value = filepath.Join(workingDirectory, value)
	}
	return filepath.Clean(value), nil
}

func validPathString(value string) bool {
	return utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func invalidConfig(message string, cause error, path *string) *model.Error {
	err := model.NewError(model.ErrorConfigInvalid, model.ExitConfiguration, message, cause)
	if path != nil {
		err.Details["path"] = *path
	}
	return err
}
