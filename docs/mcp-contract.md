# MCP Contract — hugo-public-mcp

Issue note for `#520` (shipped `v1.5.7`):

Through `v1.5.6`, successful **write/mutation** tools mirrored their whole
payload at the root in addition to `data`, as a transitional v1.x
compatibility shape. As of `v1.5.7`, that root/data payload duplication is
removed: `create_page`/`update_page`/`upload_page_asset`/`delete_page`/
`delete_page_asset` success responses now expose their payload only via
`data.*`, matching the read tools. This reverses `v1.5.6`'s changelog note
that #520 was "deferred to v1.6.0" — the maintainer decided to ship it as a
breaking patch release instead of waiting for a major version.
`request_context` (error-path only, #455) and `rate_limit_remaining`
(#466/#510/#522) remain as deliberately kept root fields — see
[§1.1](#11-flat-envelope) for why those two survive the convergence.

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

Every tool response — "flat" and "structured" alike — carries the same
`success`/`data`/`errors`/`warnings`/`meta` envelope described in
[Section 1.2](#12-structured-envelope). "Flat" does **not** mean the envelope
is skipped; it means the tool's payload is *also* mirrored as top-level
convenience field(s), in addition to `data.X`, using the natural noun for
that tool:

```json
{
  "pages": [ ... ],
  "total": 42,
  "success": true,
  "data": { "pages": [ ... ], "total": 42 },
  "errors": [],
  "warnings": [],
  "meta": { "generated_at": "...", "release_version": "...", "commit": "...", "build_channel": "...", "schema_version": "v1.0.0" }
}
```

A "structured" tool (Section 1.2) omits the top-level `pages`/`total`
duplication and exposes the payload only via `data.pages`/`data.total`. Both
shapes always carry `success`/`errors`/`warnings`/`meta` — that part of the
contract does not vary. #433 removed this top-level duplication from 9
anonymous tools; #495 removed it from the remaining read tools that still
had it. As of #495, no read or anonymous tool duplicates `data.X` at the top
level. As of `v1.5.2`, the write/mutation tools no longer use the older
`data:{}` placeholder convention (#508): their canonical payload is now
present under `data`. Through `v1.5.6`, successful write responses also
mirrored those same payload fields at the root as a transitional v1.x
compatibility shape; as of `v1.5.7` (#520), that mirroring is removed. That
means:

- **read tools**: canonical `data.*` only
- **write success responses**: canonical `data.*` only, as of `v1.5.7` — no
  root mirroring
- **write error responses**: canonical `data.*` plus two deliberately kept
  root fields: `request_context` (echoes the caller's normalized input on
  failure, #455 — meaningless on success, so it never appears there) and
  `rate_limit_remaining` (#466/#510/#522 — kept on both success and error so
  an agent can self-regulate pacing from the root alone, without inspecting
  `data` on every call)

`create_page`/`update_page`/`upload_page_asset`/`delete_page`/
`delete_page_asset`'s `slug` field on success is the canonical public
`/posts/x/` form, matching read tools (#554, shipped `v1.5.6`), not the raw
source-relative input. `source_key` (added in v1.5.4, #545) remains the
stable source-relative identifier — callers that previously reused a write
tool's returned `slug` as another write tool's `slug` input should switch to
`source_key` for that purpose.

### 1.2 Structured envelope

Used by tools that need richer output: diagnostics, pagination metadata,
partial-success signalling, or forward-compatible extension.

```json
{
  "success": true,
  "generated_at": "2026-07-12T02:30:00Z",
  "data": { ... },
  "warnings": [],
  "errors": [],
  "meta": {
    "generated_at": "2026-07-12T02:30:00Z",
    "release_version": "v1.5.1",
    "commit": "50cbc9fe4217",
    "build_channel": "release",
    "schema_version": "v1.0.0"
  }
}
```

Fields:

| Field          | Type     | Always present | Notes                                              |
|----------------|----------|---------------|----------------------------------------------------|
| `success`      | bool     | yes           | `true` even when `errors` is non-empty if partial results are returned |
| `generated_at` | string   | yes           | RFC 3339 UTC timestamp; duplicates `meta.generated_at` at the root for convenience |
| `data`         | object   | yes           | Tool-specific payload — the sole location for a tool's fields; no top-level duplicates (#433) |
| `warnings`     | string[] | yes           | Non-fatal observations (empty array when none)     |
| `errors`       | string[] | yes           | Problems that degraded the result (empty array when none) |
| `meta`         | object   | yes           | `generated_at`, `release_version` (deployed build identifier — is the release tag itself on a release build, `main-<sha>` otherwise), `commit`, `build_channel`, `schema_version` (this envelope's shape version, currently `"v1.0.0"`) — see [§5](#5-versioning) |

A root-level `version` field existed through v1.4.x but was removed (#454):
its name was ambiguous (it actually meant the schema version, not the server
version, but read like it could mean either) and it duplicated information
now available unambiguously at `meta.schema_version`.

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

### Transport-level error flag

The Go MCP SDK also carries a transport-level boolean on `CallToolResult`
named `IsError`. That flag is **not** part of the canonical JSON payload
documented in this contract. Clients that inspect raw MCP transport objects
may see it, but JSON callers should rely on the structured envelope instead:

- `success: false`
- non-empty `errors`

The server must not mirror `IsError` into the JSON body as a separate
`is_error` field.

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
Default and maximum limits vary per tool (see tool descriptions). Pagination
is always reflected at `data.limit`, `data.offset`, and `data.total` — no
tool has a top-level duplicate of these fields as of #495.

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

### Source key format

Where a tool also exposes source-oriented identity, it uses `source_key`:

- `slug` = canonical public route, for example `/posts/hello-world/`
- `source_key` = canonical source-relative Hugo content key, for example
  `posts/hello-world`

`source_key` never has leading/trailing slashes and never includes the
`content/` prefix or a concrete source filename such as `index.md`,
`index.fr.md`, or `hello.md`. It is the stable value to compare across
write tools and other source-aware workflows.

### Date format

All dates are ISO 8601 / RFC 3339. Date-only values use `YYYY-MM-DD`.
Full timestamps use `YYYY-MM-DDTHH:MM:SSZ` (UTC).

---

## 5. Versioning

- `meta.schema_version: "v1.0.0"` refers to the **response schema version**,
  not the server version. Through v1.4.x this lived at a root-level
  `version` field instead; it moved under `meta` (#454) because the old
  name was ambiguous — it read like it could mean either the schema or the
  server version, and the two now live at unambiguous, adjacent names.
- The deployed server version is carried in `meta.release_version` inside
  structured tool responses. On a release build this *is* the named
  product release (for example `v1.5.8`); on a mainline build with no
  explicit release identity it's `main-<sha>` — always populated either
  way, never empty.
- This field's name has moved twice: `release_version` (v1.5.5, #550) →
  `server_version` (v1.5.7, #560, merging two overlapping fields into one)
  → `release_version` (v1.5.8, #563, renamed back at explicit maintainer
  request). The value and semantics have been stable since v1.5.7 — only
  the name changed. `meta.build_channel` still tells apart a release build
  (`build_channel == "release"`, `release_version` is the release tag)
  from a mainline one (`build_channel == "main"`, `release_version` is
  `main-<sha>`).
- **`release_version` is frozen as of v1.5.8.** This is the field's fourth
  name/shape change in four releases (v1.5.5 add → v1.5.6/v1.5.7 merge →
  v1.5.8 rename back), and that churn was itself flagged as a contract-
  stability problem by an external client audit. The name and semantics
  described above will not change again without a major version bump —
  clients should key on `release_version` going forward.
- Production deploys always run from `main` and are tagged only
  afterward, once the deployment is live and verified (see
  `.github/workflows/release.yml`'s ancestry check) — so a deploy must be
  told which release it belongs to explicitly, via the `release_version`
  input to `.github/workflows/deploy.yml`, rather than deriving it from a
  tag that doesn't exist yet. That workflow input name is unchanged across
  all three field-name changes above; it feeds `meta.release_version` and
  `meta.build_channel` directly. A deploy triggered without that input (or
  targeting a ref that isn't the intended release commit) reports
  `meta.release_version = "main-<sha>"`, `meta.build_channel = "main"`.
- `meta.commit` is the VCS revision embedded by Go's build info.
- `meta.build_channel` identifies the deployment line (for example
  `release`, `main`, `staging`).
- Flat envelope tools do not carry either version field; their schema is
  implicitly v1.
- `meta.release_version` and the MCP `initialize` response's `serverInfo.version`
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
  failure and to read `meta.release_version` consistently across tools.
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
before this feature existed. The uniform compact-mode policy for the full
read surface is tracked in `docs/compact-response-mode-design.md` (`#526`)
and is now implemented for all anonymous/content.read tools.

| Parameter        | Type       | Meaning                                                    |
|-------------------|------------|-------------------------------------------------------------|
| `response_mode`   | string     | `standard` (default) or `compact` (reduced field set, tool-defined). `full` and `ids_only` are reserved for future work and rejected as `invalid_params` until implemented — they are never silently treated as `standard`. |
| `fields`          | string[]   | Restrict each returned item to the named JSON fields. Applied after `response_mode`, so it can further narrow a `compact` row. Unknown field names are silently dropped, not an error. |
| `include_body`    | bool       | Default `true`. When `false`, omit large body content (e.g. Markdown) and return metadata only. Same nil-means-true semantics everywhere it appears (see `export_agent_context`, #325). |
| `max_body_chars`  | int        | Truncate a body field to N characters. `0` (default) disables truncation. Truncation adds a `warnings` entry so callers know the body was cut. |

Not every tool supports every parameter — see [Section 6](#6-tool-inventory)
for which parameters each tool accepts. `response_mode` is now uniformly
available across the anonymous/content.read surface. In `compact` mode, the
envelope-level behavior is shared everywhere: `meta` keeps
`schema_version`/`release_version`/`commit`/`build_channel` — every field
except `generated_at` — while the root `generated_at` compatibility field
remains present. `compact` only ever narrows `data`/row-level payload; it
never trims `meta`'s release-identity fields, since those are cheap, static
per-process values with no payload-size cost to keep (#567 — reversing the
narrower #526/#553 trim, after three independent live audits flagged an
agent in `compact` mode being unable to tell which server build answered
it). Tool-specific data shaping remains opt-in and tool-defined:
`search_pages` still narrows each row further with `fields`, and
`build_agent_context`/`get_page_for_edit`/`export_agent_context` keep their
own body/section shaping controls.

---

## 6. Tool Inventory

### Anonymous semantics at the tool layer

These 9 tools carry the full structured envelope (`success`/`data`/`errors`/
`warnings`/`meta`) like every other tool in this document — their payload
lives solely under `data.X` below. Through v1.4.x they *also* duplicated the
same fields at the top level (`data.pages` **and** top-level `pages`,
etc.), roughly doubling response size for no functional benefit; that
duplication was removed (#433), so `data.X` is now the only place to read
each field.

| Tool                  | Envelope    | `data.X` key(s)          |
|-----------------------|-------------|---------------------------|
| `list_pages`          | structured  | `pages`; supports `response_mode` compact envelope shaping (§5.2, #526); each page carries `source_key` (source-relative, language-prefix-stripped identifier) alongside `slug`'s public-URL form, when resolvable (#576) |
| `get_page`            | structured  | `page`; supports `response_mode` compact envelope shaping (§5.2, #526); `page.html_origin` (`rendered_public`/`source_fallback`/`none`) and `page.rendered_html_available` (bool) disambiguate whether `page.html` is real rendered public HTML or a source-fallback/empty value, so a caller never has to infer that from `page.state` alone (#502) |
| `search_pages`        | structured  | `pages`; supports `response_mode`/`fields` shaping (§5.2, #337); each page carries `score` (term-match count) and `match: "title_exact"` requests a strict full-title match instead of broad term matching (#332); `source_key` alongside `slug`, same as `list_pages` (#576) |
| `get_recent_posts`    | structured  | `pages`; supports `response_mode` compact envelope shaping (§5.2, #526); `source_key` alongside `slug`, same as `list_pages` (#576) |
| `list_tags`           | structured  | `tags`; supports `response_mode` compact envelope shaping (§5.2, #526) |
| `list_categories`     | structured  | `categories`; supports `response_mode` compact envelope shaping (§5.2, #526) |
| `get_sitemap`         | structured  | `entries`; supports `response_mode` compact envelope shaping (§5.2, #526); each entry carries `source_key` alongside `slug`, when resolvable — empty for taxonomy/term entries with no backing source file (#576) |
| `get_feed`            | structured  | `items`; supports `response_mode` compact envelope shaping (§5.2, #526); site-wide across every published section, not only `/posts/` — use `get_recent_posts` for posts-only (#570); each item carries `source_key` alongside `slug`, same as `get_sitemap` (#576) |
| `get_site_information`| structured  | `site`; supports `response_mode` compact envelope shaping (§5.2, #526) |

### `read` (reader tier; on OAuth-enabled deployments, obtain a Bearer token first; see [§6.12](#612-2-scope-model-readwrite-450))

Per [§6.12](#612-2-scope-model-readwrite-450), these tools require
`RequiredScope: ""` — there is no additional per-tool split below `read`.
On deployments with OAuth disabled they can be called directly; on
OAuth-enabled deployments the transport still requires a Bearer token before
`tools/list` or `tools/call`. The per-tool notes below that once described
reader-safe restrictions (`quality` omitted, `page_count` omitted, empty
`assets` list, `content_not_public`) described the pre-#450 `reader` profile
and no longer apply to any live caller: any caller now sees full source
content, including drafts, for every tool in this table.

| Tool                    | Envelope    | Notes                                        |
|-------------------------|-------------|----------------------------------------------|
| `get_page_markdown`| structured  | `data.page` + `data.page.state` (#495); supports `response_mode` compact envelope shaping (§5.2, #526) |
| `get_page_frontmatter`  | structured  | `data.frontmatter` + `data.frontmatter.state` (#495); supports `response_mode` compact envelope shaping (§5.2, #526) |
| `get_related_content`   | structured  | `data.related_pages`; supports `response_mode` compact envelope shaping (§5.2, #526); the deprecated `related` alias (#453) was removed once #433/#454 resolved the live-client-verification question — `related_pages` was always canonical; when `data.related_pages` is empty, `data.empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458); `include: ["impact"]` opts into a pre-mutation impact summary — `data.impact.taxonomy_orphans` (tags/categories on this page with no other carrier), `data.impact.sitemap_present`, `data.impact.feed_present`, `data.impact.aliases` (this page's own front-matter redirect aliases) — omitted unless requested, advisory only, never blocks a mutation (#434); `data.index_staleness` (`newest_edit`) is present only when the in-memory index backing `related_pages`/`backlinks` is behind on-disk content — absent means current (#583); top-level duplication removed (#495) |
| `build_agent_context`   | structured  | `data.context` + `data.context.state`; supports `response_mode`/`max_body_chars` shaping (§5.2, #337); top-level duplication removed (#495) |
| `export_agent_context`  | structured  | `data.pages[*].state`, `data.total`, `data.include_body`; supports `response_mode` compact envelope shaping (§5.2, #526) — no nested `export` wrapper, `data` itself is the export result; `limit` capped at 10 when `include_body=true` (default), 50 when `include_body=false` (#325); top-level duplication removed (#495) |
| `get_page_for_edit`     | structured  | `data.page.state`, `data.page.revision`, `data.page.quality`; supports `response_mode` compact envelope shaping (§5.2, #526); each of `frontmatter`/`markdown`/`state`/`quality` is a pointer field omitted when not requested via `include` (#339); `data.page.backlinks`, `data.page.impact`, and `data.page.preview` are additional opt-in `include` values only — never part of the default bundle when `include` is omitted (#527). Equality invariants: `backlinks` is identical to `get_backlinks.data.backlinks`, `impact` is identical to `get_related_content(include=["impact"]).data.impact`, and `preview` is identical to `inspect_rendered(include_preview=true).data.preview` for the same published page. If a page has no rendered public output yet, requesting `preview` adds a warning and omits `data.page.preview` instead of failing the whole edit-prep bundle. Top-level duplication removed (#495) |
| `list_content_types`    | structured  | `data.content_types[*]` (`name`, `source`, `archetype_path?`, `expected_fields?`, `page_count?`); supports `response_mode` compact envelope shaping (§5.2, #526); `expected_fields` is the union of the archetype's declared keys and keys observed on existing pages of that type (#347); `data.special_files[*]` (`kind: "section_index"`, `section`, `languages[]`) surfaces Hugo `_index`/`_index.<lang>.md` files separately — they are structural, not creatable content types; `section: ""` means the site's root/home index, not a missing value (#457); top-level duplication removed (#495) |
| `list_page_assets`      | structured  | `data.assets[*]` (`name`, `size_bytes`, `modified_at`, `sha256` — same `sha256:<hex>` format `upload_page_asset`/`delete_page_asset` use for `expected_sha256`, #574); supports `response_mode` compact envelope shaping (§5.2, #526); lists the sibling files in a leaf page bundle's directory; `not_a_bundle` for single-file pages (#348); top-level duplication removed (#495); `data.hint` is present (and only present) when `data.assets` is empty, clarifying this tool covers page-bundle sibling files only, not the site's global static assets a page may still reference (#569) |
| `check_ai_readiness` | structured  | `data.status`, `data.checks`, `data.warnings`, `data.suggestions`; deterministic Markdown/frontmatter-only audit for heading hierarchy, section lengths, paragraph lengths, metadata presence, internal-link density, and citation structure. Explicitly does **not** cover rendered HTML, SEO, build freshness, or broken-link correctness (#437); does not yet support `response_mode` compact shaping (#526) |
| `search_content`        | structured  | `data.pages[*].state`, `data.total`, pagination echo; supports `response_mode` compact envelope shaping (§5.2, #526); top-level duplication removed (#495) |
| `explain_structure`| structured  | `data.sections`, `data.languages`, `data.summary`, `data.recent_pages[*].state`; supports `response_mode` compact envelope shaping (§5.2, #526); a non-default-language page's route prefix (e.g. `en` in `/en/posts/foo/`) is stripped before section counting and only ever surfaced via `data.languages`, never as a `data.sections[*].name` (#459); top-level duplication removed (#495) |
| `get_site_health`       | structured  | `data.score`, `data.status`, counts; supports `response_mode` compact envelope shaping (§5.2, #526); `data.score_breakdown` explains the score per category, `data.taxonomy_inconsistency_details[*].severity` explains per finding (#419); `data.taxonomy_inconsistency_details[*]` gives affected page slugs per finding (`data.taxonomy_inconsistencies` string list kept for compat) (#324); `data.advisories_count` is the total count of `data.taxonomy_inconsistency_details` findings across *both* `info` and `warning` severity, at the top level next to `score`/`status` — never moves either; deliberately broader than `score_breakdown.taxonomy.advisories`, which counts only `info`-severity findings (#591); top-level duplication removed (#495) |
| `get_broken_links`      | structured  | `data.links`, `data.broken_links`; supports `response_mode` compact envelope shaping (§5.2, #526); `data.index_staleness` (`newest_edit`) is present only on the in-memory fallback path (not the `db_path` pre-computed-graph path) when the index is behind on-disk content — absent means current (#583); top-level duplication removed (#495) |
| `get_backlinks`         | structured  | `data.backlinks`, `data.count`; supports `response_mode` compact envelope shaping (§5.2, #526); `data.index_staleness` (`newest_edit`) is present only when the index is behind on-disk content — absent means current (#583); top-level duplication removed (#495) |
| `suggest_links`         | structured  | `data.suggested_links` is canonical; supports `response_mode` compact envelope shaping (§5.2, #526); the deprecated `data.suggestions` alias (#453) was removed once #433/#454 resolved the live-client-verification question; when `data.suggested_links` is empty, `data.empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458); top-level duplication removed (#495) |
| `diff_page`             | structured  | `data` (diff result) + `data.state`; supports `response_mode` compact envelope shaping (§5.2, #526); top-level duplication removed (#495); `data.slug` is the canonical `/posts/x/`-form public slug, not the raw source-relative path (#519) |
| `inspect_rendered` | structured  | `data.checks[*].check/status/detail`, `data.status`, `data.state`; supports `response_mode` compact envelope shaping (§5.2, #526); `include_preview=true` opts into `data.preview` — a combined pre-publish summary composing `diff_page` (`diff_status`/`diff_summary`), `get_broken_links` scoped to this page (`broken_links_count`), and `validate_frontmatter` (`frontmatter_valid`/`frontmatter_issues`) into one `risks` list, so an agent doesn't have to chain three separate calls before publishing — omitted unless requested, advisory only, never blocks a mutation (#435); top-level duplication removed (#495) |
| `validate_frontmatter` | structured  | `data.pages`, `data.pages_checked`; supports `response_mode` compact envelope shaping (§5.2, #526); top-level duplication removed (#495); each `data.pages[*].slug` is the canonical `/posts/x/`-form public slug, including for Hugo section-index pages (#519); `data.test_content_slugs` separately lists any slug (last segment, case-insensitive) matching a reserved test/audit prefix (`mcp-audit-`, `test-audit-`, `codex-`) — advisory only, never affects `data.invalid`/per-page `issues`/`data.status` (#584) |
| `validate_site`         | structured  | `data.status` (`"valid"`/`"invalid"`, #568), `data.pages`, `data.pages_checked`; supports `response_mode` compact envelope shaping (§5.2, #526); defaults to invalid-only (`data.pages` omits passing pages unless `include_valid=true` or `invalid_only=false` is passed explicitly) — `data.pages_checked`/`data.pages_passed`/`data.invalid`/`data.status` always describe the full scan regardless (#456); top-level duplication removed (#495); each `data.pages[*].slug` is the canonical `/posts/x/`-form public slug (#519); `data.test_content_slugs` separately lists any slug (last segment, case-insensitive) matching a reserved test/audit prefix (`mcp-audit-`, `test-audit-`, `codex-`) — advisory only, never affects `data.invalid`/per-page `issues`/`data.status` (#584) |

### `write` (requires a registered OAuth client, see [§6.12](#612-2-scope-model-readwrite-450))

Per [§6.12](#612-2-scope-model-readwrite-450), the tools formerly split
between `content.write` and `site.admin` are now a single `write` scope with
no exceptions — `write` implies full `read` access plus everything below.

`create_page`/`update_page`/`delete_page`/`upload_page_asset`/
`delete_page_asset`/`generate_hero_image` used to leave `data` as an empty
placeholder object, with the real payload only at the top level — a
different, older convention than the read-side flat/structured duplication
#433/#495 addressed (tracked separately as #508). #508's fix (#512) made
`data.X` mirror the same fields additively, with the top-level fields kept
as compatibility aliases through `v1.5.6`. As of `v1.5.7` (#520), that
top-level mirroring is removed for `create_page`/`update_page`/
`upload_page_asset`/`delete_page`/`delete_page_asset`: they are relabeled
"structured" below,
the same way #495 did for the read tools, with only `request_context`
(error path) and `rate_limit_remaining` kept at the root (see
[§1.1](#11-flat-envelope)). `generate_hero_image` and `create_preview` were
not in #520's original scope (they gained their envelope slightly later,
via #552) — as of `v1.5.9` (#573), that gap is closed: both are now
structured too, with the same root/data convergence.

| Tool          | Envelope | Top-level key(s)                            |
|---------------|----------|---------------------------------------------|
| `create_page` | structured | `data.status`, `data.slug` (canonical public `/posts/x/` form, #554), `data.source_key`, `data.path`, `data.dry_run?`, `data.content?`, `data.warning?`; `data.resolved_lang`/`data.resolved_source_path` are omitted (not empty-stringed) unless resolution actually succeeded; on failure, root `request_context` (`slug`, `requested_lang?`) always echoes the caller's normalized input (#455); on success (non-dry-run), `data.new_revision` is the resulting page's revision, usable directly as `expected_revision` on a following `update_page`/`delete_page` without an intermediate read (#464); opt-in `normalize_taxonomy_casing` (default off) rewrites a submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index to that spelling, reported in `data.taxonomy_casing_normalized[]` (`type`/`from`/`to`); a term left untouched because the index already has 2+ conflicting spellings is reported instead in `data.taxonomy_casing_ambiguous[]` (`type`/`term`) — never guessed at (#589); `body` fails `invalid_params` if it invokes a server-configured blocked shortcode — default `raw`/`rawhtml`/`script`/`style`, tunable via `blocked_shortcodes` in server config, never opt-out-able per call; a best-effort denylist seeded from an audit of one theme, not a guarantee every theme's shortcode surface is safe (#590); root `rate_limit_remaining` reports the caller's real remaining budget on the shared create/update/upload quota, on both success and error responses — it is never a stale/zero placeholder on the error path (#466, #510); no other top-level payload duplication as of v1.5.7 (#520) |
| `update_page` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.dry_run?`, `data.diff?`, `data.warning?`; same `data.resolved_lang`/`data.resolved_source_path`/root `request_context` failure-path contract as `create_page` (#455); same `data.new_revision` success-path contract as `create_page` (#464); same opt-in `normalize_taxonomy_casing`/`data.taxonomy_casing_normalized`/`data.taxonomy_casing_ambiguous` contract as `create_page`, also populated on a `dry_run` preview (#589); same `body` blocked-shortcode contract as `create_page`, enforced on `dry_run` too (#590); same root `rate_limit_remaining` contract as `create_page`, including on error responses (#466, #510); no other top-level payload duplication as of v1.5.7 (#520) |
| `delete_page` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.warning?`; same `data.resolved_lang`/`data.resolved_source_path`/root `request_context` failure-path contract as `create_page` (#455); root `rate_limit_remaining` reports the caller's real remaining budget on `delete_page`'s own, separate quota, on both success and error responses (#466, #510); no other top-level payload duplication as of v1.5.7 (#520) |
| `upload_page_asset` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.filename`, `data.path`, `data.content_type`, `data.size_bytes`, `data.sha256`, `data.duplicate_of?` (advisory only), `data.dry_run?`; allowed types png/jpg/jpeg/gif/webp only (SVG deferred, #348); never overwrites (`already_exists`); root `rate_limit_remaining` reports the caller's real remaining budget on the shared create/update/upload quota, on both success and error responses (#466, #510); no other top-level payload duplication as of v1.5.7 (#520) |
| `delete_page_asset` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.filename`, `data.sha256`, `data.dry_run?`, `data.referenced?` (pointer — present as `false` on success, omitted on error, so "not referenced" and "never checked" stay distinguishable), `data.referenced_in?`; requires `expected_sha256` or `expected_revision` on non-dry-run calls (a mismatch fails `revision_conflict`); fails `asset_referenced` if the filename is still linked from the page body, unless `force=true`; `dry_run` previews `data.sha256`/`data.referenced` without requiring the concurrency guard or deleting anything; root `rate_limit_remaining` reports the caller's real remaining budget on `delete_page`'s own destructive quota, on both success and error responses (#460, #510). Only removes the source asset — unlike `delete_page`, it does not purge any built public copy or CDN cache; the asset stays reachable at its old URL until the next build; no other top-level payload duplication as of v1.5.7 (#520) |
| `get_mutation_status` | structured | `data.tool`, `data.idempotency_key`, `data.status` (`"succeeded"`/`"unknown"`), `data.result?` — a read-only lookup of a prior `idempotency_key`-bearing `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` call, for recovering from a timeout/ambiguous response without resending the original payload; `data.result` (present only when `status: "succeeded"`) is the *entire* original response envelope (`success`/`data`/`errors`/`warnings`/`meta`), not just its inner `data` — the same shape a same-key/same-payload retry of the mutation tool itself would replay via its own idempotency cache. `status: "unknown"` covers still-in-flight, genuinely failed, expired (15-minute TTL, shared with the underlying idempotency cache), or never-attempted equally — only successful calls are ever recorded, so this is never proof of failure. Requires `content.write` (#586) |
| `build_site`              | flat     | `status`, `duration_ms`, `build_id`, `output_revision`, `publish_ready`; `data.X` mirrors all five additively (#572) — this was the last tool with zero envelope at all (not even root-level duplication) before this change |
| `preview_build`           | flat     | `status`, `duration_ms`; `data.X` mirrors both additively (#552) |
| `run_post_build_hooks`    | flat     | `results`; `data.results` mirrors it additively (#552) |
| `generate_hero_image` | structured | `data.path`; `path` is hugo_root-relative, never the host's absolute filesystem path (#551); no root-level duplication as of v1.5.9 (#573) |
| `check_sri_versions`      | flat     | `files_scanned`, `files_with_sri_attributes`, `sri_entries_loaded`, `sri_checked`, `status`, `summary`, `findings`; `data.X` mirrors all of the above additively (#552) |
| `get_runtime_status`      | structured | `data.release_version`, `data.commit`, `data.hugo`, `data.git`, `data.site`, `data.degraded` |
| `get_theme_status`        | structured | `data.themes[*]`, `data.hugo`         |
| `verify_publication`      | structured | `data.source/build/public/index`, `data.http_status`, `data.status`, `data.explanation` |
| `create_preview`          | structured | `data.preview_id`, `data.url`, `data.expires_at`, `data.build`; no root-level duplication as of v1.5.9 (#573) |

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

**`dry_run` never consumes either per-tool-class budget above, on any tool that supports it** (#575, #588). #575 first verified this for `delete_page_asset`: a live audit observed `rate_limit_remaining` drop immediately before a real call and suspected `dry_run` itself was the cause; a regression test (`TestDeletePageAssetDryRunDoesNotConsumeDestructiveQuota`) proved repeated `dry_run` calls leave the budget unchanged — the drop was consistent with normal token-bucket refill timing between an earlier real call and the next observation, not a quota leak. #588 then swept the remaining `dry_run`-capable tools and found `create_page`/`update_page`/`upload_page_asset` did **not** actually hold this invariant — each called the rate limiter's `Allow()` before checking `dry_run`, unlike `delete_page`/`delete_page_asset`. Fixed so all five tools defer `Allow()` until after the `dry_run` early return; regression tests for each (`TestCreatePageDryRunDoesNotConsumeQuota`, `TestUpdatePageDryRunDoesNotConsumeQuota`, `TestUploadPageAssetDryRunDoesNotConsumeQuota`) confirm the invariant now holds everywhere it applies. `rate_limit_remaining` on a `dry_run` response always reflects the caller's actual current budget, never a decremented preview.

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
  `possible_duplicate`/`casing_variant` — counted as an issue, but still
  never penalizes the top-level `score`, exactly as before #419).
  `casing_variant` (#577) is a same-language, same-word, different-casing
  finding (e.g. `Infrastructure`/`infrastructure` both used on English
  pages) — a blind spot `possible_duplicate`/`translation_pair` never
  covered, since `taxonomy.Slug()` already lowercases before either of
  those two ever compares terms, so two same-slug spellings never even
  reach the edit-distance pairing pass. A pair of spellings confined to
  entirely different languages is left unflagged — that could be a
  deliberate per-language style choice, not necessarily a bug.
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

## 6.13. `/mcp` Bearer Verification Uses the SDK Primitive (#473)

The `/mcp` route now delegates transport-level bearer-token verification to
`github.com/modelcontextprotocol/go-sdk/auth.RequireBearerToken`, replacing the
older fully hand-rolled Authorization-header parsing path in
`internal/server/server.go`.

This is intentionally **not** a full transfer of authorization ownership to the
SDK. The server still keeps three project-specific responsibilities locally:

1. **client-facing challenge compatibility** — preserve the already-validated
   `WWW-Authenticate` shape (`realm=...`, `resource_metadata=...`,
   `error="invalid_token"` when appropriate) used by ChatGPT, Claude, Le Chat,
   and external MCP scanners;
2. **body-aware MCP ACL** — `tools/call` authorization still depends on the
   requested tool name inside the JSON-RPC body, which the SDK middleware does
   not know about;
3. **scope-aware context enrichment** — the project still injects its canonical
   scope, caller IP, legacy-scope metric signal, and related audit context
   after SDK authentication succeeds.

So the runtime split is deliberate:

- **SDK-owned:** bearer extraction + token-verification entry point
- **project-owned:** challenge normalization, per-tool ACL, and request-context
  enrichment

A raw drop-in use of `RequireBearerToken` would have been smaller, but it would
have changed on-wire behavior that current clients already rely on.

## 6.14. Last Build Status Surfaced Proactively (#467)

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
