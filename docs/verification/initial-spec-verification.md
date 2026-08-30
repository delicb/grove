# Grove 0.4 specification verification

Status: **NOT PASS**

Scope: `docs/SPECIFICATION.md` version 0.4, `docs/ARCHITECTURE.md`, `docs/IMPLEMENTATION_PLAN.md`, all production Go code, and all tests.

## Findings

### 1. HIGH: Human confirmation can approve fewer worktrees than cleanup deletes

- Location: `internal/cli/cli.go:427`, `internal/app/cleanup.go:62`
- Requirement: Specification section 18 requires one confirmation for all candidates.
- Impact: The CLI shows one plan and asks for approval. It then creates a new plan and deletes its full candidate set.
- Reproduction: The prompt said `Delete 1 worktree?`. A skipped dirty worktree became clean before approval. Grove then deleted two worktrees.
- Fix: Execute only candidate IDs from the displayed plan. Keep the activity-time condition and all safety checks before each removal.

### 2. MEDIUM: The nested-worktree check misses targets below a `.git` directory

- Location: `internal/app/create.go:286`
- Requirement: Specification section 6 rejects targets below any Git worktree.
- Impact: `GROVE_ROOT=<repo>/.git/grove-managed` permits creation inside another checkout's Git metadata directory.
- Reproduction: Grove created an active worktree at `<repo>/.git/grove-managed/repo/nested-gitdir` and returned exit code 0.
- Fix: Check each existing ancestor, not only the nearest ancestor. Reject the target when any ancestor belongs to a Git worktree.

### 3. MEDIUM: A pre-existing target creates a permanent `manual_review` record

- Location: `internal/app/create.go:147`
- Requirement: Grove must refuse an existing target. `manual_review` is for an uncertain operation and cannot leave quarantine.
- Impact: Grove does not run Git, but it stores a non-final record. The record blocks the same name and path permanently.
- Reproduction: `target_exists` returned exit code 5, but SQLite contained a `manual_review` record for the rejected target.
- Fix: Allocate the repository key before the worktree reservation. Complete all preflight path checks before inserting the `creating` record.

### 4. MEDIUM: Bootstrap removes inherited `GROVE_*` variables

- Location: `internal/bootstrap/bootstrap.go:214`
- Requirement: Specification section 12 says the bootstrap script inherits the user environment.
- Impact: Scripts cannot read `GROVE_ROOT`, `GROVE_DATA_DIR`, or user-defined `GROVE_*` values.
- Reproduction: A script received `unset|unset` for `GROVE_ROOT` and `GROVE_EXTRA`.
- Fix: Remove only the five variables that Grove replaces. Preserve all other environment entries.

### 5. LOW: Reconciliation clears known Git metadata when only the disk path is missing

- Location: `internal/app/reconcile.go:94`
- Requirement: Specification section 14 requires branch, detached commit, and lock refresh from Git.
- Impact: `list --all` reports null branch data when Git still contains the worktree but its directory is missing.
- Fix: Copy Git metadata whenever `gitPresent` is true. Set `active` only when both Git and disk contain the worktree.

### 6. MEDIUM: Automated tests do not prove all acceptance criteria

- Location: `internal/app/app_integration_test.go:408`, `cmd/grove/main_test.go:32`
- Requirement: Specification section 24 requires automated proof for all 41 criteria.
- Impact: The suite can pass without testing key cleanup, recovery, create, exit-code, and full JSON contracts.
- Missing cases include live and abandoned removal recovery, locked cleanup, `--allow-ignored`, removal failures, and `.git` target nesting.
- Fix: Add table-driven integration and black-box tests for criteria 10, 17, 18, 25, 29 through 33, and 36 through 38.

### 7. LOW: Release artifacts omit required license notices

- Location: `.github/workflows/ci.yml:61`
- Requirement: Specification section 3 requires third-party notices in release files.
- Impact: The uploaded artifact contains only binaries from `dist/`.
- Fix: Add the Grove license and generated third-party notices to `dist/`. Test the release bundle contents in CI.

## Checks that passed

- `gofmt -l` returned no files.
- `go vet ./...` passed.
- `go test -race ./...` passed.
- CGO-free cross-builds passed for Darwin and Linux on AMD64 and ARM64.
- Agent identity priority, validation, storage, and skill guidance match section 10.
- JSON field tags, nullable fields, stable codes, and normal result/error stream routing match section 20.
- Exit-code constants and normal result priority match section 21.
- Cleanup does not use `--force` and does not delete branches.
- Operation recovery uses tokens and advisory locks. It does not delete paths.
