# Pre-mutation Bundle Design (`#527`)

## Decision

If we consolidate the current pre-mutation checks, the aggregation point should
be `get_page_for_edit`, by extending its `include` vocabulary with:

- `backlinks` (already exists)
- `impact`
- `preview`

This keeps the "I am about to touch this page" workflow anchored on the existing
edit-oriented read bundle instead of introducing another top-level tool.

## Why `get_page_for_edit`

`get_page_for_edit` already owns the edit-preparation bundle:

- frontmatter
- markdown
- state
- quality
- revision

It is already the canonical tool recommended in mutation recovery paths such as:

- missing `expected_revision`
- `revision_conflict`

So extending it for opt-in pre-mutation diagnostics matches its current role
better than moving edit prep onto `inspect_rendered` or inventing a new
aggregator tool.

## Current workflow cost

A careful agent currently needs up to three separate calls for one mutation
decision:

1. `get_page_for_edit(include=["backlinks"])`
2. `get_related_content(include=["impact"])`
3. `inspect_rendered(include_preview=true)`

That is:

- 3 tool-call round trips
- 3 envelope/meta repetitions
- 3 places where the caller must merge advisory signals

The value is real, but the fragmentation is unnecessary because the data is
already computed by existing helpers.

## Proposed shape

### Request

```json
{
  "slug": "/posts/example/",
  "include": ["frontmatter", "markdown", "state", "quality", "backlinks", "impact", "preview"]
}
```

### Response

```json
{
  "success": true,
  "data": {
    "page": {
      "frontmatter": { ... },
      "markdown": "...",
      "state": { ... },
      "quality": { ... },
      "revision": "sha256:...",
      "backlinks": [ ... ],
      "impact": { ... },
      "preview": { ... }
    }
  }
}
```

The contract rule should be:

- `backlinks`, `impact`, and `preview` are all **opt-in only**
- omitted `include` keeps the current default behavior unchanged
- the aggregated facets must be byte-for-byte equivalent to the current
  standalone computations for the same slug

## Why not `inspect_rendered`

`inspect_rendered` is the wrong center of gravity for this workflow because it
starts from rendered/public inspection, while edit preparation starts from the
source bundle plus revision.

It already has a useful `include_preview=true` facet, and that logic should be
reused, not promoted into the main edit entry point's replacement.

## Why not a new tool

A new tool name would add one more near-duplicate entry point to an API surface
that already has several overlapping read tools. This repo has repeatedly moved
toward composition and richer `include` vocabularies instead of multiplying
tool names.

## Shareability constraint

This issue should not be implemented by copy/pasting logic across handlers.

Before coding, the helper ownership must be settled:

- backlinks helper already exists and is shared enough
- impact helper is package-internal to `get_related_content`
- preview helper is package-internal to `inspect_rendered`

Required implementation direction:

1. extract reusable helper(s)
2. keep one source of truth for each facet
3. prove equality with the standalone tool outputs in tests

## Test requirement

The minimum regression proof for implementation should be:

1. aggregated `page.backlinks` equals standalone `get_backlinks.data.backlinks`
2. aggregated `page.impact` equals standalone `get_related_content.data.impact`
3. aggregated `page.preview` equals standalone `inspect_rendered.data.preview`

If any one of those cannot be kept equal without awkward duplication, the right
fallback is to stop and keep the tools separate.

## Non-goals

This is **not**:

- a planner
- a transactional edit tool
- a publication workflow
- a site-wide audit bundle

It is just a better single-page pre-mutation read.

## Relationship to `#526`

`#526` should land first conceptually. If we unify compact-mode semantics, the
aggregated pre-mutation bundle can then expose one predictable shaping contract
instead of inventing its own token-reduction rules.

## Rollout recommendation

1. finish `#526` decision/implementation
2. factor preview/impact helpers cleanly
3. extend `get_page_for_edit.include`
4. add equality regression tests against the standalone tools
5. update `docs/mcp-contract.md` and `docs/agent-tool-matrix.md`
