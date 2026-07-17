# Git Baseline Model

This document is the design anchor for issue `#356`.

It defines how `mcp-hugo-server-go` should reason about a **local Git
checkout** used as the baseline for `diff_page`, runtime diagnostics, and later
publication verification.

It does **not** by itself enable commit, push, pull, or history rewrite.

## Goal

The server needs a trustworthy answer to:

- which local checkout is the baseline for Hugo source diffs;
- whether that baseline is usable, stale, dirty, or unavailable;
- which remote/branch it is expected to track;
- what later tools should report when the Git baseline is degraded.

The baseline is a **local checkout model**, not a claim that GitHub is always
the current source of truth.

## Source of truth model

The content source of truth remains the live Hugo content tree configured via
`content_root`.

The Git baseline is a **read-only comparison source** used to explain:

- what changed relative to a known commit;
- whether runtime Git metadata is available;
- whether the local checkout appears in sync with the expected backup remote.

For this repository, the intended backup remote is the private
`jmrGrav/hugo-arleo.eu` repository, but the MCP must never silently assume that
the remote is fresh.

## Configuration contract

The configuration now reserves a dedicated section:

```yaml
git_baseline:
  mode: auto        # auto | configured | disabled
  repo_path: ""     # absolute path when mode=configured
  branch: main
  remote: origin
```

Semantics:

- `mode: auto`
  - current runtime behavior may continue to auto-detect a local Git root from
    `content_root`;
  - later issues (`#322`, `#344`) should still expose that the baseline was
    auto-detected rather than explicitly pinned.
- `mode: configured`
  - the server should use `repo_path` as the authoritative local checkout for
    Git baseline operations;
  - `repo_path` must be absolute.
- `mode: disabled`
  - Git-backed diff/runtime diagnostics should degrade explicitly rather than
    probing the host filesystem.

`branch` and `remote` are **expectations for diagnostics**, not a command to
pull, reset, or rewrite the checkout.

## Baseline states that later runtime surfaces should use

This issue does not implement the runtime DTO yet, but it fixes the vocabulary
the follow-up issues should use.

Suggested baseline states:

- `unavailable`
  - `.git` metadata cannot be reached or Git is not installed.
- `local_only`
  - a local checkout exists, but no expected remote is configured/reachable.
- `in_sync`
  - local checkout and expected remote/branch agree.
- `ahead`
  - local checkout has commits not yet visible on the expected remote.
- `behind`
  - local checkout is behind the expected remote.
- `diverged`
  - local checkout and expected remote both advanced.
- `dirty`
  - local checkout contains uncommitted changes.
- `stale`
  - baseline is usable for diff, but old enough or desynchronized enough that
    publication/runtime conclusions must carry a warning.

Follow-up issue mapping:

- `#322` should use these states when refining `diff_page`.
- `#344` should expose the same model in `get_runtime_status`.
- `#346` should reuse the same trust model when proving publication freshness.

### `diff_page` status vocabulary (landed via #322)

`diff_page`'s per-call `status` field now distinguishes:

- `git_unavailable` — no usable Git baseline was reachable at all (`git_baseline.mode: disabled`,
  no `.git` found from `content_root`, or the `git` binary/HEAD could not be read). `diff_available`
  is `false`, `fallback_mode` is `source_content`, and the warning surfaces the underlying reason.
- `git_untracked` — a Git baseline was found, but the specific source file is not yet tracked in
  `HEAD` (e.g. immediately after `create_page`, before any commit). `diff_available` is `false`,
  `fallback_mode` is `source_content`, and the warning explicitly says the file is new.
- `unchanged` / `modified` / `deleted` — a Git baseline and a tracked version of the file were both
  found; `diff_available` is `true` and `diff` carries a real unified diff (empty for `unchanged`).
  These were never ambiguous — the issue's complaint was specifically about `git_not_available` — so
  they keep their pre-existing names rather than gaining a `git_` prefix.

This is deliberately narrower than full `git_baseline.mode: configured` wiring (a separate baseline
`repo_path` distinct from `content_root`): `diff_page` currently only respects `mode: disabled` to
skip host probing outright. Using a configured `repo_path` as the diff baseline is left to a
follow-up (`#346`) since it requires the baseline checkout to mirror `content_root`'s layout.

### `get_runtime_status` (landed via #344)

`get_runtime_status` (`site.admin`) reports a `git` sub-object that honors
`git_baseline.mode`:

- `mode: disabled` short-circuits before any host probing; `available` is
  `false` and `error` explains the baseline is disabled by configuration.
- `mode: configured` uses `git_baseline.repo_path` as the baseline root.
- `mode: auto` (default) auto-detects a Git root from `content_root`, same as
  `diff_page`.

Only `branch`, `head_commit`, and `dirty` are exposed — never an absolute host
path — so the tool cannot be used to enumerate host filesystem layout. The
same probe also backs a `degraded` list on the response explaining which other
tools (`build_site`, `diff_page`) are affected when `hugo` or the Git baseline
is unavailable.

## Service and filesystem requirements

The MCP service only needs **read-only** access to the baseline checkout for the
scope of this issue.

Operator requirements:

- the service user must be able to read:
  - the configured `repo_path`;
  - the `.git` directory or worktree metadata needed by `git -C <repo> ...`;
  - the working tree files used for diff inspection;
- the service does **not** need write access to the Git checkout for this
  issue.

With `ProtectSystem=strict`, the baseline checkout may stay read-only. Do not
add it to `ReadWritePaths` unless a later audited issue explicitly requires Git
mutation.

## Non-goals for this issue

Out of scope here:

- automatic `git pull`
- automatic commit or push
- force-push or history rewrite
- treating the remote as authoritative when the local checkout is stale
- publication-side Git workflows

Those require separate review because they broaden trust and blast radius.

## Rollback and trust model (#379)

This section is the design anchor for issue `#379`. It states explicitly, in
one place, the trust contract that `#340` (publish/rollback workflow design)
and any future rollback/publication tool must build on. It does not add new
runtime behavior — it names invariants that are already true of the model
above and closes the ambiguity #379 was filed to resolve.

1. **Commit semantics.** A write tool (`create_page`, `update_page`,
   `delete_page`, `upload_page_asset`) commits its change to the *content
   tree* (the `content_root` filesystem) synchronously, before returning
   success. It does **not** commit to Git. Git commit/push remains entirely
   out of scope for the MCP server itself (see Non-goals above) — whatever
   commits the working tree into Git history (a human, a CI job, a separate
   automation) is external to this server and operates on its own schedule.
   A source-only change (written to disk but not yet committed by whatever
   external process does that) is therefore **not yet part of the Git
   baseline** and cannot be a rollback target.

2. **Rollback safety.** Only a *committed* state in the Git baseline
   checkout is a safe rollback target — a specific `head_commit` that
   `get_runtime_status`/`diff_page` have already observed and reported. A
   rollback tool must resolve its target to a real commit in that baseline,
   never to "the state before the last write," since the last write may not
   correspond to any commit at all if nothing has committed the working tree
   since. This is exactly why #340 stays blocked: designing "roll back to
   the previous state" without this distinction would silently assume every
   write is committed, which this model explicitly says is not guaranteed.

3. **Remote sync authority.** The local baseline checkout (`git_baseline`)
   is authoritative for diagnostics and rollback targets. The configured
   `remote` is a comparison point only — used to compute `ahead`/`behind`/
   `diverged`, never treated as more current than the local checkout. No
   tool pulls from, pushes to, or otherwise mutates the remote.

4. **Divergence handling.** When the local checkout's `head_commit` and the
   expected remote disagree (see the `ahead`/`behind`/`diverged` states
   above), the correct behavior is to surface a warning, not to resolve it
   automatically. No tool may force-push, rebase, or silently prefer one
   side. Resolution is an operator action outside the MCP server.

5. **Agent trust boundaries.** An agent (of any scope, including
   `operator`) can *read* Git state (`head_commit`, `dirty`, future
   `ahead`/`behind`/`diverged`) via `get_runtime_status` and `diff_page`. No
   current or currently-designed tool lets an agent commit, push, rewrite
   history, or force a rollback without an explicit, individually-confirmed
   call naming its target commit. There is no implicit or automatic revert
   on a failed write — a failed write simply leaves the content tree as it
   was before the attempt.

These five points are the complete answer to #379's key questions. They
require no code change: `get_runtime_status`'s existing `head_commit`/`dirty`
fields (landed via `#344`, regression-tested in
`internal/tools/admin/runtime_status_test.go`) already give agents the
committed-state visibility invariant 5 requires, and the "Non-goals" section
above already forbids everything invariants 1, 3, and 4 rule out. What #379
adds is stating the *rollback* consequence explicitly, since a rollback tool
is the first place an implicit "write = committed" assumption would actually
cause harm.

## Recommended operator setup

For production or staging:

1. keep a local checkout of the backup repository on the host;
2. point `git_baseline.repo_path` at that checkout;
3. keep `branch` and `remote` explicit so degraded states can be explained;
4. grant the MCP service read-only access to that checkout;
5. let `diff_page` / runtime diagnostics warn when the baseline is dirty,
   behind, or unavailable instead of guessing.
