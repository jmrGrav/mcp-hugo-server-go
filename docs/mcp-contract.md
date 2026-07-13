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
- The server version is not included in individual tool responses; it is
  available via the MCP server metadata and `get_site_health`.
- Flat envelope tools do not carry a `version` field; their schema is
  implicitly v1.

---

## 6. Tool Inventory

### Anonymous (no auth required)

| Tool                  | Envelope  | Top-level key(s)          |
|-----------------------|-----------|---------------------------|
| `list_pages`          | flat      | `pages`                   |
| `get_page`            | flat      | `page`                    |
| `search_pages`        | flat      | `pages`                   |
| `get_recent_posts`    | flat      | `pages`                   |
| `list_tags`           | flat      | `tags`                    |
| `list_categories`     | flat      | `categories`              |
| `get_sitemap`         | flat      | `entries`                 |
| `get_feed`            | flat      | `items`                   |
| `get_site_information`| flat      | `site`                    |

### `content.read`

| Tool                    | Envelope    | Notes                                        |
|-------------------------|-------------|----------------------------------------------|
| `get_full_page_markdown`| flat        | `page` + `page.state`                        |
| `get_page_frontmatter`  | flat        | `frontmatter` + `frontmatter.state`          |
| `get_related_content`   | flat        | `related`                                    |
| `build_agent_context`   | flat        | `context` + `context.state`                  |
| `export_agent_context`  | flat        | `export.pages[*].state`, `export.total`      |
| `search_content`        | structured  | `data.pages[*].state`, `data.total`, pagination echo |
| `explain_site_structure`| structured  | `data.sections`, `data.languages`, `data.summary`, `data.recent_pages[*].state` |
| `get_site_health`       | structured  | `data.score`, `data.status`, counts          |
| `get_broken_links`      | structured  | `data.links`, `data.broken_links`            |
| `get_backlinks`         | structured  | `data.backlinks`, `data.count`               |
| `diff_page`             | structured  | `data` (diff result) + `data.state`          |
| `validate_front_matter` | structured  | `data.pages`, `data.pages_checked`           |
| `validate_site`         | structured  | `data.pages`, `data.pages_checked`           |

### `content.write`

| Tool          | Envelope | Top-level key(s)                            |
|---------------|----------|---------------------------------------------|
| `create_page` | flat     | `slug`, `path`, `dry_run?`, `content?`      |
| `update_page` | flat     | `slug`, `dry_run?`, `diff?`                 |
| `delete_page` | flat     | `slug`                                      |

### `site.admin`

| Tool                      | Envelope | Top-level key(s)                     |
|---------------------------|----------|--------------------------------------|
| `build_site`              | flat     | `status`, `duration_ms`              |
| `preview_build`           | flat     | (build result)                       |
| `run_post_build_hooks`    | flat     | (hook result)                        |
| `generate_featured_image` | flat     | `path`                               |
| `check_sri_versions`      | flat     | (SRI result)                         |

---

## 7. New tools (v1.3.8+)

New tools added in v1.3.8 use the **structured envelope** by default.
