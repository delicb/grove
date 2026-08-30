# Grove Specification Review 4

## Review scope

This review checks version 0.4 against the two blocking findings in `docs/SPEC_REVIEW_3.md`.

It checks operation-lock ordering, live-operation recovery, cleanup ownership, and `touch` isolation.

## Blocking findings

No blocking findings remain.

## Finding status

### 1. Startup recovery can take over a live operation

Resolved.

Grove gets the exclusive operation lock before it commits an incomplete state. The owner keeps the lock through the final state commit.

Startup recovery tries the stored operation lock without waiting. It leaves the record unchanged when the live owner holds the lock.

Recovery changes the record only after it gets the lock. The stored token also guards the recovery update.

This ordering protects a live `creating` or `removing` operation when its owner pauses before the Git command.

References:

- `docs/SPECIFICATION.md:303`
- `docs/SPECIFICATION.md:305`
- `docs/SPECIFICATION.md:307`
- `docs/SPECIFICATION.md:313`
- `docs/SPECIFICATION.md:315`
- `docs/SPECIFICATION.md:326`
- `docs/SPECIFICATION.md:409`
- `docs/SPECIFICATION.md:411`
- `docs/SPECIFICATION.md:1035`

### 2. Recovery contradicts the cleanup and `touch` isolation rule

Resolved.

Cleanup gets its operation lock before the conditional transaction reserves the record as `removing`.

After reservation, cleanup repeats all safety checks. It then confirms the state, token, and lock ownership immediately before Git removal.

Recovery cannot get the operation lock while cleanup owns it. Generic reconciliation also cannot change a `removing` record.

Because `touch` updates only active records, a `touch` after reservation fails and cannot take ownership from cleanup.

The acceptance criteria require tests for the ownership check and the concurrent `touch` case.

References:

- `docs/SPECIFICATION.md:328`
- `docs/SPECIFICATION.md:635`
- `docs/SPECIFICATION.md:637`
- `docs/SPECIFICATION.md:638`
- `docs/SPECIFICATION.md:639`
- `docs/SPECIFICATION.md:641`
- `docs/SPECIFICATION.md:642`
- `docs/SPECIFICATION.md:647`
- `docs/SPECIFICATION.md:649`
- `docs/SPECIFICATION.md:1050`
- `docs/SPECIFICATION.md:1051`
- `docs/SPECIFICATION.md:1052`

## Verdict

ACCEPT
