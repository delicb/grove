# Grove Specification Review 2

This review compares version 0.2 with all blocking and major findings in `docs/SPEC_REVIEW.md`.

This review accepts `--use-existing` as the explicit option for an existing branch. This behavior meets the stated user decision.

## Resolved findings

### Finding 1: Go, build policy, and license

Version 0.2 requires Go 1.25, selects a pure Go SQLite policy, names release targets, and requires the MIT license.

References:

- `docs/SPECIFICATION.md:7`
- `docs/SPECIFICATION.md:35`
- `docs/SPECIFICATION.md:48`
- `docs/SPECIFICATION.md:834`

### Finding 2: Existing-branch behavior

The default operation now fails for an existing branch. `--use-existing` gives the required explicit choice.

The specification also covers linked worktrees, detached `HEAD`, unborn branches, invalid bases, and branch races.

References:

- `docs/SPECIFICATION.md:21`
- `docs/SPECIFICATION.md:84`
- `docs/SPECIFICATION.md:331`
- `docs/SPECIFICATION.md:340`
- `docs/SPECIFICATION.md:358`

### Finding 4: Durable creation and removal

Grove now commits `creating` and `removing` states before Git changes the worktree. Recovery compares both Git and disk state.

The contract preserves uncertain paths and supports recovery after a database failure.

References:

- `docs/SPECIFICATION.md:278`
- `docs/SPECIFICATION.md:293`
- `docs/SPECIFICATION.md:370`
- `docs/SPECIFICATION.md:589`

### Finding 6: Record filters

The list and stats state rules now agree. Both commands define counts, repository totals, and size contributions.

References:

- `docs/SPECIFICATION.md:468`
- `docs/SPECIFICATION.md:485`
- `docs/SPECIFICATION.md:511`
- `docs/SPECIFICATION.md:525`

### Finding 9: Repository detection

Version 0.2 rejects bare repositories and limits repository paths. It defines linked-worktree behavior and avoids registration during read commands.

References:

- `docs/SPECIFICATION.md:68`
- `docs/SPECIFICATION.md:76`
- `docs/SPECIFICATION.md:82`
- `docs/SPECIFICATION.md:86`

### Finding 10: Current branch data

Reconciliation now refreshes branch and detached-commit data. Human and JSON output both define detached worktrees.

References:

- `docs/SPECIFICATION.md:454`
- `docs/SPECIFICATION.md:456`
- `docs/SPECIFICATION.md:474`
- `docs/SPECIFICATION.md:675`

### Finding 11: Agent identity

The specification now lists exact agent markers, conflict priority, stored prefixes, input validation, and immutable creator behavior.

References:

- `docs/SPECIFICATION.md:299`
- `docs/SPECIFICATION.md:309`
- `docs/SPECIFICATION.md:311`
- `docs/SPECIFICATION.md:313`

### Finding 12: Bootstrap execution

The bootstrap contract now defines missing scripts, option conflicts, input, output limits, encoding, interruption, shell use, and trust boundaries.

The MVP removes bootstrap retry, so it does not need a retry lookup rule.

References:

- `docs/SPECIFICATION.md:338`
- `docs/SPECIFICATION.md:386`
- `docs/SPECIFICATION.md:394`
- `docs/SPECIFICATION.md:410`
- `docs/SPECIFICATION.md:420`

### Finding 15: Configuration

The MVP now uses read-only configuration commands. It defines path priority, missing files, empty values, and relative path bases.

References:

- `docs/SPECIFICATION.md:141`
- `docs/SPECIFICATION.md:148`
- `docs/SPECIFICATION.md:180`
- `docs/SPECIFICATION.md:186`

### Finding 16: Git support and path parsing

Version 0.2 sets Git 2.36 as the minimum. It requires zero-delimited porcelain output and removes the fallback parser.

It also requires argument arrays and rejects reference values that Git could read as options.

References:

- `docs/SPECIFICATION.md:44`
- `docs/SPECIFICATION.md:360`
- `docs/SPECIFICATION.md:442`
- `docs/SPECIFICATION.md:801`

### Finding 17: Agent skill

The skill now uses a valid cleanup age and requires a stable agent identity. It separates dry-run review from deletion approval.

References:

- `docs/SPECIFICATION.md:817`
- `docs/SPECIFICATION.md:824`
- `docs/SPECIFICATION.md:826`

## Remaining blocking findings

### Repository keys do not support all accepted repository paths

Section 5 accepts repository directory names that use valid UTF-8. Section 6 restricts repository keys to a small ASCII character set.

The key must also start with the main checkout directory name. Names such as `my api` and `café` cannot meet both rules.

Define one mapping for invalid characters, empty mapped names, and hash-prefix collisions. Add tests for spaces, Unicode names, and collisions.

This issue leaves the layout contract infeasible for valid repository paths.

References:

- `docs/SPECIFICATION.md:88`
- `docs/SPECIFICATION.md:100`
- `docs/SPECIFICATION.md:102`
- `docs/SPECIFICATION.md:113`

### The root check still permits a worktree inside an unrelated worktree

Grove rejects a target inside worktrees of the source repository. It does not reject a target inside another repository's worktree.

A configured root can therefore create nested worktrees. An outer cleanup can delete an ignored nested worktree without force.

Reject a target with any Git worktree ancestor. Alternatively, define and test a safe contract for this case.

This issue leaves prior blocking finding 3 partly unresolved.

References:

- `docs/SPECIFICATION.md:125`
- `docs/SPECIFICATION.md:129`
- `docs/SPECIFICATION.md:135`

### `manual_review` reconciliation contradicts cleanup isolation

Section 9 says that a `manual_review` record never enters cleanup automatically.

Only `removed` and `create_failed` are final. Therefore, `manual_review` is a non-final state.

Section 14 changes any matching non-final record to `active`. Cleanup then accepts that record as a candidate.

Exclude `manual_review` from generic reconciliation. Another valid fix is an explicit, testable transition that proves the state safe.

This issue leaves prior major finding 8 unresolved and can violate cleanup safety.

References:

- `docs/SPECIFICATION.md:260`
- `docs/SPECIFICATION.md:295`
- `docs/SPECIFICATION.md:444`
- `docs/SPECIFICATION.md:448`
- `docs/SPECIFICATION.md:562`

### Cleanup can race with `touch`

The final safety check and the `active` to `removing` state change are separate operations.

A concurrent `touch` can update the activity time after the check. Cleanup can then remove a worktree with recent Grove activity.

Use one conditional database transaction to verify the state and activity time, then reserve the removal.

After that reservation, repeat all Git, path, and status checks. Add an acceptance test for this command interleaving.

This issue leaves the operation-race part of prior blocking finding 5 unresolved.

References:

- `docs/SPECIFICATION.md:432`
- `docs/SPECIFICATION.md:558`
- `docs/SPECIFICATION.md:585`
- `docs/SPECIFICATION.md:589`
- `docs/SPECIFICATION.md:797`

### The JSON contract is still incomplete

Version 0.2 adds useful result shapes, stream rules, warning objects, and failure objects. It does not define a complete stable API.

The contract does not list stable error, warning, failure, or cleanup-reason codes.

The `touch` and configuration shapes list field names without types or full JSON objects.

Bootstrap variants do not define required fields or `null` values. The contract also omits exit-code priority when one result has multiple failure classes.

Define all enums, field types, required fields, null rules, and error codes. Define whether implementations can add fields within schema version 1.

Acceptance criterion 33 cannot prove one fixed contract until these rules exist.

This issue leaves prior blocking finding 7 unresolved.

References:

- `docs/SPECIFICATION.md:636`
- `docs/SPECIFICATION.md:647`
- `docs/SPECIFICATION.md:690`
- `docs/SPECIFICATION.md:730`
- `docs/SPECIFICATION.md:750`
- `docs/SPECIFICATION.md:775`
- `docs/SPECIFICATION.md:793`
- `docs/SPECIFICATION.md:866`

## Remaining nonblocking findings

### Bootstrap state transitions and crash recovery remain incomplete

The specification lists bootstrap states but does not define all transitions. A process crash can leave `pending` or `running` forever.

Define startup handling for these states. Also define whether a concurrent Grove process can observe a valid `running` state.

This issue leaves part of prior major finding 8 unresolved.

References:

- `docs/SPECIFICATION.md:264`
- `docs/SPECIFICATION.md:274`
- `docs/SPECIFICATION.md:282`
- `docs/SPECIFICATION.md:418`

### Human activity guidance remains incomplete

The term `Grove activity` now has an accurate meaning. The skill also tells agents when to run `touch`.

The specification does not require equivalent guidance for humans. Cleanup output also does not explicitly show the activity timestamp and cutoff.

Add both items so users can understand why Grove selected a worktree.

This issue leaves part of prior major finding 13 unresolved.

References:

- `docs/SPECIFICATION.md:62`
- `docs/SPECIFICATION.md:436`
- `docs/SPECIFICATION.md:558`
- `docs/SPECIFICATION.md:577`

### Cached stats have an unclear measurement time

The apparent-size algorithm now defines links, sparse files, hard links, missing files, and read errors.

However, cached worktrees can have different measurement times. The stats JSON object provides only one `measured_at` value.

The term `current complete measurement` also has no freshness rule.

Define whether `measured_at` means calculation time, oldest scan time, newest scan time, or another value.

This issue leaves part of prior major finding 14 unresolved.

References:

- `docs/SPECIFICATION.md:481`
- `docs/SPECIFICATION.md:523`
- `docs/SPECIFICATION.md:529`
- `docs/SPECIFICATION.md:746`

### Platform acceptance tests only prove cross-builds

The build contract names the required operating systems and CPU targets. The acceptance criteria only require cross-build success.

Cross-builds do not prove that SQLite migrations and core commands run on each target.

Add runtime tests for the selected SQLite driver on supported macOS and Linux targets.

This issue leaves one test part of prior finding 1 unresolved.

References:

- `docs/SPECIFICATION.md:39`
- `docs/SPECIFICATION.md:46`
- `docs/SPECIFICATION.md:834`
- `docs/SPECIFICATION.md:835`

## Verdict

REJECT

Version 0.2 resolves most prior design gaps, including the required existing-branch behavior.

The remaining layout, cleanup-state, concurrency, and JSON issues prevent a safe and testable Go MVP contract.
