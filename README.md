# Grove

Grove is a command-line tool that creates and tracks local Git worktrees below one managed root.

Grove supports multiple repositories without registration. It records creator identity, bootstrap results, Grove activity, state, and apparent size.

## Requirements

- Go 1.25 or later
- Git 2.36 or later
- macOS or Linux on AMD64 or ARM64

Release builds use a pure Go SQLite driver and do not require CGO.

## Install

Install the latest released version with Go:

```sh
go install github.com/del-boy/grove/cmd/grove@latest
```

Build from a source checkout:

```sh
make check
make build
./bin/grove version
```

Install the source checkout into `GOBIN`:

```sh
make install
```

Run `make cross-build` to create all supported binaries in `dist/`.

## Configure

Grove reads the first available configuration path in this order:

1. `--config <path>`
2. `GROVE_CONFIG`
3. `$XDG_CONFIG_HOME/grove/config.toml`
4. `~/.config/grove/config.toml`

Create `~/.config/grove/config.toml` with values such as these:

```toml
root = "~/worktrees"
bootstrap_script = "bootstrap-worktree.sh"
```

The default root is `~/worktrees`. The default bootstrap script is `bootstrap-worktree.sh`.

Use `GROVE_ROOT`, `GROVE_BOOTSTRAP_SCRIPT`, and `GROVE_DATA_DIR` to override stored values.
Set `GROVE_BOOTSTRAP_SCRIPT` to an empty value to disable bootstrap execution.

Inspect effective values and their sources:

```sh
grove config show
grove config path
```

## Use Grove

Create a worktree and a new branch:

```sh
grove create feature-login --repo "$HOME/src/api"
```

Select a different branch name and base:

```sh
grove create login-ui --repo "$HOME/src/api" --branch feature/login --base main
```

Attach an existing branch:

```sh
grove create fix-timeout --repo "$HOME/src/api" --use-existing
```

List worktrees and show totals:

```sh
grove list
grove stats
```

Run `touch` when you resume work in an existing worktree:

```sh
grove touch "$HOME/worktrees/api/feature-login"
```

Grove activity only includes successful `create` and `touch` commands. It does not include other Git or file activity.

## Bootstrap trust

Grove runs the selected bootstrap script through `/bin/sh` inside the new worktree.
The script inherits your environment, rights, and credentials.
Only create worktrees from repositories that you trust.

A missing default script does not stop creation. A missing explicit script returns exit code 6.
A failed bootstrap leaves the worktree active and returns exit code 6.

## Safety limits

Do not change the managed root while `grove create` runs.
Create does not protect against another process with the same user ID that changes a checked parent while Git starts.

Always inspect a cleanup dry run first:

```sh
grove cleanup --older-than 30d --dry-run
```

Without `--yes`, cleanup requires a terminal confirmation.
JSON cleanup requires either `--dry-run` or `--yes`.

Grove blocks cleanup for staged, modified, untracked, ignored, or locked worktrees by default.
Use `--allow-ignored` only when ignored files contain no required local data.

For approved cleanup, Grove moves each candidate to a random private path and checks it again.
Do not write to a worktree while approved cleanup runs.
A process with an open directory handle can still write after Grove moves the worktree.

Grove does not force removal or delete branches. Never remove a Grove worktree with direct file deletion.

## Agent JSON workflow

Release bundles include `skills/grove-worktrees/SKILL.md`.
Install that folder through the agent's normal skill setup.

Set `GROVE_AGENT` to a stable agent and session identity before creation.
Do not rely on automatic identity detection for agent work.

This example preserves a create result when bootstrap returns exit code 6:

```sh
export GROVE_AGENT='pi:session-123'

create_result="$(mktemp)"
trap 'rm -f "$create_result"' EXIT
create_status=0
grove create feature-login --repo "$HOME/src/api" --json >"$create_result" || create_status=$?

if [ ! -s "$create_result" ]; then
  exit "$create_status"
fi

worktree_path="$(jq -er '.data.worktree.path' "$create_result")"
bootstrap_state="$(jq -er '.data.bootstrap.state' "$create_result")"

if [ "$bootstrap_state" = failed ] || [ "$bootstrap_state" = interrupted ]; then
  jq . "$create_result" >&2
  exit 6
fi

if [ "$create_status" -ne 0 ]; then
  exit "$create_status"
fi

cd "$worktree_path"
```

Run `touch` when the agent resumes work:

```sh
grove touch "$worktree_path" --json
```

Inspect cleanup data without changing worktrees:

```sh
grove cleanup --older-than 30d --dry-run --json >cleanup.json
jq . cleanup.json
```

Run approved cleanup only after the user requests deletion.

Every JSON result includes `schema_version`, `command`, `data`, `warnings`, and `failures`.
Consumers must ignore unknown fields in schema version 1.

## License

Grove uses the MIT License. See [LICENSE](LICENSE).
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for dependency license notices.
