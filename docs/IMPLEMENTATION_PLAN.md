# Grove Implementation Plan

## 1. Delivery rule

Implement the accepted specification in small phases. Run tests after each phase.

Do not commit changes until the user requests a commit.

## 2. Phase 1: Project foundation

### 2.1 Project files

Create:

- `go.mod` with Go 1.25
- `LICENSE` with MIT terms
- `.gitignore`
- `README.md`
- `Makefile`
- `.github/workflows/ci.yml`

Add the exact dependency versions from `docs/ARCHITECTURE.md`.

### 2.2 Domain model

Create `internal/model` with:

- Repository and Git worktree data
- Worktree and bootstrap state enums
- Worktree status data
- JSON result and error types
- Validation helpers for enum values

Add compile-time JSON tags and state tests.

### 2.3 Configuration

Create `internal/config` with:

- Configuration path selection
- TOML parsing
- Environment priority
- Home and relative path expansion
- Value source tracking
- Data and lock directory creation

Test missing explicit files, invalid TOML, empty values, and all priority levels.

### 2.4 Identity and size helpers

Create:

- `internal/identity` for creator selection and validation
- `internal/size` for apparent-size scans
- `internal/paths` for names, slugs, canonical paths, and containment

Test all specification edge cases.

**Phase result:** Foundation packages compile and unit tests pass.

## 3. Phase 2: Git, locks, and storage

### 3.1 Git adapter

Create `internal/git` with:

- Git 2.36 version check
- Repository detection
- Zero-delimited worktree parser
- Commit resolution
- Branch validation and existence checks
- Worktree creation
- Dirty and ignored status checks
- Worktree removal without force

Use `LC_ALL=C` and argument arrays.

Add integration tests with temporary repositories and paths that contain spaces.

### 3.2 Lock manager

Create `internal/lock` with:

- Operation lock paths by random token
- Bootstrap lock paths by worktree ID
- Blocking acquire
- Nonblocking recovery acquire
- Ownership and release checks

Test live and abandoned lock behavior with two manager instances.

### 3.3 SQLite store

Create `internal/store` with:

- Database open and pragmas
- Numbered migration runner
- Repository key allocation
- Create reservations
- Token-checked state updates
- Conditional remove reservations
- Touch updates for active records only
- List and filter queries
- Reconciliation updates
- Bootstrap and size updates
- Stats queries

Test every state transition in temporary databases.

Test unique conflicts and concurrent key allocation.

**Phase result:** Adapters and state storage compile and pass tests.

## 4. Phase 3: Application services

Create `internal/app`.

### 4.1 Shared startup

Implement:

- Configuration load
- Database open
- Git version check for Git commands
- Incomplete operation recovery
- Active and missing record reconciliation
- Fixed clock injection

### 4.2 Create service

Implement the full create flow:

1. Detect repository.
2. Validate inputs.
3. Resolve root, key, target, branch, and base.
4. Reject a nested Git worktree target.
5. Acquire the operation lock.
6. Reserve `creating`.
7. Confirm ownership.
8. Run Git add.
9. Resolve the resulting Git state.
10. Commit active or a safe failure state.
11. Release the operation lock.
12. Run bootstrap.
13. Measure size.
14. Return a typed result.

Add hooks around Git actions for concurrency tests.

### 4.3 Reconciliation service

Implement:

- Repository-grouped Git queries
- Current branch and detached commit refresh
- Missing state updates
- Locked state updates
- Warning collection for unreadable repositories
- Quarantine preservation

### 4.4 List, touch, and stats services

Implement state filters, size refresh, touch lookup, totals, and measurement ranges.

### 4.5 Cleanup service

Implement:

- Age parser
- Candidate and blocked reports
- Dirty, ignored, locked, root, and main-checkout checks
- Terminal approval contract
- Conditional remove reservation
- Exact execution of the displayed candidate set
- Full post-reservation safety check
- Private-path move and second safety check
- Token and lock confirmation
- Git removal and absence confirmation
- Partial failure handling

Add the exact create-recovery and cleanup-touch interleaving tests.

**Phase result:** App services meet domain tests without CLI output code.

## 5. Phase 4: CLI and output

### 5.1 Kong command tree

Create commands:

- `grove create`
- `grove list`
- `grove touch`
- `grove stats`
- `grove cleanup`
- `grove config show`
- `grove config path`
- `grove version`

Add global `--config` and command `--json` options.

### 5.2 Output

Create `internal/output` with:

- Fixed JSON result envelopes
- Fixed JSON error envelopes
- Human create result
- Human list table
- Human stats summary
- Human cleanup report and confirmation
- Binary size formatting

Add golden or field-set tests for every JSON command.

### 5.3 Process entry point

Create `cmd/grove/main.go` with:

- Signal-aware root context
- Dependency setup
- One final exit-code decision
- No direct domain logic

**Phase result:** The built binary supports all MVP commands.

## 6. Phase 5: Skill and user documentation

Create `skills/grove-worktrees/SKILL.md` with only the required agent procedure.

The skill must require `GROVE_AGENT` before create. It must show JSON path extraction and safe cleanup dry-run.

Create `README.md` with:

- Install and build steps
- Configuration file example
- Human command examples
- Agent JSON examples
- Bootstrap trust warning
- Cleanup safety and `touch` guidance

Package the skill with the skill-creator validation script when that script is available.

**Phase result:** Humans and agents can use Grove without source-code knowledge.

## 7. Phase 6: Verification

### 7.1 Local checks

Run:

```text
gofmt -w .
go vet ./...
go test -race ./...
go test ./...
go build ./cmd/grove
```

Run cross-build checks for the four target pairs.

### 7.2 Black-box checks

Use two temporary repositories and one isolated Grove data directory.

Verify:

1. Create one worktree in each repository.
2. Confirm both creator identities.
3. Confirm default and explicit bootstrap behavior.
4. Parse list and stats JSON.
5. Touch one worktree.
6. Make one worktree dirty.
7. Confirm cleanup dry-run blocks the dirty worktree.
8. Remove the clean worktree through approved cleanup.
9. Confirm its branch remains.
10. Confirm final stats and history.

### 7.3 Independent review

Ask separate subagents to:

- Review operation safety and SQL state transitions.
- Run unit, race, integration, and black-box tests.
- Review JSON output and the agent skill.

Record reports under `docs/verification/`.

Fix all blocking findings. Repeat the relevant review after each fix.

## 8. Parallel subagent work

Use separate file ownership to avoid edit conflicts.

### Foundation agent

Owns:

- `internal/model`
- `internal/config`
- `internal/identity`
- `internal/paths`
- `internal/size`

### Adapter agent

Owns:

- `internal/git`
- `internal/lock`

### Storage agent

Owns:

- `internal/store`

### Documentation agent

Owns:

- `README.md`
- `skills/grove-worktrees`
- `LICENSE`
- `.github/workflows`

After these agents finish, one integration agent owns:

- `internal/app`
- `internal/cli`
- `internal/output`
- `cmd/grove`

The main agent resolves shared dependency and test issues.

## 9. Completion checklist

- [ ] The accepted specification remains the source of truth.
- [ ] Architecture package boundaries match the code.
- [ ] All MVP commands work in human and JSON modes.
- [ ] Create and remove operations use durable states and locks.
- [ ] Cleanup never uses force or deletes a dirty worktree.
- [ ] Two repositories work without registration.
- [ ] Creator identity is stored and listed.
- [ ] Bootstrap results are stored and returned.
- [ ] Stats report count and apparent size.
- [ ] The agent skill requires explicit identity.
- [ ] Unit, race, integration, and black-box tests pass.
- [ ] macOS and Linux builds pass for AMD64 and ARM64.
- [ ] Independent verification has no blocking findings.
