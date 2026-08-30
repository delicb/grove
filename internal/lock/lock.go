package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gofrs/flock"
)

const (
	lockDirectoryMode = 0o700
	lockFileMode      = 0o600
	maxTokenBytes     = 200
)

type Lock interface {
	Unlock() error
	Owned() bool
	Path() string
}

type Manager interface {
	AcquireOperation(token string) (Lock, error)
	TryOperation(token string) (Lock, bool, error)
	AcquireBootstrap(worktreeID int64) (Lock, error)
	TryBootstrap(worktreeID int64) (Lock, bool, error)
}

type FileManager struct {
	directory string
}

var _ Manager = (*FileManager)(nil)

func NewManager(directory string) (*FileManager, error) {
	if directory == "" || !utf8.ValidString(directory) || strings.IndexByte(directory, 0) >= 0 {
		return nil, fmt.Errorf("lock directory is not valid")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("make lock directory absolute: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("lock path must be a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect lock directory: %w", err)
	}
	if err := os.MkdirAll(absolute, lockDirectoryMode); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve lock directory: %w", err)
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return nil, fmt.Errorf("read lock directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("lock path must be a real directory")
	}
	if err := os.Chmod(canonical, lockDirectoryMode); err != nil {
		return nil, fmt.Errorf("protect lock directory: %w", err)
	}
	return &FileManager{directory: filepath.Clean(canonical)}, nil
}

func (manager *FileManager) Directory() string {
	return manager.directory
}

func (manager *FileManager) OperationPath(token string) (string, error) {
	if err := validateToken(token); err != nil {
		return "", err
	}
	return filepath.Join(manager.directory, token+".lock"), nil
}

func (manager *FileManager) BootstrapPath(worktreeID int64) (string, error) {
	if worktreeID <= 0 {
		return "", fmt.Errorf("worktree ID must be positive")
	}
	return filepath.Join(manager.directory, "bootstrap-"+strconv.FormatInt(worktreeID, 10)+".lock"), nil
}

func (manager *FileManager) AcquireOperation(token string) (Lock, error) {
	path, err := manager.OperationPath(token)
	if err != nil {
		return nil, err
	}
	return acquire(path)
}

func (manager *FileManager) TryOperation(token string) (Lock, bool, error) {
	path, err := manager.OperationPath(token)
	if err != nil {
		return nil, false, err
	}
	return tryAcquire(path)
}

func (manager *FileManager) AcquireBootstrap(worktreeID int64) (Lock, error) {
	path, err := manager.BootstrapPath(worktreeID)
	if err != nil {
		return nil, err
	}
	return acquire(path)
}

func (manager *FileManager) TryBootstrap(worktreeID int64) (Lock, bool, error) {
	path, err := manager.BootstrapPath(worktreeID)
	if err != nil {
		return nil, false, err
	}
	return tryAcquire(path)
}

type fileLock struct {
	mu    sync.Mutex
	flock *flock.Flock
	owned bool
}

var _ Lock = (*fileLock)(nil)

func acquire(path string) (Lock, error) {
	file := flock.New(path, flock.SetPermissions(lockFileMode))
	if err := file.Lock(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire lock %q: %w", path, err)
	}
	return &fileLock{flock: file, owned: true}, nil
}

func tryAcquire(path string) (Lock, bool, error) {
	file := flock.New(path, flock.SetPermissions(lockFileMode))
	locked, err := file.TryLock()
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("try lock %q: %w", path, err)
	}
	if !locked {
		_ = file.Close()
		return nil, false, nil
	}
	return &fileLock{flock: file, owned: true}, true, nil
}

func (lock *fileLock) Unlock() error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if !lock.owned {
		return fmt.Errorf("lock %q is not owned", lock.flock.Path())
	}
	if err := lock.flock.Unlock(); err != nil {
		return fmt.Errorf("unlock %q: %w", lock.flock.Path(), err)
	}
	lock.owned = false
	return nil
}

func (lock *fileLock) Owned() bool {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	return lock.owned && lock.flock.Locked()
}

func (lock *fileLock) Path() string {
	return lock.flock.Path()
}

func validateToken(token string) error {
	if token == "" || len(token) > maxTokenBytes || !utf8.ValidString(token) {
		return fmt.Errorf("operation token is not valid")
	}
	for _, character := range token {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("operation token is not valid")
	}
	if token == "." || token == ".." {
		return fmt.Errorf("operation token is not valid")
	}
	return nil
}
