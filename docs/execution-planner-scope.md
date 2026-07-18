# Execution Planner Scope (`#438`)

## Decision

Do **not** treat `#438` as a from-scratch workflow engine.

Any future execution planner must be evaluated as an extension layered on top
of the existing transactional-edit design:

- `plan_content_change`
- `apply_content_plan`
- `publish_changes`
- `rollback_change`

as defined in `docs/transactional-edit-design.md`.

## Core question

There are two distinct problems that must not be conflated:

1. **single-page edit planning**
   - already conceptually covered by the transactional-edit design
2. **multi-tool / multi-page workflow orchestration**
   - broader, more speculative, and not yet clearly owned by the server

This issue concerns the second problem.

## Why not implement now

The repository only recently stabilized several lower-level primitives:

- revision-aware edits
- idempotent mutations
- edit-oriented read bundles
- publication verification direction
- contract cleanup and response shaping

Building a planner before those foundations fully settle would risk creating a
second orchestration model that competes with the first one.

## Planner responsibilities that must be decided explicitly

Before any implementation, the planner's boundary must be written down:

### Server-owned

- resolve target pages unambiguously
- pin revisions / expected baselines
- enforce tool sequencing constraints the server already understands
- surface rollback/verification requirements

### Agent-owned

- decide editorial intent
- choose natural-language transformations
- decide whether a suggested plan is acceptable

If the server starts inventing editorial strategy rather than sequencing known
tool steps safely, the abstraction is wrong.

## Minimal acceptable planner output

If this is ever implemented, the output should stay mechanical:

```json
{
  "plan_id": "plan_123",
  "steps": [
    {"tool": "get_page_for_edit", "why": "read current revision"},
    {"tool": "plan_content_change", "why": "preview source mutation"},
    {"tool": "apply_content_plan", "why": "apply approved change"},
    {"tool": "inspect_rendered", "why": "validate rendered result"},
    {"tool": "publish_changes", "why": "publish after verification"}
  ],
  "blocked_by": [],
  "warnings": []
}
```

It should not become a free-form AI agent inside the MCP server.

## Non-goals

This issue is not:

- a replacement for `plan_content_change`
- a replacement for client reasoning
- a new natural-language mutation interface
- a general task graph engine
- a hidden auto-publisher

## Required prerequisites

No implementation should begin before:

1. transactional-edit design remains the accepted foundation
2. compact/read shaping work is settled enough that planner step payloads are
   predictable
3. publication verification semantics are fixed
4. failure semantics are explicit for partial plans and blocked steps

## Recommended implementation sequence

If this ever moves beyond design:

1. ship lower-level edit/publication primitives first
2. measure a real multi-step workflow pain point
3. prove the server can add safety/value beyond what the agent can already
   compose client-side
4. only then introduce a planner

## Success criterion for the design phase

The design phase is successful if it prevents the wrong implementation:

- no second orchestration model
- no vague \"AI inside the MCP\" tool
- no planner that cannot explain its own sequencing in deterministic terms
