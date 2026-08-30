package paths

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
)

const (
	MaxWorktreeNameBytes   = 100
	MaxRepositorySlugBytes = 80
)

func ValidateWorktreeName(name string) error {
	if !utf8.ValidString(name) {
		return invalidName("The worktree name must use valid UTF-8.", nil)
	}
	if len(name) == 0 || len(name) > MaxWorktreeNameBytes {
		return invalidName("The worktree name must contain 1 through 100 bytes.", nil)
	}
	if !isNameStart(name[0]) {
		return invalidName("The worktree name must start with an ASCII letter, number, or underscore.", nil)
	}
	for index := range len(name) {
		if !isNameByte(name[index]) {
			return invalidName("The worktree name contains a character that is not valid.", nil)
		}
	}
	return nil
}

func Slug(name string) string {
	var slug strings.Builder
	invalidRun := false
	for _, character := range name {
		if isSlugRune(character) {
			slug.WriteByte(byte(character))
			invalidRun = false
			continue
		}
		if !invalidRun {
			slug.WriteByte('-')
			invalidRun = true
		}
	}

	value := strings.Trim(slug.String(), ".-")
	if len(value) > MaxRepositorySlugBytes {
		value = value[:MaxRepositorySlugBytes]
		value = strings.TrimRight(value, ".-")
	}
	if value == "" {
		return "repo"
	}
	return value
}

func RepositoryKeyCandidates(displayName, identity string) []string {
	slug := Slug(displayName)
	hash := sha256.Sum256([]byte(identity))
	hexHash := fmt.Sprintf("%x", hash)
	candidates := []string{slug}
	for length := 8; length <= len(hexHash); length += 4 {
		candidates = append(candidates, slug+"-"+hexHash[:length])
	}
	return candidates
}

func ValidateUTF8(path string) error {
	if !utf8.ValidString(path) {
		return invalidPath("The path must use valid UTF-8.", nil)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return invalidPath("The path contains a null byte.", nil)
	}
	return nil
}

func Canonical(path string) (string, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", invalidPath("Grove could not resolve the path.", err)
	}
	resolved = filepath.Clean(resolved)
	if err := ValidateUTF8(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func CanonicalDirectory(path string) (string, error) {
	resolved, err := Canonical(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", invalidPath("Grove could not read the directory.", err)
	}
	if !info.IsDir() {
		return "", invalidPath("The path must name a directory.", nil)
	}
	return resolved, nil
}

func CanonicalForCreation(path string) (string, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	existing, missing, err := splitAtExistingAncestor(absolute)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", invalidPath("Grove could not resolve the target path.", err)
	}
	if len(missing) > 0 {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", invalidPath("Grove could not read the target ancestor.", err)
		}
		if !info.IsDir() {
			return "", invalidPath("The target ancestor must be a directory.", nil)
		}
	}
	parts := append([]string{resolved}, missing...)
	result := filepath.Clean(filepath.Join(parts...))
	if err := ValidateUTF8(result); err != nil {
		return "", err
	}
	return result, nil
}

func NearestExistingAncestor(path string) (string, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	existing, missing, err := splitAtExistingAncestor(absolute)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", invalidPath("Grove could not resolve the target ancestor.", err)
	}
	if len(missing) > 0 {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", invalidPath("Grove could not read the target ancestor.", err)
		}
		if !info.IsDir() {
			return "", invalidPath("The target ancestor must be a directory.", nil)
		}
	}
	return filepath.Clean(resolved), nil
}

func Contains(root, target string) bool {
	if root == "" || target == "" || !validPathString(root) || !validPathString(target) {
		return false
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(rootAbsolute), filepath.Clean(targetAbsolute))
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func IsChild(root, target string) bool {
	if !Contains(root, target) {
		return false
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	return filepath.Clean(rootAbsolute) != filepath.Clean(targetAbsolute)
}

func RequireContained(root, target string) error {
	if root == "" || target == "" {
		return invalidPath("The root and target paths must not be empty.", nil)
	}
	if err := ValidateUTF8(root); err != nil {
		return err
	}
	if err := ValidateUTF8(target); err != nil {
		return err
	}
	if !Contains(root, target) {
		err := model.NewError(
			model.ErrorTargetOutsideRoot,
			model.ExitConflict,
			"The target path is outside the managed root.",
			nil,
		)
		err.Details["root"] = root
		err.Details["path"] = target
		return err
	}
	return nil
}

func absolutePath(path string) (string, error) {
	if err := ValidateUTF8(path); err != nil {
		return "", err
	}
	if path == "" {
		return "", invalidPath("The path must not be empty.", nil)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", invalidPath("Grove could not make the path absolute.", err)
	}
	return filepath.Clean(absolute), nil
}

func splitAtExistingAncestor(path string) (string, []string, error) {
	current := filepath.Clean(path)
	missing := []string{}
	for {
		_, err := os.Lstat(current)
		if err == nil {
			return current, missing, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, invalidPath("Grove could not inspect the target path.", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, invalidPath("Grove could not find an existing path ancestor.", err)
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}

func validPathString(path string) bool {
	return utf8.ValidString(path) && strings.IndexByte(path, 0) < 0
}

func isNameStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func isNameByte(value byte) bool {
	return isNameStart(value) || value == '.' || value == '-'
}

func isSlugRune(value rune) bool {
	return value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' ||
		value == '.' || value == '_' || value == '-'
}

func invalidName(message string, cause error) *model.Error {
	return model.NewError(model.ErrorInvalidName, model.ExitInvalidArguments, message, cause)
}

func invalidPath(message string, cause error) *model.Error {
	return model.NewError(model.ErrorInvalidPath, model.ExitInvalidArguments, message, cause)
}
