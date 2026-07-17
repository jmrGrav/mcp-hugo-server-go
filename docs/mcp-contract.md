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
| `rate_limit_exceeded:` | Per-caller budget exceeded (`create_page`/`update_page`/`upload_page_asset` share one budget, `delete_page` has its own — see §6.3) |

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
section — see #325), `get_page_for_edit` (`include`, a named-section variant
of `fields` — see #339 — plus `max_body_chars`). Additional tools adopt
these parameters incrementally; adding support to a new tool is not a
breaking change since the parameters are optional and additive.

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
| `get_page_for_edit`     | flat        | `page.state`, `page.revision`, `page.quality`; each of `frontmatter`/`markdown`/`state`/`quality` is a pointer field omitted when not requested via `include` or unavailable for the caller's profile; `quality` requires source access and is omitted for `reader` (#339) |
| `list_content_types`    | flat        | `content_types[*]` (`name`, `source`, `archetype_path?`, `expected_fields?`, `page_count?`); `expected_fields` is the union of the archetype's declared keys and keys observed on existing pages of that type; `page_count` and observed-page-derived fields (source-derived) are omitted for `reader`, archetype metadata is not (#347) |
| `list_page_assets`      | flat        | `assets[*]` (`name`, `size_bytes`, `modified_at`); lists the sibling files in a leaf page bundle's directory; `not_a_bundle` for single-file pages; entirely source-derived, so `reader` gets an empty `assets` list for a public page and `content_not_public` for a non-public one (#348) |
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
| `upload_page_asset` | flat | `status`, `slug`, `filename`, `path`, `content_type`, `size_bytes`, `sha256`, `duplicate_of?` (advisory only), `dry_run?`; allowed types png/jpg/jpeg/gif/webp only (SVG deferred, #348); never overwrites (`already_exists`) |

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

## 6.1. Git Trust Model (#379)

The full model — configuration, baseline states, and per-tool wiring — lives
in `docs/git-baseline-model.md`. That document is the design anchor; the
five points below are its normative summary and the ones any future
publish/rollback tool (`#340`) must build on:

1. A write tool commits its change to the content tree, not to Git. Git
   commit/push is out of scope for this server and happens externally.
2. Only a *committed* baseline state (a real `head_commit`) is a valid
   rollback target — never "whatever was on disk before the last write,"
   since that may not correspond to any commit.
3. The local baseline checkout is authoritative; the configured remote is a
   comparison point only, never a sync source.
4. Divergence between local and remote is surfaced as a warning, never
   resolved automatically (no force-push, no auto-merge).
5. Agents can read Git state (`get_runtime_status`, `diff_page`) but cannot
   commit, push, rewrite history, or roll back without an explicit,
   individually-confirmed call naming a target commit.

## 6.2. Transactional Edit Design (#338, #340)

`docs/transactional-edit-design.md` is the design anchor for two future
tools, `plan_content_change`/`apply_content_plan` (#338) and
`publish_changes`/`rollback_change` (#340). **Neither pair is implemented
yet** — this section exists only so the design is discoverable from the
contract doc, per #338/#340's acceptance criteria.

Summary: `plan_content_change` (read-only, `content.read`) previews a small
set of named operations (`update_body`, `add_tag`, ...) against one page,
returning a diff and a short-lived `plan_id` without writing anything.
`apply_content_plan` (`content.write`) re-verifies the plan's pinned
revision and writes exactly what was previewed — it is a deferred,
pre-validated `update_page` call, not a new write path. `publish_changes`/
`rollback_change` sit one layer above (build/publish confirmation, and
rollback to a Git-committed state per [§6.1](#61-git-trust-model-379)) and
remain design-only until the plan/apply foundation exists in production.

## 6.3. Write Input Validation Contract (#380)

`create_page` and `update_page` enforce, in addition to the existing
`content.write` scope check and `pg.SafeJoin` path-traversal guard:

- **Slug format**: `^[a-z0-9]([a-z0-9/_-]*[a-z0-9])?$` — lowercase
  alphanumeric segments joined by `/`, `_`, or `-`. Rejected with
  `invalid_params`. This is a content-convention check layered on top of,
  not instead of, the path-safety check `pg.SafeJoin` already performs.
- **Title**: at most 255 characters (Unicode code points, not bytes).
- **Body**: at most 1MB (bytes).
- **Text sanitization** (title, body, and `update_page`'s `description`):
  null bytes and C0/C1 control characters other than `\n`, `\r`, `\t` are
  rejected with `invalid_params`. Valid multibyte UTF-8 (accents, CJK,
  emoji) is unaffected — only the control-character range is policed.
- **Frontmatter well-formedness**: unchanged from the existing
  `validateFrontmatterRoundTrip` check, which parses the generated
  frontmatter block and rejects malformed/duplicated YAML.

On `update_page`, title/body/description are optional (omitting one leaves
that field unchanged) — these checks only run when the caller actually sets
a value, matching the tool's existing "empty means unchanged" semantics.

These are enforced as runtime Go checks in the tool handlers — this is the
actual security boundary and stays regardless of what the published schema
says. The schema library this server uses
(`github.com/google/jsonschema-go`, via `tools.MustSchema`) does not parse
constraint sub-keys out of Go struct tags — a `jsonschema:"pattern=..."`
tag becomes the field's description text, not a schema constraint — but its
underlying `*jsonschema.Schema` type does support real `pattern`/
`maxLength`/`enum` fields, settable by post-processing the schema after
generation. Publishing these same constraints (plus enum values for other
string parameters across the read surface) in the JSON Schema itself, so a
client rejects an invalid call before sending it rather than after, is
tracked separately — see the schema-constraints issue filed after the
ChatGPT connector audit (2026-07-17). Runtime validation and schema
publication are complementary layers, not alternatives; this issue lands
the runtime layer first because it is the one that cannot be skipped.

## 6.4. Per-Caller Mutation Rate Limits (#378)

Two independent layers protect the write surface:

1. **Per-scope, per-IP, HTTP-layer** (`internal/oauth/ratelimit.go`, pre-existing): every `tools/call` request is throttled by `(caller IP, token scope)`, configured via `rate_limit.content_write_per_min` etc. — a single shared budget across every tool in that scope tier.
2. **Per-tool-class, per-caller, in-process** (`internal/tools/write`, this section): `create_page`, `update_page`, and `upload_page_asset` share one budget, configured via `rate_limit.create_update_per_min` (default 60/min); `delete_page` has its own, separate budget via `rate_limit.destructive_per_min` (default 5/min, unchanged from before this issue). These are independent of each other and independent of the layer-1 limit above — exhausting one never blocks the other.

Both layers key on caller IP (the only caller identity currently available in tool-handler context); a true per-`client_id` budget would need OAuth `client_id` propagated into context, which is a larger change tracked separately.

A budget-exceeded call returns `rate_limit_exceeded: <tool> is limited to N per minute`.

## 6.5. Read-Only Tool Path/Content Leakage Audit (#376)

Follow-up to #334 (logical path exposure) and #354 (reader-safe response
policy). Audited every anonymous, `content.read`, and read-only `site.admin`
tool (`get_runtime_status`, `check_sri_versions`, `get_theme_status`,
`verify_publication`) for two failure modes: absolute host filesystem paths
leaking into any response field (including `warnings`/`meta`), and
source-only/non-public content (drafts, future posts, expired posts) being
returned to reader-scoped callers.

**Result: no leaks found in any read-only tool.** The existing sanitizers
from #334/#354 already cover the full surface:

- `fileutil.LogicalContentPath` (#334) relativizes every source path exposed
  in read-tool responses against `content_root`, and returns `""` rather
  than falling back to a raw absolute path if relativization fails.
- `site.ReaderSafeResolvedPage` / `readerSafeResolvedPage` (#354) reject
  reader-scoped calls against any page with no public counterpart
  (`content_not_public`), and `sourceIndexForProfile` nulls the source index
  entirely for the reader profile — draft/future/expired filtering falls out
  of this for free, since `Public` is only ever populated from Hugo's
  already-filtered `public/` build output.
- Admin diagnostic output (git errors, build stderr) is separately sanitized
  via `sanitiseStderr`/`sanitiseGitError` (`internal/tools/admin/build.go`,
  `internal/tools/admin/runtime_status.go`), which string-replace the
  configured `hugo_root`/`site_root`/git-resolved-root before truncating.

**Automated regression test**: `internal/contracttests/path_leak_audit_test.go`
(`TestAuditAnonymousAndReadToolsNeverLeakAbsolutePaths`) calls every
anonymous/`content.read`/read-only-`site.admin` tool (including `diff_page`'s
expected-error path) against a fixture config with real absolute
`site_root`/`content_root` paths, and asserts the full JSON response body
never contains those exact paths nor any string matching common deployment
path prefixes (`/home/`, `/root/`, `/var/{www,lib,opt}/`, `/srv/`, `/opt/`,
`/etc/`, `/runner/`). This runs as part of the normal `go test ./...` suite,
so a future regression fails CI rather than requiring manual re-audit.

**Intentional exception, documented rather than filed as a new issue**:
`generate_hero_image`'s success/error responses (`internal/tools/admin/image.go`)
and `build_site`'s preflight error (`internal/tools/admin/build.go`) return
an absolute path (`hugo_root`-derived write target / preflight directory).
Both are `site.admin`-only, mutating tools, out of this audit's read-only
scope — and the caller is the same operator who configured `hugo_root` in
the first place, so this crosses no trust boundary the way a reader-scoped
leak would.

## 7. New tools (v1.3.8+)

New tools added in v1.3.8 use the **structured envelope** by default.
