package lock

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOperationAndBootstrapLocks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lock directory")
	first, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}

	operation, err := first.AcquireOperation("operation-token")
	if err != nil {
		t.Fatal(err)
	}
	if !operation.Owned() || !operation.Locked() {
		t.Fatal("acquired operation lock is not owned")
	}
	wantOperationPath := filepath.Join(first.Directory(), "operation-token.lock")
	if operation.Path() != wantOperationPath {
		t.Errorf("operation path = %q, want %q", operation.Path(), wantOperationPath)
	}
	if candidate, acquired, err := second.TryOperation("operation-token"); err != nil || acquired || candidate != nil {
		t.Errorf("TryOperation() = %v, %t, %v", candidate, acquired, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if operation.Owned() {
		t.Error("released operation lock is owned")
	}
	if err := operation.Unlock(); err == nil {
		t.Error("a second operation unlock succeeded")
	}
	operation, acquired, err := second.TryOperation("operation-token")
	if err != nil || !acquired || operation == nil {
		t.Fatalf("TryOperation() after release = %v, %t, %v", operation, acquired, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	bootstrap, err := first.AcquireBootstrap(42)
	if err != nil {
		t.Fatal(err)
	}
	wantBootstrapPath := filepath.Join(first.Directory(), "bootstrap-42.lock")
	if bootstrap.Path() != wantBootstrapPath {
		t.Errorf("bootstrap path = %q, want %q", bootstrap.Path(), wantBootstrapPath)
	}
	if candidate, acquired, err := second.TryBootstrap(42); err != nil || acquired || candidate != nil {
		t.Errorf("TryBootstrap() = %v, %t, %v", candidate, acquired, err)
	}
	if err := bootstrap.Unlock(); err != nil {
		t.Fatal(err)
	}
	bootstrap, acquired, err = second.TryBootstrap(42)
	if err != nil || !acquired || bootstrap == nil {
		t.Fatalf("TryBootstrap() after release = %v, %t, %v", bootstrap, acquired, err)
	}
	if err := bootstrap.Unlock(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{wantOperationPath, wantBootstrapPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("lock file %q mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestAcquireBlocksUntilRelease(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.AcquireOperation("blocking")
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan Lock, 1)
	failure := make(chan error, 1)
	go func() {
		second, err := manager.AcquireOperation("blocking")
		if err != nil {
			failure <- err
			return
		}
		result <- second
	}()

	select {
	case lock := <-result:
		_ = lock.Unlock()
		t.Fatal("AcquireOperation did not block")
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-result:
		if err := second.Unlock(); err != nil {
			t.Fatal(err)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireOperation stayed blocked after release")
	}
}

func TestAbandonedLocksCanBeAcquired(t *testing.T) {
	for _, kind := range []string{"operation", "bootstrap"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestLockOwnerProcess$")
			command.Env = append(
				os.Environ(),
				"GROVE_LOCK_HELPER=1",
				"GROVE_LOCK_DIRECTORY="+directory,
				"GROVE_LOCK_KIND="+kind,
			)
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}

			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != "locked" {
				_ = command.Process.Kill()
				t.Fatalf("lock helper did not report readiness: %q, %v", scanner.Text(), scanner.Err())
			}
			manager, err := NewManager(directory)
			if err != nil {
				t.Fatal(err)
			}
			tryLock := func() (Lock, bool, error) {
				if kind == "bootstrap" {
					return manager.TryBootstrap(42)
				}
				return manager.TryOperation("abandoned")
			}
			if candidate, acquired, err := tryLock(); err != nil || acquired || candidate != nil {
				_ = command.Process.Kill()
				t.Fatalf("live lock attempt = %v, %t, %v", candidate, acquired, err)
			}

			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			acquiredLock, acquired, err := tryLock()
			if err != nil || !acquired || acquiredLock == nil {
				t.Fatalf("abandoned lock attempt = %v, %t, %v", acquiredLock, acquired, err)
			}
			if err := acquiredLock.Unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLockOwnerProcess(t *testing.T) {
	if os.Getenv("GROVE_LOCK_HELPER") != "1" {
		return
	}
	manager, err := NewManager(os.Getenv("GROVE_LOCK_DIRECTORY"))
	if err != nil {
		t.Fatal(err)
	}
	var held Lock
	if os.Getenv("GROVE_LOCK_KIND") == "bootstrap" {
		held, err = manager.AcquireBootstrap(42)
	} else {
		held, err = manager.AcquireOperation("abandoned")
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("locked")
	_, _ = os.Stdin.Read(make([]byte, 1))
	runtime.KeepAlive(held)
}

func TestNewManagerRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "locks")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(link); err == nil {
		t.Fatal("NewManager() accepted a symlink directory")
	}
}

func TestLockInputValidation(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", ".", "..", "../escape", "path/name", "space token", string([]byte{0xff}), strings.Repeat("a", maxTokenBytes+1)} {
		if _, err := manager.AcquireOperation(token); err == nil {
			t.Errorf("AcquireOperation(%q) returned nil", token)
		}
	}
	for _, worktreeID := range []int64{0, -1} {
		if _, err := manager.AcquireBootstrap(worktreeID); err == nil {
			t.Errorf("AcquireBootstrap(%d) returned nil", worktreeID)
		}
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(file); err == nil {
		t.Error("NewManager accepted a file")
	}
	if _, err := NewManager(""); err == nil {
		t.Error("NewManager accepted an empty path")
	}
}
