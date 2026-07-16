# Mutation Coordination Model

This document is the design anchor for issue `#374`.

It defines the concurrency unit and locking rules for page mutations,
multilingual bundles, and site builds, so that multiple agents (or multiple
concurrent requests from one agent) cannot produce ambiguous or corrupted
outcomes.

## Concurrency unit: the whole content root, not per-page or per-bundle

The server does **not** implement per-page or per-bundle locking. The
concurrency unit is the entire configured `content_root`, guarded by a single
process-wide `sync.RWMutex` (`hugosite.ContentMu`).

This is deliberately coarser than the minimum the issue asks about (page,
bundle, build, site-global). A single global lock trivially satisfies every
race the issue lists — same-page, same-bundle, write-vs-build — by
construction, because **only one mutating or build operation can be in
flight at any moment, repository-wide**. The cost is throughput: two agents
editing unrelated pages still serialize behind each other. At this server's
actual scale (a single personal Hugo site, not a multi-tenant CMS), that
cost is negligible and the correctness guarantee it buys — no possible
interleaving between any two mutations, ever — is worth far more than the
complexity of a page/bundle-level lock manager.

Finer-grained locking (page-level or bundle-level) is explicitly **not**
planned unless a real throughput need materializes. Introducing it later
would require: a lock-ordering strategy that avoids deadlock across bundle
members (e.g. `index.md` + `index.fr.md`), a way to detect that two slugs
resolve to the same bundle directory before deciding whether they conflict,
and updated documentation and tests. That is a separate, larger, and
separately-reviewable change.

## Lock acquisition rules

| Operation | Lock | Mode |
| --- | --- | --- |
| `create_page` | `ContentMu` | write (`Lock`), retry loop up to 10s |
| `update_page` | `ContentMu` | write (`Lock`), retry loop up to 10s |
| `delete_page` | `ContentMu` | write (`Lock`), retry loop up to 10s |
| `build_site` | `ContentMu` | write (`Lock`), single `TryLock`, no retry |
| `preview_build` | `ContentMu` | read (`RLock`), retry loop up to 10s |

Write tools (`create_page`/`update_page`/`delete_page`) and `preview_build`
all poll `TryLock`/`TryRLock` every 50ms for up to 10 seconds before giving
up — a short, real content lock hold by another operation is expected to
clear quickly, so a brief wait is more useful to an agent than an immediate
failure.

`build_site` is the one exception: it takes the **write** lock (not a read
lock, even though a build only reads source content — because Hugo must see
a fully consistent snapshot of the content tree, and a build that started
reading half a mutation's writes would be non-deterministic) and does
**not** retry on contention. Builds are already expected to be an
occasional, deliberate operation rather than a tight polling loop, and a
`build_in_progress` response is a completely normal, expected outcome to
hand back to the caller immediately rather than block it for up to 10s.

`preview_build` takes a **read** lock: it does not mutate source content
(`--renderToMemory`), so several previews may run concurrently with each
other, but never concurrently with a real mutation or `build_site` — it
never observes a torn write in progress.

## Deterministic failure and retry guidance

Every lock-contention failure uses the same error code prefix,
`build_in_progress:`, regardless of which two operations collided (mutation
vs mutation, mutation vs build, or preview vs mutation).

- On the **structured envelope** (`create_page`, `update_page`,
  `delete_page` — see `docs/mcp-contract.md` §1.2), `build_in_progress:`
  errors are parsed by `toolcontract.ParseToolError` into
  `retryable: true` with `resolution.action: "retry_later"`, giving the
  caller an explicit, machine-readable instruction rather than just a
  string to pattern-match.
- On the **flat envelope** (`build_site`, `preview_build`), the same
  `build_in_progress:` prefix is returned as a plain MCP protocol error
  string, with no machine-readable `retryable`/`resolution` fields attached.
  **This is a known, only-partial fulfillment of "structured retry
  guidance"** for these two tools specifically — wrapping them in
  `toolcontract.WrapTool` is a plausible follow-up, but it is a distinct,
  separately-reviewable change (it touches only the error path, not the
  flat success envelope that #210 defers to v2.0, so it isn't blocked by
  that freeze — it's simply out of scope for this issue, which is about the
  coordination *model*, not converting individual tools' error envelopes).

In both cases the outcome is deterministic: exactly one contending caller
succeeds, and any other observes `build_in_progress:` — never a corrupted or
partially-applied write, never two conflicting writes both reported as
successful.

## Interaction with `expected_revision`

`update_page`/`delete_page`'s `expected_revision` optimistic-concurrency
check (see the v1.4.2 CHANGELOG entry, #335) composes with the lock model
rather than duplicating it: the lock only prevents *simultaneous* writes,
while `expected_revision` prevents a *queued* write from silently
overwriting a change that landed while it was waiting. Two concurrent
`update_page` calls against the same page with the same captured
`expected_revision` will serialize behind `ContentMu`; the first to acquire
the lock succeeds and advances the revision, and the second — now holding a
stale `expected_revision` — deterministically fails with
`revision_conflict:` after acquiring the lock, rather than silently
clobbering the first write.

## Non-goals

- Per-page or per-bundle lock granularity (see above).
- A distributed lock (this server has a single process; there is no
  multi-instance deployment to coordinate across).
- Wrapping `build_site`/`preview_build` errors in the structured
  `toolcontract.ToolResponse` envelope for machine-readable retry guidance —
  a reasonable follow-up, but a distinct, separately-reviewable change left
  for later rather than folded into this coordination-model issue.
- New workflow/state-machine tooling for future publish/rollback operations
  (#340) — this document only covers the mutation/build primitives that
  exist today; a future transactional workflow layer should build on top of
  this model, not replace it.
