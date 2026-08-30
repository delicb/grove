# Grove Specification Review 3

## Review scope

This review checks version 0.3 against only the blocking findings in the two prior reviews.

It also checks for new contradictions that prevent a safe implementation.

## Prior blocking status

| Prior finding | Status |
| --- | --- |
| Go, release targets, SQLite policy, and MIT license | Resolved |
| New-branch default and explicit existing-branch use | Resolved |
| One-root model, stored creation root, and path containment | Resolved |
| Durable create and remove recovery | Not resolved |
| Cleanup safety and command races | Not resolved |
| List and stats state filters | Resolved |
| Fixed JSON contract | Resolved |
| Repository-key mapping and collisions | Resolved |
| Targets below any Git worktree | Resolved |
| `manual_review` quarantine | Resolved |

Version 0.3 now gives safe, testable rules for the resolved findings.

## Blocking findings

### 1. Startup recovery can take over a live operation

Startup recovery changes every `creating` or `removing` record from current Git and disk state. It does not first prove that the owner stopped.

A second command can run after create commits `creating`, but before create runs Git. Recovery then changes the record to final `create_failed`.

The first process can still create the Git worktree. It cannot safely change the final record to `active`.

The same race lets recovery change a live `removing` record to `active`. A random token protects writes, but it does not prove owner liveness.

Require an operation lock or another testable owner-liveness rule. Recovery must change only operations whose owner has stopped.

Add interleaving tests for live create and live remove operations. Each test must pause the owner before the Git command and start another Grove command.

docs/SPECIFICATION.md:307

### 2. Recovery contradicts the cleanup and `touch` isolation rule

Every `touch` runs recovery first. Recovery changes a matching `removing` record back to `active`.

`touch` can then update the activity time after cleanup reserves removal. Cleanup does not recheck the database state, token, or activity before Git removal.

This behavior contradicts the rule that `touch` cannot update a reserved record. It also makes acceptance criterion 32 impossible under the stated transitions.

Recovery must leave a live removal in `removing`. Cleanup must confirm its state and operation token immediately before Git removal.

Add the exact interleaving test: reserve removal, start `touch`, then let cleanup continue. `touch` must fail, and cleanup must keep exclusive ownership.

docs/SPECIFICATION.md:631

I found no other new blocking contradiction.

## Verdict

REJECT
