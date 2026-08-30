# Grove safety verification

Result: **FAIL**

The review found seven destructive-operation or state-corruption risks.

## 1. High: Cleanup can delete worktrees that the user did not approve

Review comment: Execute the displayed cleanup plan. Do not build a new candidate set after confirmation.

`internal/cli/cli.go:427`

### Failure scenario

Human cleanup builds and displays one plan at lines 379 through 410. After approval, line 427 calls `Cleanup`, which builds a new plan.

A blocked worktree can become clean while the prompt waits. The new plan then adds it and deletes it without approval.

A black-box check displayed one candidate and one dirty skip. The check removed the dirty file before answering `yes`.

Grove then reported two deletions and removed both worktrees.

### Fix

Pass the approved worktree IDs, paths, and observed activity times to the cleanup execution step.

Recheck each approved item before removal. Skip unsafe or changed items. Never add an item after confirmation.

## 2. High: Create can adopt a competing worktree and report success

Review comment: Do not complete creation from path presence alone. Verify the Git result against the reserved create request.

`internal/app/create.go:220`

### Failure scenario

A competing `git worktree add` can win after Grove validates the target. Grove's Git command then fails because the target exists.

Lines 183 through 220 accept any Git worktree at the reserved path. They do not check `addErr`, the requested branch, or the expected commit.

A black-box check inserted a competing worktree on branch `foreign`. Grove requested branch `claimed`, but its Git add returned nonzero.

Grove exited zero and stored the foreign worktree as active. A later cleanup can delete this worktree as Grove-managed data.

Recovery has the same path-only test at `internal/app/recovery.go:73`.

SQLite stores `requested_base`, but it does not store the requested branch or create mode at `internal/store/migrations.go:41`.

### Fix

If `AddWorktree` returns an error and either Git or disk contains the target, set `manual_review` and return the Git error.

On success, verify the branch and expected commit before `CompleteCreate`.

Store the requested branch, create mode, and expected commit. Use these fields during recovery.

## 3. High: A late ignored file can be deleted by cleanup

Review comment: The final status check and Git removal do not form one protected operation. Git deletes ignored files without `--force`.

`internal/app/cleanup.go:302`

### Failure scenario

Line 285 checks ignored files. A process can create an ignored file after that check and before line 302 starts Git.

`git worktree remove` deletes ignored files without `--force`.

A black-box Git wrapper created `secret.local` when Grove invoked `worktree remove`. Grove marked the item removed and deleted the file.

This can occur when a build, editor, or agent writes ignored local data during cleanup.

### Fix

Move the worktree to a private, unguessable quarantine path before the last status check.

Revalidate Git identity, containment, status, and ignored files in quarantine. Abort on any change or move error.

Document any remaining same-user open-file limit if the platform cannot give full exclusion.

## 4. Medium: A parent symlink race can create a worktree outside the managed root

Review comment: Revalidate the created parent before Git add. The current check occurs before `MkdirAll` creates that parent.

`internal/app/create.go:157`

### Failure scenario

Grove validates the missing target at line 147. It then creates the repository parent at line 157.

Another process can replace that parent with a symlink before line 172 runs Git.

A black-box check replaced `<root>/repo` with a symlink to an outside directory at Git invocation.

Git created the worktree outside the root. Grove returned exit code 4 and stored `manual_review`, but the outside worktree remained.

### Fix

Create the repository parent before target validation. Canonicalize it and reject symlink components.

Repeat parent and target validation immediately before Git add. Use owner-only directories and reject unsafe existing root permissions.

Use no-follow directory operations if Grove must protect against a process with the same user ID.

## 5. Medium: An interrupted bootstrap can keep Grove alive forever

Review comment: Signal the bootstrap process group and use a bounded wait before a forced group termination.

`internal/bootstrap/bootstrap.go:164`

### Failure scenario

On context cancellation, Grove sends `SIGINT` only to `/bin/sh`. It then waits without a time limit at line 164.

A script can ignore `SIGINT`, or its child can continue running. Grove then keeps the bootstrap lock and does not return.

`signal.NotifyContext` remains active at `cmd/grove/main.go:15`, so a second interrupt does not restore the default exit action.

A black-box script ignored `INT` and `TERM`. Grove remained alive two seconds after `SIGTERM` and required `SIGKILL`.

The next startup recovered the bootstrap state as `interrupted`, so durable recovery worked after the forced stop.

### Fix

Start the shell in its own process group. Send the interrupt to the group.

After a short grace period, send `SIGKILL` to the group and wait for it.

Restore default signal handling after the first signal so a second signal stops Grove.

## 6. Medium: A symlinked lock directory can break operation ownership

Review comment: Reject a symlinked lock directory and bind locks to the canonical data directory.

`internal/lock/lock.go:60`

### Failure scenario

`EnsureDataDirs` accepts a symlink at `internal/config/config.go:161`. `NewManager` follows it with `os.Stat` and stores the lexical path.

If the symlink target changes, two processes can lock different files for the same operation token.

A focused check held `locks/token.lock`, changed the `locks` symlink target, and acquired the same token through the same lexical path.

A recovery process can then treat a live create or removal as abandoned. It can clear the operation token while the owner still runs Git.

### Fix

Canonicalize the data directory once after creation. Require `locks` to be a real directory under that canonical path.

Use `Lstat` to reject symlinks. Also reject unsafe ownership or group and world write permissions.

## 7. Medium: Cleanup can prompt while the candidate list is hidden

Review comment: Require the candidate output stream to be a terminal before an interactive approval prompt.

`internal/cli/cli.go:159`

### Failure scenario

The terminal check validates only standard input and standard error. Grove writes candidate paths to standard output.

With `grove cleanup ... >cleanup.log`, the user sees only `Delete N worktrees?` on the terminal.

The user can approve deletion without seeing the absolute paths that the command will remove.

### Fix

Require standard output to be a terminal for interactive confirmation.

If standard output is redirected, require `--yes` or write the full candidate list and prompt to the same terminal stream.

## Verification

This verification did not change repository files.

These test commands passed:

```text
go test ./...
go test -race ./cmd/grove ./internal/app ./internal/bootstrap ./internal/git ./internal/lock ./internal/paths ./internal/store
```

Focused black-box results:

```text
Prompt race:       displayed 1 candidate, deleted 2
Competing create:  exit 0, stored state active, stored branch foreign
Ignored-file race: cleanup action deleted, injected file deleted
Parent symlink:    outside worktree created, record manual_review
Signal handling:   process alive two seconds after SIGTERM
Lock symlink:      second lock for the same token acquired
```

Current-revision test data remains under these temporary paths:

```text
/tmp/grove-current-prompt.laH02z
/tmp/grove-current-adopt.37yr5Z
/tmp/grove-current-ignored.zLrdbc
/tmp/grove-current-outside.5zAYP9
/tmp/grove-current-signal.Fqm08J
```
