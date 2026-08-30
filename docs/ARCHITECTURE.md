# Grove Architecture

## 1. Goals

Grove must keep Git and its database consistent without a service process.

The design favors these properties:

- Safe cleanup
- Recoverable state changes
- Deterministic JSON output
- Clear package boundaries
- Real Git integration tests
- Pure Go release builds

## 2. Technology

Grove uses Go 1.25.

The module path is `github.com/del-boy/grove`.

The main dependencies are:

| Dependency | Version | Purpose |
| --- | --- | --- |
| `github.com/alecthomas/kong` | `v1.16.1` | CLI parsing and help |
| `modernc.org/sqlite` | `v1.57.0` | Pure Go SQLite driver |
| `github.com/pelletier/go-toml/v2` | `v2.4.3` | TOML parsing |
| `github.com/gofrs/flock` | `v0.13.1` | Advisory operation locks |
| `golang.org/x/term` | `v0.45.0` | Terminal and prompt checks |

Grove uses `database/sql` directly. It does not use an object-relational mapper.

Grove uses `os/exec` for Git and bootstrap processes. It uses `log/slog` only for optional diagnostics.

## 3. Source layout

```text
cmd/grove/main.go              Process entry point
internal/app/                  Command orchestration
internal/bootstrap/            Bootstrap selection and execution
internal/cli/                  Kong command types and dispatch
internal/config/               Configuration loading and path expansion
internal/git/                  Git command adapter and porcelain parser
internal/identity/             Creator agent selection and validation
internal/lock/                 Operation and bootstrap lock handling
internal/model/                Shared domain and JSON types
internal/output/               Human tables, JSON envelopes, and errors
internal/size/                 Apparent-size measurement
internal/store/                SQLite schema, migrations, and queries
internal/testutil/             Integration-test repository helpers
skills/grove-worktrees/        Agent skill
.github/workflows/             Build and test automation
docs/                          Product and engineering documents
```

Packages under `internal` are not public API packages.

## 4. Dependency direction

The dependency direction is:

```text
cmd -> cli -> app -> domain adapters
                    |-> config
                    |-> git
                    |-> store
                    |-> lock
                    |-> bootstrap
                    |-> size
                    |-> identity
cli -> output
all domain packages -> model
```

Adapter packages do not import `cli` or `output`.

`app` owns command transactions and state transitions. Adapters perform one bounded operation and return typed results.

## 5. Main interfaces

### 5.1 Git adapter

```go
type Client interface {
    CheckVersion(ctx context.Context) error
    DetectRepository(ctx context.Context, path string) (model.RepositoryInfo, error)
    ListWorktrees(ctx context.Context, repo model.RepositoryInfo) ([]model.GitWorktree, error)
    ResolveCommit(ctx context.Context, repoPath, ref string) (string, error)
    BranchExists(ctx context.Context, repoPath, branch string) (bool, error)
    ValidateBranch(ctx context.Context, branch string) error
    AddWorktree(ctx context.Context, request AddRequest) error
    WorktreeGitDirectory(ctx context.Context, path string) (model.GitDirectoryIdentity, error)
    Status(ctx context.Context, path string) (model.WorktreeStatus, error)
    MoveWorktree(ctx context.Context, repoPath, path, target string) error
    RemoveWorktree(ctx context.Context, repoPath, path string) error
}
```

The production client executes one Git process for each method. Tests can use it against temporary repositories.

The client always sets `LC_ALL=C`. It parses only machine formats.

The Git directory identity hashes the canonical path, device number, and inode number.

A Git worktree move keeps this identity. A removed and recreated worktree gets a new identity.

### 5.2 Store

```go
type Store interface {
    Recoverable(ctx context.Context) ([]model.Worktree, error)
    ReserveRepository(ctx context.Context, repo model.RepositoryInfo, at time.Time) (model.Repository, error)
    ReserveCreate(ctx context.Context, request CreateReservation) (model.Worktree, error)
    CompleteCreate(ctx context.Context, id int64, token string, git model.GitWorktree) error
    FailCreate(ctx context.Context, id int64, token string, state model.WorktreeState) error
    ReserveRemoval(ctx context.Context, request RemoveReservation) (model.Worktree, error)
    CompleteRemoval(ctx context.Context, id int64, token string, result RemovalResult) error
    CancelRemoval(ctx context.Context, id int64, token string) error
    List(ctx context.Context, filter Filter) ([]model.Worktree, error)
    Touch(ctx context.Context, id int64, at time.Time) (model.Worktree, time.Time, error)
    UpdateReconciled(ctx context.Context, update ReconcileUpdate) error
    UpdateBootstrap(ctx context.Context, update BootstrapUpdate) error
    UpdateSize(ctx context.Context, update SizeUpdate) error
}
```

The concrete store can expose smaller transaction helpers. The app layer does not use raw SQL.

### 5.3 Locks

```go
type Manager interface {
    AcquireOperation(token string) (Lock, error)
    TryOperation(token string) (Lock, bool, error)
    AcquireBootstrap(worktreeID int64) (Lock, error)
    TryBootstrap(worktreeID int64) (Lock, bool, error)
}
```

A lock owner must call `Unlock` after it commits the terminal state.

### 5.4 Clock

The app layer receives a clock function:

```go
type Clock func() time.Time
```

Tests use a fixed clock for age and JSON assertions.

## 6. Database schema

### 6.1 Schema migrations

The `schema_migrations` table records each applied migration.

Migration 1 creates the MVP tables and indexes. Migration 2 adds the removal identity columns.

Each migration runs in one transaction.

### 6.2 Repositories table

```sql
CREATE TABLE repositories (
    id INTEGER PRIMARY KEY,
    common_dir TEXT NOT NULL UNIQUE,
    main_checkout TEXT NOT NULL,
    display_name TEXT NOT NULL,
    directory_key TEXT NOT NULL UNIQUE,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
```

### 6.3 Worktrees table

```sql
CREATE TABLE worktrees (
    id INTEGER PRIMARY KEY,
    repository_id INTEGER NOT NULL REFERENCES repositories(id),
    name TEXT NOT NULL,
    creation_root TEXT NOT NULL,
    path TEXT NOT NULL,
    branch TEXT,
    detached_commit TEXT,
    requested_base TEXT,
    requested_branch TEXT NOT NULL,
    expected_commit TEXT NOT NULL,
    creator_agent TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_grove_activity_at TEXT NOT NULL,
    state TEXT NOT NULL,
    locked INTEGER NOT NULL DEFAULT 0,
    bootstrap_state TEXT NOT NULL,
    bootstrap_script TEXT,
    bootstrap_source TEXT NOT NULL,
    bootstrap_exit_code INTEGER,
    bootstrap_started_at TEXT,
    bootstrap_finished_at TEXT,
    size_bytes INTEGER,
    size_complete INTEGER NOT NULL DEFAULT 0,
    size_measured_at TEXT,
    removed_at TEXT,
    removal_reason TEXT,
    removal_git_dir TEXT,
    removal_git_identity TEXT,
    operation_token TEXT,
    operation_started_at TEXT
);
```

Partial unique indexes protect non-final paths and names.

```sql
CREATE UNIQUE INDEX worktrees_live_path
ON worktrees(path)
WHERE state NOT IN ('removed', 'create_failed');

CREATE UNIQUE INDEX worktrees_live_name
ON worktrees(repository_id, name)
WHERE state NOT IN ('removed', 'create_failed');
```

Check constraints limit state values and Boolean integers.

## 7. Create state flow

```text
validate
  -> reserve repository key
  -> validate target before a worktree record exists
  -> acquire operation lock
  -> reserve creating record
  -> repeat target checks
  -> create and verify with Git
  -> active
  -> release operation lock
  -> bootstrap
  -> size scan
  -> result
```

Git failure follows one of these paths:

```text
Git absent + disk absent -> create_failed
Git present + disk present -> active
one source present -> manual_review
```

The app keeps the lock until it commits one of these states.

## 8. Cleanup state flow

The app first builds a candidate report without state changes.

After approval, each candidate follows this flow:

```text
acquire operation lock
  -> store the Git directory identity
  -> conditional active-to-removing update
  -> repeat all safety and identity checks
  -> move to a random private path
  -> repeat all safety and identity checks
  -> confirm token and lock
  -> Git remove without force
  -> confirm absence
  -> removed
  -> release operation lock
```

A failed safety recheck returns the record to active before lock release.

A Git failure reconciles Git and disk while the lock remains held. Uncertain state becomes manual review.

## 9. Recovery

Each command opens the store, then runs recovery.

Recovery scans only `creating` and `removing` records.

For each record, it tries the stored operation lock without waiting.

- A busy lock means the owner remains live.
- An acquired lock means Grove can recover the abandoned operation.

Recovery never runs Git add, Git remove, bootstrap, or file deletion.

Recovery restores an abandoned private path only when its Git directory identity matches the stored identity.

A replacement worktree at the original or private path changes the record to `manual_review`.

Bootstrap recovery uses its separate worktree lock. It marks only abandoned `pending` or `running` attempts as interrupted.

## 10. Repository keys

The slug function is deterministic and has unit tests.

The store allocates keys in a transaction. It tries these candidates:

1. Plain slug
2. Slug plus eight hash characters
3. Slug plus 12 hash characters
4. Longer hash prefixes in four-character steps

The full SHA-256 hash is the final candidate.

A unique-index conflict retries the next candidate.

## 11. Path safety

The path package performs all path checks.

It uses `filepath.Rel` for containment. A valid child cannot equal `..` or start with `..` plus a separator.

The app creates the root, resolves root symbolic links, and stores the result.

Before Git creation, the app checks each existing target ancestor with Git repository detection. Any successful worktree detection rejects the target.

Cleanup repeats canonical containment and symbolic-link checks before removal.

Create cannot stop the same user from changing a checked parent while Git starts. Users must not change the managed root during create.

## 12. JSON and human output

The app returns typed result values. It does not print.

The output package converts each result into one JSON envelope or one human view.

All JSON structs use explicit tags. Contract tests compare decoded field sets, enum values, and null behavior.

Bootstrap output uses a bounded writer. The writer keeps 1 MiB, drains later bytes, and records truncation.

Human tables use deterministic columns and no terminal control codes when output is redirected.

## 13. Error model

Domain errors use one type:

```go
type Error struct {
    Code     string
    ExitCode int
    Message  string
    Details  map[string]any
    Err      error
}
```

The CLI unwraps this type once. JSON mode writes the error envelope to standard error.

Partial results use failure entries and exit code 7. A bootstrap failure keeps create data and uses exit code 6.

## 14. Test design

### 14.1 Unit tests

Unit tests cover:

- Configuration priority and path expansion
- Agent identity priority and validation
- Repository slug mapping
- Worktree name validation
- Age parsing and boundary checks
- Path containment
- Git porcelain parsing
- Size measurement
- JSON field contracts
- Store state transitions

### 14.2 Integration tests

Integration tests create real temporary Git repositories. They set local test-only Git user data.

Tests cover create, existing branches, linked-repository detection, bootstrap, list, touch, stats, and cleanup.

Tests never change global Git configuration.

Concurrency tests use app hooks or channels to pause before Git actions. They test recovery and touch isolation.

### 14.3 CLI tests

CLI tests build one temporary binary. They use isolated `HOME`, XDG directories, and managed roots.

Each JSON test parses exactly one stream document. It also checks that the other stream is empty.

### 14.4 Release checks

Continuous integration runs unit and integration tests on macOS and Linux.

The release matrix builds AMD64 and ARM64 binaries. Target runners execute database and command smoke tests.

## 15. Security limits

Grove trusts the selected repository bootstrap script.

Grove never evaluates configuration as shell text.

Grove passes Git arguments without shell parsing.

Grove rejects Git option-like branch and base inputs.

Cleanup never uses force. It fails closed on state, status, path, or lock errors.

A process with the same user ID and an open worktree handle can still write during cleanup. Grove cannot provide full exclusion from that process.
