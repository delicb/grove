package store

import (
	"errors"
	"time"

	"github.com/del-boy/grove/internal/model"
)

var (
	ErrNotFound       = errors.New("store record not found")
	ErrNotActive      = errors.New("worktree is not active")
	ErrConflict       = errors.New("store constraint conflict")
	ErrStateChanged   = errors.New("worktree state changed")
	ErrOperationToken = errors.New("operation token does not match")
	ErrBusy           = errors.New("database is busy")
)

type CreateReservation struct {
	Repository         model.RepositoryInfo
	Name               string
	CreationRoot       string
	RequestedBase      *string
	RequestedBranch    string
	ExpectedCommit     string
	CreatorAgent       string
	CreatedAt          time.Time
	OperationToken     string
	OperationStartedAt time.Time
	BootstrapScript    *string
	BootstrapSource    model.ValueSource
}

type RemoveReservation struct {
	WorktreeID         int64
	OperationToken     string
	OperationStartedAt time.Time
	ObservedActivityAt time.Time
	CutoffAt           time.Time
	Reason             model.RemovalReason
	GitDirectory       model.GitDirectoryIdentity
}

type RemovalResult struct {
	RemovedAt      time.Time
	Reason         model.RemovalReason
	SizeBytes      *int64
	SizeComplete   bool
	SizeMeasuredAt *time.Time
}

type Filter struct {
	RepositoryID        *int64
	RepositoryCommonDir *string
	Name                *string
	Path                *string
	States              []model.WorktreeState
}

type RepositoryFilter struct {
	ID        *int64
	CommonDir *string
}

type RepositoryUpdate struct {
	MainCheckout string
	SeenAt       time.Time
}

type ReconcileUpdate struct {
	WorktreeID     int64
	FromState      model.WorktreeState
	State          model.WorktreeState
	OperationToken *string
	Branch         *string
	DetachedCommit *string
	Locked         bool
	RemovedAt      *time.Time
	RemovalReason  *model.RemovalReason
}

type BootstrapUpdate struct {
	WorktreeID int64
	FromState  model.BootstrapState
	State      model.BootstrapState
	Script     *string
	Source     model.ValueSource
	ExitCode   *int
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type SizeUpdate struct {
	WorktreeID int64
	Bytes      int64
	Complete   bool
	MeasuredAt time.Time
}

type StatsRequest struct {
	RepositoryID        *int64
	RepositoryCommonDir *string
	IncludeFinal        bool
}

type Stats struct {
	Active              int
	Missing             int
	ManualReview        int
	Removed             *int
	CreateFailed        *int
	RepositoryCount     int
	SizeBytes           int64
	UnknownSizeCount    int
	IncompleteSizeCount int
	SizeComplete        bool
	OldestMeasurementAt *time.Time
	NewestMeasurementAt *time.Time
}
