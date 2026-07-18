# AI Readiness Rubric (`#437`)

## Goal

Define a **deterministic**, **source-oriented** rubric for a future
`validate_ai_readiness` tool or equivalent validation mode.

This document intentionally avoids any opaque single score. The server should
answer a narrow question:

> Is this page structurally easy for an agent to read, segment, cite, and
> transform safely?

It should **not** answer:

> Is this a good article?

## Output shape

Recommended response family:

```json
{
  "status": "pass|warn|fail",
  "checks": {
    "heading_hierarchy": { ... },
    "section_lengths": { ... },
    "paragraph_lengths": { ... },
    "metadata_presence": { ... },
    "internal_link_density": { ... },
    "citation_structure": { ... }
  },
  "warnings": [],
  "suggestions": []
}
```

If a score is ever added later, it must be a mechanical roll-up of the checks
below, not an independent heuristic.

## Deterministic checks

## 1. `heading_hierarchy`

Purpose:

- detect skipped heading levels that make section structure harder to follow

Rules:

- pass: no heading level jumps greater than 1
- warn: one or more jumps greater than 1
- fail: malformed heading syntax prevents reliable section extraction

Example warning:

- `H2` followed directly by `H4`

## 2. `section_lengths`

Purpose:

- detect long undivided sections that are hard for an agent to segment or cite

Rules:

- measure character count between headings
- warn when a section exceeds a configured threshold without a subheading
- default initial threshold proposal: **2500 characters**

This threshold should be configurable, but the default must be stable and
documented.

## 3. `paragraph_lengths`

Purpose:

- detect very large uninterrupted text blocks

Rules:

- warn when any paragraph exceeds a configured threshold
- default initial threshold proposal: **900 characters**

This is not a readability score. It is only a structure/segmentability signal.

## 4. `metadata_presence`

Purpose:

- ensure the page exposes the minimum source metadata that helps downstream
  agent workflows

Checks:

- title present
- date present
- summary or description present
- tags/categories presence reported, but not required for pass

Suggested statuses:

- fail: title or date missing
- warn: summary/description missing

## 5. `internal_link_density`

Purpose:

- flag long pages that have no internal navigation anchors toward the rest of
  the site

Rules:

- for pages above a length threshold, warn if there are zero internal links
- initial proposal:
  - only evaluate when markdown body length >= **2000 characters**
  - warn if internal link count == 0

This is **not** link correctness. Broken-link validation remains elsewhere.

## 6. `citation_structure`

Purpose:

- estimate whether an agent can cite stable subsections instead of a single
  undifferentiated body

Rules:

- warn when heading count is too low for page length
- initial proposal:
  - if body length >= **3000 characters** and heading count < 2 -> warn

## Explicit non-goals

This rubric must stay out of:

- SEO scoring
- broken-link correctness
- rendered HTML correctness
- publication/build freshness
- model-specific evaluation (ChatGPT vs Claude vs Gemini)
- subjective prose quality
- LLM-generated judgments

Those concerns already belong elsewhere or would make the tool too vague to
trust.

## Reuse path

Preferred future implementation path:

1. load the page through the same source-aware read path as
   `get_page_for_edit` / `build_agent_context`
2. analyze Markdown + frontmatter only
3. return the deterministic report

No new resolver or content-loading branch should be introduced for this tool.

## Acceptance requirements before implementation

Before any code starts, the issue should treat the following as locked:

1. the exact check names
2. initial thresholds
3. pass/warn/fail semantics
4. explicit exclusions

Without those four, implementation would drift into accidental policy.
