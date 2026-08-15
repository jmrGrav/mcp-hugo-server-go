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

**#1118** (shipped alongside this schema major bump to `v2.0.0`) finishes this
convergence for the three tools #520/#573 left out because they gained their
envelope later, via #552: `check_sri_versions`, `run_post_build_hooks`, and
`preview_build` no longer mirror their payload at the root either — `data.*`
is now the sole payload location for every structured tool in this server.

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
  "meta": { "generated_at": "...", "release_version": "...", "commit": "...", "build_channel": "...", "schema_version": "v2.0.0" }
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
    "schema_version": "v2.0.0"
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
| `meta`         | object   | yes           | `generated_at`, `release_version` (deployed build identifier — is the release tag itself on a release build, `main-<sha>` otherwise), `commit`, `build_channel`, `schema_version` (this envelope's shape version, currently `"v2.0.0"`); plus `request_id` and `duration_ms` (#860) — a per-call correlation id (`req-<hex>`) and the envelope-level wall-clock latency in ms, injected by the served tool-call middleware onto every success and error response (omitted only on responses built outside the served path, e.g. in-process unit tests); and optional `content_provenance` with `site_source_untrusted` for raw source reads, `site_rendered_public_untrusted` for rendered public HTML, or `server_generated_trusted` for payloads computed solely from server/runtime metadata (#1006, #1021) — see [§5](#5-versioning) |

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

Some connector or bridge layers expose an additional generic field such as
`error_code: INVALID_ARGUMENT` for any tool result whose MCP `isError` flag is
true. That field is not emitted by this server and is not authoritative for
tool semantics. Clients MUST use `errors[0].code` from `structuredContent`
(or the equivalent canonical JSON envelope) to distinguish
`content_not_found`, `invalid_params`, `revision_conflict`, and other tool
errors. Raw MCP responses from this server keep `tools/call` at the JSON-RPC
result layer with `isError:true`; they do not convert a business failure into a
JSON-RPC `error` object or add a top-level `error_code`.

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

- **Version history**: `v1.0.0` (initial), `v1.1.0` (#1042/#1043 — schema
  versioning itself introduced, under the additive→minor/breaking→major
  policy this history follows), `v2.0.0` (#1118 — `check_sri_versions`/
  `run_post_build_hooks`/`preview_build` root-level payload aliases removed;
  breaking per that same policy).
- `meta.schema_version: "v2.0.0"` refers to the **response schema version**,
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
  afterward, once the deployment is live and verified — `.github/workflows/
  deploy.yml`'s own `release` job creates the tag and GitHub release right
  after its `deploy` job succeeds, in the same run — so a deploy must be
  told which release it belongs to explicitly, via the `release_version`
  input, rather than deriving it from a tag that doesn't exist yet. That
  workflow input name is unchanged across all three field-name changes
  above; it feeds `meta.release_version` and `meta.build_channel` directly.
  A deploy triggered without that input (or targeting a ref that isn't the
  intended release commit) reports `meta.release_version = "main-<sha>"`,
  `meta.build_channel = "main"`, and skips the tag/release job entirely.
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
`version: "v2.0.0"` is today), never as a stealth v1.x patch that changes
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
| `response_mode`   | string     | `standard` (default) or `compact` (reduced field set, tool-defined). Each supporting tool publishes these values in its property description, rather than a JSON Schema `enum`: SDK enum validation would bypass this server's structured `invalid_params` envelope. `full` and `ids_only` are reserved for future work and rejected as `invalid_params` until implemented — they are never silently treated as `standard`. |
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
| `list_pages`          | structured  | `pages`; supports `response_mode` compact envelope shaping (§5.2, #526); each page carries `source_key` (source-relative, language-prefix-stripped identifier) alongside `slug`'s public-URL form, when resolvable (#576); `data.total` is every published content page site-wide — broader than, and not comparable to, `get_recent_posts`'s posts-only `data.total`; a gap between the two reflects non-post pages, not missing content |
| `get_page`            | structured  | `page`; supports `response_mode` compact envelope shaping (§5.2, #526); `page.html_origin` (`rendered_public`/`source_fallback`/`none`) and `page.rendered_html_available` (bool) disambiguate whether `page.html` is real rendered public HTML or a source-fallback/empty value, so a caller never has to infer that from `page.state` alone (#502); **BREAKING (#619): `content_only` now defaults to `true`**, not `false` — `page.html` returns just the article body by default (theme chrome stripped) rather than the full rendered page, which previously ran to several thousand tokens for a short article. Pass `content_only=false` explicitly to opt back into full-page HTML; opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit `page.tag_terms`/`page.category_terms` (richer `{label,slug,source}` objects duplicating `page.tags`/`page.categories` at 3-4x the size) when only the plain string arrays are needed (#618); `response_mode=compact` clears `page.html` (still sets `page.rendered_html_available`/`page.html_origin` so a caller knows rendered HTML exists without paying for it) and omits `page.tag_terms`/`page.category_terms`, for a page-selection step that only needs metadata (#687); for a source-only multilingual bundle with no explicit `lang` and no built public page yet (nothing in `Index` to derive a language from), resolution now deterministically prefers the server's configured `DefaultLanguage` over whichever language file the source index happens to iterate to first — previously this could resolve to a different language file across otherwise-identical calls, since the source index's slug lookup has no defined preference between same-slug language variants (#684) |
| `search_pages`        | structured  | `pages`; supports `response_mode`/`fields` shaping (§5.2, #337); each page carries `score` (term-match count) and `match: "title_exact"` requests a strict full-title match instead of broad term matching (#332); `source_key` alongside `slug`, same as `list_pages` (#576) |
| `get_recent_posts`    | structured  | `pages`; supports `response_mode` compact envelope shaping (§5.2, #526); `source_key` alongside `slug`, same as `list_pages` (#576); `data.total` is scoped to `/posts/` only — not comparable to `list_pages`'s site-wide `data.total` |
| `list_tags`           | structured  | `tags`; supports `response_mode` compact envelope shaping (§5.2, #526) |
| `list_categories`     | structured  | `categories`; supports `response_mode` compact envelope shaping (§5.2, #526) |
| `get_sitemap`         | structured  | `entries`; supports `response_mode` compact envelope shaping (§5.2, #526); each entry carries `source_key` alongside `slug`, when resolvable — empty for taxonomy/term entries with no backing source file (#576) |
| `get_feed`            | structured  | `items`; supports `response_mode` compact envelope shaping (§5.2, #526); site-wide across every published section, not only `/posts/` — use `get_recent_posts` for posts-only (#570); each item carries `source_key` alongside `slug`, same as `get_sitemap` (#576) |
| `get_site_information`| structured  | `site`; supports `response_mode` compact envelope shaping (§5.2, #526) |
| `get_changelog`       | structured  | `data.entries[*]` (`version`, `date?`, `body?`), `data.total`; content is embedded at build time (not read from a runtime path), so it always matches the running binary exactly, with zero drift. Without arguments, returns the 5 most recent versioned releases (default limit, max 20) — bounded, never a full dump of CHANGELOG.md. `since_version` returns every release strictly newer than that version instead; fails `invalid_params` if it doesn't match any release heading. `response_mode:"compact"` is audit-oriented: when `limit` is omitted it defaults to 1 entry instead of 5, and omits each entry's raw Markdown `body`; pass explicit `limit` to raise the compact entry count while keeping body omitted. Standard mode keeps each entry's `body` as the release section's raw Markdown verbatim, not parsed into structured Added/Fixed/Security subsections — CHANGELOG.md's own formatting is the source of truth. Anonymous tier: the changelog is already public on GitHub (#612, #720) |

### `read` (reader tier; on OAuth-enabled deployments, obtain a Bearer token first; see [§6.12](#612-3-scope-model-readwriteadmin-450))

Per [§6.12](#612-3-scope-model-readwriteadmin-450), these tools require
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
| `get_page_markdown`| structured  | `data.page` + `data.page.state` (#495); supports `response_mode` compact envelope shaping (§5.2, #526); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit `data.page.tag_terms`/`data.page.category_terms` when the plainer `tags`/`categories` arrays are sufficient; `response_mode=compact` implies the same omission (#618) |
| `get_page_frontmatter`  | structured  | `data.frontmatter` + `data.frontmatter.state` (#495); supports `response_mode` compact envelope shaping (§5.2, #526); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit `data.frontmatter.tag_terms`/`data.frontmatter.category_terms` when the plainer `tags`/`categories` arrays are sufficient; `response_mode=compact` implies the same omission (#618) |
| `get_related_content`   | structured  | `data.related_pages`; supports `response_mode` compact envelope shaping (§5.2, #526); `lang` filters relationship rows to one language and `one_per_source_key=true` collapses translated siblings by conceptual source key; compact mode omits backlinks/translations and taxonomy detail while retaining ranked recommendations; the deprecated `related` alias (#453) was removed once #433/#454 resolved the live-client-verification question — `related_pages` was always canonical; when `data.related_pages` is empty, `data.empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458); `include: ["impact"]` opts into a pre-mutation impact summary — `data.impact.taxonomy_orphans` (tags/categories on this page with no other carrier), `data.impact.sitemap_present`, `data.impact.feed_present`, `data.impact.aliases` (this page's own front-matter redirect aliases) — omitted unless requested, advisory only, never blocks a mutation (#434); `data.index_staleness` (`newest_edit`, `likely_source`) is present only when the in-memory index backing `related_pages`/`backlinks` is behind on-disk content — absent means current (#583); `likely_source` is `"mcp_pending_build"` (a known write via this server awaiting the next build) or `"external_or_unknown"` (no such record — most plausibly an out-of-band edit), a coarse best-effort hint, not per-caller attribution (#617); top-level duplication removed (#495) |
| `build_agent_context`   | structured  | `data.context` + `data.context.state`; supports `response_mode`/`max_body_chars` shaping (§5.2, #337); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit nested `data.context.frontmatter.tag_terms`/`category_terms`; `response_mode=compact` implies the same omission (#618); top-level duplication removed (#495) |
| `export_agent_context`  | structured  | `data.pages[*].state`, `data.total`, `data.include_body`; supports `response_mode` compact envelope shaping (§5.2, #526) — no nested `export` wrapper, `data` itself is the export result; `limit` capped at 10 when `include_body=true` (default), 50 when `include_body=false` (#325); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit nested `data.pages[*].frontmatter.tag_terms`/`category_terms`; `response_mode=compact` implies the same omission (#618); top-level duplication removed (#495) |
| `get_page_for_edit`     | structured  | `data.page.state`, `data.page.revision`, `data.page.quality`; supports `response_mode` compact envelope shaping (§5.2, #526); each of `frontmatter`/`markdown`/`state`/`quality` is a pointer field omitted when not requested via `include` (#339); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit nested `data.page.frontmatter.tag_terms`/`category_terms`; `response_mode=compact` implies the same omission (#618); `data.page.backlinks`, `data.page.impact`, `data.page.preview`, and `data.page.readiness` are additional opt-in `include` values only — never part of the default bundle when `include` is omitted (#527, #621). Equality invariants: `backlinks` is identical to `get_backlinks.data.backlinks`, `impact` is identical to `get_related_content(include=["impact"]).data.impact`, `preview` is identical to `inspect_rendered(include_preview=true).data.preview` for the same published page, and `readiness` (`status`/`checks`/`warnings`/`suggestions`) is identical to `check_ai_readiness`'s own check result for the same slug (#621) — combining `preview`+`quality`+`readiness` in one `include` list is the one-call pre-publish check (#621). If a page has no rendered public output yet, requesting `preview` adds a warning and omits `data.page.preview` instead of failing the whole edit-prep bundle; a page with no matching source omits `data.page.readiness` the same way. Top-level duplication removed (#495) |
| `list_content_types`    | structured  | `data.content_types[*]` (`name`, `source`, `archetype_path?`, `expected_fields?`, `page_count?`); supports `response_mode` compact envelope shaping (§5.2, #526); `expected_fields` is the union of the archetype's declared keys and keys observed on existing pages of that type (#347); `data.special_files[*]` (`kind: "section_index"`, `section`, `languages[]`) surfaces Hugo `_index`/`_index.<lang>.md` files separately — they are structural, not creatable content types; `section: ""` means the site's root/home index, not a missing value (#457); top-level duplication removed (#495) |
| `plan_page`             | structured  | Pre-writing scaffold bundling three calls into one, before writing a new article: `data.content_types`/`data.special_files` are byte-identical to `list_content_types`'s own fields in standard mode; `response_mode=compact` keeps only the lighter `content_types[*].name`/`source` pair and omits heavier `expected_fields`/`archetype_path`/`page_count` detail when the caller only wants planning guidance (#723). `data.suggested_links` is derived from the same scoring logic as `suggest_links`, populated only when `tags` and/or `categories` is provided (omitted with `data.empty_links_reason` — same shape as `suggest_links`'s `empty_reason` — otherwise). Optional `language` filters those suggestions to one language, and `one_per_source_key=true` collapses FR/EN translation siblings to one conceptual recommendation while preserving the default backward-compatible ungrouped behavior (#722, #723) — both are applied against the full scored candidate pool before `suggestion_limit` truncates, so a matching lower-ranked candidate is never lost to the limit before the filter runs. `suggestion_limit` narrows only the final suggestion list, not `content_types` or taxonomy hints. `data.relevant_tags`/`data.relevant_categories` are the subset of the site's existing tag/category vocabulary matching `topic` or any submitted `tags`/`categories`, via a case-insensitive substring match in either direction — this also surfaces an existing differently-cased spelling for a tag/category about to be introduced; when category input is present but no existing category matches, `data.empty_categories_reason` makes that absence explicit (#723). Takes optional `topic`/`tags`/`categories`, all read-only, no new ranking heuristic beyond the tag/category substring match plus the opt-in translation grouping/filtering (#622, #722, #723) |
| `list_page_assets`      | structured  | `data.assets[*]` (`name`, `size_bytes`, `modified_at`, `sha256` — same `sha256:<hex>` format `upload_page_asset`/`delete_page_asset` use for `expected_sha256`, #574); supports `response_mode` compact envelope shaping (§5.2, #526); lists the sibling files in a leaf page bundle's directory; `not_a_bundle` for single-file pages (#348); top-level duplication removed (#495); `data.hint` is present (and only present) when `data.assets` and `data.generated_assets` are both empty, clarifying this tool covers page-bundle sibling files only, not the site's global static assets a page may still reference (#569); `data.generated_assets[*]` (`name`, `path`, `kind: "global_static"`, `size_bytes`, `modified_at`, `sha256`) separately surfaces a `generate_hero_image`-generated `{HugoRoot}/static/images/{slug}-featured.jpg`, when one exists, distinct from `data.assets`' bundle-local files (#683) |
| `check_ai_readiness` | structured  | `data.status`, `data.checks`, `data.warnings`, `data.suggestions`; deterministic Markdown/frontmatter-only audit for heading hierarchy, section lengths, paragraph lengths, metadata presence, internal-link density, and citation structure. Explicitly does **not** cover rendered HTML, SEO, build freshness, or broken-link correctness (#437); does not yet support `response_mode` compact shaping (#526); optional `lang` disambiguates a multilingual bundle explicitly; omitting it on a bare slug that resolves among ≥2 translations appends an explicit `data.warnings` entry naming which translation was implicitly selected, rather than resolving silently (#1063) |
| `search_content`        | structured  | `data.pages[*].state`, `data.total`, pagination echo; supports `response_mode` compact envelope shaping (§5.2, #526); opt-in `include_terms` (default `true`, non-breaking) — pass `include_terms=false` to omit `data.pages[*].tag_terms`/`category_terms`; `response_mode=compact` implies the same omission (#618, #720); top-level duplication removed (#495) |
| `explain_structure`| structured  | `data.sections`, `data.languages`, `data.summary`, `data.recent_pages[*].state`; supports `response_mode` compact envelope shaping (§5.2, #526); `response_mode:"compact"` keeps only the structural overview (`summary`, `sections`, `languages`, taxonomy counts) and omits `recent_pages` example rows (including their `tag_terms`/`category_terms`, #618) and the long `notes` list entirely for a lower-token first pass (#720); a non-default-language page's route prefix (e.g. `en` in `/en/posts/foo/`) is stripped before section counting and only ever surfaced via `data.languages`, never as a `data.sections[*].name` (#459); top-level duplication removed (#495) |
| `get_site_health`       | structured  | `data.score`, `data.status`, counts; supports `response_mode` compact envelope shaping (§5.2, #526); `data.publication_coverage` explains why all sources, publishable ordinary content, section indexes, and rendered content pages can have different counts while output remains complete (#992); `data.score_breakdown` explains the score per category, `data.taxonomy_inconsistency_details[*].severity` explains per finding (#419); `data.taxonomy_inconsistency_details[*]` gives affected page slugs per finding (`data.taxonomy_inconsistencies` string list kept for compat) (#324); `data.advisories_count` is the total count of `data.taxonomy_inconsistency_details` findings across *both* `info` and `warning` severity, at the top level next to `score`/`status` — never moves `score`; deliberately broader than `score_breakdown.taxonomy.advisories`, which counts only `info`-severity findings (#591); `data.actionable_taxonomy_findings_count` (warning-severity only) and `data.translation_pairs_detected` (info-severity `translation_pair` findings only) split `advisories_count` into an action-required count and an expected-localization count, so a client doesn't have to inspect `taxonomy_inconsistency_details[*].severity`/`kind` itself to tell them apart — `advisories_count` itself is unchanged, for existing clients (#1061); `data.status` is `"healthy_with_advisories"` rather than `"healthy"` on an otherwise-healthy site only when at least one taxonomy finding is actionable (`severity: "warning"`) — a pure `translation_pair`/`info` finding remains visible via `advisories_count` and `taxonomy_inconsistency_details`, but no longer degrades the top-level status on its own (#761); info-only taxonomy findings still leave `data.score` untouched, but warning-level taxonomy findings now cap an otherwise-perfect top-level score at `99` so the payload no longer advertises a perfect `100` while surfacing actionable drift (#719); `data.runtime_degraded`/`data.runtime_degraded_reasons` surface build/publication problems separately from content health — `runtime_degraded_reasons` can include `last_build_failed[:error_class]` (score caps at `99` independently whenever the last recorded `build_site` attempt failed, #719/#1066) and `public_output_incomplete` (a source page has no matching public output; affects `status`/`runtime_degraded_reasons` only, deliberately does not move `score`, since it also fires for the ordinary `create_page` → `build_site` window before a build has run, #1066); `data.bad_title_shape_pages` lists slugs whose title field is a bare URL instead of page text, forcing `status`/`content_status` off healthy and capping `score` at `99` regardless of `score_breakdown.title_shape`'s weight-0 (#1105, see §6.8); `data.broken_links_count`/`score_breakdown.broken_links` resolve #1105's own open design question — broken-link volume does feed this tool, as the same status-override/99-cap treatment as `title_shape`, but only when `db_path` is configured (`get_broken_links`'s own O(1) pre-computed link graph); both fields are entirely omitted, not a `0`, when not computed, since computing them without `db_path` would mean paying `get_broken_links`'s full-HTML-rescan cost on every `get_site_health` call (#1105); top-level duplication removed (#495) |
| `get_broken_links`      | structured  | `data.links`, `data.broken_links`; supports `response_mode` compact envelope shaping (§5.2, #526); `data.index_staleness` (`newest_edit`, `likely_source`) is present only on the in-memory fallback path (not the `db_path` pre-computed-graph path) when the index is behind on-disk content — absent means current (#583); `likely_source` is `"mcp_pending_build"` or `"external_or_unknown"`, a coarse best-effort hint (#617); a link to a target the site's content classifier doesn't consider ordinary content — Hugo pagination routes (`/en/page/2/`), technical routes, and home — is never reported broken, on both the in-memory and `db_path` paths, since the indexer's own canonical-URL dedup legitimately drops those routes from its slug lookup without them being missing (#1101); a link to a canonical-collapsed alias's own URL (e.g. a Grav legacy route whose `<link rel=canonical>` points at a different page) is likewise never reported broken on either path — it's a real, walkable file, just not canonical for its content (#1112); top-level duplication removed (#495) |
| `get_backlinks`         | structured  | `data.backlinks`, `data.count`; supports `response_mode` compact envelope shaping (§5.2, #526); `data.index_staleness` (`newest_edit`, `likely_source`) is present only when the index is behind on-disk content — absent means current (#583); `likely_source` is `"mcp_pending_build"` or `"external_or_unknown"`, a coarse best-effort hint (#617); top-level duplication removed (#495) |
| `suggest_links`         | structured  | `data.suggested_links` is canonical; `language` filters rows to one language and `one_per_source_key=true` collapses translated siblings; `response_mode` compact keeps ranked slug/title/score/anchor rows while omitting translations and taxonomy detail (§5.2, #526); the deprecated `data.suggestions` alias (#453) was removed once #433/#454 resolved the live-client-verification question; when `data.suggested_links` is empty, `data.empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) explains why — additive only, never replaces the empty array (#458); top-level duplication removed (#495); when tag/category taxonomy overlap yields zero candidates but `body` is provided, falls back to lexical term matching against the same indexed page fields `search_pages`/`search_content` already score against (via the existing `scoreContentPage` matcher) — reused rather than a new search subsystem, so it works on reader-only deployments without a `db_path`/FTS dependency; taxonomy-based candidates always take precedence and suppress the lexical fallback entirely whenever any exist (#680) |
| `diff_page`             | structured  | `data` (diff result) + `data.state`; supports `response_mode` compact envelope shaping (§5.2, #526); `response_mode:"compact"` omits the full raw `data.diff` (and any `data.source_content` fallback) in favor of a short `data.diff_summary`, so a caller can see that changes exist without paying for the unified diff unless it explicitly asks for standard mode (#720); top-level duplication removed (#495); `data.slug` is the canonical `/posts/x/`-form public slug, not the raw source-relative path (#519); optional `lang` disambiguates a multilingual bundle explicitly (validated for slug/lang consistency, same `ambiguous_language` contract as `update_page`); omitting it on a bare slug that resolves among ≥2 translations appends an explicit `data.warnings` entry rather than resolving silently (#1063) |
| `list_page_revisions`   | structured  | `data.revisions[*]` (`commit`, `short_commit`, `date`, `subject`), most recent first, `data.total`; requires a local Git repository and configured content root, same as `diff_page` — `data.status: "git_unavailable"` (empty `revisions`, explanatory warning) when git metadata can't be resolved, rather than failing outright; `limit` caps how many commits are returned (default 20, max 100), `--follow` tracks renames across history. Read-only "what could I revert to" answer, deliberately not paired with any write-path rollback tool yet — a second mutation path needing `expected_revision`/idempotency/index/rate-limit interop is a separate, larger design question (#615); optional `lang` disambiguates a multilingual bundle explicitly; omitting it on a bare slug that resolves among ≥2 translations appends an explicit `data.warnings` entry naming which translation was implicitly selected (#1063) |
| `inspect_rendered` | structured  | `data.checks[*].check/status/detail`, `data.status`, `data.state`; supports `response_mode` compact envelope shaping (§5.2, #526); `include_preview=true` opts into `data.preview` — a combined pre-publish summary composing `diff_page` (`diff_status`/`diff_summary`), `get_broken_links` scoped to this page (`broken_links_count`), and `validate_frontmatter` (`frontmatter_valid`/`frontmatter_issues`) into one `risks` list, so an agent doesn't have to chain three separate calls before publishing — omitted unless requested, advisory only, never blocks a mutation (#435); top-level duplication removed (#495); optional `lang` disambiguates a multilingual bundle explicitly; omitting it on a bare slug that resolves among ≥2 translations appends an explicit `data.warnings` entry naming which translation was implicitly selected, rather than resolving silently (#1063) |
| `validate_frontmatter` | structured  | `data.pages`, `data.pages_checked`; supports `response_mode` compact envelope shaping (§5.2, #526); top-level duplication removed (#495); each `data.pages[*].slug` is the canonical `/posts/x/`-form public slug, including for Hugo section-index pages (#519); `data.test_content_slugs` separately lists any slug (last segment, case-insensitive) matching a reserved test/audit prefix (`mcp-audit-`, `test-audit-`, `codex-`) — advisory only, never affects `data.invalid`/per-page `issues`/`data.status` (#584) |
| `validate_site`         | structured  | `data.status` (`"valid"`/`"invalid"`, #568), `data.pages`, `data.pages_checked`; supports `response_mode` compact envelope shaping (§5.2, #526); defaults to invalid-only (`data.pages` omits passing pages unless `include_valid=true` or `invalid_only=false` is passed explicitly) — `data.pages_checked`/`data.pages_passed`/`data.invalid`/`data.status` always describe the full scan regardless (#456); top-level duplication removed (#495); each `data.pages[*].slug` is the canonical `/posts/x/`-form public slug (#519); `data.test_content_slugs` separately lists any slug (last segment, case-insensitive) matching a reserved test/audit prefix (`mcp-audit-`, `test-audit-`, `codex-`) — advisory only, never affects `data.invalid`/per-page `issues`/`data.status` (#584) |

### `write` (requires a registered OAuth client, see [§6.12](#612-3-scope-model-readwriteadmin-450))

Per [§6.12](#612-3-scope-model-readwriteadmin-450), editorial and site-operation
tools use `write`; managed Hugo binary lifecycle tools use the separate
`admin` scope. `write` implies full `read` access plus everything below except
the four explicitly admin-gated Hugo upgrade tools.

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
| `create_page` | structured | `data.status`, `data.slug` (canonical public `/posts/x/` form, #554), `data.source_key`, `data.path`, `data.dry_run?`, `data.content?`, `data.warning?`; `data.resolved_lang`/`data.resolved_source_path` are omitted (not empty-stringed) unless resolution actually succeeded; on failure, root `request_context` (`slug`, `requested_lang?`) always echoes the caller's normalized input (#455); on success (non-dry-run), `data.new_revision` is the resulting page's revision, usable directly as `expected_revision` on a following `update_page`/`delete_page` without an intermediate read (#464); opt-in `normalize_taxonomy_casing` (default off) rewrites a submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index to that spelling, reported in `data.taxonomy_casing_normalized[]` (`type`/`from`/`to`); a term left untouched because the index already has 2+ conflicting spellings is reported instead in `data.taxonomy_casing_ambiguous[]` (`type`/`term`) — never guessed at (#589); **matching is scoped to the exact `lang` bucket of the page being written** — on a bilingual site where every real page specifies `lang` explicitly, omitting `lang` on a `normalize_taxonomy_casing` call resolves to the empty-string bucket, which has no existing forms to match against and so silently no-ops (no rewrite, no entry in either report array); always pass `lang` explicitly when using `normalize_taxonomy_casing` on such a site (#604); `body` fails `invalid_params` if it invokes a server-configured blocked shortcode — default `raw`/`rawhtml`/`script`/`style`, tunable via `blocked_shortcodes` in server config, never opt-out-able per call; a best-effort denylist seeded from an audit of one theme, not a guarantee every theme's shortcode surface is safe (#590); root `rate_limit_remaining` reports the caller's real remaining budget on the shared create/update/upload quota, on both success and error responses — it is never a stale/zero placeholder on the error path (#466, #510); no other top-level payload duplication as of v1.5.7 (#520); opt-in `test_content: {ttl_hours?, owner?}` (default `ttl_hours` 24) marks disposable test/audit content — a deliberate, explicit opt-in, never inferred from `slug`/`title` — forcing `draft: true` regardless of any other setting and writing `test_content`/`test_content_owner`/`test_content_expires_at` into the page's frontmatter; the effective expiry is echoed in `data.test_content_expires_at`. `owner` remains advisory metadata only: it can be used later for filtering or audit correlation, but it is not treated as authenticated ownership or authorization. This is an **ongoing publication-safety invariant**, not just a creation-time convenience: later write paths reject `draft:false` while `test_content` remains present (#661, #728). `build_site`/`publish_changes`'s post-build advisory (#608) honors `test_content_expires_at` unconditionally, independent of the server-wide `stale_test_content_threshold_hours` setting (#661) |
| `create_bundle` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.languages[]`, `data.dry_run?`, `data.revisions?` (per-language `expected_revision`, same as `create_page`'s `data.new_revision` but keyed by `lang` since a bundle writes several files atomically, #1038). Atomically creates every translation passed in `pages[]`; every page is validated before any file is written, so a validation failure on any one translation leaves no partial bundle on disk. Each entry in `pages[]` may independently set `draft`, `description`, `featured_image`, and the same explicit `test_content: {ttl_hours?, owner?}` marker `create_page` has — **deliberately per-translation, not bundle-wide** (#1038): translations legitimately need their own description/hero image, and a caller auditing only one language of a bundle shouldn't be forced to mark every language as draft/test content. `test_content` on a translation forces that translation's `draft: true` regardless of any other setting, independently of its sibling translations, and its effective expiry is reported per-language in `data.test_content_expires_at` (keyed by `lang`, same TTL/owner/forced-draft contract as `create_page`, #661) |
| `update_page` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.dry_run?`, `data.diff?`, `data.warning?`; same `data.resolved_lang`/`data.resolved_source_path`/root `request_context` failure-path contract as `create_page` (#455); same `data.new_revision` success-path contract as `create_page` (#464); same opt-in `normalize_taxonomy_casing`/`data.taxonomy_casing_normalized`/`data.taxonomy_casing_ambiguous` contract as `create_page`, also populated on a `dry_run` preview (#589); same `body` blocked-shortcode contract as `create_page`, enforced on `dry_run` too (#590); same root `rate_limit_remaining` contract as `create_page`, including on error responses (#466, #510); no other top-level payload duplication as of v1.5.7 (#520); if the page still carries `test_content: true`, attempts to set `draft: false` fail validation — test content remains non-publishable while that marker remains present (#728); on a successful (non-`dry_run`) write, the pre-write content is snapshotted (24h TTL) keyed by the revision it replaced, the same mechanism `apply_content_plan` uses, so `rollback_change` can restore to it — `create_page` is deliberately not snapshotted the same way, since there's no meaningful pre-create state to roll back to (#629); `data.tags_delta`/`data.categories_delta` (`added`/`removed`/`unchanged`, each omitted when empty) report the per-term outcome of the whole-list-replacement `tags`/`categories` input against the page's current value — populated whenever the corresponding key is present in the request at all (including an explicit empty list, meaning "clear them all"), on both `dry_run` and a real write; omitted entirely when the key is left out of the request, matching the existing "omit = leave unchanged" contract (#645) |
| `delete_page` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.warning?`, `data.bundle_fully_removed` (#682); same `data.resolved_lang`/`data.resolved_source_path`/root `request_context` failure-path contract as `create_page` (#455); accepts `lang` — on a bundle with more than one language file, omitting `lang` fails with `ambiguous_language` rather than guessing, matching `update_page`'s existing contract (#682); only the resolved language's source file is removed, and the bundle directory (plus any shared assets, hero image, public output, derived DB/index entries) is only removed once no language file remains — `data.bundle_fully_removed` reports which happened on a real delete, while `dry_run` uses the separate predictive field `data.bundle_will_be_fully_removed` so callers do not have to overload the real-execution field with two meanings (#762); deleting one of several surviving languages leaves public output untouched (surfaced via `data.warning`, since reconciling per-language public output needs a rebuild) rather than risk deleting a surviving translation's live public page (#682); root `rate_limit_remaining` reports the caller's real remaining budget on `delete_page`'s own, separate quota, on both success and error responses (#466, #510); best-effort removes any `generate_hero_image`-generated `{HugoRoot}/static/images/{slug}-featured.jpg` for the deleted slug when the whole bundle is removed, since that file lives outside the page's own content bundle and `os.RemoveAll` of the source directory never reaches it — a failure to remove it (or its absence, the common case) never fails the delete, but a removal failure is folded into `data.warning` alongside the existing public-dir/DB/audit-log best-effort cleanup failures (#606); on a `dry_run` call, optional `response_mode=compact` omits `data.content` and `data.backlinks` (the full list), returning `data.backlinks_count` instead so the impact size is still visible without the full source body/backlink details — `data.backlinks_count` is present only on a `dry_run` response (compact or not), never on a real delete's response, since no backlink scan runs for a real delete (#687); `dry_run` also previews `data.generated_assets` (same shape as `list_page_assets`') when the whole bundle would be removed, so an agent can see generated-hero-image cleanup impact before committing (#683); no other top-level payload duplication as of v1.5.7 (#520) |
| `upload_page_asset` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.filename`, `data.path`, `data.content_type`, `data.size_bytes`, `data.sha256`, `data.duplicate_of?` (advisory only), `data.dry_run?`; allowed types png/jpg/jpeg/gif/webp/svg (SVG added #571, superseding the earlier deferral, #348) — png/jpg/jpeg/gif/webp are validated by sniffing the actual bytes against the declared extension (`http.DetectContentType`, which has no SVG signature); `.svg` is instead validated by a strict structural allowlist parser (only allowlisted shape/text/gradient/reuse elements and attributes; `<script>`/`<style>`/`<foreignObject>`/`<image>`/SMIL `<animate*>`, `on*` event-handler attributes, DOCTYPE/entity declarations, and any `href`/`xlink:href` that isn't a local `"#id"` fragment reference are all rejected outright as `invalid_svg`, never silently stripped; any `url(...)` reference inside `fill`/`stroke`/`clip-path`/`mask` is likewise restricted to a local fragment `url(#id)`, never an external host, #626) — never overwrites (`already_exists`); root `rate_limit_remaining` reports the caller's real remaining budget on the shared create/update/upload quota, on both success and error responses (#466, #510); no other top-level payload duplication as of v1.5.7 (#520) |
| `delete_page_asset` | structured | `data.status`, `data.slug` (canonical public form, #554), `data.source_key`, `data.filename`, `data.sha256`, `data.dry_run?`, `data.referenced?` (pointer — present as `false` on success, omitted on error, so "not referenced" and "never checked" stay distinguishable), `data.referenced_in?`; requires `expected_sha256` or `expected_revision` on non-dry-run calls (a mismatch fails `revision_conflict`); fails `asset_referenced` if the filename is still linked from the page body, unless `force=true`; `dry_run` previews `data.sha256`/`data.referenced` without requiring the concurrency guard or deleting anything; root `rate_limit_remaining` reports the caller's real remaining budget on `delete_page`'s own destructive quota, on both success and error responses (#460, #510). Only removes the source asset — unlike `delete_page`, it does not purge any built public copy or CDN cache; the asset stays reachable at its old URL until the next build; optional `scope: "generated"` explicitly retargets the call from the default bundle-local file to the `generate_hero_image`-generated `{HugoRoot}/static/images/{slug}-featured.jpg` instead — `data.scope`/`data.path`/`data.kind` echo which target was resolved; omitting `scope` (or passing `"bundle"`) keeps the existing bundle-local behavior unchanged (#683). Optional `owner` is advisory metadata only and never changes authorization or destructive quota behavior. No other top-level payload duplication as of v1.5.7 (#520) |
| `get_mutation_status` | structured | `data.tool`, `data.idempotency_key`, `data.status` (`"succeeded"`/`"unknown"`), `data.result?` — a read-only lookup of a prior `idempotency_key`-bearing `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` call, for recovering from a timeout/ambiguous response without resending the original payload; `data.result` (present only when `status: "succeeded"`) is the *entire* original response envelope (`success`/`data`/`errors`/`warnings`/`meta`), not just its inner `data` — the same shape a same-key/same-payload retry of the mutation tool itself would replay via its own idempotency cache. `status: "unknown"` covers still-in-flight, genuinely failed, expired (idempotency-key TTL, shared with the underlying idempotency cache; defaults to 15 minutes, configurable via `idempotency_ttl_seconds` in server config — a deployment-level setting only, never a per-call parameter, since a caller-supplied TTL could be used to shorten the window and evade duplicate-submission protection, #616), or never-attempted equally — only successful calls are ever recorded, so this is never proof of failure. Requires `content.write` (#586) |
| `get_rate_limits` | structured | `data.create_update_upload`/`data.destructive`, each `{remaining, limit, retry_after_seconds}` — the caller's current budget on both independent per-caller mutation quotas (`create_update_upload` shared by `create_page`/`update_page`/`upload_page_asset`/`apply_content_plan`/`rollback_change`; `destructive` shared by `delete_page`/`delete_page_asset`), reusing the exact same `callerLimiter`/`rateLimitRemaining` machinery those tools already populate their own `rate_limit_remaining` from (#378, #466) — a caller previously had no way to check quota before acting, only after a mutation call already reported `rate_limit_remaining`. `retry_after_seconds` is `0` if a call would succeed right now. Calling this tool is a pure read — it never itself consumes either quota. Requires `content.write`, the same trust level as the mutation tools whose quota it reports (#614) |
| `plan_content_change` | structured | **Requires no scope** — planning never writes (#450), unlike every other tool in this table. `data.target` (`slug`, `resolved_source_path`, `revision`, `state`), `data.operations_applied[]`, `data.operations_rejected[]` (`op`, `reason` — an operation that doesn't apply cleanly, e.g. `remove_tag` for a tag the page doesn't have, is reported here without failing the whole plan), `data.diff`, `data.estimated_diff` (`lines_added`, `lines_removed`), `data.plan_id`, `data.plan_expires_at` (5-minute TTL), `data.requires_confirmation` (informational only — `apply_content_plan` requiring a separate call is the actual enforcement). Takes `slug`/`lang` plus an `operations[]` list from a deliberately small vocabulary — `update_body`, `set_title`, `add_tag`/`remove_tag`, `add_category`/`remove_category` (computed as a delta against the page's current tags/categories, not a full-list replacement like `update_page`), `set_draft`, `set_field` (`field: "description"` only). A page still carrying `test_content: true` cannot be planned into `draft:false`; that invariant is validated during planning, not left to build time (#728). No multi-page plans, no general JSON-patch. Design anchor: `docs/transactional-edit-design.md` (#338) (#438) |
| `apply_content_plan` | structured | `data.status`, `data.plan_id`, `data.slug`, `data.dry_run?`, `data.before_revision`, `data.after_revision?`, `data.validation`, `data.warning?`, `data.state?`; root `rate_limit_remaining` on the shared create/update/upload quota, on both success and error (#466, #510). Takes only `plan_id` (+ `idempotency_key`/`dry_run`) — no body/title/tags resent, apply writes exactly what the plan already computed. Fails `plan_not_found` if `plan_id` is unknown, already applied, or its TTL expired; fails `revision_conflict` if the page changed since the plan was created. Content that still carries `test_content: true` is revalidated here as well and cannot be applied in a `draft:false` state, even if a stale or externally-crafted plan attempts it (#728). **A plan is single-use after a terminal apply attempt**; retryable revision conflicts and transient content-lock/build failures preserve it for retry or re-planning. `dry_run` re-verifies without consuming it. On a successful write, the pre-write content is snapshotted (24h TTL) keyed by the revision it replaced, for `rollback_change` to consume. Deliberately writes source only — no build/publish fields; that is `publish_changes`'s layer (#340), a separate, later, explicitly-confirmed step (#438) |
| `rollback_change` | structured | `data.status`, `data.slug`, `data.dry_run?`, `data.diff?`, `data.before_revision`, `data.after_revision?`, `data.warning?`, `data.state?`; root `rate_limit_remaining` on the shared create/update/upload quota, on both success and error (#466, #510). Restores a page's source to a prior revision `apply_content_plan` *or* `update_page` itself snapshotted (**amended #379**, 2026-07-24: not a git-commit target — this deployment has no controlled git-commit capability, so the rollback target is a server-held snapshot keyed by `(resolved file, revision)`, scoped to revisions produced by one of these two write tools, not arbitrary git history; extended from `apply_content_plan`-only to also cover `update_page` in #629, since `update_page` remains the primary write tool most edits actually use). `create_page` is not snapshotted — there is no meaningful pre-create state to roll back to. Takes `slug`/`lang`, `to_revision` (the target snapshot's revision), and (non-`dry_run`) `expected_revision` — a stale value fails `revision_conflict`, the same optimistic-concurrency guard every other write tool uses, so this can never silently undo a newer, unrelated change. Fails `snapshot_not_found` if no snapshot exists for that revision of this page. Re-validates the snapshot content against the same blocked-shortcode denylist `create_page`/`update_page` enforce (#590) before restoring it — a snapshot may predate that denylist, so restoring one without re-checking would be a way around a policy direct writes now enforce; fails `invalid_params` if the snapshot itself invokes a blocked shortcode. Unlike a plan, a snapshot is **not** consumed on use — `IdempotentHint: true`, rolling back to the same revision twice is safe. `dry_run` previews the diff without writing. Design anchor: `docs/transactional-edit-design.md` (#340) (#438, #629) |
| `build_site`              | flat     | `status`, `duration_ms`, `build_id`, `output_revision`, `publish_ready`; `data.X` mirrors all five additively (#572) — this was the last tool with zero envelope at all (not even root-level duplication) before this change |
| `preview_build`           | structured | `data.status`, `data.duration_ms`; no root-level duplication as of #1118 (root aliases #552 originally added and #1060 deprecated are removed) |
| `run_post_build_hooks`    | structured | `data.results`, `data.status`, `data.configured_count`; no root-level duplication as of #1118 (root aliases #552 originally added and #1060 deprecated are removed). `dry_run:true` returns the configured hook targets without contacting them, alongside `configured_count`, so callers can distinguish `no hooks configured` from `hooks configured but intentionally not executed` (#760) |
| `generate_hero_image` | structured | `data.path`; `slug` accepts either the canonical public form (`/posts/example/`) or the source-key form (`posts/example`) and normalizes any language-prefixed public slug to the same source key before writing, so generated-asset lifecycle tools keep one stable identity; `style` accepts `""`/`tech`/`geo`, validated in the handler with a structured `invalid_params` error (`code`/`resolution`, #892). As of #1056, `tools/list` also advertises `style` as a JSON-Schema `enum: ["tech", "geo"]` via a reusable `AdvertiseInputEnum` decorator (`internal/tools/advertised_schema.go`) that clones only the outgoing `tools/list` copy of the tool — the SDK-held validation schema used by `tools/call` stays permissive, so an out-of-enum value still reaches the handler and returns the same structured `invalid_params` envelope #892 established, never the SDK's bare-text pre-handler validation error. The named styles control fallback gradient/accent treatment; when bundled Unsplash backgrounds are available, one of six backgrounds is selected deterministically from the title and is not a separate style value. `path` is hugo_root-relative, never the host's absolute filesystem path (#551); no root-level duplication as of v1.5.9 (#573) |
| `check_sri_versions`      | structured | `data.files_scanned`, `data.files_with_sri_attributes`, `data.sri_entries_loaded`, `data.sri_checked`, `data.status`, `data.summary`, `data.findings`; no root-level duplication as of #1118 (root aliases #552 originally added and #1060 deprecated are removed) |
| `get_runtime_status`      | structured | `data.release_version`, `data.commit`, `data.hugo`, `data.git` (includes `changed_files_count` when `dirty: true`, a count of `git status --porcelain` lines; a safe aggregate never exposing paths; a `dirty_reason` mcp-vs-external classifier was considered per #775, but the only comparable existing signal — `index_staleness.likely_source`'s `mcp_pending_build`/`external_or_unknown` on the read tools, #583/#617 — documents itself as a coarse, best-effort hint, not per-caller attribution; reusing that same best-effort standard for git-dirty provenance risked exactly the "looks precise but isn't trustworthy" outcome the issue warns against, so `dirty_reason` was deliberately deferred rather than shipped on a shakier guarantee), `data.site`, `data.degraded`; `data.source_ahead_reason` explains whether the source is ahead because of `pending_mcp_changes`, `out_of_band_source_drift`, `generated_asset_drift`, or `none`; `data.publication_state` is the corresponding machine-readable state (`pending`, `source_drift_only`, `generated_asset_drift`, `clean`), making `source_ahead_of_public:true` with zero server-known pending pages explicit; when disposable `test_content` has expired, `data.site.overdue_test_content[]` exposes a machine-readable cleanup/advisory list (`slug`, `owner?`, `expires_at`, `overdue_seconds`, `reason`) without requiring a build/publish call first (#757) |
| `get_theme_status`        | structured | `data.themes[*]`, `data.hugo`         |
| `get_hugo_update`         | structured | `data.installed`, `data.latest?`, `data.network_checked`, managed-upgrade capability and platform |
| `stage_hugo_upgrade`      | structured | exact target/archive, dry-run state, official checksum and logical staged artifact |
| `activate_hugo`           | structured | target/previous version, activation state, restart requirement and operator action |
| `rollback_hugo`           | structured | restored version, rollback state, restart requirement and operator action |
| `bootstrap_hugo`          | structured | `data.detected_version`, staged/activated state, checksum, restart requirement and operator action |
| `verify_publication`      | structured | `data.source/build/public/index`, `data.http_status`, `data.status`, `data.explanation` |
| `create_preview`          | structured | `data.preview_id`, `data.url`, `data.expires_at`, `data.build`; no root-level duplication as of v1.5.9 (#573) |
| `publish_changes` | structured | `data.status` (`"published"` only when the build succeeds cleanly — no failed post-build callback — *and* `data.publication.status` is `"fresh"`; otherwise `"build_succeeded_unverified"` — a partial-success build, e.g. a failed CDN purge, never reports `"published"` even if local file/HTTP state happens to read fresh), `data.build` (`build_id`, `duration_ms`, `output_revision?`, `warning?` — the same fields `build_site` returns, nested), `data.publication` (the full `verify_publication` response shape, nested verbatim — not summarized). A failed build surfaces as a tool error (`build_error`/`build_in_progress`), identical to `build_site`'s own behavior; it never reaches `data.status`. Bundles `build_site` + `verify_publication` into one explicit, separately-confirmed step (#340) — never auto-chained onto `apply_content_plan`/`update_page`. Takes `slug` (required — `verify_publication` checks one page) and optional `wait_seconds` (forwarded as-is). Writes only build output and derived indexes, never page source (#438) |

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

**Structured `data.rate_limit` (#690)**: every mutation tool (`create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`, `apply_content_plan`, `rollback_change`) now also returns `data.rate_limit` — `{remaining, limit, window_seconds, refill_rate_per_second, scope, reset_at, retry_after_seconds}` — on both success and error responses, alongside the pre-existing scalar `rate_limit_remaining`/root `rate_limit_remaining` (kept unchanged for v1.x compatibility, never removed). `scope` is `"create_update_upload"` or `"destructive"`, matching which of the two independent budgets above applies to that call. The runtime uses a token-bucket limiter, not a fixed window: `window_seconds` is the nominal refill period (`60`), `refill_rate_per_second` is the continuous refill rate (`limit / window_seconds`), and `reset_at` is derived as "the earliest timestamp a next call would succeed" (`now` when the budget isn't currently exhausted, `now + retry_after_seconds` otherwise), not a scheduled reset time. `get_rate_limits` (#614) now returns the same enriched bucket shape for its own `data.create_update_upload`/`data.destructive` fields. Quota behavior itself is unchanged — this is reporting/contract only.

**Canonical source of truth (#852)**: the structured `data.rate_limit.remaining` is the **canonical** remaining-budget field for all future callers. The scalar root-level `rate_limit_remaining` is a **legacy, deprecated mirror** kept only for v1.x backward compatibility on the error path; it is never the authoritative value and MUST NOT diverge from `data.rate_limit.remaining`. Both are always derived from the same `rateLimitRemaining(limiter)` read at the same instant, so they cannot drift; `TestRateLimitScalarMirrorsStructuredRemaining` (`internal/tools/write`) is a standing invariant test asserting the two stay identical wherever both appear on a response. **Phased deprecation plan**: (1) *now* — document canonicality, add the invariant test (this issue); (2) *next major* — mark the scalar deprecated in tool descriptions and stop populating it on newly-added tools; (3) *v2* — remove the scalar entirely, leaving `data.rate_limit` as the sole surface. New tools SHOULD emit only `data.rate_limit` and skip the scalar.

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
| `content_not_public` | no (deliberate) | overloaded across two meanings in this codebase — a "draft/source-only content hidden from a public-safe reader" path and a "diagnostics sub-feature unavailable to the reader profile" path. Both are unreachable in the live runtime: every site is gated on `site.IsReaderProfile`, which can never be true now that `CanonicalScope` resolves any `"reader"` input to `"read"` before it reaches that check (see the "Dormant machinery" note below). Kept without a hint because reviving either meaning would need a real decision about what a narrower profile should return, not a generic retry action |
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

Two additive fields. #419 itself was presentation-only: it exposed
`taxonomy_inconsistency_details[*].severity` and `score_breakdown` without
changing the underlying score formula. Later follow-up issues refined only
the *meaning* of the exposed top-level score/status when taxonomy drift is
present:

- Each entry in `taxonomy_inconsistency_details[*]` now carries a
  `severity`: `"info"` (`translation_pair` — the site's own localization,
  never counted as an issue) or `"warning"` (`alias_mismatch`/
  `possible_duplicate`/`casing_variant` — counted as an issue). Info-only
  findings still never penalize the top-level `score`; warning findings are
  still zero-weight in `score_breakdown.taxonomy.weight`, but #719 caps an
  otherwise-perfect top-level score at `99` so the payload no longer says
  "100/healthy enough" while also surfacing actionable drift.
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
  reference, and never directly feeds weighted points into the top-level
  `score`; the only top-level taxonomy effect is the #719 perfect-score cap
  described above).
- `score_breakdown.title_shape` (#1105) also carries weight 0, but — unlike
  taxonomy's weight 0 — is not purely informational: `data.bad_title_shape_pages`
  lists slugs whose title field is a bare `http(s)` URL instead of actual page
  text (a corrupted-title defect a frontmatter-presence check can't see, since
  the field is non-empty; see arleo.eu's #1099-adjacent incident, where
  `get_site_health` kept reporting `status: "healthy"`/`score: 100` through
  exactly this). Whenever that list is non-empty, `title_shape.score` reports
  `0`, and — independent of the weighted `score` calculation — `status`/
  `content_status` are forced off `healthy`/`healthy_with_advisories` and an
  otherwise-perfect `score` is capped at `99`, the same #719-established
  pattern as taxonomy's cap. Do not read `title_shape`'s weight 0 as
  "harmless" the way taxonomy's is.

- `score_breakdown.broken_links` (#1105) resolves this issue's own open
  design question — closing it deliberately as a **status override**, not as
  weighted scoring: broken-link volume never feeds the weighted `score`
  arithmetic directly (weight 0, like `title_shape`), and this tool never
  duplicates `get_broken_links`'s own link-resolution logic — it only reacts
  to that count. This avoids the "quiet coupling" #1101 already showed drifts
  easily between the in-memory and `db_path`-backed broken-link checkers.
  **Only ever computed when `db_path` is configured** (`get_broken_links`'s
  own O(1) pre-computed link graph, `db.GetBrokenLinks()`) — without
  `db_path`, computing this would mean paying `get_broken_links`'s
  full-HTML-rescan cost on every `get_site_health` call, not just an
  explicit one, which would make this tool's own stated purpose ("use this
  before publishing") noticeably more expensive on every reader-profile
  deployment. Both `data.broken_links_count` and
  `score_breakdown.broken_links` are entirely **omitted**, not a `0`, when
  not computed — a present `0` means "checked, clean," matching the same
  distinction `data.untracked_source_pages` already establishes for the same
  reason. When computed and nonzero: `score_breakdown.broken_links.score`
  reports `0` (binary, same as `title_shape` — a broken link is a real
  content defect with no natural severity gradient), and `status`/
  `content_status` are forced off `healthy`/`healthy_with_advisories` with
  an otherwise-perfect `score` capped at `99`, the same #719-established
  pattern.

`score_breakdown` covers `frontmatter`, `taxonomy`, `title_shape`, and
(when `db_path` is configured) `broken_links` — the categories this server
computes a real signal for today. It omits `rendering`/`publication`
placeholders an earlier proposal sketched; publishing a fabricated 100 for a
category with no underlying check would be more misleading than omitting it.

Behavior summary today:

- `translation_pair` / `info` findings: `score` unchanged, `status` may
  still become `healthy_with_advisories`
- warning-severity taxonomy drift: `score_breakdown.taxonomy` shows the
  local issue, `status` becomes `healthy_with_advisories`, and a would-be
  perfect top-level `score: 100` is capped to `99`
- frontmatter issues still drive the underlying weighted score as before
- a URL-shaped title (#1105): `score_breakdown.title_shape` shows `0`,
  `bad_title_shape_pages` lists the affected slugs, `status`/
  `content_status` are forced off `healthy`/`healthy_with_advisories`
  regardless of the weighted score, and a would-be perfect `score: 100`
  is capped to `99`
- nonzero broken links, `db_path` configured (#1105): same treatment —
  `score_breakdown.broken_links` shows `0`, `data.broken_links_count`
  reports the figure, `status`/`content_status` forced off healthy, `score`
  capped to `99`. Without `db_path`, both fields are simply absent — no
  status effect, since nothing was actually checked

### Publication counter populations (#992)

The publication counters intentionally describe different populations and
therefore are not expected to be equal:

- `source_pages` counts every indexed source document, including ordinary
  content, drafts, headless/future/expired content, and `_index` documents.
- `publishable_source_pages` is the backward-compatible count of ordinary
  source content expected to resolve to its own public page. New clients should
  prefer the identically valued, clearer `publishable_content_pages`.
- `section_index_pages` counts `_index.md` and `_index.<lang>.md` source
  documents. They are excluded from publishable ordinary content because their
  route is the containing section. The rendered section route is classified
  separately and is not assumed to contribute to `published_pages`.
- `published_pages` counts routes classified as rendered content in the public
  index. It is an independent public population and can include routes that do
  not match the publishable ordinary source population; clients must not infer
  that the numerical difference corresponds to `_index` sources.
- `missing_public_pages` checks only publishable ordinary content.
  `public_output_complete:true` means that count is zero; it does not assert
  that `source_pages == publishable_source_pages == published_pages`.

`publication_coverage` repeats these populations in one typed object:
`source_documents`, `publishable_content_sources`, `section_index_sources`,
`other_excluded_sources`, `published_content_pages`,
`missing_publishable_content_pages`, `completeness_basis`,
`counters_directly_comparable`, and `complete`. `completeness_basis` is
`publishable_content_sources`; `counters_directly_comparable` is `false` because
the source categories and rendered public population are independently
classified. This is the preferred surface for new agents because an
82-source/80-publishable/82-published shape is explicit without claiming that
two equal residual counts represent the same documents.

### `status: "healthy_with_advisories"` (#681)

A follow-up live audit found the `score`/`status`-never-moves guarantee
above made `status` misleading on its own: a site could show
`score: 100, status: "healthy"` while `advisories_count` was non-zero, and
an agent that only reads `status` at a glance had no signal to look
further. `get_site_health` now reports `status: "healthy_with_advisories"`
instead of `"healthy"` whenever `advisories_count > 0` and the site would
otherwise be healthy — presentation only, same as #419: `score` and
`score_breakdown` are unchanged, and a `warning` vs. `info` severity
finding still affects `status` identically (both count toward
`advisories_count`, which is all that drives this status value).

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

## 6.12. 3-Scope Model: `read`/`write`/`admin` (#450)

Collapses the pre-#450 4-tier scope model (`reader`, `content.read`,
`content.write`, `site.admin`) down to two scopes, then reintroduces a third,
strictly-additive `admin` tier on top (extended by #1039/#1050) — not a
return to the old 4-tier split, since every existing `read`/`write` caller
keeps working unchanged:

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
  versa. Managed Hugo binary lifecycle tools are now the explicit `admin`
  tier; legacy `site.admin` and `system.admin` inputs now resolve to `admin`
  (see the alias table below), not `write` — already-issued tokens carrying
  those strings retain the full capability they originally had, rather than
  being silently downgraded.

- **`admin`** — requires an explicitly approved administrator OAuth client.
  It implies `write` and is required for `stage_hugo_upgrade`,
  `activate_hugo`, `rollback_hugo`, and `bootstrap_hugo`.

`revoke_all_previews` remains write-scoped: its bulk operation calls
`RevokeAllOwned` and only revokes previews belonging to the current caller.
It is not a cross-tenant administrative capability.

`tools.KnownScopes` is now `{"read", "write", "admin"}`; `tools.ScopeRank` gives
`read` the same rank (0) as anonymous — capability-identical, matching the
"no gate" decision above — `write` rank 1, and `admin` rank 2.
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
| `write`, `content.write` | `write`   |
| `site.admin`, `site_admin`, `siteadmin`, `system.admin`, `system_admin`, `systemadmin` | `admin`   |
| `admin`                                                        | `admin`   |

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

## 6.15. Change-Set Attribution for Mutation Ownership (#1135)

`principal_id` (the OAuth identity `mutationCallerKey`/rate limits/idempotency
already scope everything to) is not fine-grained enough to answer "did *I*
write this, or did someone else sharing my credentials?" — a realistic shape
on a single-operator deployment where two distinct clients (e.g. two
different agents) are configured with the same OAuth token. This is the
scenario behind the 2026-08-14 incident that motivated this feature: a
second agent editing concurrently with no way to distinguish its changes
from the first's.

**`create_change_set`** mints a new opaque `change_set_id` (format `cs_<32
hex chars>`), owned by the calling principal. Requires no input. Both
ownership and every mutation's attribution record are tracked in-memory
unconditionally — neither the ownership-check nor the attribution-recording
property depends on `db_path` being set. SQLite persistence (best-effort,
only when `db_path` is configured) is asymmetric: ownership is lazily
rehydrated from SQLite after a restart, so it survives one. The per-mutation
attribution list does not — it is process-lifetime only in memory; the
durable record after a restart is SQLite's `change_set_mutations` table
directly (queried via `ListChangeSetMutations`), not an in-memory replay.
Any future feature that needs mutation history to survive a restart must
read that table rather than assume the in-memory list rehydrates.

Every mutation tool — `create_page`, `update_page`, `delete_page`,
`create_bundle`, `delete_bundle`, `upload_page_asset`, `delete_page_asset`,
`apply_content_plan`, `rollback_change`, `apply_bundle_plan`,
`rollback_bundle` — accepts an optional `change_set_id`:

- **Omitted** (every caller before this feature, and any caller that never
  adopts it): the mutation is attributed to a stable implicit
  per-principal default (`default:<principal_id>`) — a pure additive
  change, zero behavior difference for existing clients.
- **A valid id this principal created**: the mutation is attributed to that
  change-set instead.
- **Unknown, or belonging to a different principal**: rejected with
  `invalid_params` before any write — the same error either way, so a
  caller can never learn whether a given id belongs to someone else, only
  that it isn't usable by them.

This issue establishes the ownership primitive and per-mutation attribution
record (`change_set_mutations` table) only. It does **not** yet change what
`build_site`/`publish_changes` are allowed to publish — a foreign
change-set's unpublished work is not blocked from being swept into a build
by this issue alone; that guard is #1140, which depends on this primitive.
`get_runtime_status`/`get_mutation_status` do not yet surface change-set
state either — that's #1142.

### 6.16 Foreign-Change-Set Guard on Build/Publish (#1140)

`build_site` and `publish_changes` both accept the same optional
`change_set_id` #1135 introduced for mutation tools. Because a Hugo build
renders the entire content tree — there is no mechanism to build one
change-set's pages and not another's — the guard is a pre-flight refusal,
not selective publishing: if any page currently pending a build is known
(to this same running server process) to belong to a change-set other than
the one this call resolves to, the build is refused entirely with
`foreign_change_set_present`, listing the offending page(s). This is the
direct fix for the 2026-08-14 incident that motivated #1135/#1140: two
agents sharing one OAuth principal, one bumping Hugo while the other
concurrently edited `posts/csp-nonce`, corrupting it.

Omitting `change_set_id` resolves to the caller's implicit per-principal
default, matching every mutation tool's own omitted-`change_set_id`
behavior — so two agents that never adopt `create_change_set` at all still
share one bucket and are not distinguished from each other, exactly as
before this feature existed. The guard only protects agents that have
explicitly adopted separate `change_set_id`s, the same opt-in shape #1135
established.

**What this guard does not do**, deliberately: it cannot tell "no
change-set this process tracked ever touched this pending page" apart from
"this page was edited outside the MCP server entirely" (a direct
filesystem/SSH write) or "was edited by a change-set from before this
process last restarted" — #1135's mutation-attribution record is
in-memory-only and never rehydrates after a restart (see
`internal/changeset.Registry`'s own doc comment). An unowned pending page
is therefore always allowed through unguarded. This is **not** full
external-source-drift detection; that needs the per-file fingerprinting
#1141 adds. Until #1141 lands, `foreign_change_set_present` only fires for
concurrent edits made by two change-sets both tracked live, within the same
process uptime — the exact incident shape #1140 targets, not a general
drift detector.

## 7. New tools (v1.3.8+)

New tools added in v1.3.8 use the **structured envelope** by default.
