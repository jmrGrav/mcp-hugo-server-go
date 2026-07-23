# Compact Response Mode Design (`#526`)

> **Superseded in part by #567**: the `meta` trimming decision below (compact
> keeps only `schema_version`) was reversed after three independent live
> audits flagged an agent in compact mode being unable to tell which server
> build answered it. As of #567, compact mode keeps
> `schema_version`/`release_version`/`commit`/`build_channel` — every `meta`
> field except `generated_at` — and only ever narrows `data`. The rest of
> this document (the `response_mode` mechanism itself, `data` shaping) is
> still current; only the "`meta` keeps only `schema_version`" rationale
> below is historical.

## Decision

Extend the existing `response_mode=compact` vocabulary uniformly across the
**read tool surface**, instead of inventing a second meta-specific flag such as
`include_meta=false`.

This keeps one shaping mechanism, not two:

- `response_mode=standard` remains the default
- `response_mode=compact` becomes the uniform low-token shape for read tools

The envelope stays intact in both modes:

- `success`
- `data`
- `errors`
- `warnings`
- `meta`

Only the **contents** of `data` and `meta` are narrowed in compact mode.

## Why this option

The repo already has a shaping concept (`response_mode`) and a contract section
for it (`docs/mcp-contract.md` §5.2). Extending that concept is lower-risk than
adding a second flag that overlaps semantically with the first.

Rejected alternative:

- `include_meta=false`
  - smaller local change
  - but creates two orthogonal shaping controls
  - harder to explain and harder to apply uniformly

## Main observation from live audit

For a typical structured response, the repeated `meta` object alone is
substantial:

```json
{
  "generated_at": "2026-07-18T18:00:00Z",
  "server_version": "main-3fb254677090",
  "release_version": "v1.5.2",
  "commit": "3fb25467709019d1611a252f2ccfc9376677031d",
  "build_channel": "main",
  "schema_version": "v1.0.0"
}
```

Measured as minified JSON:

- full `meta`: **204 bytes**
- compact `meta` with only `schema_version`: **27 bytes**
- saved per call from `meta` alone: **177 bytes**

At 20 read calls in a normal editorial session, that is about:

- **3540 bytes** saved before counting any additional payload shaping in `data`

This is large enough to justify the extra contract surface, but small enough
that the rollout should stay additive and conservative.

## Compact-mode semantics

### `meta`

In compact mode, `meta` keeps only:

- `schema_version`

Rationale:

- `generated_at` already exists at the root in the structured envelope
- `server_version`, `release_version`, `commit`, and `build_channel` are stable
  session-level identity signals that callers can recover from `initialize` or
  a first standard response when needed

Compact target:

```json
{
  "success": true,
  "generated_at": "2026-07-18T18:00:00Z",
  "data": { ... },
  "errors": [],
  "warnings": [],
  "meta": {
    "schema_version": "v1.0.0"
  }
}
```

### `data`

`response_mode=compact` continues to be **tool-defined** inside `data`, but the
policy should become uniform:

- compact mode may remove convenience/detail fields
- compact mode must not remove the tool's primary identifiers
- `fields`, `include_body`, and `max_body_chars` still apply after compact mode

Required invariants for compact mode:

1. the same call in `standard` and `compact` must describe the same underlying
   resource(s)
2. compact mode must preserve the fields required to page, identify, or retry
3. compact mode must never alter error semantics

## Scope

Apply uniformly to read tools first:

- anonymous read tools
- `content.read` tools

Explicitly **out of scope** for this issue:

- write tools
- build/admin tools
- transport-level flattening
- changing the default away from `standard`

## Rollout plan

1. add a shared helper in the tool-contract layer for compact `meta`
2. extend `response_mode=compact` to every read tool
3. keep the default as `standard`
4. add contract coverage proving:
   - compact mode still returns valid envelope shape
   - compact mode narrows `meta` as designed
   - unsupported modes still fail explicitly
5. measure representative before/after response sizes for:
   - `search_pages`
   - `list_pages`
   - `get_page_for_edit`
   - `get_related_content`

## Risks

### Risk 1 — silent contract drift across tools

If each tool hand-rolls compact mode, the surface will diverge quickly.

Mitigation:

- central helper for compact `meta`
- contract tests over multiple tools

### Risk 2 — compact mode strips too much for agent workflows

If compact mode removes identifiers or retry-critical fields, it saves tokens
but becomes unusable.

Mitigation:

- preserve primary IDs and pagination fields
- preserve revision/state fields on edit-oriented tools

### Risk 3 — write/admin tools copied into scope accidentally

That would create pressure to strip operator-visible diagnostics that are often
the whole point of those tools.

Mitigation:

- keep this issue explicitly read-only in scope

## Why this is not `#520`/`#525`

- `#520` is about write-response root aliases vs canonical `data.*`
- `#525` is about transport-level vs JSON-level error signalling
- `#526` is about token cost for already-correct responses

Those are adjacent contract topics, but they are not the same change.
