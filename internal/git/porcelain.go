package git

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
)

const localBranchPrefix = "refs/heads/"

type worktreeRecord struct {
	path     string
	head     string
	branch   *string
	detached bool
	locked   bool
	bare     bool
}

func ParseWorktreePorcelain(data []byte) ([]model.GitWorktree, error) {
	if len(data) == 0 {
		return []model.GitWorktree{}, nil
	}

	fields := bytes.Split(data, []byte{0})
	worktrees := make([]model.GitWorktree, 0)
	var current *worktreeRecord
	for _, fieldBytes := range fields {
		if len(fieldBytes) == 0 {
			if current == nil {
				continue
			}
			worktree, err := finishWorktreeRecord(*current, len(worktrees) == 0)
			if err != nil {
				return nil, err
			}
			worktrees = append(worktrees, worktree)
			current = nil
			continue
		}
		if !utf8.Valid(fieldBytes) {
			return nil, errorsForField("worktree field does not use valid UTF-8", fieldBytes)
		}
		field := string(fieldBytes)
		if strings.HasPrefix(field, "worktree ") {
			if current != nil {
				return nil, fmt.Errorf("worktree record has no separator")
			}
			current = &worktreeRecord{path: strings.TrimPrefix(field, "worktree ")}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("worktree field appears before a worktree path: %q", field)
		}

		switch {
		case strings.HasPrefix(field, "HEAD "):
			if current.head != "" {
				return nil, fmt.Errorf("worktree record contains more than one HEAD")
			}
			current.head = strings.TrimPrefix(field, "HEAD ")
		case strings.HasPrefix(field, "branch "):
			if current.branch != nil || current.detached {
				return nil, fmt.Errorf("worktree record contains conflicting branch state")
			}
			branchRef := strings.TrimPrefix(field, "branch ")
			if !strings.HasPrefix(branchRef, localBranchPrefix) || branchRef == localBranchPrefix {
				return nil, fmt.Errorf("worktree branch reference is not valid: %q", branchRef)
			}
			branch := strings.TrimPrefix(branchRef, localBranchPrefix)
			current.branch = &branch
		case field == "detached":
			if current.branch != nil || current.detached {
				return nil, fmt.Errorf("worktree record contains conflicting detached state")
			}
			current.detached = true
		case field == "bare":
			current.bare = true
		case field == "locked" || strings.HasPrefix(field, "locked "):
			current.locked = true
		default:
			// Ignore prunable annotations and any unrecognized fields.
		}
	}
	if current != nil {
		return nil, fmt.Errorf("worktree record is not zero-delimited")
	}
	return worktrees, nil
}

func finishWorktreeRecord(record worktreeRecord, main bool) (model.GitWorktree, error) {
	if record.path == "" {
		return model.GitWorktree{}, fmt.Errorf("worktree record has an empty path")
	}
	if !filepath.IsAbs(record.path) {
		return model.GitWorktree{}, fmt.Errorf("worktree path is not absolute: %q", record.path)
	}
	if record.bare {
		return model.GitWorktree{}, fmt.Errorf("bare worktree entry is not supported")
	}
	if record.head == "" {
		return model.GitWorktree{}, fmt.Errorf("worktree record has no HEAD")
	}
	if !isObjectID(record.head) {
		return model.GitWorktree{}, fmt.Errorf("worktree HEAD is not a full object ID: %q", record.head)
	}
	if record.branch == nil && !record.detached {
		return model.GitWorktree{}, fmt.Errorf("worktree record has no branch state")
	}

	worktree := model.GitWorktree{
		Path:   filepath.Clean(record.path),
		HEAD:   record.head,
		Branch: record.branch,
		Locked: record.locked,
		Main:   main,
	}
	if record.detached {
		commit := record.head
		worktree.DetachedCommit = &commit
	}
	return worktree, nil
}

func ParseStatusPorcelain(data []byte) (model.WorktreeStatus, error) {
	status := model.WorktreeStatus{}
	if len(data) == 0 {
		return status, nil
	}

	fields := bytes.Split(data, []byte{0})
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if len(field) == 0 {
			if index == len(fields)-1 {
				continue
			}
			return model.WorktreeStatus{}, fmt.Errorf("status output contains an empty record")
		}
		if len(field) < 4 || field[2] != ' ' {
			return model.WorktreeStatus{}, fmt.Errorf("status record is not valid: %q", field)
		}

		indexState := field[0]
		worktreeState := field[1]
		switch {
		case indexState == '?' && worktreeState == '?':
			status.Untracked = true
		case indexState == '!' && worktreeState == '!':
			status.Ignored = true
		default:
			if indexState != ' ' {
				status.Staged = true
			}
			if worktreeState != ' ' {
				status.Modified = true
			}
		}

		if indexState == 'R' || indexState == 'C' || worktreeState == 'R' || worktreeState == 'C' {
			index++
			if index >= len(fields) || len(fields[index]) == 0 {
				return model.WorktreeStatus{}, fmt.Errorf("renamed status record has no source path")
			}
		}
	}
	return status, nil
}

func errorsForField(message string, field []byte) error {
	return fmt.Errorf("%s: %q", message, field)
}
