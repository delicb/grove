# Grove Specification

Version: 0.6

## 1. Purpose

Grove is a Go command-line tool for local Git worktrees.

Grove creates worktrees below one configured root. It supports multiple repositories without a registration command.

Grove records each worktree that it creates. Each record includes the creator, state, bootstrap result, activity time, and measured size.

Grove supports interactive human use and non-interactive agent use.

## 2. MVP scope

The MVP provides these functions:

- Detect a repository from the current directory or `--repo`.
- Create a worktree and a new branch.
- Attach an existing branch only with `--use-existing`.
- Run one bootstrap script after creation.
- List all Grove-managed worktrees.
- Report worktree counts and apparent disk size.
- Record the agent that created each worktree.
- Mark agent activity through an explicit command.
- Find and delete old, clean worktrees.
- Produce fixed JSON output for agents.
- Recover incomplete create and cleanup operations.

The MVP does not clone repositories or require registration. It does not delete branches or test merge status.

The MVP does not import worktrees that another tool created. It does not use a background service or support Windows.

## 3. Build and platform contract

Grove uses Go 1.25 or later.

Grove supports these targets:

- macOS on AMD64 and ARM64
- Linux on AMD64 and ARM64

Grove requires Git 2.36 or later. This version provides zero-delimited worktree porcelain output.

Grove uses a pure Go SQLite driver. Release builds do not require CGO.

The repository contains an MIT `LICENSE` file. Release files include `THIRD_PARTY_NOTICES.md` with required dependency notices.

## 4. Terms

**Repository** means one non-bare Git repository that owns worktrees.

**Managed worktree** means a worktree that Grove created and recorded.

**Worktree name** means the directory name below a repository directory in the managed root.

**Creator agent** means the identity that Grove records during creation.

**Bootstrap script** means the configured POSIX shell script that Grove runs after creation.

**Grove activity** means a successful `create` or `touch` operation. It does not represent all file or Git activity.

**Apparent size** means the sum of file entry sizes, not allocated file-system blocks.

## 5. Repository detection

The `create`, `list`, `stats`, `touch`, and `cleanup` commands accept `--repo <path>`.

If `--repo` is absent, `create` detects the repository from the current directory. Other commands use all known repositories by default.

For `list`, `stats`, and `cleanup`, `--repo` filters records. It does not register an unknown repository.

For `touch`, a name requires `--repo` or current-directory repository detection. An absolute managed path does not require repository detection.

A repository path must name a directory inside a working tree. Grove rejects files, `.git` paths, bare repositories, and paths outside a working tree.

Grove resolves symbolic links and converts the selected path to an absolute path.

Grove uses `git rev-parse --path-format=absolute --git-common-dir` as the repository identity. Grove canonicalizes the returned path.

Grove gets the main checkout from the first entry in `git worktree list --porcelain -z`. It refreshes this path during reconciliation.

When `--repo` points into a linked worktree, that linked worktree supplies the default `HEAD` base for creation.

Grove creates a repository record only before its first managed worktree creation. Read commands do not create repository records.

Grove rejects a repository path that does not use valid UTF-8.

## 6. Managed directory layout

The built-in managed root is `~/worktrees`.

Grove uses this layout:

```text
<root>/<repository-key>/<worktree-name>
```

Grove maps the main checkout directory name to an ASCII slug.

The mapping follows these rules:

1. Keep ASCII letters, numbers, periods, underscores, and hyphens.
2. Replace each run of other Unicode characters with one hyphen.
3. Remove leading periods and hyphens.
4. Remove trailing periods and hyphens.
5. Use `repo` when the result is empty.
6. Limit the slug to 80 bytes.

Examples include `my api` to `my-api`, `café` to `caf`, and `東京` to `repo`.

Grove first tries the slug as the repository key. If another identity owns it, Grove adds an identity hash.

Grove first uses eight hash characters. It adds four more characters until the key is unique, up to the full hash.

Examples:

```text
~/worktrees/api/feature-login
~/worktrees/api-a81c91d2/fix-timeout
```

A transaction and unique database constraints protect repository-key allocation. The allocation retries after a concurrent conflict.

A worktree name must meet these rules:

- It contains ASCII letters, numbers, periods, underscores, or hyphens.
- It starts with an ASCII letter, number, or underscore.
- It does not contain a path separator.
- It is 1 through 100 bytes.
- It is unique among non-final records for its repository.

Grove creates and canonicalizes the root before it builds a target path. A relative root resolves from the current directory.

Grove uses path components for containment checks. It does not use string-prefix checks.

Grove refuses these targets:

- An existing file or directory
- A path outside the canonical creation root
- A path below any Git worktree from any repository
- A path whose parent resolves outside the canonical creation root
- A path that does not use valid UTF-8

Before creation, Grove runs repository detection on each existing target ancestor. A successful detection proves that the target has a Git worktree ancestor.

Each worktree record stores its canonical creation root. Cleanup uses this stored root after configuration changes.

If a configured root symlink later points elsewhere, Grove still validates an old record against its stored canonical root.

## 7. Configuration

Grove reads TOML configuration from one path.

Path priority is:

1. `--config <path>`
2. Nonempty `GROVE_CONFIG`
3. `$XDG_CONFIG_HOME/grove/config.toml`
4. `~/.config/grove/config.toml`

An explicit `--config` or `GROVE_CONFIG` path must exist and contain valid TOML.

For implicit paths, Grove uses the first existing file. No file means built-in defaults.

The MVP supports these stored values:

```toml
root = "~/worktrees"
bootstrap_script = "bootstrap-worktree.sh"
```

`root` selects the root for new worktrees.

`bootstrap_script` selects a path relative to a new worktree. An absolute path is valid.

An empty `bootstrap_script` disables bootstrap execution.

Value priority is:

1. Command option, when the command supports one
2. Nonempty environment value
3. Configuration file value
4. Built-in default

Supported environment variables are:

- `GROVE_ROOT`
- `GROVE_BOOTSTRAP_SCRIPT`
- `GROVE_CONFIG`
- `GROVE_AGENT`
- `GROVE_DATA_DIR`

An empty `GROVE_BOOTSTRAP_SCRIPT` disables bootstrap execution. Other empty environment values are ignored.

`GROVE_DATA_DIR` changes the database directory. A relative value resolves from the current directory.

Grove expands a leading `~` in all configured paths.

The configuration commands are read-only:

```text
grove config show [--json]
grove config path [--json]
```

`config show` returns effective values and their sources. `config path` returns the selected file path or `null`.

Users edit the TOML file to change stored values.

## 8. Data storage

Grove stores metadata in SQLite.

The default database path is `$XDG_DATA_HOME/grove/grove.db`. The fallback is `~/.local/share/grove/grove.db`.

Grove enables foreign keys, write-ahead logging, and a five-second busy timeout. It applies numbered migrations during startup.

Grove stores all times as UTC RFC 3339 values with nanosecond precision.

### 8.1 Repository fields

A repository record contains:

- Integer ID
- Canonical common Git directory
- Current main checkout path
- Display name
- Repository directory key
- First seen time
- Last seen time

The common Git directory and repository directory key are unique.

### 8.2 Worktree fields

A worktree record contains:

- Integer ID
- Repository ID
- Worktree name
- Canonical creation root
- Canonical worktree path
- Current branch reference or `null`
- Current detached commit or `null`
- Requested base reference
- Requested branch
- Expected creation commit
- Creator agent
- Creation time
- Last Grove activity time
- Worktree state
- Bootstrap state and selected script
- Bootstrap exit result and times
- Last measured apparent size
- Size completeness and measurement time
- Removal time and reason
- Git directory identity for an active removal
- Current operation token and start time

A non-final worktree path is unique. A non-final repository and worktree-name pair is also unique.

After a record reaches `removed` or `create_failed`, a later create can reuse its name and path. History records remain unchanged.

### 8.3 Worktree states

Valid worktree states are:

- `creating`: The durable record exists, but Git creation is not confirmed.
- `active`: Git and the worktree directory contain the worktree.
- `removing`: Cleanup started, but removal is not confirmed.
- `missing`: The record was active, but Git or disk no longer contains the worktree.
- `removed`: Grove confirmed removal.
- `create_failed`: Git creation failed, and the target is absent.
- `manual_review`: Grove cannot prove a safe state.

`removed` and `create_failed` are final states.

`manual_review` is quarantined. No MVP command changes it to another state, and cleanup never selects it.

### 8.4 Bootstrap states

Valid bootstrap states are:

- `pending`: Git creation succeeded, but script execution did not start.
- `disabled`: Configuration disabled script execution.
- `not_present`: The built-in default script was absent.
- `running`: A Grove process owns the bootstrap lock and runs the script.
- `succeeded`: The script returned zero.
- `failed`: The script returned nonzero or could not start.
- `interrupted`: The owner stopped before a terminal result.

Transitions start at `pending`. They continue to `disabled`, `not_present`, `failed`, or `running`.

A direct `failed` transition means Grove could not select an explicit script. `running` continues to `succeeded`, `failed`, or `interrupted`.

Terminal bootstrap states do not change.

Grove holds an advisory lock on a per-worktree lock file while the script runs. Another Grove process leaves `running` unchanged when it cannot get that lock.

At startup, Grove changes stale `pending` to `interrupted`. It changes `running` to `interrupted` only when it can get the abandoned lock.

A failed or interrupted bootstrap does not change an active worktree state.

## 9. Operation recovery

Each create or remove operation uses a random token and one advisory operation lock.

The lock file is `<data-dir>/locks/<token>.lock`. Grove gets the exclusive lock before it commits an incomplete state.

The owner holds the lock until it commits a final operation state. Process exit releases the operating-system lock.

At startup, Grove removes each operation lock file whose token has no incomplete operation and whose lock it can get without waiting. Grove removes the file before it releases the lock. Bootstrap lock files stay because their names repeat across runs.

Grove writes a `creating` record and commits it before it runs `git worktree add`.

After Git succeeds, Grove changes the record to `active`. A later database failure leaves a recoverable `creating` record.

At command startup, Grove tries to get each incomplete operation lock without waiting.

If another process holds the lock, Grove leaves the record unchanged. The current operation remains the only owner.

If Grove gets the lock, the prior owner stopped. Grove then applies these recovery rules:

- `creating` plus matching Git and disk paths becomes `active`.
- `creating` plus absent Git and disk paths becomes `create_failed`.
- `creating` with only one matching source becomes `manual_review`.
- `removing` plus absent Git and disk paths becomes `removed`.
- `removing` plus matching paths and the stored Git directory identity becomes `active`.
- `removing` at its private path moves back only when its Git directory identity matches.
- `removing` with a changed identity or only one matching source becomes `manual_review`.

Recovery updates require the stored operation token. Grove releases the lock after it commits the recovery state.

Generic reconciliation applies only to `active` and `missing`. It never changes `creating`, `removing`, or `manual_review`.

Grove never deletes a path during recovery.

A database failure after successful Git removal leaves a recoverable `removing` record.

A `manual_review` record never enters cleanup automatically.

## 10. Agent identity

The `create` command resolves the creator in this order:

1. `--agent <id>`
2. Nonempty `GROVE_AGENT`
3. `PI_SESSION_ID`, stored as `pi:<value>`
4. `CLAUDE_CODE_SESSION_ID`, stored as `claude-code:<value>`
5. `CODEX_THREAD_ID`, stored as `codex:<value>`
6. `GEMINI_SESSION_ID`, stored as `gemini:<value>`
7. `human`

The first matching known variable wins. This list defines conflict behavior.

An agent ID must contain 1 through 200 printable UTF-8 characters. Grove trims outer spaces and rejects control characters or a whitespace-only value.

Grove never changes the creator after creation.

Agents must provide a stable identity through `--agent` or `GROVE_AGENT`. The shipped skill treats this step as required.

## 11. Create command

The command form is:

```text
grove create <name> [options]
```

Options are:

```text
--repo <path>             Repository path
--branch <name>           Branch name, default: worktree name
--base <ref>              Base commit for a new branch, default: selected HEAD
--use-existing            Attach an existing branch
--agent <id>              Creator agent identity
--bootstrap-script <path> Bootstrap script override
--no-bootstrap            Disable bootstrap execution
--json                    Write JSON output
```

`--bootstrap-script` and `--no-bootstrap` are mutually exclusive.

Without `--use-existing`, the target branch must not exist. Grove validates it with `git check-ref-format --branch`.

Grove resolves the base to a commit before it runs Git. A detached selected checkout uses its `HEAD` commit.

An unborn selected branch has no default base. Grove then requires `--base` that resolves to a commit.

For a new branch, Grove runs this operation without a shell:

```text
git worktree add -b <branch> <path> <base>
```

With `--use-existing`, the branch must exist and `--base` is invalid. Grove runs this operation without a shell:

```text
git worktree add <path> <branch>
```

Grove lets Git reject a branch that another worktree already uses. Git also closes the race between validation and creation.

Grove rejects branch or base values that Git reads as command options. It uses Git end-of-options markers where supported.

Creation uses this sequence:

1. Load configuration and open the database.
2. Recover incomplete operations.
3. Detect and validate the repository.
4. Validate the worktree name, branch, base, and creator.
5. Create and canonicalize the managed root.
6. Allocate the repository key and target path.
7. Complete all target checks before a worktree record exists.
8. Get the operation lock and commit a `creating` record with its token.
9. Create and protect target parent directories.
10. Repeat target checks and confirm operation ownership.
11. Run `git worktree add`.
12. Verify the created branch and commit against the stored request.
13. Mark the record active and release the operation lock.
14. Run the bootstrap script when enabled.
15. Measure apparent size.
16. Return one result.

If Git fails and the target is absent, Grove marks the record `create_failed`. It removes empty parent directories that it created.

If Git fails but the target or Git record exists, Grove marks the record `manual_review`.

If bootstrap fails, Grove keeps the active worktree and branch. It records the failure and returns exit code 6.

## 12. Bootstrap behavior

The built-in bootstrap value is `bootstrap-worktree.sh`.

A missing built-in default script produces `not_present` and a successful create result.

A path from the configuration file, environment, or command option is explicit. A missing explicit script produces `failed` and exit code 6.

This rule applies even when an explicit value equals `bootstrap-worktree.sh`.

Grove resolves a relative script from the new worktree root. It canonicalizes the parent and refuses a resolved script outside the worktree.

Grove runs the script through `/bin/sh`. The script must use POSIX shell syntax. Grove does not require an executable permission bit.

Grove sets the worktree root as the process directory. The script inherits the user environment and rights.

Repository code is trusted during bootstrap. Grove does not remove credentials from the inherited environment.

Grove replaces these environment variables:

- `GROVE_WORKTREE_PATH`
- `GROVE_WORKTREE_NAME`
- `GROVE_REPOSITORY_PATH`
- `GROVE_BRANCH`
- `GROVE_AGENT`

Grove preserves all other inherited environment variables, including other `GROVE_*` values.

In human mode, the script inherits standard input, output, and error.

In JSON mode, standard input is closed. Grove captures standard output and standard error separately.

Each captured stream has a 1 MiB limit. Grove drains and discards additional bytes and sets a truncation flag.

Valid UTF-8 output uses `utf-8` encoding. Other output uses base64 encoding.

Grove does not set a timeout. If the user interrupts Grove, Grove forwards the interrupt and records `interrupted` when the child ends by signal.

The MVP does not provide a bootstrap retry command.

## 13. Touch command

The command form is:

```text
grove touch <name-or-absolute-path> [--repo <path>] [--json]
```

A worktree name resolves only inside the selected repository. An absolute path resolves through the database.

`touch` requires an active managed worktree. It sets `last_grove_activity_at` to the current time.

`touch` does not change the creator.

Agents run `touch` when they resume work in an existing worktree.

## 14. Reconciliation

Grove runs recovery and reconciliation before `list`, `stats`, `touch`, and `cleanup`.

Grove reads `git worktree list --porcelain -z`. It does not parse the older line-delimited format.

For each non-final record, Grove compares canonical paths with Git and disk.

Reconciliation follows these rules:

- Matching Git and disk paths produce `active`.
- An absent Git path or disk path produces `missing` for a prior active record.
- An unreadable repository produces a warning and leaves stored state unchanged.
- A removed or create-failed record stays final.
- Incomplete states follow the recovery rules in section 9.

Grove refreshes the current branch, detached commit, locked state, and main checkout path from Git.

A detached worktree has `branch: null` and a nonempty detached commit.

Grove does not erase records when Git or the file system returns an error.

## 15. List command

The command form is:

```text
grove list [--repo <path>] [--all] [--refresh-size] [--json]
```

Default `list` shows active records only. `--all` shows all worktree states.

The human table contains:

- Repository
- Worktree name
- Branch or detached commit
- Creator agent
- State
- Last Grove activity
- Cached apparent size
- Path

`--refresh-size` measures each active worktree before output. Without it, Grove uses the cached value.

A missing measurement displays an unknown value.

The list summary counts each shown state. Only active worktrees contribute to its size total.

## 16. Apparent size measurement

Grove measures apparent size by walking each active worktree.

It sums the `size` value from `lstat` for each non-directory entry. It includes the linked worktree `.git` file.

Grove does not follow symbolic links. It counts the link entry size.

Sparse files contribute their logical size. Each hard-linked directory entry contributes its full logical size.

Shared Git objects outside the linked worktree do not contribute.

If a file disappears during the walk, Grove records a warning and marks that worktree measurement incomplete.

Permission and other read errors also make the measurement incomplete. Grove keeps the measured subtotal and never labels it complete.

## 17. Stats command

The command form is:

```text
grove stats [--repo <path>] [--all] [--refresh] [--json]
```

Grove reconciles records before it calculates stats.

Default stats report:

- Active worktree count
- Missing worktree count
- Manual-review worktree count
- Repository count with at least one non-final record
- Active apparent-size subtotal
- Unknown-size count
- Incomplete-size count
- Whether the total is complete
- Measurement time

`--all` also reports removed and create-failed counts. Final records do not affect repository count or size.

`--refresh` measures active worktrees before calculation.

A cached total is complete when every active worktree has a complete stored measurement. Cached measurements do not have a freshness promise.

Stats report the calculation time and the oldest and newest included measurement times. Both range values are `null` when no size exists.

With `--refresh`, all successful measurement times come from the current command.

Human output uses binary units. JSON output uses integer bytes.

A partial measurement returns the data, failure entries, and exit code 7.

## 18. Cleanup command

The command form is:

```text
grove cleanup --older-than <age> [options]
```

Options are:

```text
--repo <path>       Filter by repository
--older-than <age>  Required positive inactivity age
--allow-ignored     Permit deletion of ignored files
--dry-run           Show decisions without deletion
--yes               Approve deletion without a prompt
--json              Write JSON output
```

Supported suffixes are `h`, `d`, and `w`. A day is 24 hours. A week is 168 hours.

Zero, negative, missing, and unsupported ages are usage errors.

A record is old when `last_grove_activity_at <= now - age`.

A cleanup candidate must meet all these rules:

- Its state is active.
- Its Grove activity time meets the age limit.
- Git and disk contain it.
- It is not the main checkout.
- Git does not mark it locked.
- It is inside its stored canonical creation root.
- It has no staged, modified, or untracked files.
- It has no ignored files unless `--allow-ignored` is set.

Grove checks ignored files separately. The safe default treats ignored files as local data.

Any Git status, repository, path, or containment error blocks deletion. Grove reports the exact skip reason.

Grove measures final size before deletion.

Human mode shows each candidate and blocked record. Each row includes the Grove activity time and calculated cutoff.

Human documentation tells users to run `grove touch` when they resume an existing worktree.

Grove requests one confirmation for all candidates.

Without `--yes`, Grove requires an interactive terminal. A negative answer changes no records.

`--dry-run` never prompts or changes records.

JSON mode never prompts. It requires `--dry-run` or `--yes`.

After confirmation, Grove processes only the records in the displayed candidate list.

Grove processes each approved record with this sequence:

1. Create a random operation token and get its operation lock.
2. Start one conditional database transaction.
3. Confirm state `active`, the observed activity time, and the age condition.
4. Store the Git directory identity, change state to `removing`, and commit the operation token.
5. Check identity, Git, path, containment, lock, dirty, ignored, and main-checkout rules again.
6. Restore `active` and release the lock when any rule fails.
7. Move the worktree to a random private path below its stored creation root.
8. Repeat all safety checks and the identity check at the private path.
9. Restore the original path and active state when a safety check fails.
10. Confirm state `removing`, the operation token, and lock ownership.
11. Run `git worktree remove <private-path>` without `--force`.
12. Confirm that Git and disk no longer contain the original or private path.
13. Mark the record `removed` with time, reason, and final size.
14. Release the operation lock.

`touch` updates active records only. Recovery cannot change a live `removing` record.

Cleanup confirms ownership immediately before Git removal. Thus, `touch` cannot update a record after cleanup reserves removal.

Grove never deletes the Git branch.

If Git removal fails, Grove restores `active` when Git and disk still match. An uncertain result becomes `manual_review`.

Grove continues after a removal failure. Any failed removal returns exit code 7.

## 19. Human output

Human output uses plain tables and short messages.

The help and cleanup text tell users to run `grove touch` after they resume an existing worktree.

Destructive output shows absolute paths before confirmation.

When standard output is not a terminal, Grove uses plain text without color. `NO_COLOR` also disables color.

Grove writes operational errors to standard error.

## 20. JSON contract

Every state command supports `--json`.

On full success, standard output contains one result document. Standard error is empty.

On an error before a result exists, standard output is empty. Standard error contains one error document.

A bootstrap failure after successful creation is a result. Grove writes it to standard output and returns exit code 6.

A partial cleanup or size result uses standard output and exit code 7. Its `failures` array is nonempty.

If several result failures apply, exit-code priority is 8, 10, 6, 7, 5, 4, 3, and 2. A database error normally prevents a result.

JSON mode never writes prompts or human text.

Version 1 documents can add fields. Consumers must ignore unknown fields. Grove does not remove fields or change types without a schema-version change.

All fields shown in this section are required. A field marked nullable uses JSON `null` when it has no value.

All result documents use this envelope:

```json
{
  "schema_version": 1,
  "command": "list",
  "data": {},
  "warnings": [],
  "failures": []
}
```

A warning or failure uses this object:

```json
{
  "code": "size_incomplete",
  "message": "Grove could not read one file.",
  "path": null,
  "worktree_id": null
}
```

`path` is a nullable string. `worktree_id` is a nullable integer.

All error documents use this shape:

```json
{
  "schema_version": 1,
  "command": "create",
  "error": {
    "code": "branch_exists",
    "message": "The branch exists. Use --use-existing to attach it.",
    "details": {}
  }
}
```

`details` is an object with error-specific values. Clients must not require a specific `details` shape.

Paths are absolute UTF-8 strings. Times are UTC RFC 3339 strings.

### 20.1 Stable codes

Error codes are:

- `invalid_arguments`
- `invalid_age`
- `config_not_found`
- `config_invalid`
- `git_version_unsupported`
- `not_repository`
- `bare_repository`
- `invalid_path`
- `invalid_name`
- `invalid_agent`
- `invalid_branch`
- `invalid_base`
- `branch_exists`
- `branch_in_use`
- `target_exists`
- `target_outside_root`
- `target_nested_in_worktree`
- `worktree_not_found`
- `worktree_not_active`
- `worktree_conflict`
- `confirmation_required`
- `unsafe_cleanup`
- `bootstrap_missing`
- `bootstrap_failed`
- `git_error`
- `database_busy`
- `database_error`
- `internal_error`

Warning and failure codes are:

- `repository_unreadable`
- `worktree_missing`
- `recovery_manual_review`
- `size_incomplete`
- `file_disappeared`
- `permission_denied`
- `cleanup_recent`
- `cleanup_dirty`
- `cleanup_ignored`
- `cleanup_locked`
- `cleanup_outside_root`
- `cleanup_status_error`
- `cleanup_state_changed`
- `cleanup_remove_failed`

Cleanup reason values are:

- `old_and_clean`
- `not_old`
- `dirty`
- `ignored_files`
- `locked`
- `main_checkout`
- `outside_root`
- `state_changed`
- `status_error`
- `remove_failed`

### 20.2 Worktree JSON object

Commands use one worktree shape:

```json
{
  "id": 12,
  "repository_id": 3,
  "repository": "api",
  "name": "feature-login",
  "path": "/Users/me/worktrees/api/feature-login",
  "creation_root": "/Users/me/worktrees",
  "branch": "feature-login",
  "detached_commit": null,
  "creator_agent": "pi:session-123",
  "state": "active",
  "created_at": "2026-01-02T03:04:05Z",
  "last_grove_activity_at": "2026-01-02T03:04:05Z",
  "size_bytes": 1234,
  "size_complete": true,
  "size_measured_at": "2026-01-02T03:04:06Z",
  "bootstrap_state": "succeeded"
}
```

`id` and `repository_id` are integers. `size_bytes` and `size_measured_at` are nullable.

`branch` and `detached_commit` are nullable strings. All other string and Boolean fields are not nullable.

### 20.3 Command data shapes

`create` data is:

```json
{
  "worktree": {},
  "bootstrap": {
    "state": "succeeded",
    "script": "/absolute/bootstrap-worktree.sh",
    "source": "built-in",
    "exit_code": 0,
    "stdout": "",
    "stdout_encoding": "utf-8",
    "stdout_truncated": false,
    "stderr": "",
    "stderr_encoding": "utf-8",
    "stderr_truncated": false
  }
}
```

`worktree` uses section 20.2. Bootstrap `script` and `exit_code` are nullable.

Bootstrap `source` is `built-in`, `config`, `environment`, `command`, or `disabled`.

Stream encoding is `utf-8` or `base64`. Stream strings and truncation flags are always present.

`list` data is:

```json
{
  "worktrees": [],
  "summary": {
    "active": 0,
    "creating": 0,
    "removing": 0,
    "missing": 0,
    "removed": 0,
    "create_failed": 0,
    "manual_review": 0,
    "size_bytes": 0,
    "unknown_size_count": 0,
    "size_complete": true
  }
}
```

All summary counts and `size_bytes` are nonnegative integers. `size_complete` is Boolean.

`touch` data is:

```json
{
  "worktree": {},
  "previous_activity_at": "2026-01-02T03:04:05Z"
}
```

`worktree` uses section 20.2. `previous_activity_at` is a non-null time string.

`stats` data is:

```json
{
  "active": 0,
  "missing": 0,
  "manual_review": 0,
  "removed": null,
  "create_failed": null,
  "repository_count": 0,
  "size_bytes": 0,
  "unknown_size_count": 0,
  "incomplete_size_count": 0,
  "size_complete": true,
  "calculated_at": "2026-01-02T03:04:06Z",
  "oldest_measurement_at": null,
  "newest_measurement_at": null
}
```

Counts and bytes are nonnegative integers. `removed` and `create_failed` are nullable integers when `--all` is absent.

The three measurement fields are nullable time strings, except `calculated_at`, which is not nullable.

`cleanup` data is:

```json
{
  "dry_run": true,
  "approved": false,
  "cutoff_at": "2025-12-03T03:04:06Z",
  "items": [
    {
      "worktree": {},
      "action": "candidate",
      "reason": "old_and_clean",
      "final_size_bytes": 1234
    }
  ],
  "summary": {
    "candidate": 1,
    "deleted": 0,
    "skipped": 0,
    "failed": 0
  }
}
```

Cleanup action values are `candidate`, `deleted`, `skipped`, and `failed`.

Each item uses a section 20.2 worktree. `final_size_bytes` is a nullable integer. Summary fields are nonnegative integers.

`config show` data is:

```json
{
  "root": "/Users/me/worktrees",
  "root_source": "built-in",
  "bootstrap_script": "bootstrap-worktree.sh",
  "bootstrap_script_source": "built-in",
  "data_dir": "/Users/me/.local/share/grove",
  "config_path": null
}
```

Path and script values are strings. Source values use the create source enum. `config_path` is a nullable string.

`config path` data is:

```json
{
  "config_path": null
}
```

`config_path` is a nullable string.

## 21. Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Success |
| 2 | Invalid command or option |
| 3 | Configuration error |
| 4 | Repository or Git error |
| 5 | Worktree conflict or unsafe cleanup request |
| 6 | Bootstrap failed after Git creation |
| 7 | Partial cleanup or size result |
| 8 | Database error |
| 10 | Unexpected internal error |

Each JSON error also contains a stable text code.

## 22. Concurrency and safety

SQLite transactions and unique constraints protect concurrent processes.

Grove gives each create and remove operation a random token. Updates require the matching token.

Advisory operation locks prove owner liveness for incomplete create and remove operations.

Grove resolves its data directory once. It rejects a symbolic link at the lock directory path.

Grove invokes Git with argument arrays. It does not build shell command strings.

Only bootstrap execution uses `/bin/sh`.

Grove never runs `git worktree remove --force`.

Grove never deletes a path outside the canonical root stored with that worktree.

Grove does not follow symbolic links during size or cleanup checks.

Create does not protect against another process with the same user ID that changes a checked parent while Git starts.

Do not change the managed root while `grove create` runs.

Grove cannot stop a process with the same user ID and an open directory handle from writing during cleanup. The private-path check reduces this window.

## 23. Agent skill

The repository includes `skills/grove-worktrees/SKILL.md`.

The skill tells an agent to:

1. Confirm that `grove` and `jq` are installed.
2. Set `GROVE_AGENT` to a stable agent and session identity.
3. Run `grove create <name> --repo <path> --json`.
4. Read `data.worktree.path` from the result.
5. Change its working directory to that path.
6. Stop and report when bootstrap state is `failed` or `interrupted`.
7. Run `grove touch <path> --json` when work resumes.
8. Run `grove cleanup --older-than 30d --dry-run --json` before a cleanup request.
9. Never delete a Grove worktree with direct file removal.
10. Never run approved cleanup unless the user requests deletion.

The skill explains that automatic agent detection is only a fallback. Explicit identity is required for agent use.

## 24. Acceptance criteria

The MVP is complete when automated tests prove these statements:

1. The project builds with Go 1.25 and a pure Go SQLite driver.
2. Cross-builds succeed for macOS and Linux on AMD64 and ARM64.
3. SQLite migration and command smoke tests run on all four supported target pairs.
4. Grove rejects Git older than 2.36 with a clear error.
5. The repository contains the MIT license text.
6. Grove detects main and linked worktree repository paths.
7. Grove rejects bare and non-repository paths.
8. Repository names with spaces, Unicode, empty slugs, and hash conflicts get unique valid keys.
9. Concurrent creation cannot reserve equal keys, names, or paths.
10. Each target stays inside its creation root and outside every Git worktree.
11. Create makes a new branch from the selected checkout `HEAD`.
12. `--branch` and `--base` select a valid new branch and commit.
13. An existing branch requires `--use-existing` and cannot already be checked out.
14. A detached `HEAD` works, and an unborn default base fails clearly.
15. A successful Git create stays recorded after bootstrap failure.
16. Recovery changes an abandoned, confirmed `creating` record to active.
17. Recovery does not change a live `creating` or `removing` record.
18. Recovery never deletes an uncertain path or changes `manual_review`.
19. Agent identity priority and validation match section 10.
20. A missing built-in bootstrap script succeeds with `not_present`.
21. A missing explicit bootstrap script returns exit code 6.
22. JSON bootstrap closes input and caps both captured streams.
23. Bootstrap locks distinguish a live run from an abandoned run.
24. List filters states as section 15 defines.
25. Reconciliation refreshes branch, detached, locked, and missing states.
26. Apparent-size tests cover links, sparse files, disappearing files, and read errors.
27. Stats reconcile first and report calculation and measurement-range times.
28. Cleanup rejects zero and negative ages.
29. Cleanup blocks staged, modified, untracked, ignored, and locked worktrees by default.
30. `--allow-ignored` does not permit other dirty states.
31. Cleanup fails closed on each Git, path, and repository error.
32. Cleanup reserves removal with an activity-time condition and checks all safety rules again.
33. Cleanup confirms its token and operation lock immediately before Git removal.
34. A concurrent `touch` fails while cleanup owns a removal.
35. Cleanup never removes the main checkout or a branch.
36. Cleanup never uses force and records partial failures.
37. Recovery resolves confirmed incomplete removals and rejects replacement Git worktrees without path deletion.
38. Every JSON command matches section 20 and keeps output streams clean.
39. Every stable code and enum has an automated contract test.
40. JSON mode never prompts.
41. The agent skill requires a stable creator identity and safe cleanup dry-run.

## 25. Future work

Later versions can add:

- Repository-specific configuration
- Merge-aware cleanup filters
- Agent leases or heartbeats
- Branch deletion as a separate confirmed command
- Bootstrap retry
- Remote cloning
- Shell completion
- Windows support
