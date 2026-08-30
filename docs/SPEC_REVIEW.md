# Grove Specification Review

## Review scope

This review compares `docs/SPECIFICATION.md` with the stated user requirements.

The specification covers most product functions. However, it does not define a safe and testable MVP contract.

The main blockers concern implementation language, license, branch creation, root handling, cleanup safety, recovery, and JSON schemas.

## Requirement coverage

| Requirement | Status | Review |
| --- | --- | --- |
| Simple Go CLI | Missing | The specification does not require Go or define a supported Go version. The current scope is also too large for a simple MVP. |
| One configured worktree directory | Contradicted | The specification adds command-level root overrides. It does not store the root that owns each worktree. |
| Multiple repositories without registration | Mostly covered | Current-directory and `--repo` detection are present. However, a read-only list can create repository records. |
| Track every created worktree and creator | Partial | SQLite fields exist, but failures and process crashes can leave a Git worktree without an active record. |
| Configurable bootstrap script | Partial | The default convention exists. Missing-script rules, input handling, output limits, and retry lookup need clear rules. |
| List worktrees | Covered with gaps | The list command exists. State filters and current branch reporting are not clear. |
| Report count and disk use | Partial | The stats command exists. The meaning of disk use and stale records needs a precise contract. |
| Safely clean old worktrees | Not met | Ignored files, locked worktrees, status errors, root changes, and operation races need fail-closed rules. |
| Support humans and agents | Partial | Human and JSON modes exist. Some commands can still block agents through bootstrap input or terminal prompts. |
| Ship an agent skill | Mostly covered | The skill path and identity rule exist. Its cleanup example omits a required option. |
| macOS and Linux | Covered with gaps | The specification omits minimum Git and Go versions, CPU targets, and the SQLite build policy. |
| Root/repository/worktree layout | Covered | The repository key collision rule needs concurrency tests. |
| New branch from current branch | Contradicted | The specification also attaches existing branches without an explicit opt-in option. |
| Explicit agent identity priority | Partial | The priority exists. Automatic marker names and conflicts are not defined. |
| Clean-only age cleanup | Partial | The high-level rule exists, but the dirty check does not cover all local files. |
| JSON output | Partial | A common envelope exists, but command data schemas and partial-failure rules are missing. |
| SQLite metadata | Covered with gaps | SQLite is required, but state transitions and crash recovery are not complete. |
| MIT license | Missing | The specification does not require an MIT `LICENSE` file or an acceptance test for it. |

## Blocking findings

### 1. The specification omits Go and the MIT license

The user requires a Go CLI and an MIT license. The specification states neither requirement.

Add these items to scope and acceptance criteria:

- Grove uses Go.
- The specification names the minimum supported Go version.
- Release builds support the selected macOS and Linux CPU targets.
- The SQLite driver and build policy support those targets.
- The repository contains an MIT `LICENSE` file.
- Distributed files include all required third-party license notices.

The SQLite choice affects release feasibility. A CGO driver needs platform toolchains and more release work.

A pure Go driver can make cross-platform builds simpler. The specification must select a policy before implementation.

References:

- `docs/SPECIFICATION.md:15`
- `docs/SPECIFICATION.md:161`
- `docs/SPECIFICATION.md:577`

### 2. Existing-branch behavior conflicts with the accepted creation model

The accepted default creates a new branch from the current branch. `--base` selects another base, and `--branch` selects the new branch name.

The specification instead attaches an existing branch when Git permits it. This changes `create` from one clear operation into two operations.

This behavior can attach an old branch when the user expected a new branch. It also changes the meaning of `--base` after a branch lookup.

For the MVP, `create` must fail when the target branch already exists. A later command can add existing-branch support.

If existing-branch support remains, it needs an explicit option such as `--use-existing`. The default must still fail on an existing branch.

The specification must also define these cases:

- The repository path points into a linked worktree.
- The selected checkout has a detached `HEAD`.
- The repository has an unborn branch.
- The base reference does not resolve to a commit.
- The branch appears after validation but before Git runs.

References:

- `docs/SPECIFICATION.md:20`
- `docs/SPECIFICATION.md:237`
- `docs/SPECIFICATION.md:246`
- `docs/SPECIFICATION.md:248`
- `docs/SPECIFICATION.md:250`
- `docs/SPECIFICATION.md:256`
- `docs/SPECIFICATION.md:262`

### 3. Root overrides break the one-root rule and cleanup safety

The purpose says Grove uses one configured root. The create command also accepts `--root`.

A worktree can therefore use root A while later commands use root B. The worktree record does not store root A.

Cleanup refuses paths outside the current effective root. A configuration change can therefore make a valid managed worktree impossible to clean.

The same problem affects command-level root overrides. It also makes the phrase "effective managed root" unsafe and time-dependent.

For the MVP, remove `--root` from `create`. Use one root from configuration or `GROVE_ROOT` for all commands.

If root overrides remain, store the canonical creation root in each worktree record. Cleanup must validate against that stored root.

The specification must also define this behavior:

- Resolve a relative root against a stated directory.
- Canonicalize a root before the target exists.
- Reject a target nested inside the source repository or another Git worktree.
- Use path-component containment, not a string prefix check.
- Handle a root symlink that changes after creation.
- Handle old records after the configured root changes.

References:

- `docs/SPECIFICATION.md:7`
- `docs/SPECIFICATION.md:80`
- `docs/SPECIFICATION.md:182`
- `docs/SPECIFICATION.md:240`
- `docs/SPECIFICATION.md:551`

### 4. Creation can leave an untracked Git worktree

Grove reserves a database path, runs Git, and then records the active worktree. A process crash can occur between those steps.

A database write can also fail after `git worktree add` succeeds. The specification does not define rollback or recovery for that case.

The stale reservation rule makes this worse. It changes an old reservation to `create_failed` without first checking Git and disk state.

This behavior violates the requirement to track every worktree that Grove creates.

Define a durable state sequence such as:

1. Commit a `creating` record before Git runs.
2. Run Git.
3. Change the record to `active` after Git succeeds.
4. On startup, reconcile every `creating` record with Git and the file system.
5. Recover an existing Git worktree as `active`.
6. Mark a record `create_failed` only when Git and the target path are absent.
7. Mark uncertain cases for manual action. Never delete them automatically.

The specification also needs a rule for a database failure after successful cleanup. Reconciliation must recover a pending removal as `removed`.

References:

- `docs/SPECIFICATION.md:271`
- `docs/SPECIFICATION.md:274`
- `docs/SPECIFICATION.md:279`
- `docs/SPECIFICATION.md:448`
- `docs/SPECIFICATION.md:465`
- `docs/SPECIFICATION.md:543`

### 5. Cleanup does not yet meet the safe clean-only requirement

The cleanup design has the correct intent. It uses an age limit, rejects dirty worktrees, keeps branches, and does not use force.

However, the dirty check is incomplete. Normal Git porcelain output does not report ignored files.

A worktree can contain an ignored `.env` file, generated data, or another local file. Cleanup can remove those files without warning.

The specification says no untracked files may exist. It must state whether ignored files count as untracked files.

This choice has a real tradeoff. Treating ignored files as dirty protects data but may retain most bootstrapped worktrees.

Deleting ignored files makes cleanup useful, but it is not a conservative clean-only default. The MVP must choose and document one policy.

A strict safe default should treat ignored files as local data. A later explicit option can permit their removal.

Cleanup must also define these rules:

- Exclude locked Git worktrees.
- Treat every status or repository access error as not safe.
- Recheck state and status after confirmation and before each removal.
- Require a positive, nonzero age.
- Define `d` as 24 hours and `w` as 168 hours.
- Define behavior at the exact age boundary.
- Refuse an interactive cleanup when no terminal can answer the prompt.
- Keep the record active when Git removal fails.
- Update metadata only after Git confirms removal.
- Recover safely if Git succeeds but the database update fails.
- Report each skipped path and the exact skip reason.

`git worktree remove` without `--force` adds protection. The specification must not use that check as its only safety rule.

The final size must be measured before deletion. The current order says Grove records it after deletion.

References:

- `docs/SPECIFICATION.md:417`
- `docs/SPECIFICATION.md:423`
- `docs/SPECIFICATION.md:425`
- `docs/SPECIFICATION.md:431`
- `docs/SPECIFICATION.md:434`
- `docs/SPECIFICATION.md:440`
- `docs/SPECIFICATION.md:446`
- `docs/SPECIFICATION.md:450`

### 6. Record filters contradict each other

The data section says default list and stats commands exclude only removed records. This statement implies that they include missing records.

The list section says `--all` includes missing and removed records. This statement implies that the default list excludes both states.

The stats option says `--all` includes missing records in counts. The result list always names a missing count.

These rules can produce different active, missing, removed, and repository counts across implementations.

Define one state matrix for `list` and `stats`. State which records affect each count, size total, and repository count.

A simple rule is:

- Default `list` shows active records only.
- `list --all` shows all final states.
- Default `stats` reports active totals and separate missing counts.
- `stats --all` also reports removed counts.
- Only active worktrees contribute to disk-use totals.

Stats must reconcile records before it counts them. The current stats section does not require reconciliation.

References:

- `docs/SPECIFICATION.md:197`
- `docs/SPECIFICATION.md:329`
- `docs/SPECIFICATION.md:346`
- `docs/SPECIFICATION.md:384`
- `docs/SPECIFICATION.md:389`

### 7. The JSON contract is not implementable as a stable API

The common envelope does not define the `data` object for any command. It also does not define warning and partial-failure objects.

Agents need exact field names, types, null rules, and state values. A sample empty object cannot provide that contract.

The output stream rules also conflict. One rule requires one JSON document on standard output, but errors go to standard error.

Define the stream rule separately for success and failure. Then define schemas for these commands:

- `create`
- `bootstrap`
- `list`
- `touch`
- `stats`
- `cleanup --dry-run`
- `cleanup --yes`
- `config show`, if it supports JSON

The schemas must include these cases:

- Bootstrap failure after successful worktree creation.
- Partial cleanup success.
- Partial size measurement success.
- Repository reconciliation warnings.
- Unknown size values.
- Missing and removed records.
- Captured bootstrap output that is not valid UTF-8.

For partial success, one document must contain both successful results and failures. The exit code must still show partial failure.

Captured bootstrap output needs a size limit or temporary-file policy. An unbounded capture can exhaust memory.

References:

- `docs/SPECIFICATION.md:313`
- `docs/SPECIFICATION.md:354`
- `docs/SPECIFICATION.md:401`
- `docs/SPECIFICATION.md:448`
- `docs/SPECIFICATION.md:483`
- `docs/SPECIFICATION.md:487`
- `docs/SPECIFICATION.md:496`
- `docs/SPECIFICATION.md:507`
- `docs/SPECIFICATION.md:539`

## Major findings

### 8. Worktree and bootstrap state values are incomplete

The database stores state fields, but the specification does not list their allowed values or transitions.

The terms define `active`, `missing`, and `removed`. Later sections also use reservations, `create_failed`, `bootstrap_failed`, and `not_present`.

Define all worktree and bootstrap states. Define which commands can change each state.

Also define uniqueness rules for:

- Repository identity.
- Repository directory key.
- Active worktree path.
- Active repository and worktree name.
- Reuse of a removed worktree name and path.

Without these rules, migrations, queries, and JSON output cannot share one contract.

References:

- `docs/SPECIFICATION.md:52`
- `docs/SPECIFICATION.md:188`
- `docs/SPECIFICATION.md:189`
- `docs/SPECIFICATION.md:281`
- `docs/SPECIFICATION.md:297`
- `docs/SPECIFICATION.md:465`
- `docs/SPECIFICATION.md:545`

### 9. Repository detection needs exact limits

The common Git directory is a useful repository identity for linked worktrees. The main checkout path is less stable and needs clear recovery rules.

The specification must state whether Grove supports bare repositories. A bare repository has no main checkout or current worktree branch.

Rejecting bare repositories is the simpler MVP rule.

The specification must also define:

- Whether `--repo` accepts a file, a `.git` path, or only a directory inside a checkout.
- Which checkout supplies the current branch when `--repo` points into a linked worktree.
- What happens when the main checkout moves.
- What happens when the common Git directory cannot be canonicalized.
- Whether a read-only `list --repo` may create a repository record.

A list operation should not create an empty repository record. Create the record only when Grove creates its first managed worktree.

References:

- `docs/SPECIFICATION.md:60`
- `docs/SPECIFICATION.md:64`
- `docs/SPECIFICATION.md:66`
- `docs/SPECIFICATION.md:68`
- `docs/SPECIFICATION.md:70`
- `docs/SPECIFICATION.md:72`

### 10. Branch data can become stale

A user can change a worktree branch after creation. The stored branch reference can then differ from Git.

Reconciliation only defines path and state checks. It does not say whether Grove refreshes the branch reference.

`list` must report the current branch from Git. Bootstrap retries must set `GROVE_BRANCH` to the current branch or detached commit.

The specification must also define detached worktrees in human and JSON output.

References:

- `docs/SPECIFICATION.md:183`
- `docs/SPECIFICATION.md:310`
- `docs/SPECIFICATION.md:338`
- `docs/SPECIFICATION.md:346`
- `docs/SPECIFICATION.md:460`

### 11. Agent detection and agent options are unclear

The priority order meets the broad requirement, but automatic detection has no testable contract.

List the exact environment variable for each known agent. Define the result when two agent markers exist.

Define the stored family values and letter case. Reject an ID that contains only spaces.

`bootstrap` and `touch` accept `--agent`, but the database stores only the immutable creator. The specification does not explain these option values.

Either remove those options or store the actor for each activity update and bootstrap run. The MVP only requires the creating agent.

An unknown agent that omits identity becomes `human`. This produces false creator data in JSON and other non-interactive sessions.

Consider requiring `--agent` or `GROVE_AGENT` in a documented agent mode. At minimum, the skill must treat identity as required.

References:

- `docs/SPECIFICATION.md:203`
- `docs/SPECIFICATION.md:210`
- `docs/SPECIFICATION.md:217`
- `docs/SPECIFICATION.md:219`
- `docs/SPECIFICATION.md:221`
- `docs/SPECIFICATION.md:293`
- `docs/SPECIFICATION.md:365`

### 12. Bootstrap rules can block agents and expose sensitive data

The default convention meets the user requirement. Several execution rules still need clear limits.

The missing-script rule depends on the source of the same path value. An explicit default path can fail while the built-in default succeeds.

Define this rule with exact examples for command, environment, configuration, and built-in values.

Also define the conflict between `--no-bootstrap` and `--bootstrap-script`. Define the meaning of an empty environment value.

Human mode inherits terminal input. JSON mode captures output, but it does not define standard input.

A bootstrap script can wait forever for input. JSON mode should use closed input unless an explicit option permits input.

No timeout can be acceptable for an MVP. The specification must define interrupt behavior and the stored bootstrap result after interruption.

The script runs with the user's rights and normally inherits the user's environment. This can expose credentials to repository code.

Document this trust boundary. Also state that scripts must use POSIX `sh`, because Grove ignores their shebangs.

The `name-or-path` lookup also needs exact rules. Names are only unique within a repository.

References:

- `docs/SPECIFICATION.md:127`
- `docs/SPECIFICATION.md:242`
- `docs/SPECIFICATION.md:290`
- `docs/SPECIFICATION.md:293`
- `docs/SPECIFICATION.md:297`
- `docs/SPECIFICATION.md:299`
- `docs/SPECIFICATION.md:301`
- `docs/SPECIFICATION.md:303`
- `docs/SPECIFICATION.md:313`
- `docs/SPECIFICATION.md:315`

### 13. Activity time does not reliably mean inactivity

Cleanup uses `last_activity_at`, but Grove updates it only during create, bootstrap, and touch.

A human can make commits for weeks without running `touch`. Grove can then call the clean worktree old after the user commits all changes.

Branch retention prevents loss of committed Git data. It does not prevent removal of an active checkout or ignored local files.

Keep the simple timestamp model for the MVP, but name it accurately. It records Grove activity, not all worktree activity.

Human documentation must tell users when to run `grove touch`. Cleanup output must show the timestamp used for each candidate.

References:

- `docs/SPECIFICATION.md:358`
- `docs/SPECIFICATION.md:360`
- `docs/SPECIFICATION.md:368`
- `docs/SPECIFICATION.md:370`
- `docs/SPECIFICATION.md:428`

### 14. Disk-use behavior is not precise

The phrase "disk use" can mean apparent file size or allocated file-system blocks. The specification only explains symbolic links.

Choose one measure for the MVP. Apparent bytes are easier to calculate in portable Go.

Define these cases:

- Sparse files.
- Hard-linked files.
- The linked worktree `.git` file.
- Shared Git objects in the main repository.
- Files that disappear during a scan.
- Permission and read errors.
- Cached totals with unknown worktree sizes.

The summary must not present a partial byte total as complete. It must include an incomplete flag or error list.

References:

- `docs/SPECIFICATION.md:23`
- `docs/SPECIFICATION.md:193`
- `docs/SPECIFICATION.md:395`
- `docs/SPECIFICATION.md:399`
- `docs/SPECIFICATION.md:401`
- `docs/SPECIFICATION.md:403`

### 15. Configuration command rules are incomplete

"First available path" does not say what happens when an explicit file does not exist.

The specification also does not say which file `config set` changes. Relative paths have no defined base directory.

Define these cases:

- Missing `--config` file.
- Missing `GROVE_CONFIG` file.
- Both XDG and fallback files exist.
- Empty environment values.
- Relative root and data paths.
- `config set` while `GROVE_CONFIG` is set.
- Failure during atomic rename.
- Existing file permissions.

The full set of configuration write commands is excess scope for the MVP. A read-only TOML file and environment overrides meet the requirement.

References:

- `docs/SPECIFICATION.md:109`
- `docs/SPECIFICATION.md:129`
- `docs/SPECIFICATION.md:144`
- `docs/SPECIFICATION.md:146`
- `docs/SPECIFICATION.md:148`
- `docs/SPECIFICATION.md:555`

### 16. Git support and path parsing need a fixed platform contract

The specification tries `--porcelain -z` and falls back to line parsing. This adds compatibility work and path edge cases.

Set a minimum Git version that supports the required zero-delimited output. Then remove the fallback from the MVP.

If the fallback remains, define quoted path parsing for spaces, tabs, newlines, backslashes, and non-UTF-8 path bytes.

Argument arrays prevent shell injection. They do not replace option and reference validation.

Validate branch and base values with Git. Ensure that user values cannot become Git options.

References:

- `docs/SPECIFICATION.md:264`
- `docs/SPECIFICATION.md:456`
- `docs/SPECIFICATION.md:458`
- `docs/SPECIFICATION.md:467`
- `docs/SPECIFICATION.md:547`

### 17. The skill contains an invalid cleanup command

The skill says agents must run `grove cleanup --dry-run --json`. Cleanup requires `--older-than`.

The shipped example must include an age, such as `--older-than 30d`. It must also explain that dry-run never grants deletion approval.

The skill should require an explicit agent ID before `create`. It should show the same ID on later `touch` calls if that option remains.

References:

- `docs/SPECIFICATION.md:410`
- `docs/SPECIFICATION.md:417`
- `docs/SPECIFICATION.md:564`
- `docs/SPECIFICATION.md:569`
- `docs/SPECIFICATION.md:570`

## Test gaps

The acceptance criteria are too broad for the stated safety contract. Add tests for the following behavior.

### Repository and layout tests

- Detect a repository from its main checkout.
- Detect a repository from a linked worktree.
- Let `--repo` take priority over the current directory.
- Reject a non-repository path.
- Reject or define bare repository behavior.
- Separate repositories with the same display name.
- Reserve colliding directory keys under concurrent creation.
- Reject targets outside the canonical root.
- Reject nested targets inside a source worktree.
- Test roots and repository paths that contain spaces and symbolic links.

### Creation and recovery tests

- Create the default branch from the current branch.
- Apply both `--branch` and `--base`.
- Fail when the new branch already exists.
- Handle a detached `HEAD`.
- Handle an unborn branch.
- Preserve a successful Git worktree after bootstrap failure.
- Recover a process crash after Git succeeds.
- Recover a database failure after Git succeeds.
- Prevent duplicate paths and names under concurrent processes.
- Define reuse after a record reaches `removed`.

### Agent identity tests

- Test every identity priority level.
- Test conflicts between known agent markers.
- Reject control characters and whitespace-only IDs.
- Keep the creator unchanged after bootstrap and touch.
- Confirm the skill uses a stable agent and session ID.

### Bootstrap tests

- Skip a missing built-in default script.
- Fail for each form of explicit missing script.
- Set the correct working directory and environment.
- Define standard input in human and JSON modes.
- Capture standard output and standard error separately.
- Handle invalid UTF-8 and output limits.
- Record nonzero exit, signal, and user interruption results.
- Resolve a worktree name only within the selected repository.

### List, reconciliation, and stats tests

- Show active, missing, and removed records under the defined filters.
- Refresh a changed branch and detached state.
- Fail safely when a repository cannot be read.
- Recover a `creating` record that exists in Git.
- Mark only confirmed absent reservations as `create_failed`.
- Reconcile before stats counts.
- Measure the defined byte type.
- Report unknown and partial sizes without a false complete total.
- Do not follow symbolic links.

### Cleanup tests

- Reject zero and negative ages.
- Test the exact age boundary.
- Reject modified, staged, and untracked files.
- Test the selected policy for ignored files.
- Exclude locked worktrees.
- Fail closed on status, path, and repository errors.
- Recheck a candidate that changes after confirmation.
- Never remove the main checkout.
- Never delete a branch.
- Never use `--force`.
- Refuse a non-terminal prompt without `--yes`.
- Require `--dry-run` or `--yes` in JSON mode.
- Keep correct metadata after partial failure.
- Recover when Git removes a worktree but the database update fails.
- Verify canonical containment after a root symlink changes.

### JSON and exit-code tests

- Validate every command against a fixed JSON schema.
- Keep standard output clean on successful JSON commands.
- Define and test the output stream for JSON errors.
- Return useful data with partial cleanup and size failures.
- Match every stable text error code to a process exit code.
- Confirm that JSON mode never asks Grove prompts.

### Build and license tests

- Build the CLI with the selected Go version.
- Build supported macOS and Linux targets in continuous integration.
- Test the selected SQLite driver on each target.
- Confirm that the repository contains the MIT license text.

## Excess scope for a simple MVP

The current specification is feasible as a larger first release. It is not a simple MVP.

Move these items out of the MVP unless the user confirms them:

- Attaching existing branches.
- Configuration write commands.
- Old Git porcelain fallback parsing.
- Color output.
- Automatic registration side effects from `list --repo`.
- Source reporting for every configuration value.
- Automatic agent-family detection beyond clearly documented markers.
- Bootstrap retry, if creation-time bootstrap is sufficient.

Keep these items in the MVP because the requirements or safety model need them:

- SQLite records and migrations.
- Durable create and remove states.
- Current-directory and `--repo` detection.
- One configured root and the required layout.
- New branch creation from the current branch.
- Explicit agent identity priority.
- Bootstrap execution and result storage.
- Active-only list and clear all-state output.
- Count and defined byte measurement.
- `touch`, if cleanup age uses Grove activity.
- Dry-run, confirmation, clean checks, and no-force cleanup.
- Fixed JSON schemas.
- The agent skill.
- macOS and Linux tests.
- MIT licensing.

## MVP feasibility

A safe MVP is feasible in Go. The current specification needs a smaller scope and stronger operation-state rules.

The best MVP uses one root, new branches only, one minimum Git version, and fixed JSON schemas.

It must recover create and remove operations after process or database failures. It must also use a strict, documented cleanup policy.

The specification is not ready for implementation or acceptance testing in its current form.

## Verdict

REJECT
