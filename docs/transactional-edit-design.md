# Transactional Edit Design

This document is the design anchor for issues `#338` (`plan_content_change` /
`apply_content_plan`) and `#340` (`publish_changes` / `rollback_change`).

Both issues are explicitly design-only. **No code in this repository
implements any tool named in this document.** Implementation is a distinct,
future issue that must reference this design and re-verify it against
whatever the mutation foundation looks like at that time.

## 1. Why (#338)

Editing a page today is a 2-3 call sequence: read (`get_page_for_edit`),
decide what to change, then `update_page` with `expected_revision`. That's
already better than the pre-#339 4-5 call sequence, but it still asks the
agent to compute the *whole* new body/frontmatter itself and send it in one
shot, with no server-side preview of what will actually change before it's
written.

The problem this solves is narrower than "add a diff view" — `diff_page`
already exists for that, after the fact. What's missing is a **structured,
reviewable, non-writing plan step** that:

- names discrete operations (`update_body`, `add_tag`, ...) instead of a
  full replacement body, so the estimated blast radius is visible before
  anything is sent to disk;
- can be inspected or re-planned without holding a write lock or mutating
  `expected_revision`'s target;
- becomes the one place all of #380's validation and #379's rollback
  semantics apply consistently, instead of being re-derived per write tool.

## 2. `plan_content_change` (#338)

**Read-only.** Requires `content.read` (not `content.write` — planning never
writes). Takes the same target-resolution parameters as `get_page_for_edit`
(`slug`, optional `lang`) plus an `operations` list.

### Request shape

```json
{
  "slug": "/posts/example/",
  "operations": [
    {"op": "update_body", "body": "..."},
    {"op": "add_tag", "value": "hugo"},
    {"op": "set_field", "field": "description", "value": "..."}
  ]
}
```

Operation vocabulary for v1 (deliberately small — this is not a general
patch language):

| op | Fields | Maps to existing `update_page` param |
|---|---|---|
| `update_body` | `body` | `body` |
| `set_title` | `value` | `title` |
| `add_tag` / `remove_tag` | `value` | `tags` (computed diff against current) |
| `add_category` / `remove_category` | `value` | `categories` |
| `set_draft` | `value: bool` | `draft` |
| `set_field` | `field, value` | `description` only for v1 — see Non-goals |

Every operation in a plan targets the *same* page. Multi-page plans are
explicitly out of scope (see Non-goals) — this is a richer single-page edit,
not a batch-mutation tool.

### Response shape

```json
{
  "target": {
    "slug": "/posts/example/",
    "resolved_source_path": "content/posts/example/index.md",
    "revision": "sha256:abc123",
    "state": { "source_state": "present", "public_state": "available", ... }
  },
  "operations_applied": ["update_body", "add_tag"],
  "operations_rejected": [],
  "warnings": [],
  "estimated_diff": {
    "lines_added": 24,
    "lines_removed": 3
  },
  "plan_id": "plan_a1b2c3",
  "plan_expires_at": "2026-07-17T05:20:00Z",
  "requires_confirmation": true
}
```

Differences from the issue's originally proposed shape, decided here:

- **`revision` is the plan's pinned baseline**, computed the same way
  `expected_revision` is today (`contentmodel.SourceRevisionBytes`) — this
  is the value `apply_content_plan` will re-check, not a new mechanism.
- **`estimated_diff` is computed by actually building the candidate content
  server-side** (reusing `applyPageUpdates` + `simpleDiff`, the exact code
  `update_page`'s `dry_run` path already uses) and diffing it against the
  current source — not estimated/guessed. This means `plan_content_change`
  internally does the same work `update_page(dry_run=true)` does today, on
  top of validating each operation resolves cleanly. It intentionally
  overlaps with `dry_run`; see §5 for why both survive.
- **`plan_id` + `plan_expires_at` replace an implicit "send the same content
  back" contract.** A plan is a server-held, TTL'd, immutable snapshot of
  "if you apply this plan against `revision`, here is exactly what will be
  written" — not just a client-side echo. This matters for §3: apply must
  be able to replay the *exact* plan, not a re-derived one, or the "plan
  preview equals what gets applied" guarantee breaks.
- **`operations_rejected`** (empty in the happy path) reports operations
  that don't apply cleanly (e.g. `remove_tag` for a tag the page doesn't
  have) without failing the whole plan — planning surfaces problems, it
  doesn't require the caller to get every operation right first try.

### Storage

A plan is held server-side, in-memory, keyed by `plan_id`, with a **fixed 5
minute TTL** and a cap on outstanding plans (mirrors the write package's
existing `idempotencyStore` pattern in `internal/tools/write/idempotency.go`
— same shape: `map[string]entry` + mutex + TTL prune + max-entries eviction,
new instance, not a shared store, since plans and idempotency results have
different lifetimes and replay semantics). A plan is single-use: applying it
(successfully or not) removes it from the store, so a plan can't be replayed
against a page that has since changed without going through `apply`'s
revision check again via a fresh `plan_content_change` call.

## 3. `apply_content_plan` (#338)

**Mutating.** Requires `content.write`. Takes only `plan_id` (+ the same
`idempotency_key`/`dry_run` parameters every write tool already accepts).

### Request shape

```json
{ "plan_id": "plan_a1b2c3", "idempotency_key": "..." }
```

No body/title/tags are passed again — the whole point of a plan is that
apply executes *exactly* what was previewed, nothing re-derived from fresh
input. If the caller wants something different, they call
`plan_content_change` again.

### Apply-time verification, in order

1. **Plan exists and hasn't expired** → `plan_not_found` (covers expiry,
   already-applied, and unknown IDs identically — no observable difference
   between "never existed" and "expired," matching how `update_page`
   already treats revision mismatches: tell the agent to re-read/re-plan,
   don't help it distinguish attack surface from expiry).
2. **Idempotency replay check** (existing `idempotencyStore.replay`,
   keyed by `plan_id` + a hash of the plan) — same ordering rationale as
   `update_page`'s existing comment: a true replay must return the original
   result even if the underlying page changed after the first apply.
3. **Revision re-check**: the *current* on-disk revision must still equal
   the plan's pinned `revision`. If not → `revision_conflict`, same error
   code `update_page` already uses. This is the one invariant that must
   never weaken: a plan is a promise conditioned on a specific starting
   point, and apply must re-verify that promise still holds, not trust that
   nothing changed between plan and apply.
4. **Validation** (#380's unified contract, once implemented): the exact
   content the plan would write passes the same null-byte/control-char/
   YAML-well-formedness checks any other write goes through. A plan does
   not bypass validation just because it was pre-approved at plan time —
   content can't change between plan and apply (the plan holds the literal
   bytes), so this is a re-check, not new risk, but it must still run.
5. **Write**: identical mechanism to `update_page` — `pg.RevalidateForWrite`
   → `fileutil.AtomicWriteChecked`. Nothing new here; a plan is a deferred,
   pre-validated `update_page` call, not a different write path.

### Response shape

```json
{
  "success": true,
  "plan_id": "plan_a1b2c3",
  "before_revision": "sha256:abc123",
  "after_revision": "sha256:def456",
  "validation": "passed",
  "state": { "source_state": "present", "build_state": "pending", ... }
}
```

Deliberately **no** `build`/`publication` fields, unlike the issue's
originally proposed response — `apply_content_plan` writes source only,
exactly like `update_page` today. Build/publish status belongs to `#340`'s
`publish_changes`, a distinct, later confirmation step (see §4). Collapsing
"apply my edit" and "publish it" into one call would remove the human/agent
checkpoint #340 is designed around.

## 4. Relationship to `#340` (`publish_changes` / `rollback_change`)

`#340` remains explicitly **design/sequencing only** per its own acceptance
criteria — this section records the sequencing decision, not an
implementation.

`publish_changes` and `rollback_change` sit **one layer above**
`apply_content_plan`, not beside it:

- `apply_content_plan` mutates source. It does not build, publish, or
  commit anything (per `docs/git-baseline-model.md`'s trust model, #379:
  write tools commit to the content tree, not to Git — publish/rollback
  tools would be the first place Git-level guarantees actually matter).
- `publish_changes` would take the *result* of one or more prior
  `apply_content_plan` calls (or plain `update_page` calls — it doesn't
  need to know which) and drive the existing `build_site` +
  `verify_publication` pipeline, adding an explicit confirmation gate before
  the build actually goes live in the sense `verify_publication` checks.
- `rollback_change` would resolve its target to a **committed** Git
  baseline state (per `docs/git-baseline-model.md`'s rollback invariant:
  only a real `head_commit` is a valid rollback target, never "the state
  before the last apply") — which is exactly why it cannot be designed
  further until the committed-vs-source-only distinction from `#379` has a
  concrete implementation to point at, and why `#340` stays blocked.

Sequencing, restated: `plan_content_change` → `apply_content_plan` → (human
or agent decides to) `publish_changes` → optionally, later, `rollback_change`
targeting a commit that `publish_changes` itself produced. Each arrow is a
distinct, separately-confirmed step; none of them collapse.

### `#340`'s five key questions, answered

`#340`'s acceptance criteria require these answered even though
implementation stays blocked — a design that only sequences the tools
without resolving them isn't the deliverable the issue asks for.

1. **What scope should be required?** `site.admin` for both
   `publish_changes` and `rollback_change` — matching `build_site`'s
   existing scope, since publishing is fundamentally a build/deploy
   operation, not a content edit (`content.write` covers `apply_content_plan`
   already; publish is one tier up, same as `build_site` today).

2. **How should confirmation work?** No implicit confirmation and no
   auto-publish after apply. `publish_changes` is always a separate,
   explicit call — never chained automatically onto `apply_content_plan`
   (same reasoning as §4: collapsing apply+publish removes the checkpoint).
   MCP has no native "are you sure" primitive, so confirmation here means
   *procedural* separation (a distinct tool call an agent/human must
   deliberately make), not an in-band prompt — consistent with how
   `delete_page` today relies on being a separate, explicit call rather
   than a confirmation flag.

3. **What is rolled back: source only, public output, derived indexes, or
   hooks?** Source is what `rollback_change` reverts (checks out the target
   commit's version of the affected file(s) back into the content tree),
   then triggers the normal `build_site` pipeline so public output and
   derived indexes follow from the reverted source the same way they follow
   any other source change — no separate "roll back the build artifact"
   path. Post-build hooks (`run_post_build_hooks`) are **not** automatically
   re-run on rollback: re-firing outbound webhooks as a side effect of
   undoing a change is a surprising, potentially harmful default (e.g. a
   hook that posts "new content published" to a feed shouldn't fire again
   for a rollback). If hooks need to re-run after a rollback, that's a
   separate, explicit `run_post_build_hooks` call, same as after any build.

4. **How do we avoid undoing another operator's newer change?**
   Optimistic concurrency, the same primitive `expected_revision` already
   provides for regular writes: `rollback_change` takes both a target
   commit and an `expected_head_commit` (the commit the caller believes is
   still current). If the actual current baseline `head_commit`
   (`docs/git-baseline-model.md`) has moved past `expected_head_commit`
   since the caller last checked, the call fails with `revision_conflict`
   (the same error code `update_page` uses for the same class of problem)
   instead of silently reverting on top of someone else's newer change.
   This is the direct payoff of `#379`'s point 2 (only a committed state is
   a valid rollback target) — the check is only meaningful because
   `head_commit` reliably names a real, comparable committed state.

5. **What verification proves publication really matches the intended
   source?** The existing `verify_publication` tool is that proof step, not
   a new mechanism — it already compares source/build/public/index
   freshness and does a live HTTP check. `publish_changes` is designed to
   call it internally after the build completes and surface its result
   (`data.status`) as part of its own response, rather than reporting
   "build succeeded" and leaving the caller to separately verify freshness.
   A `publish_changes` call is not considered fully successful unless
   `verify_publication`'s own status is clean.

## 5. Call-count and payload comparison (#338 acceptance criterion)

Current multi-call edit path for "change the body and add a tag" on an
existing page:

1. `get_page_for_edit` — read frontmatter+markdown+state+revision (~1 call)
2. `update_page` with the full new body, full tag list, `expected_revision`
   — full body payload sent up (~1 call, but the outbound payload is the
   entire new page body, not just the delta)

That's already 2 calls — #339 collapsed what used to be 3-4 into this. What
the multi-call path lacks is not call count, it's **preview**: there is no
step between "decide what to change" and "it's written" where the agent (or
a human supervising the agent) can see the actual diff without also
committing to it. `update_page(dry_run=true)` gets close, but requires
sending the *full* candidate body to get a preview, same as the real write
— there's no cheaper "just tell me what changing this tag would do" step.

With the plan/apply split:

1. `get_page_for_edit` (unchanged, ~1 call)
2. `plan_content_change` with **operations, not a full body** — for a
   small edit (add a tag, tweak a sentence), the request payload is
   smaller than `update_page`'s full-body request, and the response
   includes the diff for free (~1 call)
3. `apply_content_plan` with just `plan_id` (~1 call, tiny payload — no
   body/tags resent)

Net: **same call count (3 vs 3, once `get_page_for_edit` is counted on both
sides)**, not fewer. The win is not fewer round-trips — it's:

- smaller outbound payload for small, targeted edits (operations vs. full
  body);
- a genuine preview step that doesn't require committing to a write to see
  it (`plan_content_change` never touches disk, `update_page(dry_run=true)`
  computes the same diff but is still framed as "the write call, just not
  executed");
- the plan_id becoming a stable handle a supervising human can review
  before an agent is allowed to call apply, which a raw `dry_run` response
  (discarded immediately, nothing to point back at) doesn't give you.

This is a smaller win than the issue's framing implied ("make editing feel
agent-native" suggested fewer calls). Recorded here explicitly per the
acceptance criterion, rather than overselling it: the honest case for
`plan_content_change`/`apply_content_plan` is payload shape and a reviewable
checkpoint, not round-trip reduction. `update_page` remains the right tool
for a caller that already knows the exact final content it wants to write
and doesn't need a preview step; `plan_content_change` isn't a required hop
before every edit.

## 6. Non-goals (both issues)

- **No multi-page plans.** One plan targets one page. A batch-edit tool
  (multiple pages in one plan) is a different, larger surface with its own
  partial-failure semantics question (see the existing partial-success
  precedent, issue #372) and is not implied by this design.
- **No general JSON-patch/arbitrary-field operation.** `set_field` is
  scoped to `description` only for v1; opening it to arbitrary frontmatter
  keys reintroduces exactly the free-form-body-replacement risk this design
  exists to avoid, and would need its own validation story.
- **No automatic apply.** `plan_content_change` never writes. There is no
  "plan and apply in one call" shortcut — that would just be `update_page`
  with extra steps, and would defeat the reviewable-checkpoint purpose.
- **No build/publish coupling in `apply_content_plan`.** See §4 — that's
  `#340`'s layer, deliberately kept separate.
- **`#340` implementation stays blocked** on: this design being reviewed,
  `plan_content_change`/`apply_content_plan` actually existing and being
  exercised in production long enough to trust the revision-check
  invariant in §3 holds under real concurrent use, and `docs/git-baseline-
  model.md`'s committed-state distinction (#379) having a concrete runtime
  implementation a rollback target can resolve against.

## 7. Open questions for the implementation issue

Recorded, not answered, here — the acceptance criteria for #338/#340 ask
for the design to exist, not for every question to be pre-resolved:

- Should `plan_content_change` acquire `hugosite.ContentMu` at all (even a
  read-lock), or is reading the current revision outside the lock
  acceptable given `apply_content_plan` re-verifies it under the lock
  anyway? (Leaning: no lock needed for planning — the revision re-check at
  apply time is the actual safety boundary, matching how `update_page`
  itself doesn't need a pre-lock read to be correct.)
- Does a plan need to be visible/listable (`list_pending_plans`), or is
  `plan_id` opaque and caller-held sufficient? (Leaning: caller-held is
  sufficient for v1 — a listing tool adds a new visibility/scope question
  this design doesn't need to answer to be useful.)
- `plan_content_change`'s `requires_confirmation` field (§2) and `#340`'s
  confirmation answer (procedural separation, not an in-band prompt) are
  resolved independently — `requires_confirmation` is informational
  metadata on the plan response (e.g. `true` when the diff exceeds some
  size threshold), not a gate the server itself enforces, since
  `apply_content_plan` requiring a separate call *is* the enforcement.
  Whether `requires_confirmation` should influence anything beyond that is
  left for the implementation issue.
