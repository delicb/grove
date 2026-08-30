# Grove JSON and agent skill verification

Result: **FAIL**

## Findings

### HIGH: Missing explicit bootstrap scripts break the JSON result contract

> Allow `BootstrapStateFailed` in the pending transition list. A missing explicit script selects `failed`, but this validator rejects that transition.
>
> Grove then reports a database error after Git created the worktree. The caller does not receive the required worktree path.

internal/store/updates.go:258

**Proof**

Command:

```sh
grove create missing-bootstrap --repo "$repo" \
  --bootstrap-script does-not-exist.sh --json
```

Actual result:

```text
exit:   8
stdout: empty
stderr: {"schema_version":1,"command":"create","error":{"code":"database_error",...}}
```

The next Grove command changed the stored bootstrap state from `pending` to `interrupted`.

`docs/SPECIFICATION.md:430` requires state `failed` and exit code 6. `docs/SPECIFICATION.md:677` requires a result document on standard output.

**Fix**

Permit `pending` to `failed` in `validateBootstrapUpdate`. Add a black-box test for a missing explicit script.

The test must verify these results:

- Exit code is 6.
- Standard output contains one `create` result.
- Standard error is empty.
- `data.bootstrap.state` is `failed`.
- `data.bootstrap.exit_code` is `null`.
- `data.worktree.path` is present.

### MEDIUM: The skill reads an empty result file after normal command errors

> Capture the create exit status before the skill reads `create.json`. Pre-result errors leave that file empty by contract.

skills/grove-worktrees/SKILL.md:25

**Proof**

A `not_repository` error returned exit code 4 and wrote zero bytes to `create.json`. The prescribed `jq` command then returned exit code 4.

This behavior matches `docs/SPECIFICATION.md:675`. The skill only explains exit code 6 and then tells the agent to read the file.

The README already handles this case at `README.md:139` through `README.md:155`.

**Fix**

Use the README status flow in the skill. Stop on an empty result file. Preserve and inspect valid results for exit codes 6 and 7.

### LOW: The skill accepts a whitespace-only agent identity

> Reject a whitespace-only `GROVE_AGENT`. The current check accepts spaces, although Grove rejects them as an invalid agent ID.

skills/grove-worktrees/SKILL.md:17

**Proof**

```text
GROVE_AGENT='   ' sh -c 'test -n "${GROVE_AGENT:-}"'
exit: 0
```

Section 10 trims outer spaces and rejects a whitespace-only value. The skill says the identity check is required.

**Fix**

Check for at least one non-space character. Let Grove perform the full length, UTF-8, and control-character checks.

### LOW: The skill does not quote path placeholders

> Quote the repository and worktree path placeholders. Grove supports spaces in paths, but these shell forms split such paths into arguments.

skills/grove-worktrees/SKILL.md:25

skills/grove-worktrees/SKILL.md:38

**Proof**

An unquoted repository path named `repository with spaces` returned this result:

```text
exit: 2
stdout: empty
error: invalid_arguments: unexpected argument with
```

The README quotes repository and worktree paths. The focused checks also used a repository and managed root with spaces.

**Fix**

Use these forms:

```sh
grove create <name> --repo "<repository-path>" --json >create.json
grove touch "<absolute-worktree-path>" --json
```

## Checks that passed

- `go test ./...` passed.
- A fresh binary passed 978 focused JSON assertions.
- Every command result had all required fields.
- Nullable fields used JSON `null`.
- Empty arrays used `[]`, not `null`.
- Stable error codes and issue codes matched section 20.1.
- Worktree, bootstrap, source, encoding, cleanup action, and cleanup reason enums matched the specification.
- Successful JSON results used standard output only.
- Pre-result JSON errors used standard error only.
- Bootstrap standard output and error stayed separate.
- Invalid UTF-8 bootstrap output used base64.
- Each captured bootstrap stream stopped at 1 MiB and set its truncation flag.
- Partial `list`, `stats`, and `cleanup` results returned exit code 7 with nonempty `failures`.
- Focused runs covered exit codes 0, 2, 3, 4, 5, 6, 7, and 8.
- Source review confirmed exit code 10.
- Unit tests confirmed that bootstrap exit code 6 takes priority over partial exit code 7.
- JSON cleanup did not prompt in dry-run, approved, or unapproved modes.
- Root help and each command help returned exit code 0 with clean standard error.
- Help and cleanup text included `grove touch` guidance.
- README JSON examples used valid fields, status handling, safe cleanup, and quoted paths.
- Final repository hashes matched before and after the checks.

Focused check proof: `/tmp/grove-json-verification.Fr4XBi/proof.txt`
