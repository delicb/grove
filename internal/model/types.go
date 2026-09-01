package model

import "time"

type Repository struct {
	ID           int64     `json:"id"`
	CommonDir    string    `json:"common_dir"`
	MainCheckout string    `json:"main_checkout"`
	DisplayName  string    `json:"display_name"`
	DirectoryKey string    `json:"directory_key"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type RepositoryInfo struct {
	CommonDir        string `json:"common_dir"`
	MainCheckout     string `json:"main_checkout"`
	SelectedCheckout string `json:"selected_checkout"`
	DisplayName      string `json:"display_name"`
}

type GitDirectoryIdentity struct {
	Path  string `json:"path"`
	Token string `json:"token"`
}

type GitWorktree struct {
	Path           string               `json:"path"`
	GitDirectory   GitDirectoryIdentity `json:"git_directory"`
	HEAD           string               `json:"head"`
	Branch         *string              `json:"branch"`
	DetachedCommit *string              `json:"detached_commit"`
	Locked         bool                 `json:"locked"`
	Main           bool                 `json:"main"`
}

type WorktreeStatus struct {
	Staged    bool `json:"staged"`
	Modified  bool `json:"modified"`
	Untracked bool `json:"untracked"`
	Ignored   bool `json:"ignored"`
}

type Worktree struct {
	ID                  int64          `json:"id"`
	RepositoryID        int64          `json:"repository_id"`
	Repository          string         `json:"repository"`
	Name                string         `json:"name"`
	Path                string         `json:"path"`
	CreationRoot        string         `json:"creation_root"`
	Branch              *string        `json:"branch"`
	DetachedCommit      *string        `json:"detached_commit"`
	CreatorAgent        string         `json:"creator_agent"`
	State               WorktreeState  `json:"state"`
	CreatedAt           time.Time      `json:"created_at"`
	LastGroveActivityAt time.Time      `json:"last_grove_activity_at"`
	SizeBytes           *int64         `json:"size_bytes"`
	SizeComplete        bool           `json:"size_complete"`
	SizeMeasuredAt      *time.Time     `json:"size_measured_at"`
	BootstrapState      BootstrapState `json:"bootstrap_state"`

	RequestedBase       *string               `json:"-"`
	RequestedBranch     string                `json:"-"`
	ExpectedCommit      string                `json:"-"`
	Locked              bool                  `json:"-"`
	BootstrapScript     *string               `json:"-"`
	BootstrapSource     ValueSource           `json:"-"`
	BootstrapExitCode   *int                  `json:"-"`
	BootstrapStartedAt  *time.Time            `json:"-"`
	BootstrapFinishedAt *time.Time            `json:"-"`
	RemovedAt           *time.Time            `json:"-"`
	RemovalReason       *RemovalReason        `json:"-"`
	RemovalGitDirectory *GitDirectoryIdentity `json:"-"`
	OperationToken      *string               `json:"-"`
	OperationStartedAt  *time.Time            `json:"-"`
}
