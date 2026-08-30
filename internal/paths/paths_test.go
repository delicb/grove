package paths

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/del-boy/grove/internal/model"
)

func TestValidateWorktreeName(t *testing.T) {
	valid := []string{
		"a",
		"0",
		"_private",
		"feature-login",
		"release.1_test",
		strings.Repeat("a", MaxWorktreeNameBytes),
	}
	for _, name := range valid {
		t.Run("valid_"+name[:1], func(t *testing.T) {
			if err := ValidateWorktreeName(name); err != nil {
				t.Errorf("ValidateWorktreeName(%q) returned %v", name, err)
			}
		})
	}

	invalid := []string{
		"",
		".hidden",
		"-leading",
		"has space",
		"path/name",
		`path\name`,
		"café",
		strings.Repeat("a", MaxWorktreeNameBytes+1),
		string([]byte{0xff}),
	}
	for index, name := range invalid {
		t.Run(fmt.Sprintf("invalid_%d", index), func(t *testing.T) {
			err := ValidateWorktreeName(name)
			if err == nil {
				t.Fatalf("ValidateWorktreeName(%q) returned nil", name)
			}
			var domainErr *model.Error
			if !errors.As(err, &domainErr) || domainErr.Code != model.ErrorInvalidName {
				t.Errorf("error = %#v, want invalid_name", err)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"my api":          "my-api",
		"café":            "caf",
		"東京":              "repo",
		"":                "repo",
		"...---":          "repo",
		"..hello--":       "hello",
		"hello___world":   "hello___world",
		"one / 東京 + two":  "one-two",
		"UPPER.release_1": "UPPER.release_1",
		"a🙂🙂🙂b":           "a-b",
		"-._kept_.-":      "_kept_",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := Slug(input); got != want {
				t.Errorf("Slug(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestSlugLimit(t *testing.T) {
	input := strings.Repeat("a", 79) + "-a"
	got := Slug(input)
	if len(got) > MaxRepositorySlugBytes {
		t.Fatalf("slug length = %d, want at most %d", len(got), MaxRepositorySlugBytes)
	}
	if strings.HasSuffix(got, "-") || strings.HasSuffix(got, ".") {
		t.Errorf("slug %q has a trimmed suffix", got)
	}
	if got != strings.Repeat("a", 79) {
		t.Errorf("Slug() = %q", got)
	}

	exact := strings.Repeat("b", MaxRepositorySlugBytes)
	if got := Slug(exact); got != exact {
		t.Errorf("Slug(exact limit) changed the value")
	}
}

func TestRepositoryKeyCandidates(t *testing.T) {
	candidates := RepositoryKeyCandidates("my api", "/repo/.git")
	if len(candidates) != 16 {
		t.Fatalf("candidate count = %d, want 16", len(candidates))
	}
	if candidates[0] != "my-api" {
		t.Errorf("plain candidate = %q", candidates[0])
	}
	hash := sha256.Sum256([]byte("/repo/.git"))
	wantHash := fmt.Sprintf("%x", hash)
	if candidates[1] != "my-api-"+wantHash[:8] {
		t.Errorf("first hash candidate = %q", candidates[1])
	}
	if candidates[len(candidates)-1] != "my-api-"+wantHash {
		t.Errorf("full hash candidate = %q", candidates[len(candidates)-1])
	}
	other := RepositoryKeyCandidates("my api", "/other/.git")
	if candidates[0] != other[0] || candidates[1] == other[1] {
		t.Error("identity hash does not distinguish a slug collision")
	}
}

func TestContainment(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "root")
	tests := []struct {
		name   string
		target string
		within bool
		child  bool
	}{
		{"same", root, true, false},
		{"child", filepath.Join(root, "repo", "tree"), true, true},
		{"sibling prefix", root + "ed", false, false},
		{"parent", filepath.Dir(root), false, false},
		{"clean child", filepath.Join(root, "repo", "..", "tree"), true, true},
		{"escaped", filepath.Join(root, "..", "outside"), false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Contains(root, test.target); got != test.within {
				t.Errorf("Contains() = %t, want %t", got, test.within)
			}
			if got := IsChild(root, test.target); got != test.child {
				t.Errorf("IsChild() = %t, want %t", got, test.child)
			}
		})
	}
}

func TestCanonicalAndCanonicalDirectory(t *testing.T) {
	base := t.TempDir()
	realDirectory := filepath.Join(base, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalDirectory(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(realDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("CanonicalDirectory() = %q, want %q", got, want)
	}

	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalDirectory(file); err == nil {
		t.Error("CanonicalDirectory accepted a file")
	}
	if _, err := Canonical(filepath.Join(base, "missing")); err == nil {
		t.Error("Canonical accepted a missing path")
	}
}

func TestCanonicalForCreationResolvesExistingSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "repo")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(link, "new", "tree")
	got, err := CanonicalForCreation(target)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalOutside, "new", "tree")
	if got != want {
		t.Errorf("CanonicalForCreation() = %q, want %q", got, want)
	}
	if Contains(root, got) {
		t.Error("resolved target remains inside the lexical root")
	}

	ancestor, err := NearestExistingAncestor(target)
	if err != nil {
		t.Fatal(err)
	}
	if ancestor != canonicalOutside {
		t.Errorf("NearestExistingAncestor() = %q, want %q", ancestor, canonicalOutside)
	}
}

func TestCanonicalForCreationRejectsDanglingSymlink(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalForCreation(filepath.Join(link, "tree")); err == nil {
		t.Error("CanonicalForCreation accepted a dangling symlink")
	}
}

func TestCanonicalForCreationRejectsFileAncestor(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(file, "tree")
	if _, err := CanonicalForCreation(target); err == nil {
		t.Error("CanonicalForCreation accepted a file ancestor")
	}
	if _, err := NearestExistingAncestor(target); err == nil {
		t.Error("NearestExistingAncestor accepted a file ancestor")
	}
}

func TestRequireContained(t *testing.T) {
	root := t.TempDir()
	if err := RequireContained(root, filepath.Join(root, "repo")); err != nil {
		t.Fatalf("RequireContained returned %v", err)
	}
	err := RequireContained(root, filepath.Join(filepath.Dir(root), "outside"))
	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %#v, want *model.Error", err)
	}
	if domainErr.Code != model.ErrorTargetOutsideRoot || domainErr.ExitCode != model.ExitConflict {
		t.Errorf("error = %#v", domainErr)
	}

	for _, target := range []string{"", string([]byte{0xff}), "bad\x00path"} {
		err := RequireContained(root, target)
		if !errors.As(err, &domainErr) || domainErr.Code != model.ErrorInvalidPath {
			t.Errorf("RequireContained(%q) error = %#v, want invalid_path", target, err)
		}
		if Contains(root, target) {
			t.Errorf("Contains accepted %q", target)
		}
	}
}

func TestValidateUTF8(t *testing.T) {
	for _, value := range []string{string([]byte{0xff}), "bad\x00path"} {
		if err := ValidateUTF8(value); err == nil {
			t.Errorf("ValidateUTF8(%q) returned nil", value)
		}
	}
	if err := ValidateUTF8("valid/東京"); err != nil {
		t.Errorf("ValidateUTF8 returned %v", err)
	}
	if _, err := Canonical(""); err == nil {
		t.Error("Canonical accepted an empty path")
	}
}
