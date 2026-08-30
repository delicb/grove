package size

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/del-boy/grove/internal/model"
)

func TestMeasureApparentSize(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, "file"),
		filepath.Join(root, "directory", "nested"),
	}
	contents := [][]byte{[]byte("gitdir: elsewhere"), []byte("hello"), []byte("nested data")}
	for index, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents[index], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	var want int64
	for _, path := range append(paths, link) {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		want += info.Size()
	}
	measurement := Measure(root)
	if measurement.Bytes != want {
		t.Errorf("Bytes = %d, want %d", measurement.Bytes, want)
	}
	if !measurement.Complete {
		t.Errorf("measurement is incomplete: %#v", measurement.Warnings)
	}
	if len(measurement.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want none", measurement.Warnings)
	}
}

func TestMeasureSparseAndHardLinkedFiles(t *testing.T) {
	root := t.TempDir()
	sparse := filepath.Join(root, "sparse")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	const sparseSize = int64(8 << 20)
	if err := file.Truncate(sparseSize); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("hard-link-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}

	measurement := Measure(root)
	want := sparseSize + 2*int64(len("hard-link-data"))
	if measurement.Bytes != want {
		t.Errorf("Bytes = %d, want %d", measurement.Bytes, want)
	}
	if !measurement.Complete {
		t.Errorf("measurement is incomplete: %#v", measurement.Warnings)
	}
}

func TestMeasureMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	measurement := Measure(root)
	if measurement.Complete {
		t.Error("missing root measurement is complete")
	}
	if measurement.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0", measurement.Bytes)
	}
	assertWarning(t, measurement, model.IssueFileDisappeared, root)
}

func TestMeasureClassifiesWalkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code model.IssueCode
	}{
		{"missing", fs.ErrNotExist, model.IssueFileDisappeared},
		{"permission", fs.ErrPermission, model.IssuePermissionDenied},
		{"other", errors.New("read failed"), model.IssueSizeIncomplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			walk := func(root string, callback fs.WalkDirFunc) error {
				return callback(filepath.Join(root, "entry"), nil, test.err)
			}
			measurement := measure("/root", walk)
			assertWarning(t, measurement, test.code, "/root/entry")
		})
	}
}

func TestMeasureClassifiesInfoErrors(t *testing.T) {
	entry := errorEntry{name: "gone", err: fs.ErrNotExist}
	walk := func(root string, callback fs.WalkDirFunc) error {
		return callback(filepath.Join(root, entry.name), entry, nil)
	}
	measurement := measure("/root", walk)
	assertWarning(t, measurement, model.IssueFileDisappeared, "/root/gone")
}

func TestMeasureKeepsSubtotalAfterError(t *testing.T) {
	entry := fs.FileInfoToDirEntry(staticInfo{name: "file", size: 42})
	walk := func(root string, callback fs.WalkDirFunc) error {
		if err := callback(filepath.Join(root, "file"), entry, nil); err != nil {
			return err
		}
		return callback(filepath.Join(root, "missing"), nil, fs.ErrNotExist)
	}
	measurement := measure("/root", walk)
	if measurement.Bytes != 42 {
		t.Errorf("Bytes = %d, want 42", measurement.Bytes)
	}
	assertWarning(t, measurement, model.IssueFileDisappeared, "/root/missing")
}

func TestMeasureClassifiesFinalWalkError(t *testing.T) {
	walk := func(string, fs.WalkDirFunc) error {
		return fs.ErrPermission
	}
	measurement := measure("/root", walk)
	assertWarning(t, measurement, model.IssuePermissionDenied, "/root")
}

func assertWarning(t *testing.T, measurement Measurement, code model.IssueCode, path string) {
	t.Helper()
	if measurement.Complete {
		t.Error("measurement is complete")
	}
	if len(measurement.Warnings) != 1 {
		t.Fatalf("Warnings = %#v, want one", measurement.Warnings)
	}
	warning := measurement.Warnings[0]
	if warning.Code != code {
		t.Errorf("warning code = %q, want %q", warning.Code, code)
	}
	if warning.Path == nil || *warning.Path != path {
		t.Errorf("warning path = %v, want %q", warning.Path, path)
	}
	if warning.WorktreeID != nil {
		t.Errorf("worktree ID = %v, want nil", warning.WorktreeID)
	}
}

type errorEntry struct {
	name string
	err  error
}

func (entry errorEntry) Name() string               { return entry.name }
func (entry errorEntry) IsDir() bool                { return false }
func (entry errorEntry) Type() fs.FileMode          { return 0 }
func (entry errorEntry) Info() (fs.FileInfo, error) { return nil, entry.err }

type staticInfo struct {
	name string
	size int64
}

func (info staticInfo) Name() string       { return info.name }
func (info staticInfo) Size() int64        { return info.size }
func (info staticInfo) Mode() fs.FileMode  { return 0 }
func (info staticInfo) ModTime() time.Time { return time.Time{} }
func (info staticInfo) IsDir() bool        { return false }
func (info staticInfo) Sys() any           { return nil }
