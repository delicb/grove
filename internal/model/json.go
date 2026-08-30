package model

import "time"

const SchemaVersion = 1

type Issue struct {
	Code       IssueCode `json:"code"`
	Message    string    `json:"message"`
	Path       *string   `json:"path"`
	WorktreeID *int64    `json:"worktree_id"`
}

func NewIssue(code IssueCode, message string, path *string, worktreeID *int64) Issue {
	return Issue{
		Code:       code,
		Message:    message,
		Path:       path,
		WorktreeID: worktreeID,
	}
}

type Result[T any] struct {
	SchemaVersion int     `json:"schema_version"`
	Command       string  `json:"command"`
	Data          T       `json:"data"`
	Warnings      []Issue `json:"warnings"`
	Failures      []Issue `json:"failures"`
}

func NewResult[T any](command string, data T) Result[T] {
	return Result[T]{
		SchemaVersion: SchemaVersion,
		Command:       command,
		Data:          data,
		Warnings:      []Issue{},
		Failures:      []Issue{},
	}
}

type ErrorDocument struct {
	SchemaVersion int    `json:"schema_version"`
	Command       string `json:"command"`
	Error         *Error `json:"error"`
}

func NewErrorDocument(command string, err *Error) ErrorDocument {
	return ErrorDocument{
		SchemaVersion: SchemaVersion,
		Command:       command,
		Error:         err,
	}
}

type CreateData struct {
	Worktree  Worktree        `json:"worktree"`
	Bootstrap BootstrapResult `json:"bootstrap"`
}

type BootstrapResult struct {
	State           BootstrapState `json:"state"`
	Script          *string        `json:"script"`
	Source          ValueSource    `json:"source"`
	ExitCode        *int           `json:"exit_code"`
	Stdout          string         `json:"stdout"`
	StdoutEncoding  StreamEncoding `json:"stdout_encoding"`
	StdoutTruncated bool           `json:"stdout_truncated"`
	Stderr          string         `json:"stderr"`
	StderrEncoding  StreamEncoding `json:"stderr_encoding"`
	StderrTruncated bool           `json:"stderr_truncated"`
}

type ListData struct {
	Worktrees []Worktree  `json:"worktrees"`
	Summary   ListSummary `json:"summary"`
}

type ListSummary struct {
	Active           int   `json:"active"`
	Creating         int   `json:"creating"`
	Removing         int   `json:"removing"`
	Missing          int   `json:"missing"`
	Removed          int   `json:"removed"`
	CreateFailed     int   `json:"create_failed"`
	ManualReview     int   `json:"manual_review"`
	SizeBytes        int64 `json:"size_bytes"`
	UnknownSizeCount int   `json:"unknown_size_count"`
	SizeComplete     bool  `json:"size_complete"`
}

type TouchData struct {
	Worktree           Worktree  `json:"worktree"`
	PreviousActivityAt time.Time `json:"previous_activity_at"`
}

type StatsData struct {
	Active              int        `json:"active"`
	Missing             int        `json:"missing"`
	ManualReview        int        `json:"manual_review"`
	Removed             *int       `json:"removed"`
	CreateFailed        *int       `json:"create_failed"`
	RepositoryCount     int        `json:"repository_count"`
	SizeBytes           int64      `json:"size_bytes"`
	UnknownSizeCount    int        `json:"unknown_size_count"`
	IncompleteSizeCount int        `json:"incomplete_size_count"`
	SizeComplete        bool       `json:"size_complete"`
	CalculatedAt        time.Time  `json:"calculated_at"`
	OldestMeasurementAt *time.Time `json:"oldest_measurement_at"`
	NewestMeasurementAt *time.Time `json:"newest_measurement_at"`
}

type CleanupData struct {
	DryRun   bool           `json:"dry_run"`
	Approved bool           `json:"approved"`
	CutoffAt time.Time      `json:"cutoff_at"`
	Items    []CleanupItem  `json:"items"`
	Summary  CleanupSummary `json:"summary"`
}

type CleanupItem struct {
	Worktree       Worktree      `json:"worktree"`
	Action         CleanupAction `json:"action"`
	Reason         RemovalReason `json:"reason"`
	FinalSizeBytes *int64        `json:"final_size_bytes"`
}

type CleanupSummary struct {
	Candidate int `json:"candidate"`
	Deleted   int `json:"deleted"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

type ConfigShowData struct {
	Root                  string      `json:"root"`
	RootSource            ValueSource `json:"root_source"`
	BootstrapScript       string      `json:"bootstrap_script"`
	BootstrapScriptSource ValueSource `json:"bootstrap_script_source"`
	DataDir               string      `json:"data_dir"`
	ConfigPath            *string     `json:"config_path"`
}

type ConfigPathData struct {
	ConfigPath *string `json:"config_path"`
}
