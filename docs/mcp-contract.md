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
| `rate_limit_exceeded:` | Per-caller budget exceeded (`create_page`/`update_page`/`upload_page_asset` share one budget, `delete_page`/`delete_page_asset` share their own, separate one — see §6.3) |
| `asset_referenced:`  | `delete_page_asset`'s filename is still linked from the page's own body; pass `force=true` to delete anyway (#460) |

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

### `read` (ungated — no scope required, see [§6.12](#612-2-scope-model-readwrite-450))

Per [§6.12](#612-2-scope-model-readwrite-450), these tools require
`RequiredScope: ""` — they are fully public, identical in gating to the
Anonymous tier above. The per-tool notes below that once described
reader-safe restrictions (`quality` omitted, `page_count` omitted, empty
`assets` list, `content_not_public`) described the pre-#450 `reader` profile
and no longer apply to any live caller: any caller now sees full source
content, including drafts, for every tool in this table.

| Tool                    | Envelope    | Notes                                        |
|-------------------------|-------------|----------------------------------------------|
| `get_page_markdown`| flat        | `page` + `page.state`                        |
| `get_page_frontmatter`  | flat        | `frontmatter` + `frontmatter.state`          |
| `get_related_content`   | flat        | `related`; `related_pages` is canonical, `related` is a deprecated alias always identical to it, kept pending #433's live-client-verification question (#453); when `related_pages` is empty, `empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458) |
| `build_agent_context`   | flat        | `context` + `context.state`; supports `response_mode`/`max_body_chars` shaping (§5.2, #337) |
| `export_agent_context`  | flat        | `export.pages[*].state`, `export.total`, `export.include_body`; `limit` capped at 10 when `include_body=true` (default), 50 when `include_body=false` (#325) |
| `get_page_for_edit`     | flat        | `page.state`, `page.revision`, `page.quality`; each of `frontmatter`/`markdown`/`state`/`quality` is a pointer field omitted when not requested via `include` (#339); `page.backlinks` is a fifth, opt-in-only `include` value (identical data to a standalone `get_backlinks` call) — never part of the default bundle when `include` is omitted (#465) |
| `list_content_types`    | flat        | `content_types[*]` (`name`, `source`, `archetype_path?`, `expected_fields?`, `page_count?`); `expected_fields` is the union of the archetype's declared keys and keys observed on existing pages of that type (#347); `special_files[*]` (`kind: "section_index"`, `section`, `languages[]`) surfaces Hugo `_index`/`_index.<lang>.md` files separately — they are structural, not creatable content types; `section: ""` means the site's root/home index, not a missing value (#457) |
| `list_page_assets`      | flat        | `assets[*]` (`name`, `size_bytes`, `modified_at`); lists the sibling files in a leaf page bundle's directory; `not_a_bundle` for single-file pages (#348) |
| `search_content`        | structured  | `data.pages[*].state`, `data.total`, pagination echo |
| `explain_structure`| structured  | `data.sections`, `data.languages`, `data.summary`, `data.recent_pages[*].state`; a non-default-language page's route prefix (e.g. `en` in `/en/posts/foo/`) is stripped before section counting and only ever surfaced via `data.languages`, never as a `data.sections[*].name` (#459) |
| `get_site_health`       | structured  | `data.score`, `data.status`, counts; `data.score_breakdown` explains the score per category, `data.taxonomy_inconsistency_details[*].severity` explains per finding (#419); `data.taxonomy_inconsistency_details[*]` gives affected page slugs per finding (`data.taxonomy_inconsistencies` string list kept for compat) (#324) |
| `get_broken_links`      | structured  | `data.links`, `data.broken_links`            |
| `get_backlinks`         | structured  | `data.backlinks`, `data.count`               |
| `suggest_links`         | structured  | `data.suggested_links` is canonical, `data.suggestions` is a deprecated alias always identical to it, kept pending #433's live-client-verification question (#453); when `data.suggested_links` is empty, `data.empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458) |
| `diff_page`             | structured  | `data` (diff result) + `data.state`          |
| `inspect_rendered` | structured  | `data.checks[*].check/status/detail`, `data.status`, `data.state` |
| `validate_frontmatter` | structured  | `data.pages`, `data.pages_checked`           |
| `validate_site`         | structured  | `data.pages`, `data.pages_checked`; defaults to invalid-only (`data.pages` omits passing pages unless `include_valid=true` or `invalid_only=false` is passed explicitly) — `data.pages_checked`/`data.pages_passed`/`data.invalid` always describe the full scan regardless (#456) |

### `write` (requires a registered OAuth client, see [§6.12](#612-2-scope-model-readwrite-450))

Per [§6.12](#612-2-scope-model-readwrite-450), the tools formerly split
between `content.write` and `site.admin` are now a single `write` scope with
no exceptions — `write` implies full `read` access plus everything below.

| Tool          | Envelope | Top-level key(s)                            |
|---------------|----------|---------------------------------------------|
| `create_page` | flat     | `status`, `slug`, `path`, `dry_run?`, `content?`, `warning?`; `resolved_lang`/`resolved_source_path` are omitted (not empty-stringed) unless resolution actually succeeded; on failure, `request_context` (`slug`, `requested_lang?`) always echoes the caller's normalized input (#455); on success (non-dry-run), `new_revision` is the resulting page's revision, usable directly as `expected_revision` on a following `update_page`/`delete_page` without an intermediate read (#464); `rate_limit_remaining` is always present on success, reporting the caller's remaining budget on the shared create/update/upload quota (#466) |
| `update_page` | flat     | `status`, `slug`, `dry_run?`, `diff?`, `warning?`; same `resolved_lang`/`resolved_source_path`/`request_context` failure-path contract as `create_page` (#455); same `new_revision` success-path contract as `create_page` (#464); same `rate_limit_remaining` contract as `create_page` (#466) |
| `delete_page` | flat     | `status`, `slug`, `warning?`; same `resolved_lang`/`resolved_source_path`/`request_context` failure-path contract as `create_page` (#455); `rate_limit_remaining` reports the caller's remaining budget on `delete_page`'s own, separate quota (#466) |
| `upload_page_asset` | flat | `status`, `slug`, `filename`, `path`, `content_type`, `size_bytes`, `sha256`, `duplicate_of?` (advisory only), `dry_run?`; allowed types png/jpg/jpeg/gif/webp only (SVG deferred, #348); never overwrites (`already_exists`); `rate_limit_remaining` reports the caller's remaining budget on the shared create/update/upload quota (#466) |
| `delete_page_asset` | flat | `status`, `slug`, `filename`, `sha256`, `dry_run?`, `referenced?` (pointer — present as `false` on success, omitted on error, so "not referenced" and "never checked" stay distinguishable), `referenced_in?`; requires `expected_sha256` or `expected_revision` on non-dry-run calls (a mismatch fails `revision_conflict`); fails `asset_referenced` if the filename is still linked from the page body, unless `force=true`; `dry_run` previews `sha256`/`referenced` without requiring the concurrency guard or deleting anything; `rate_limit_remaining` reports the caller's remaining budget on `delete_page`'s own destructive quota (#460). Only removes the source asset — unlike `delete_page`, it does not purge any built public copy or CDN cache; the asset stays reachable at its old URL until the next build |
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
2. **Per-tool-class, per-caller, in-process** (`internal/tools/write`, this section): `create_page`, `update_page`, and `upload_page_asset` share one budget, configured via `rate_limit.create_update_per_min` (default 60/min); `delete_page` and `delete_page_asset` share their own, separate budget via `rate_limit.destructive_per_min` (default 5/min, unchanged from before this issue). These are independent of each other and independent of the layer-1 limit above — exhausting one never blocks the other.

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

## 6.6. Structured Error Recovery Hints (#428)

Beyond `code`/`message`/`field`/`retryable`, `errors[*].resolution` (when
present) tells an agent concretely how to recover, not just what failed:

```json
"resolution": {
  "action": "reread_then_retry",
  "parameter": "expected_revision",
  "recommended_tool": "get_page_for_edit"
}
```

Populated in `toolcontract.ParseToolError` for `ambiguous_language`,
`invalid_params`/`missing_required_parameter`, `build_in_progress`,
`rate_limit_exceeded`, `revision_conflict`, and `content_not_found`. Not
every error code carries a `resolution` — absence means there's no more
specific recovery action than "read `message` and adjust."

### Per-code resolution audit (#461)

Every tool-facing error code, whether it carries a `resolution` and why:

| Code | Resolution? | Reasoning |
|---|---|---|
| `ambiguous_language` | yes | `retry_with_parameter` on `lang`, with `allowed_values` |
| `missing_required_parameter` | yes | `retry_with_parameter` on the missing field; `expected_revision` specifically recommends `get_page_for_edit` (its own message shape — "expected_revision is required for non-dry-run update_page/delete_page" — is matched separately from the generic "X must not be empty" pattern) |
| `invalid_params` (other) | yes | `retry_with_parameter`, with `field`/`allowed_values` inferred from the message where possible |
| `build_in_progress`, `rate_limit_exceeded` | yes | `retry_later`; `rate_limit_exceeded` additionally carries `resolution.retry_after_seconds` (a concrete wait time parsed from the message), `build_in_progress` does not (#466) |
| `revision_conflict` | yes | `reread_then_retry` via `get_page_for_edit`, except `delete_page_asset`'s own "asset changed" message, which recommends `list_page_assets` instead (#460) — `get_page_for_edit` doesn't return an asset's hash |
| `content_not_found`, `not_found` | yes | `search_then_retry` via `search_pages` — both mean the named slug doesn't resolve |
| `already_exists` | conditional | `use_different_tool` → `update_page`, but only for `create_page`'s own "page already exists" message; `upload_page_asset`'s "asset already exists" message deliberately gets no hint, since there's no update path for an existing asset by design |
| `asset_referenced` | yes | `retry_with_parameter` on `force` — `delete_page_asset`'s guard against deleting a still-linked asset is retryable via the documented override, not a caller mistake to fix by changing input shape (#460) |
| `content_not_public` | no (deliberate) | overloaded across two meanings in this codebase — a draft the caller's profile can't see, vs. a diagnostics sub-feature unavailable to the reader profile. Only the first would benefit from "search again"; a single static hint would misguide the second, so neither gets one |
| `not_a_bundle`, `build_precondition_failed`, `idempotency_conflict`, `validation_error`, `security_error` | no | caller-input-adjacent, but the fix is specific to the message text (e.g. which validation rule failed) in a way a single static action can't generalize |
| `internal_error`, `read_error`, `write_error`, `delete_error`, `parse_error`, `scan_error`, `render_output_unavailable`, `git_metadata_unavailable`, `config_error`, `fetch_error`, `image_api_error`, `request_error`, `build_error` | no | opaque server-side faults, not caused by caller input — there's nothing for the caller to change, only something to report or retry blindly |

Out of scope for this table: `server`/`server_error`-prefixed errors from
`internal/server` (process startup/config validation) and `internal/oauth`
(the `/token`/`/register` HTTP endpoints) never reach `ParseToolError` at
all — they're outside the MCP tool-call error path entirely, not a tool
response code an agent would ever see.

## 6.7. Published Schema Constraints (#418)

`tools.MustSchema[T]()` (via `github.com/google/jsonschema-go`) infers a
schema from Go struct types but does not parse constraint sub-keys out of
`jsonschema:"..."` tags — the tag becomes description text, not a real
`enum`/`maximum`. Where a field only accepts a small fixed set of
values, or a pagination `limit` has a real enforced ceiling, the tool
registration post-processes the inferred schema with `tools.WithEnum`/
`tools.WithMaxLimit` so a well-behaved client discovers the constraint from
`tools/list` instead of learning it from a runtime rejection.

Note: only `maximum` is published for `limit`, never `minimum`. Every
paginated tool's `clampLimit(v, defaultVal, maxVal)` treats any `v <= 0`
(including `0` itself) as "use the default", not as an error — a real,
currently-accepted request shape. Publishing `minimum: 1` would make the SDK
reject `limit: 0` before the handler runs, breaking that existing behavior.

**Tradeoff, by design**: once a field carries a schema constraint, the MCP
SDK's own request validation rejects an out-of-range value *before* the
tool handler runs, returning a plain-text validation error rather than this
server's structured envelope. A conforming client that reads the schema
never hits this path; a non-conforming client that ignores it gets a less
structured, but still clearly rejected, response instead of the server
silently rewriting its request (e.g. `list_pages(limit: 250)` used to
silently return `limit: 50` with no indication anything changed — it now
rejects the call outright, with the correct ceiling visible in the schema
beforehand).

Applied to: `search_pages.match` (`enum: ["", "any", "title_exact"]`),
`search_pages.response_mode`/`build_agent_context.response_mode`
(`enum: ["", "standard", "compact"]` — `"full"`/`"ids_only"` are deliberately
excluded as reserved-but-unimplemented vocabulary, #337), and `limit` on
every paginated anonymous/content.read tool (`maximum` matches that tool's
actual `clampLimit` ceiling).

**Deliberately not applied**: `search_content.type` accepts its values
case-insensitively at runtime (`post`/`Post`/`POST` all work) — a schema
`enum` can only match exact strings, so publishing one would newly reject
mixed-case values the handler currently accepts. Left unconstrained pending
a decision on whether to normalize case at the schema layer or keep runtime
leniency; already validated at runtime with a clear `invalid_params` error,
so this is a smaller gap than the ones this issue fixes. `validate_site`/
`validate_frontmatter`'s `limit` has no enforced ceiling by design (omitting
it returns the full scan) and so publishes no `maximum`.

`internal/contracttests/schema_constraints_test.go` asserts the published
enum/range for each of the above matches what the runtime actually accepts,
so schema and validation can't silently drift apart again.

## 6.8. `get_site_health` Score Breakdown and Finding Severity (#419)

A live connector audit (ChatGPT, 2026-07-17) found `get_site_health` could
report `score: 100, status: "healthy"` while `taxonomy_inconsistencies`
still listed a finding — an agent had no way to tell *why* a listed finding
didn't move the score short of re-deriving the server's internal scoring
logic.

Two additive fields. `score`/`status` are byte-for-byte the same formula as
before #419 for every input — this is presentation only, not a scoring
algorithm change (per the issue's own scope note):

- Each entry in `taxonomy_inconsistency_details[*]` now carries a
  `severity`: `"info"` (`translation_pair` — the site's own localization,
  never counted as an issue) or `"warning"` (`alias_mismatch`/
  `possible_duplicate` — counted as an issue, but still never penalizes the
  top-level `score`, exactly as before #419).
- `score_breakdown` gives a per-category `{score, weight, issues,
  advisories?}`. `weight` is each category's actual share of the top-level
  `score`, not a decorative number: `frontmatter` carries weight 100 (it's
  the only category the formula has ever penalized — `frontmatter.score`
  always equals the top-level `score`) and `taxonomy` carries weight 0
  (`taxonomy.score` is informational, a local per-finding penalty shown for
  reference, and never feeds into the top-level `score`).

`score_breakdown` deliberately covers only `frontmatter` and `taxonomy` —
the two categories this server computes a real signal for today. It omits
`links`/`rendering`/`publication` placeholders an earlier proposal sketched;
publishing a fabricated 100 for a category with no underlying check would
be more misleading than omitting it.

No behavior change: `score`/`status` are identical to pre-#419 for every
input, including sites with `alias_mismatch`/`possible_duplicate` findings.

## 6.9. `verify_publication` Bounded Wait (#421)

Extends the existing `verify_publication` tool with an optional
`wait_seconds` rather than inventing a parallel polling mechanism — a
mutation's `expected_revision` is already sufficient to identify what a
caller is waiting for, so no new `mutation_id` concept was added.

- Omitted or `0`: unchanged — a single point-in-time check, exactly as
  before #421.
- A positive value: the tool polls the *local* source/build/public/index
  state (disk mtimes/presence — no network) internally and returns as soon
  as that local state settles, or once the wait budget is exhausted with
  whatever state it has by then. Exactly one outbound HTTP probe is made,
  at the end, regardless of how many local-state ticks the wait took —
  never once per tick, since that could push a "20s" wait to ~30s
  wall-clock on a slow host (bounded by `verifyPublicationHTTPTimeout`,
  10s) and would otherwise fire dozens of GETs at the live site for no
  benefit. Clamped server-side to a small maximum (currently 20s) so this
  can never become a long-held connection. The response always echoes the
  actual (clamped) budget in `data.wait_seconds`, so a caller who requested
  more than the maximum can see it was capped.
- Scope limit: the in-memory site index is a snapshot from server
  startup/last reindex, not a live filesystem view — a page the index
  hasn't picked up at all (e.g. a brand-new page) cannot resolve mid-wait
  no matter how long `wait_seconds` runs; it fails fast with
  `content_not_found` instead. `wait_seconds` smooths the "an edit to a
  page the index already knows about is catching up with a build" lag, not
  "wait for the index to notice a brand-new page."

Does not overlap with the `docs/transactional-edit-design.md` proposal:
that `publish_changes` concept is a full confirmation gate for the
build/publish step itself; this is narrower — making the existing
post-write settle time observable in one call instead of several, and
applies today without depending on that design.

## 6.10. CORS on `/register`, `/authorize`, `/token` (browser-based OAuth clients)

Found live (Mistral Le Chat, 2026-07-18): these three endpoints had no CORS
support at all — an OPTIONS preflight got a plain 405 with no
`Access-Control-Allow-Origin`. A browser-based OAuth client calling one of
them directly via `fetch()`/XHR (not just navigating to `/authorize`) would
have its preflight rejected and the browser would block the real request
before it ever reached this server — surfacing to the client as a generic
connection failure, with nothing in this server's own request logs to
explain it (confirmed: zero origin log entries for the failed attempt).

All three now respond to `OPTIONS` with `204` and
`Access-Control-Allow-Origin: *` (matching the existing policy on discovery
endpoints — these are public metadata/registration surfaces, not
authenticated data, so there's no per-origin access control to enforce
here), and the *real* GET/POST responses carry the same header too — a
passing preflight alone isn't sufficient for a browser to let client-side
JS read the actual response.

## 6.11. Scope Resolution Skips Unrecognized Tokens (#449)

`requestedScope` (internal/oauth/scope_config.go) resolves a request's
space-delimited `scope` parameter to the single highest-ranked recognized
scope. Per RFC 6749 §3.3, a token that doesn't normalize is now skipped, not
fatal — the request still resolves using whatever valid tokens remain,
erroring only if *every* token is unrecognized. Follow-up on the 2026-07-18
"reader" scope outage (#448, §6.10's neighbor): that outage was one specific
unrecognized token causing an otherwise-valid request to fail outright; this
generalizes the fix so `scopes_supported` gaining a new value a client echoes
back doesn't cause the same class of outage before `normalizeConfiguredScope`
is updated to match it.

## 6.12. 2-Scope Model: `read`/`write` (#450)

Collapses the pre-#450 4-tier scope model (`reader`, `content.read`,
`content.write`, `site.admin`) down to exactly two scopes:

- **`read`** — full visibility, **including drafts and other
  source-only/pre-publication content**. This is an explicit operator
  risk-acceptance decision, not an oversight: drafts are short-lived
  pre-publication content in this operator's workflow, and the prior
  `reader`-safe restriction (public-only, no drafts) was judged to be
  unnecessary risk for read-only access. `read` requires no secret and is
  auto-registrable — the same self-service mechanism the old `reader`
  profile used (`AllowReaderSelfRegistration`).
- **`write`** — requires a registered OAuth client (`client_id` +
  `client_secret` in `oauth-clients.yaml` or the equivalent SQLite-backed
  registry), same as before #450. `write` **implies `read`**: a `write`
  token gets everything a `read` token gets, plus every mutating and
  operational tool. All 9 tools that used to require `site.admin`
  (`build_site`, `preview_build`, `run_post_build_hooks`,
  `generate_hero_image`, `check_sri_versions`, `get_runtime_status`,
  `get_theme_status`, `verify_publication`, `create_preview`) now fold into
  `write` with **no exceptions** — there is no longer a way to get write
  access to content without also getting the operational tools, or vice
  versa.

`tools.KnownScopes` is now `{"read", "write"}`; `tools.ScopeRank` gives
`read` the same rank (0) as anonymous — capability-identical, matching the
"no gate" decision above — and `write` rank 1 (now the top rank).
`tools.IsWriteScope` (renamed from `IsAdminScope`) reports whether a scope
carries write privileges.

**Backward compatibility**: every scope string from the pre-#450 model,
plus the original `mcp` legacy alias, is still accepted — resolved via
`oauth.CanonicalScope`, which is now the single source of truth for scope
aliasing at both config time (client registry, `/authorize` requests) and
request time (bearer token validation):

| Legacy string                                                  | Canonical |
|------------------------------------------------------------------|-----------|
| `mcp`, `read`, `content.read`, `reader`                           | `read`    |
| `write`, `content.write`, `site.admin`, `site_admin`, `siteadmin`, `system.admin`, `admin`, `system_admin`, `systemadmin` | `write`   |

This mirrors the existing `mcp` legacy-alias pattern (§6.11): already-issued
access tokens (up to `AccessTokenTTLSeconds` old) and OAuth clients with a
stale cached copy of `scopes_supported` may present these old strings for a
while after this migration ships, and rejecting them outright would repeat
the exact "reader" outage class from #448/#449 — a request or token carrying
a scope string the server no longer advertises must still resolve, not
fail. `scopes_supported` in discovery documents only ever advertises the
current canonical `["read", "write"]`; the table above is accepted on input
but never re-advertised.

**Dormant machinery, intentionally left in place**: `site.IsReaderProfile`,
`site.ReaderSafeResolvedPage`, and `site.AccessProfileReader` (the
reader-safe response-stripping logic from #354) remain in the codebase
untouched. They are simply never triggered anymore, since no scope value
the server issues or accepts will ever equal the literal string `"reader"`
again (it is resolved to `"read"` by `CanonicalScope` before reaching any
code that checks the access profile). This is intentional dead code, not an
oversight — removing it is out of scope for #450.

## 6.13. Last Build Status Surfaced Proactively (#467)

`get_runtime_status` now includes an optional `last_build` field reporting
the outcome of the most recent `build_site` attempt in this process:

```json
{
  "last_build": {
    "status": "failed",
    "error_class": "permission_denied",
    "at": "2026-07-18T04:45:20Z"
  }
}
```

`last_build` is omitted entirely until `build_site` has been called at least
once in this process's lifetime (there is nothing to report yet — a restart
clears this state, since it's in-memory and process-lifetime only). When the
last attempt failed, the same summary is also appended to `degraded`.

`create_page` and `update_page` responses carry a lightweight `warning`
advisory (never a hard failure — the write itself still succeeds) when the
last known `build_site` attempt failed, so an agent notices a broken publish
pipeline from the write call itself instead of only discovering it by
calling `build_site` at the end of a write cycle:

```
"the last build_site attempt failed (permission_denied) — this write
succeeded but may not go live until build_site is retried"
```

If a write's own DB-sync warning is also present, both are combined into one
`warning` string rather than one silently overwriting the other.

## 7. New tools (v1.3.8+)

New tools added in v1.3.8 use the **structured envelope** by default.
