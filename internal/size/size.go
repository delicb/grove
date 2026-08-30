package size

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/del-boy/grove/internal/model"
)

type Measurement struct {
	Bytes    int64
	Complete bool
	Warnings []model.Issue
}

type walkDirFunc func(string, fs.WalkDirFunc) error

func Measure(root string) Measurement {
	return measure(root, filepath.WalkDir)
}

func measure(root string, walk walkDirFunc) Measurement {
	measurement := Measurement{
		Complete: true,
		Warnings: []model.Issue{},
	}
	walkErr := walk(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			measurement.addWarning(path, err)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			measurement.addWarning(path, err)
			return nil
		}
		if !info.IsDir() {
			measurement.Bytes += info.Size()
		}
		return nil
	})
	if walkErr != nil {
		measurement.addWarning(root, walkErr)
	}
	return measurement
}

func (measurement *Measurement) addWarning(path string, err error) {
	measurement.Complete = false
	code := model.IssueSizeIncomplete
	message := "Grove could not read a path during the size scan."
	if errors.Is(err, fs.ErrNotExist) {
		code = model.IssueFileDisappeared
		message = "A file disappeared during the size scan."
	} else if errors.Is(err, fs.ErrPermission) {
		code = model.IssuePermissionDenied
		message = "Grove could not read a path because permission was denied."
	}
	pathCopy := path
	measurement.Warnings = append(measurement.Warnings, model.NewIssue(code, message, &pathCopy, nil))
}
