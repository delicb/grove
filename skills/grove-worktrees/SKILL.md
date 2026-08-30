---
name: grove-worktrees
description: Manage local Git worktrees with Grove. Use for agent worktree creation, resume activity, worktree lists, stats, and safe cleanup requests.
---

# Grove Worktrees

1. Confirm that Grove and `jq` are installed:

   ```sh
   command -v grove
   command -v jq
   ```

2. Require an explicit, stable agent and session identity in `GROVE_AGENT`:

   ```sh
   case "${GROVE_AGENT:-}" in
     *[![:space:]]*) ;;
     *) printf 'Set GROVE_AGENT to an agent and session identity.\n' >&2; exit 2 ;;
   esac
   ```

   Do not use Grove's automatic agent detection as a substitute.

3. Create the worktree and capture its exit status:

   ```sh
   create_result="$(mktemp)"
   create_status=0
   grove create <name> --repo "<repository-path>" --json >"$create_result" || create_status=$?

   if [ ! -s "$create_result" ]; then
     exit "$create_status"
   fi

   worktree_path="$(jq -er '.data.worktree.path' "$create_result")"
   bootstrap_state="$(jq -er '.data.bootstrap.state' "$create_result")"
   ```

4. If the bootstrap state is `failed` or `interrupted`, preserve the result and stop.

   ```sh
   if [ "$bootstrap_state" = failed ] || [ "$bootstrap_state" = interrupted ]; then
     jq . "$create_result" >&2
     exit 6
   fi
   ```

   If the status is 7, report the warnings and failures before you continue.
   Remove the result file and change to the parsed worktree path.

   ```sh
   if [ "$create_status" -eq 7 ]; then
     jq '{warnings, failures}' "$create_result" >&2
   fi
   rm -f "$create_result"
   cd "$worktree_path"
   ```

5. Mark resumed work before you change files:

   ```sh
   grove touch "<absolute-worktree-path>" --json
   ```

6. For every cleanup request, run a JSON dry run first:

   ```sh
   grove cleanup --older-than 30d --dry-run --json
   ```

   Review all candidates, skipped items, warnings, and failures.
   Run cleanup with `--yes --json` only after the user explicitly requests deletion.

Never remove a Grove worktree with `rm` or direct file deletion.
