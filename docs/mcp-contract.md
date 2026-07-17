# MCP Contract — hugo-public-mcp

This document specifies the observable contract for all tools exposed by the
server: response envelopes, error model, pagination, naming conventions, and
versioning. Agents may use this as a stable reference; deviations are bugs.

---

## 1. Response Envelopes

Two envelope shapes are in use. The shape each tool uses is listed in
[Section 6](#6-tool-inventory). A future major version will standardize all
tools on the structured envelope; flat envelopes are not changed in v1.x
(breaking change — deferred to v2.0, tracked in #210).

### 1.1 Flat envelope

Used by discovery and simple data tools. The top-level object **is** the
result; field names are the natural nouns for that tool.

```json
{ "pages": [ ... ], "total": 42 }
{ "page": { "slug": "/posts/hello/", ... } }
{ "tags": ["go", "hugo"] }
{ "entries": [ ... ] }
{ "slug": "/posts/new/", "path": "content/posts/new/index.md" }
```

There are no `success`, `errors`, or `warnings` fields. Tool-level errors are
reported as MCP protocol errors (non-zero result code), not inside the JSON.

### 1.2 Structured envelope

Used by tools that need richer output: diagnostics, pagination metadata,
partial-success signalling, or forward-compatible extension.

```json
{
  "success": true,
  "version": "v1.0.0",
  "generated_at": "2026-07-12T02:30:00Z",
  "data": { ... },
  "warnings": [],
  "errors": []
}
```

Fields:

| Field          | Type     | Always present | Notes                                              |
|----------------|----------|---------------|----------------------------------------------------|
| `success`      | bool     | yes           | `true` even when `errors` is non-empty if partial results are returned |
| `version`      | string   | yes           | Schema version; currently `"v1.0.0"`               |
| `generated_at` | string   | yes           | RFC 3339 UTC timestamp                             |
| `data`         | object   | yes           | Tool-specific payload                              |
| `warnings`     | string[] | yes           | Non-fatal observations (empty array when none)     |
| `errors`       | string[] | yes           | Problems that degraded the result (empty array when none) |

`success: false` means the call produced no usable result. `success: true`
with non-empty `errors` means a partial result was returned.

---

## 2. Error Model

### Protocol-level errors

Returned as MCP error responses (not inside the JSON body) when the tool
cannot produce any result:

- Missing required parameter → `invalid_params: <field> is required`
- Resource not found → `content_not_found: page not found for slug "<slug>"`
- Authorization failure → MCP 401 / 403
- Index not initialized → `index not initialized`

### In-band errors

Structured-envelope tools may include degraded results with error strings in
`errors[]`. Flat-envelope tools do not use in-band errors.

### Error codes

Error strings use a `snake_case_prefix:` convention for machine-parseable
classification:

| Prefix               | Meaning                                        |
|----------------------|------------------------------------------------|
| `invalid_params:`    | Bad or missing input                           |
| `content_not_found:` | Slug or resource does not exist                |
| `ambiguous_language:`| Multiple language variants and no `lang` param |
| `not_found:`         | File or path does not exist on disk            |
| `rate_limited:`      | Per-caller budget exceeded (delete operations) |

---

## 3. Pagination

Tools that return lists support optional `limit` and `offset` parameters.
Default and maximum limits vary per tool (see tool descriptions). The
structured envelope reflects applied pagination in `data.limit`,
`data.offset`, and `data.total`.

Flat tools that support pagination include `total` at the top level of the
nested result object (e.g., `export.total`).

---

## 4. Naming Conventions

### Tool names

All tools use `snake_case`. Verbs come first:
`get_`, `list_`, `search_`, `create_`, `update_`, `delete_`, `build_`,
`validate_`, `diff_`, `export_`, `explain_`, `generate_`, `suggest_`.

### Response field names

All field names are `snake_case`.

### Slug format

Slugs are always absolute paths with a trailing slash:
`/posts/hello-world/`. Leading slash and trailing slash are both required.
The server normalizes slugs before lookup; partial slugs (`posts/hello`) are
accepted but normalized internally.

### Date format

All dates are ISO 8601 / RFC 3339. Date-only values use `YYYY-MM-DD`.
Full timestamps use `YYYY-MM-DDTHH:MM:SSZ` (UTC).

---

## 5. Versioning

- `version: "v1.0.0"` in structured envelopes refers to the **response schema
  version**, not the server version.
- The deployed server version is carried separately in
  `meta.server_version` inside structured tool responses.
- Flat envelope tools do not carry a `version` field; their schema is
  implicitly v1.
- `meta.server_version` and the MCP `initialize` response's `serverInfo.version`
  both come from `internal/buildinfo.Version`, injected at build time via
  `-ldflags`. It defaults to the placeholder `"dev"` when a binary is built
  without that flag (e.g. `go run`/`go build` during local development). CI,
  the deploy workflow, and the Makefile all set it to the real git tag or
  commit; a release or production build should never report `"dev"`.

### 5.1 Envelope nesting vs. third-party scanner expectations (#328)

Automated scanners such as [mcpscan.dev](https://mcpscan.dev) score tools
against a convention where a tool's primary output schema is the top-level
JSON payload. The structured envelope described in
[Section 1.2](#12-structured-envelope) deliberately nests that payload under
`data` instead, alongside `success`/`warnings`/`errors`/`meta` — this is the
documented v1.x contract (#278), not an oversight. mcpscan flags this as
`Non-Standard Response Wrapping` and deducts score accordingly.

This is a known, accepted tradeoff, not a bug to silently fix:

- **Real cost**: lower mcpscan score.
- **No cost to real clients**: Claude.ai, ChatGPT, and other live MCP
  integrations already depend on the uniform envelope (`success`/`data`/
  `warnings`/`errors`/`meta`) to distinguish partial success from hard
  failure and to read `meta.server_version` consistently across tools.
  Flattening the payload in place would be a breaking change to every
  existing caller, for a scanner-score gain with no functional benefit to
  agents.

**Decision**: do not flatten the structured envelope in v1.x. If a flattened
top-level payload is ever wanted, it ships as an explicit new contract
version (a hypothetical `v2` response shape, versioned the same way
`version: "v1.0.0"` is today), never as a stealth v1.x patch that changes
what existing callers already parse. This mirrors the flat-envelope freeze
already documented in [Section 1](#1-response-envelopes) (`#210`) — both are
the same category of decision: a v1.x compatibility guarantee outranks a
scanner-score optimization.

### 5.2 Response shaping (#337)

Some read tools accept optional shaping parameters that reduce payload size
without changing the envelope (Section 5.1 still applies — shaping narrows
what's inside `data`/the flat top level, never removes `success`/`errors`/
`warnings`/`meta`). Omitting all shaping parameters is always a no-op: a
call with no shaping parameters returns the exact same shape it returned
before this feature existed.

| Parameter        | Type       | Meaning                                                    |
|-------------------|------------|-------------------------------------------------------------|
| `response_mode`   | string     | `standard` (default) or `compact` (reduced field set, tool-defined). `full` and `ids_only` are reserved for future work and rejected as `invalid_params` until implemented — they are never silently treated as `standard`. |
| `fields`          | string[]   | Restrict each returned item to the named JSON fields. Applied after `response_mode`, so it can further narrow a `compact` row. Unknown field names are silently dropped, not an error. |
| `include_body`    | bool       | Default `true`. When `false`, omit large body content (e.g. Markdown) and return metadata only. Same nil-means-true semantics everywhere it appears (see `export_agent_context`, #325). |
| `max_body_chars`  | int        | Truncate a body field to N characters. `0` (default) disables truncation. Truncation adds a `warnings` entry so callers know the body was cut. |

Not every tool supports every parameter — see [Section 6](#6-tool-inventory)
for which parameters each tool accepts. Current adopters: `search_pages`
(`response_mode`, `fields`), `build_agent_context` (`response_mode`,
`max_body_chars`), `export_agent_context` (`include_body`, predates this
section — see #325). Additional tools adopt these parameters incrementally;
adding support to a new tool is not a breaking change since the parameters
are optional and additive.

---

## 6. Tool Inventory

### Anonymous (no auth required)

| Tool                  | Envelope  | Top-level key(s)          |
|-----------------------|-----------|---------------------------|
| `list_pages`          | flat      | `pages`                   |
| `get_page`            | flat      | `page`                    |
| `search_pages`        | flat      | `pages`; supports `response_mode`/`fields` shaping (§5.2, #337); each page carries `score` (term-match count) and `match: "title_exact"` requests a strict full-title match instead of broad term matching (#332) |
| `get_recent_posts`    | flat      | `pages`                   |
| `list_tags`           | flat      | `tags`                    |
| `list_categories`     | flat      | `categories`              |
| `get_sitemap`         | flat      | `entries`                 |
| `get_feed`            | flat      | `items`                   |
| `get_site_information`| flat      | `site`                    |

### `content.read`

| Tool                    | Envelope    | Notes                                        |
|-------------------------|-------------|----------------------------------------------|
| `get_page_markdown`| flat        | `page` + `page.state`                        |
| `get_page_frontmatter`  | flat        | `frontmatter` + `frontmatter.state`          |
| `get_related_content`   | flat        | `related`                                    |
| `build_agent_context`   | flat        | `context` + `context.state`; supports `response_mode`/`max_body_chars` shaping (§5.2, #337) |
| `export_agent_context`  | flat        | `export.pages[*].state`, `export.total`, `export.include_body`; `limit` capped at 10 when `include_body=true` (default), 50 when `include_body=false` (#325) |
| `search_content`        | structured  | `data.pages[*].state`, `data.total`, pagination echo |
| `explain_structure`| structured  | `data.sections`, `data.languages`, `data.summary`, `data.recent_pages[*].state` |
| `get_site_health`       | structured  | `data.score`, `data.status`, counts; `data.taxonomy_inconsistency_details[*]` gives affected page slugs per finding (`data.taxonomy_inconsistencies` string list kept for compat) (#324) |
| `get_broken_links`      | structured  | `data.links`, `data.broken_links`            |
| `get_backlinks`         | structured  | `data.backlinks`, `data.count`               |
| `diff_page`             | structured  | `data` (diff result) + `data.state`          |
| `inspect_rendered` | structured  | `data.checks[*].check/status/detail`, `data.status`, `data.state` |
| `validate_frontmatter` | structured  | `data.pages`, `data.pages_checked`           |
| `validate_site`         | structured  | `data.pages`, `data.pages_checked`           |

### `content.write`

| Tool          | Envelope | Top-level key(s)                            |
|---------------|----------|---------------------------------------------|
| `create_page` | flat     | `status`, `slug`, `path`, `dry_run?`, `content?`, `warning?`      |
| `update_page` | flat     | `status`, `slug`, `dry_run?`, `diff?`, `warning?`                 |
| `delete_page` | flat     | `status`, `slug`, `warning?`                                      |

### `site.admin`

| Tool                      | Envelope | Top-level key(s)                     |
|---------------------------|----------|--------------------------------------|
| `build_site`              | flat     | `status`, `duration_ms`, `build_id`, `output_revision`, `publish_ready` |
| `preview_build`           | flat     | (build result)                       |
| `run_post_build_hooks`    | flat     | (hook result)                        |
| `generate_hero_image` | flat     | `path`                               |
| `check_sri_versions`      | flat     | (SRI result)                         |
| `get_runtime_status`      | structured | `data.server_version`, `data.commit`, `data.hugo`, `data.git`, `data.site`, `data.degraded` |
| `get_theme_status`        | structured | `data.themes[*]`, `data.hugo`         |
| `verify_publication`      | structured | `data.source/build/public/index`, `data.http_status`, `data.status`, `data.explanation` |
| `create_preview`          | flat     | `preview_id`, `url`, `expires_at`, `build` |

---

## 7. New tools (v1.3.8+)

New tools added in v1.3.8 use the **structured envelope** by default.
