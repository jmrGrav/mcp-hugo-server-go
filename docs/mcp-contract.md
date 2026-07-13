# MCP Contract — hugo-public-mcp

This document specifies the observable contract for all tools exposed by the
server: response envelopes, error model, pagination, naming conventions, and
versioning. Agents may use this as a stable reference; deviations are bugs.

---

## 1. Response Envelopes

v1.x now serves a single **canonical structured envelope** for read and
discovery tools, while preserving the legacy top-level result fields as
compatibility aliases. Write/admin tools remain unchanged unless documented
otherwise.

### 1.1 Canonical structured envelope

This is the authoritative shape for read-style tool responses:

```json
{
  "success": true,
  "version": "v1.0.0",
  "generated_at": "2026-07-12T02:30:00Z",
  "meta": {
    "server_version": "v1.0.0",
    "generated_at": "2026-07-12T02:30:00Z"
  },
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
| `meta`         | object   | yes           | Canonical metadata container (`server_version`, `generated_at`) |
| `data`         | object   | yes           | Tool-specific payload                              |
| `warnings`     | string[] | yes           | Non-fatal observations (empty array when none)     |
| `errors`       | object[] | yes           | Structured tool errors or degradations (empty array when none) |

`success: false` means the call produced no usable result. `success: true`
with non-empty `errors` means a partial result was returned.

### 1.2 Legacy top-level aliases

For backward compatibility during v1.x, the server also mirrors legacy result
fields at the top level for tools that historically returned a flat object.
Examples:

```json
{
  "success": true,
  "data": { "page": { ... } },
  "page": { ... }
}
```

```json
{
  "success": true,
  "data": { "pages": [ ... ], "total": 42 },
  "pages": [ ... ],
  "total": 42
}
```

```json
{
  "success": true,
  "data": { "pages": [ ... ], "total": 42 },
  "export": { "pages": [ ... ], "total": 42 },
  "pages": [ ... ],
  "total": 42
}
```

These aliases are compatibility affordances, not the canonical contract. New
clients should read from `data` and `meta`.

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

Structured-envelope tools may include degraded results with structured entries
in `errors[]`.

### Error codes

Protocol-level error messages use a `snake_case_prefix:` convention for
machine-parseable classification:

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
canonical envelope reflects applied pagination in `data.limit`,
`data.offset`, `data.total`, `data.returned_count`, `data.has_more`, and
`data.next_offset`.

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

- `version: "v1.0.0"` and `meta.server_version` refer to the **response schema
  version**, not the release tag or binary version.
- `generated_at` and `meta.generated_at` carry the same timestamp during the
  v1.x compatibility window.
- Individual tool responses do not expose the binary release version.

---

## 6. Tool Inventory

### Anonymous (no auth required)

| Tool                  | Envelope                    | Legacy alias(es)         |
|-----------------------|-----------------------------|--------------------------|
| `list_pages`          | structured + compat aliases | `pages`, pagination keys |
| `get_page`            | structured + compat aliases | `page`                   |
| `search_pages`        | structured + compat aliases | `pages`, pagination keys |
| `get_recent_posts`    | structured + compat aliases | `pages`, pagination keys |
| `list_tags`           | structured + compat aliases | `tags`                   |
| `list_categories`     | structured + compat aliases | `categories`             |
| `get_sitemap`         | structured + compat aliases | `entries`, pagination    |
| `get_feed`            | structured + compat aliases | `items`, pagination      |
| `get_site_information`| structured + compat aliases | `site`                   |

### `content.read`

| Tool                    | Envelope                    | Legacy alias(es)                             |
|-------------------------|-----------------------------|----------------------------------------------|
| `get_full_page_markdown`| structured + compat aliases | `page`                                       |
| `get_page_frontmatter`  | structured + compat aliases | `frontmatter`                                |
| `get_related_content`   | structured + compat aliases | `translations`, `related_pages`, `related`   |
| `build_agent_context`   | structured + compat aliases | `context`                                    |
| `export_agent_context`  | structured + compat aliases | `export`, `pages`, pagination keys           |
| `search_content`        | structured + compat aliases | `pages`, pagination and filter echo          |
| `explain_site_structure`| structured + compat aliases | `sections`, `languages`, `summary`           |
| `get_site_health`       | structured + compat aliases | `score`, `status`, counts                    |
| `get_broken_links`      | structured + compat aliases | `links`, `broken_links`                      |
| `get_backlinks`         | structured + compat aliases | `backlinks`, `count`, `slug`                 |
| `diff_page`             | structured + compat aliases | diff fields mirrored at top level            |
| `validate_front_matter` | structured + compat aliases | `pages`, `pages_checked`, `pages_passed`     |
| `validate_site`         | structured + compat aliases | `pages`, `pages_checked`, `pages_passed`     |

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

## 7. Migration Note

During v1.x:

- `data` and `meta` are canonical for read/discovery tools.
- legacy top-level aliases remain intentionally available for compatibility.
- write/admin tools may still use flat result objects until explicitly migrated.
