# Grove QA verification

**Result: PASS**

All required checks passed. All final black-box assertions passed. I found no reproducible Grove failure.

- Time: `2026-08-29T23:02:18Z`
- Platform: Darwin 25.5.0, arm64
- Go: `go1.27.0 darwin/arm64`
- Git: `2.54.0`
- SQLite CLI: `3.52.0`
- Source tree: `/Users/del-boy/src/grove`
- Test HOME: `/tmp/grove-qa-blackbox/home`
- Test XDG config: `/tmp/grove-qa-blackbox/xdg-config`
- Test XDG data: `/tmp/grove-qa-blackbox/xdg-data`
- Test Grove data: `/tmp/grove-qa-blackbox/data`
- Test Grove root: `/tmp/grove-qa-blackbox/worktrees`
- Test repository: `/tmp/grove-qa-blackbox/repos/source`

## Required Go checks

| Exact command | Expected behavior | Actual behavior | Result |
|---|---|---|---|
| `go test ./...` | Exit 0. All package tests pass. | Exit 0. All test packages passed. Go used valid cached results. | PASS |
| `go test -race ./...` | Exit 0. All package tests pass with no race report. | Exit 0. All test packages passed. No race report occurred. | PASS |
| `go vet ./...` | Exit 0. Vet writes no findings. | Exit 0. Vet wrote no findings. | PASS |

I also ran fresh tests to remove test-cache uncertainty.

| Exact command | Actual behavior | Result |
|---|---|---|
| `go test -count=1 ./...` | Exit 0. All test packages passed. Total wall time was 13.70 seconds. | PASS |
| `go test -race -count=1 ./...` | Exit 0. All test packages passed. No race report occurred. Total wall time was 14.59 seconds. | PASS |
| `go build -o /tmp/grove-qa-results/grove ./cmd/grove` | Exit 0. Grove built successfully. | PASS |

The test output covered these packages:

```text
github.com/del-boy/grove/cmd/grove
github.com/del-boy/grove/internal/app
github.com/del-boy/grove/internal/bootstrap
github.com/del-boy/grove/internal/cli
github.com/del-boy/grove/internal/config
github.com/del-boy/grove/internal/git
github.com/del-boy/grove/internal/identity
github.com/del-boy/grove/internal/lock
github.com/del-boy/grove/internal/model
github.com/del-boy/grove/internal/output
github.com/del-boy/grove/internal/paths
github.com/del-boy/grove/internal/size
github.com/del-boy/grove/internal/store
```

`github.com/del-boy/grove/internal/testutil` had no test files.

## Black-box command environment

Each Grove command used this isolated environment. `PATH` kept its value from the QA process.

```sh
env -i \
  "PATH=$PATH" \
  HOME=/tmp/grove-qa-blackbox/home \
  XDG_CONFIG_HOME=/tmp/grove-qa-blackbox/xdg-config \
  XDG_DATA_HOME=/tmp/grove-qa-blackbox/xdg-data \
  GROVE_DATA_DIR=/tmp/grove-qa-blackbox/data \
  GROVE_ROOT=/tmp/grove-qa-blackbox/worktrees \
  GROVE_AGENT=qa:session-1 \
  GIT_CONFIG_NOSYSTEM=1 \
  GIT_CONFIG_GLOBAL=/dev/null \
  LC_ALL=C \
  <command>
```

macOS resolved `/tmp` to `/private/tmp` in canonical Grove paths. This behavior is correct.

## Fixture setup

I created a new temporary Git repository and made one seed commit.

```sh
rm -rf /tmp/grove-qa-blackbox
mkdir -p \
  /tmp/grove-qa-blackbox/home \
  /tmp/grove-qa-blackbox/xdg-config \
  /tmp/grove-qa-blackbox/xdg-data \
  /tmp/grove-qa-blackbox/data \
  /tmp/grove-qa-blackbox/worktrees \
  /tmp/grove-qa-blackbox/repos/source

env -i "PATH=$PATH" HOME=/tmp/grove-qa-blackbox/home GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null LC_ALL=C \
  git init -b main /tmp/grove-qa-blackbox/repos/source
env -i "PATH=$PATH" HOME=/tmp/grove-qa-blackbox/home GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null LC_ALL=C \
  git -C /tmp/grove-qa-blackbox/repos/source config user.name 'Grove QA'
env -i "PATH=$PATH" HOME=/tmp/grove-qa-blackbox/home GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null LC_ALL=C \
  git -C /tmp/grove-qa-blackbox/repos/source config user.email grove-qa@example.invalid
```

The successful bootstrap script contained:

```sh
#!/bin/sh
set -eu
test "$PWD" = "$GROVE_WORKTREE_PATH"
test "$GROVE_WORKTREE_NAME" = "alpha"
test "$GROVE_BRANCH" = "alpha"
test "$GROVE_AGENT" = "qa:session-1"
test -n "$GROVE_REPOSITORY_PATH"
printf 'bootstrap-ok\n'
```

The failed bootstrap script contained:

```sh
#!/bin/sh
printf 'bootstrap-fail-stdout\n'
printf 'bootstrap-fail-stderr\n' >&2
exit 23
```

I added `README.md` and both scripts, then ran:

```sh
git -C /tmp/grove-qa-blackbox/repos/source add README.md bootstrap-success.sh bootstrap-fail.sh
git -C /tmp/grove-qa-blackbox/repos/source commit -m seed
```

## Black-box results

The command paths below use `/tmp`. Grove returned canonical paths below `/private/tmp`.

### Version

Exact command:

```sh
/tmp/grove-qa-results/grove version
```

Expected: exit 0 and `grove dev`.

Actual: exit 0 and `grove dev`.

Result: PASS.

Code area: `cmd/grove/main.go`, `internal/cli/cli.go`.

### Create with successful bootstrap and JSON

Exact command:

```sh
/tmp/grove-qa-results/grove create alpha \
  --repo /tmp/grove-qa-blackbox/repos/source \
  --bootstrap-script bootstrap-success.sh \
  --json
```

Expected:

- Exit 0.
- Create branch `alpha` and an active worktree.
- Run the bootstrap script in the worktree.
- Pass the documented `GROVE_*` values.
- Return schema version 1 JSON on stdout.
- Keep stderr empty.

Actual:

- Exit 0.
- Grove created `/private/tmp/grove-qa-blackbox/worktrees/source/alpha`.
- Git reported branch `alpha` in the worktree.
- The worktree state was `active`.
- The creator was `qa:session-1`.
- Bootstrap state was `succeeded` with exit code 0.
- Captured stdout was `bootstrap-ok\n`.
- Warnings and failures were empty.
- Stderr was empty.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/create.go`, `internal/bootstrap/bootstrap.go`, `internal/model/json.go`.

### List in human and JSON modes

Exact commands:

```sh
/tmp/grove-qa-results/grove list
/tmp/grove-qa-results/grove list --json
```

Expected:

- Exit 0 for both commands.
- Human output includes the table header and the `alpha` path.
- JSON output reports one active worktree and schema version 1.

Actual:

- Both commands exited 0.
- Human output included `alpha`, branch `alpha`, state `active`, and the canonical path.
- Human output reported `Total: 1 worktrees, 384 B, 0 unknown sizes`.
- JSON reported one active worktree, complete size data, and no issues.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/list.go`, `internal/output/output.go`, `internal/model/json.go`.

### Touch in JSON and human modes

Exact commands:

```sh
/tmp/grove-qa-results/grove touch alpha \
  --repo /tmp/grove-qa-blackbox/repos/source \
  --json
/tmp/grove-qa-results/grove touch /private/tmp/grove-qa-blackbox/worktrees/source/alpha
```

Expected:

- Exit 0 for both commands.
- Update Grove activity for `alpha`.
- JSON includes the old and new times.
- Human output includes the canonical path.

Actual:

- Both commands exited 0.
- JSON changed activity from `2026-08-29T23:01:09.542886Z` to `2026-08-29T23:01:09.886174Z`.
- Human output then changed activity to `2026-08-29T23:01:09.942599Z`.
- JSON warnings and failures were empty.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/touch.go`, `internal/store/updates.go`.

### Stats in human and JSON modes

Exact commands:

```sh
/tmp/grove-qa-results/grove stats
/tmp/grove-qa-results/grove stats --refresh --json
```

Expected:

- Exit 0 for both commands.
- Report one active worktree and one repository.
- Refresh and report complete size data in JSON.

Actual:

- Both commands exited 0.
- Human output reported one active worktree and one repository.
- JSON reported 384 bytes, zero unknown sizes, and complete size data.
- JSON warnings and failures were empty.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/stats.go`, `internal/size/size.go`, `internal/model/json.go`.

### Cleanup approval refusal

I aged `alpha` with this fixture command:

```sh
sqlite3 /tmp/grove-qa-blackbox/data/grove.db \
  "UPDATE worktrees SET last_grove_activity_at='2000-01-01T00:00:00Z' WHERE name='alpha'; SELECT changes();"
```

Expected fixture result: one updated row.

Actual fixture result: `1`.

Exact Grove command:

```sh
/tmp/grove-qa-results/grove cleanup --older-than 1h --json
```

Expected:

- Exit 5.
- Refuse JSON cleanup without `--dry-run` or `--yes`.
- Return a JSON error on stderr.
- Keep stdout empty and keep the worktree.

Actual:

- Exit 5.
- Error code was `confirmation_required`.
- Message was `JSON cleanup requires --dry-run or --yes.`
- Stdout was empty.
- The worktree remained on disk.

Result: PASS.

Code area: `internal/cli/cli.go`.

### Cleanup dry-run in JSON and human modes

Exact commands:

```sh
/tmp/grove-qa-results/grove cleanup --older-than 1h --dry-run --json
/tmp/grove-qa-results/grove cleanup --older-than 1h --dry-run
```

Expected:

- Exit 0 for both commands.
- Report `alpha` as an old, clean candidate.
- Report zero deletions.
- Keep the worktree and its Git registration.

Actual:

- Both commands exited 0.
- JSON reported action `candidate` and reason `old_and_clean`.
- JSON summary reported one candidate and zero deletions.
- Human output reported `Cleanup: 1 candidates, 0 deleted, 0 skipped, 0 failed`.
- The worktree remained on disk.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/cleanup.go`, `internal/output/output.go`, `internal/model/json.go`.

### Safe cleanup refusal for a dirty worktree

I added local data with this fixture command:

```sh
printf 'do not delete\n' > /private/tmp/grove-qa-blackbox/worktrees/source/alpha/untracked-local-data.txt
```

Exact Grove command:

```sh
/tmp/grove-qa-results/grove cleanup --older-than 1h --yes --json
```

Verification command:

```sh
git -C /tmp/grove-qa-blackbox/repos/source worktree list --porcelain
```

Expected:

- Grove exits 0 because it safely skips the unsafe item.
- Report action `skipped` and reason `dirty`.
- Report warning `cleanup_dirty`.
- Do not delete the directory or Git worktree registration.

Actual:

- Grove exited 0.
- JSON reported `approved: true`, action `skipped`, and reason `dirty`.
- Summary reported zero deleted and one skipped.
- Warning code was `cleanup_dirty`.
- The directory remained on disk.
- Git still listed the worktree.

Result: PASS.

Code area: `internal/app/cleanup.go`, especially `inspectCleanup` and `removeCleanupItem`.

### Create with failed bootstrap and JSON

Exact command:

```sh
/tmp/grove-qa-results/grove create beta \
  --repo /tmp/grove-qa-blackbox/repos/source \
  --bootstrap-script bootstrap-fail.sh \
  --json
```

Expected:

- Exit 6 for bootstrap failure.
- Keep the new worktree active.
- Return the create result on stdout.
- Capture bootstrap stdout, stderr, and exit code.
- Keep process stderr empty in JSON mode.

Actual:

- Exit 6.
- Grove created `/private/tmp/grove-qa-blackbox/worktrees/source/beta`.
- Worktree state was `active`.
- Bootstrap state was `failed` with exit code 23.
- Captured stdout was `bootstrap-fail-stdout\n`.
- Captured stderr was `bootstrap-fail-stderr\n`.
- Warnings and failures were empty.
- Process stderr was empty.

Result: PASS.

Code area: `internal/cli/cli.go`, `internal/app/create.go`, `internal/bootstrap/bootstrap.go`, `internal/output/output.go`.

### Final JSON list

Exact command:

```sh
/tmp/grove-qa-results/grove list --json
```

Expected: exit 0 and two active worktrees named `alpha` and `beta`.

Actual: exit 0 and two active worktrees named `alpha` and `beta`.

Result: PASS.

Code area: `internal/app/list.go`, `internal/model/json.go`.

## JSON contract checks

`jq -e` checks passed for every tested JSON result.

The checks confirmed:

- `schema_version` was 1.
- `command` matched the invoked command.
- Success results included `data`, `warnings`, and `failures`.
- Success warnings and failures matched expected values.
- The cleanup error included the expected structured `error` value.
- Successful JSON commands kept stderr empty.
- The bootstrap failure result stayed on stdout and used exit code 6.

Result: PASS.

## Repository file check

I did not change repository files.

The final `git status --short --branch` output matched the initial output:

```text
## No commits yet on main
?? .github/
?? .gitignore
?? LICENSE
?? Makefile
?? README.md
?? cmd/
?? docs/
?? go.mod
?? go.sum
?? internal/
?? skills/
```

All QA artifacts are outside the repository.

## Failures

No reproducible Grove failures occurred.

The first harness run expected canonical paths below `/tmp`. macOS returned `/private/tmp`.

I corrected only the external QA assertion and reran the full black-box suite. Every final assertion passed.

## Artifacts

- Report: `/tmp/grove-qa-verification.md`
- Raw logs: `/tmp/grove-qa-results/`
- Black-box script: `/tmp/grove-qa-results/run-blackbox.sh`
- Black-box logs and assertions: `/tmp/grove-qa-results/blackbox/`
- Temporary repository and state: `/tmp/grove-qa-blackbox/`
