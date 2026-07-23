# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added
- **`get_backlinks`/`get_related_content`/`get_broken_links` (in-memory fallback path) expose `data.index_staleness`** (#583): populated only when the in-memory site index is behind on-disk content — e.g. a manual `hugo` build or direct filesystem edit that bypassed `build_site`/`create_page`/`delete_page` (the only paths that refresh the index). Absence of the field means the index reflects current source. Detected via a cached, stat-only disk walk (30s TTL) to avoid re-walking the site on every read call.
- **`validate_frontmatter`/`validate_site` expose `data.test_content_slugs`** (#584): a slug whose last segment starts with `mcp-audit-`, `test-audit-`, or `codex-` (case-insensitive — a known subset of the throwaway-content prefixes observed in this project's own audit history, deliberately excluding bare `test-`/`audit-` so real content like a published security-audit article isn't misclassified) is listed here, so it surfaces during routine validation instead of only being caught by an external audit days later. Advisory only — never affects `data.invalid`, per-page `issues`, or `data.status`.
- **`get_site_health` exposes `data.advisories_count`** (#591): the total count of taxonomy findings across *both* `info` and `warning` severity, at the top level next to `score`/`status` — deliberately broader than `score_breakdown.taxonomy.advisories` (info-only), so a `casing_variant`/`alias_mismatch`/`possible_duplicate` finding is just as visible as a `translation_pair` one. Not a scoring change — `score_breakdown.taxonomy.weight: 0` staying zero is correct and unchanged; this only fixes a discoverability gap where an agent reading just `status`/`score` at a glance had no way to notice pending findings without deliberately drilling into `score_breakdown.<category>`.
- **New tool `get_mutation_status`** (#586): a read-only way to ask "did my last `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` call actually land" after a timeout or otherwise ambiguous response, using the same `idempotency_key` — without resending the original mutation payload. Backed by the existing per-tool idempotency cache (15-minute TTL), so it only ever confirms a *successful* call; a failed/still-in-flight/expired/never-attempted key all report the same `status: "unknown"`, which is never proof of failure — retrying the original call with the same key is always safe regardless. Requires `content.write`.

### Fixed
- **`create_page`/`update_page`/`upload_page_asset`'s `dry_run` no longer consumes the shared create/update rate-limit quota** (#588). A sweep triggered by #575 (which had verified this invariant for `delete_page_asset` only) found these three tools called the rate limiter's `Allow()` before checking `dry_run`, unlike `delete_page`/`delete_page_asset` — so a caller previewing a mutation with repeated `dry_run` calls was silently burning the real budget it would need for the actual write. Fixed so all five `dry_run`-capable tools consistently defer `Allow()` until after the `dry_run` early return.

## [v1.5.9] - 2026-07-20

Follow-up from ChatGPT's and Claude.ai's independent live audits of v1.5.8 (both 9.2/10, 31/31 and 25/41 tools tested — no failures, refinement items only).

### Changed
- **BREAKING: finished #520's root/data de-duplication on `create_preview` and `generate_hero_image`** (#573): both tools gained their envelope slightly later than the 5 mutation tools #520 originally covered (via #552, in the same release cycle), and were missed by that convergence. Their success payload now lives only under `data.*` — `preview_id`/`url`/`expires_at`/`build` (`create_preview`) and `path` (`generate_hero_image`) are no longer mirrored at the root. Callers reading those fields at the root must switch to `data.*`.
- **`build_site` now uses the standard structured envelope** (#572): the last tool with zero envelope at all (not even root-level duplication) — success/error responses now carry `success`/`data`/`errors`/`warnings`/`meta` like every other tool. Existing flat fields (`status`, `duration_ms`, `build_id`, `output_revision`, `publish_ready`) are kept as root compatibility aliases — additive, not breaking.
- **`get_site_health` detects same-language taxonomy casing variants** (#577): a new `casing_variant` finding kind catches e.g. `Infrastructure`/`infrastructure` used within the same language — a blind spot `possible_duplicate`/`translation_pair` structurally couldn't see, since `taxonomy.Slug()` already lowercases before either of those checks ever runs. This will surface new findings on sites with existing casing drift; `score`/`status` are unaffected (taxonomy findings never move the top-level score).

### Fixed
- **`validate_site` exposes `data.status`** (`"valid"`/`"invalid"`, #568) instead of requiring callers to derive validity from an empty `pages` list plus a counter.
- **`list_page_assets` now returns `sha256`** (#574), matching what its own description already promised as the way to get the current hash for `delete_page_asset`'s `expected_sha256` guard.
- **`list_page_assets` adds `data.hint` when `data.assets` is empty** (#569), clarifying the tool only covers page-bundle sibling files, not the site's global static assets a page may still reference.
- **`list_pages`/`search_pages`/`get_recent_posts`/`get_sitemap`/`get_feed` expose `source_key`** alongside `slug` (#576), matching `get_page`/`get_page_frontmatter`, so a browsing result can feed directly into a write tool's `slug` input without guessing the expected format.

### Docs
- **`get_feed`'s description now states it's site-wide**, not posts-only (#570) — use `get_recent_posts` for posts-only.
- **`dry_run` quota semantics clarified** (#575): investigated a live audit's observation that `delete_page_asset`'s `dry_run` appeared to consume its destructive quota. A regression test proves it does not — the observed drop is consistent with normal token-bucket refill timing between an earlier real call and the next observation, not a leak. No code change; documented in `docs/mcp-contract.md` §6.4.

## [v1.5.8] - 2026-07-19

**BREAKING.** Follow-up from the v1.5.6/v1.5.7 "ChatGPT tool disabled" incident report (confirmed by production nginx/mcp log audit to be a client-side connector safety trip, not a server regression — zero 5xx/429/unexpected-4xx responses to the ChatGPT connector across the full log history) plus an explicit maintainer field-naming request.

### Changed
- **BREAKING: `meta.server_version` renamed back to `meta.release_version`** (#563, PR #566), at explicit maintainer request. Same value, same always-populated semantics (release tag on a release build, `main-<sha>` otherwise) — only the JSON key name changed. Callers reading `meta.server_version` must switch to `meta.release_version`.
- **`release_version` is now frozen.** This is the fourth change to this one field across four releases (v1.5.5 add → v1.5.6/v1.5.7 merge → v1.5.8 rename back) — churn that an external client audit flagged as a contract-stability problem. The name and semantics will not change again without a major version bump; see `docs/mcp-contract.md` §5.

### Investigated
- **"ChatGPT tool disabled" incident (#565): confirmed not a server-side regression.** Full nginx access/error log review across the incident window and full history shows zero anomalous responses to the ChatGPT connector — the identical healthy request pattern (200 → 202 ack → 200 payload → 499 client-closed SSE) repeats cleanly through all 35 tool-call cycles in the flagged session, with no error on the final cycle. The larger cycle count versus prior successful audits (35 vs. 11–15) points to ChatGPT's own connector-side safety circuit breaker tripping, not a passthrough of a proxied error.
- **nginx `mcp_oauth` rate-limit zone reviewed** (#564): current limits (30r/m, burst 20 nodelay) were not the cause of this incident, but are tight enough that a somewhat larger legitimate multi-call agent session could plausibly trip them in the future. Hardening tracked separately in #564; no config change shipped in this release.

## [v1.5.7] - 2026-07-19

**BREAKING.** Ships #520's preferred remediation as a patch release rather than waiting for v1.6.0 — the maintainer decided to reverse v1.5.6's deferral note once the fix turned out to be low-risk to implement cleanly.

### Changed
- **BREAKING: mutation root/data payload duplication removed** (#520, PR #562): `create_page`/`update_page`/`upload_page_asset`/`delete_page`/`delete_page_asset` success responses no longer mirror their payload at the root — `data.*` is now the sole payload location, matching the read tools' contract. Two root fields are deliberately kept, not part of this removal: `request_context` (error-path only, #455) and `rate_limit_remaining` (#466/#510/#522), so an agent can still self-regulate pacing from the root alone. **Note:** the v1.5.6 changelog said this was "deferred to v1.6.0" — that plan changed; this ships now, one patch release later, as an explicit breaking change rather than a silent contradiction. Callers reading `create_page`'s (etc.) root-level `slug`/`path`/`new_revision`/`state`/`status`/`warning`/`content`/`dry_run`/`diff`/`backlinks`/`filename`/`content_type`/`size_bytes`/`sha256`/`duplicate_of`/`referenced`/`referenced_in` directly must switch to reading the same fields under `data`.

## [v1.5.6] - 2026-07-19

Fast-turnaround fixes from the v1.5.5 live ChatGPT audit and direct user feedback on the same build-identity fields v1.5.5 just added.

### Changed
- **Write tools' `slug` on success is now the canonical public `/posts/x/` form** (#554, PR #559), matching read tools (#519), instead of echoing the raw source-relative input — applies to `create_page`/`update_page`/`upload_page_asset`/`delete_page`/`delete_page_asset`. `source_key` (v1.5.4, #545) remains the stable source-relative identifier for callers that need to feed a value back into another write tool's `slug` input. **Note:** the v1.5.5 changelog said this was deferred to v1.6.0 — it shipped here in v1.5.6 instead, once bundled with #520 turned out to need less coordination than expected (root-alias removal, #520, is still deferred; only the slug format changed here).
- **`meta.release_version` removed from the contract** (#560, PR #561): it was added in v1.5.5 (#550) to expose the deploy-time release identity, but turned out to be pure duplication — `meta.server_version` already *is* the release tag on a release build (and `main-<sha>` otherwise), with `meta.build_channel` distinguishing the two cases. `server_version` + `build_channel` are now the sole version signal across every tool response and `get_runtime_status`.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520): still needs a documented deprecation window per the v1.x compatibility policy: the root aliases stay in place, not removed.

## [v1.5.5] - 2026-07-19

Fast-turnaround fixes from the v1.5.4 live audits (Claude.ai and ChatGPT), triaged into quick wins (this release) versus breaking changes (deferred to v1.6.0).

### Added
- **`meta.release_version` now honors an explicit deploy-time input** (#550): `deploy.yml` gained a `release_version` workflow_dispatch input that sets `server_version`/`release_version`/`build_channel=release` at deploy time, instead of requiring the git tag to already exist — this repo deploys main, then tags after production validation, so the old exact-tag-only policy could never populate the field for a normal release. `docs/mcp-contract.md` and `docs/operator-guide.md` document the new `-f release_version=vX.Y.Z` deploy → approve → `release.yml` tag sequence.

### Fixed
- **`generate_hero_image` no longer leaks the host's absolute filesystem path** (#551): the returned `path` is now projected to a `hugo_root`-relative logical path (e.g. `static/images/my-post-featured.jpg`), matching the existing convention used for content-root-relative paths elsewhere.
- **`check_sri_versions`/`preview_build`/`create_preview`/`run_post_build_hooks` now return the standard structured envelope** (#552): all four previously returned flat, un-enveloped output; they now embed `toolcontract.ToolResponse[XxxData]` (existing flat root fields kept as compatibility aliases) and route errors through `toolcontract.WrapTool` for consistent structured error responses, matching every other tool.

### Docs
- **Clarified that `response_mode=compact` trims `meta` by design** (#553): ChatGPT's audit flagged `search_pages`'s compact-mode `meta` as looking incomplete; contract tests now prove this is the documented `compact` behavior, not a default-mode regression, and `docs/mcp-contract.md` §5.2 carries an explicit callout so future audits don't re-flag it.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520) and **write-tool `slug` canonicalization** (#554): both touch the same mutation-tool response surface and are breaking changes against the v1.x compatibility policy; drafted together for review, not merged.
- **Execution planner** (#438): still blocked on `plan_content_change`/`apply_content_plan` (#338/#340) maturing first.

## [v1.5.4] - 2026-07-19

Implementation follow-through on the four design proposals locked in v1.5.3, plus the top priority from the v1.5.3 live ChatGPT audit (complex front-matter preservation proof) and a canonical source-identity field.

### Added
- **`check_ai_readiness` tool** (#437, PR #541): deterministic, source-oriented audit of a page's Markdown/frontmatter — heading hierarchy, section/paragraph length outliers, metadata presence, internal-link density, and citation structure. Explicitly does not score SEO, rendered HTML, or build freshness.
- **`response_mode=compact` extended uniformly across the full read/anonymous tool surface** (#526, PR #540): in compact mode, `meta` trims to `schema_version` only (root `generated_at` is preserved for compatibility); every read tool's input schema now advertises the enum, with contract tests covering all 20 tools.
- **`get_page_for_edit` gains `impact` and `preview` as opt-in `include` facets** (#527, PRs #542, #543), alongside the existing `backlinks`: identical data to standalone `get_related_content(include=["impact"])` and `inspect_rendered(include_preview=true)` calls, sharing the same underlying helpers (no forked logic). A page with no rendered public output yet omits `preview` with a warning instead of failing the whole bundle.
- **`source_key` field added across source-aware read and write tools** (#545, PR #547): a canonical, unslashed source-relative identity (`posts/hello`) distinct from the canonical public-route `slug` (`/posts/hello/`), resolving the slug-format ambiguity flagged in the v1.5.3 audit. Write tools expose it as an alias of their existing (already source-relative) `slug` value — additive, non-breaking.

### Fixed
- **Strong regression proof that `update_page` preserves complex front matter** (#544, PR #546): a new end-to-end test exercises nested maps, lists of maps, translations, custom nested fields, and field ordering, confirming the yaml.v3 node-level rewriter leaves untouched fields byte-identical and does not reorder sections — directly answering the v1.5.3 audit's top trust request.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520): the v1.5.3 audit reconfirmed `create_page`/`update_page`/`upload_page_asset`/`delete_page` still duplicate their payload at the root in addition to `data`. Removing the root aliases is a breaking change against the now-documented v1.x compatibility policy (#531) and needs a real deprecation window, not a v1.5.x patch.
- **Execution planner** (#438): remains blocked on the transactional-edit primitives (`plan_content_change`/`apply_content_plan`, #338/#340) maturing first.

## [v1.5.3] - 2026-07-18

Follow-up fixes and contract clarifications from the v1.5.2 live audit, plus a real build-pipeline bug found while investigating one of them. Four adjacent design proposals (compact response mode, pre-mutation bundling, AI-readiness rubric, execution planner) were reviewed and locked as design docs but deferred to v1.5.4 for implementation.

### Fixed
- **`diff_page`/`validate_frontmatter`/`validate_site` now return canonical `/posts/x/`-form slugs** (#519, PR #529), including for Hugo section-index pages, instead of the raw source-relative path.
- **`rate_limit_remaining` no longer reports a stale value inside nested `data` on write-tool errors** (#522, PR #530): the field is now derived from the same value mirrored at the root, so the two can never disagree.
- **`build_site` permission-denied hints now distinguish ownership drift from a missing `ReadWritePaths` entry** (#521, PR #532): a `chtimes ... operation not permitted` failure on an existing output file now points at file ownership specifically, not just the systemd write allowlist.
- **`build_site` now passes `--cleanDestinationDir` to Hugo** (#524, PR #539): previously, output for pages deleted since the last build (stale taxonomy list entries, orphaned static assets) was never removed from `site_root`. Live investigation traced a reported broken link on production not to any current content defect, but to twenty builds' worth of accumulated orphaned output from posts deleted over the prior week.

### Docs
- **Reader tool descriptions and docs no longer overstate bearerless access on OAuth-enabled deployments** (#518, PR #528): `search_pages`, `get_page`, `get_sitemap`, `search_content`, and other `read`-tier tools now say a Bearer token is required when OAuth is enabled, rather than "no authentication required."
- **Write-tool root/data field duplication documented as an explicit v1.x compatibility alias** (#520, PR #531), with regression tests proving root and `data` values can never drift apart.
- **`meta.release_version` mainline-build policy made explicit** (#523, PR #533): a normal `main` deploy reports `server_version=main-<sha>`/`build_channel=main` and omits `release_version` by design; only exact-tag deploys populate it.
- **`IsError` documented as a transport-only MCP signal** (#525, PR #534): the canonical JSON contract is `success`/`errors`, never a mirrored `is_error` field in the structured payload.

### Design (deferred to v1.5.4 — not yet implemented)
- **Compact response mode** (#526, PR #535): locks `response_mode=compact` as the single uniform shaping mechanism for the read surface, trimming `meta` to `schema_version` only.
- **Pre-mutation bundle consolidation** (#527, PR #536): locks `get_page_for_edit` as the aggregation point for `backlinks`/`impact`/`preview`, with an equality invariant against the standalone tools.
- **AI-readiness rubric** (#437, PR #538): locks a deterministic, source-oriented check family (heading hierarchy, section/paragraph length, metadata presence, link density, citation structure) for a future `check_ai_readiness` tool.
- **Execution planner scope** (#438, PR #537): locks the planner as an extension of the existing `plan_content_change`/`apply_content_plan` transactional-edit foundation, not a competing orchestration model.

## [v1.5.2] - 2026-07-18

Security-driven release from a same-day live production audit (external ChatGPT/Codex audit + Claude Code, 2026-07-18) of the v1.5.1 deploy, plus the resulting envelope-contract and observability follow-ups.

### Security
- **P0: public Dynamic Client Registration could mint `write`-scope tokens** (#497, PR #505): `resolveRegistrationScope` inherited the scope of any pre-registered client (e.g. `claude-admin`/`chatgpt-write`) whose redirect URI textually overlapped a DCR request — with no secret and no proof of redirect-URI ownership required. A caller could register a public client with a known privileged client's callback URI and obtain a `write` token directly. Public DCR clients now always get `read`; `write` is only obtainable by authenticating with a pre-registered client's own secret. Verified live (registering with Claude.ai's exact callback now returns `read`, not `write`) and covered by a new regression test proving a known `client_id` cannot mint a token without its secret.

### Changed
- **`/mcp` bearer verification now uses the Go MCP SDK's `auth.RequireBearerToken`** (#473, PR #493), via a local compatibility adapter that preserves the existing `WWW-Authenticate` challenge shape, per-tool JSON-RPC ACL, and scope/audit context enrichment — moves the riskiest parsing path onto the actively-maintained upstream primitive with no observable behavior change for existing clients.
- **Remaining top-level envelope duplication removed from 13 read tools** (#495, PR #511): `get_page_markdown`, `get_page_frontmatter`, `get_related_content`, `build_agent_context`, `export_agent_context`, `get_page_for_edit`, `list_content_types`, `list_page_assets`, `search_content`, `explain_structure`, `get_site_health`, `get_broken_links`, `get_backlinks`, `suggest_links`, `diff_page`, `inspect_rendered`, `validate_frontmatter`, `validate_site` now expose their payload only via `data.X` — continues #433's dedup to the rest of the read surface.
- **Mutation tools now populate `data.X` additively** (#508, PR #512): `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset`/`generate_hero_image` previously left `data` as an empty placeholder with the real payload only at the top level; `data.X` now mirrors the same fields, alongside the unchanged top-level fields (non-breaking, interim state).
- **`meta` now carries release identity** (#509, PR #513): `meta.release_version`, `meta.commit`, and `meta.build_channel` are exposed alongside the existing `meta.server_version`/`meta.schema_version`, so an external audit can confirm which named release it's testing without out-of-band GitHub context.
- **Reader-acquisition discovery metadata now reflects actual deployment mode** (#498, PR #516): `access_profiles.reader.acquisition`/`acquisition_mode` in OAuth discovery metadata previously said "anonymous or self-serve registration" unconditionally; it now derives from the live `oauth.dynamic_client_enabled`/`allow_reader_self_registration` config, so it never overstates bearerless anonymous access when the deployment actually requires self-serve OAuth registration.

### Fixed
- **`rate_limit_remaining` no longer reports a stale/zero value on write-tool error paths** (#510, PR #514): error responses from `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` previously left the field at its Go zero value; it now always reflects the caller's real remaining quota, matching the success-path contract.
- **`get_page`'s source-fallback `html` field is now explicitly labeled** (#502, PR #515): new `html_origin` (`rendered_public`/`source_fallback`/`none`) and `rendered_html_available` fields let a caller distinguish real rendered public HTML from source-fallback content without inferring it from `state` alone.
- **Token response omitting granted scope on silent reader downgrade** (#499): resolved as a side effect of #505 — DCR clients always getting an explicit `read` scope (never the previously possible empty string) means `/token`'s `scope` field is never omitted by `omitempty`.
- **`docs/mcp-contract.md` §1.1 "flat envelope" description corrected** (#496, PR #507): the doc claimed flat tools have no `success`/`errors`/`warnings` fields, which was never true for any tool — clarified that "flat" only ever meant a top-level convenience-field duplicate of `data.X`.

## [v1.5.1] - 2026-07-18

Consolidation release driven by three live connector audits (ChatGPT x2, Claude.ai, 2026-07-17/18), focused on OAuth scope simplification, response-contract cleanup, and token-cost reduction on high-traffic tools.

### Changed
- **OAuth scope model collapsed to 2 tiers: `read`/`write`** (#450, PR #472): replaces the older `content.read`/`content.write`/`site.admin` model. `read` is fully ungated (no client secret needed, includes tools that used to require authentication); `write` requires a registered OAuth client and implies `read` plus every build/admin tool. All older scope strings remain accepted as compatibility aliases. `requestedScope` also now skips unrecognized scope tokens in a multi-scope request instead of rejecting the whole request (#449, PR #471).
- **`validate_site` defaults to invalid-only** (#456, PR #492): a no-argument call now returns only failing pages, instead of every page including all-valid ones — the common case (most pages pass) no longer pays full response cost to confirm nothing is wrong. `include_valid=true` (or `invalid_only=false`) opts into the full listing; `pages_checked`/`pages_passed`/`invalid` are unaffected.
- **Duplicate envelope payloads removed from 9 anonymous tools** (#433, PR #494): `list_pages`, `get_page`, `search_pages`, `get_recent_posts`, `list_tags`, `list_categories`, `get_sitemap`, `get_feed`, `get_site_information` previously carried their payload twice (once under `data.X`, once as top-level convenience fields); `data.X` is now the sole canonical location.
- **Root-level `version` field replaced with `meta.schema_version`** (#454, PR #494): the old `version` field was ambiguous — it read like it could mean the server version, but actually meant the response schema version. That signal now lives unambiguously alongside `meta.server_version`.
- **Deprecated `related`/`suggestions` aliases removed** (#453, PR #494): `related_pages`/`suggested_links` were always canonical; the aliases were kept pending #433/#454's resolution and are now gone.

### Added
- **`get_related_content` pre-mutation impact facet** (#434, PR #500): `include: ["impact"]` returns `taxonomy_orphans` (tags/categories with no other carrier), `sitemap_present`/`feed_present`, and this page's own redirect `aliases` — advisory only, opt-in, never blocks a mutation.
- **`inspect_rendered` pre-publish preview facet** (#435, PR #503): `include_preview: true` composes `diff_page`'s git-diff status, a page-scoped broken-link count (sharing the exact same doc-based scan as the tool's own `internal_links` check, so the two can never disagree), and `validate_frontmatter`'s per-page checks into one `risks` list — instead of chaining three separate calls before publishing.
- **`delete_page_asset` tool** (#460, PR #489): removes a single asset from a page bundle, with hash/revision preconditions, a referenced-by-body guard (bypassable with `force`), dry-run preview, and idempotency-key replay.
- **Rate-limit state surfaced on write tools** (#466, PR #488): `create_page`/`update_page`/`delete_page`/`upload_page_asset` responses report `rate_limit_remaining`; `rate_limit_exceeded` errors carry `resolution.retry_after_seconds`.
- **`get_page_for_edit` backlinks facet** (#465, PR #486): opt-in `include: ["backlinks"]` returns impact-analysis data (pages linking here) in the same call, before a risky edit/delete.
- **Structured empty-result explanations** (#458, PR #487): `get_related_content`/`suggest_links` return `empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) when their result list is empty, distinguishing "no qualifying candidates" from "nothing else exists to compare against."
- **`new_revision` returned directly from `create_page`/`update_page`** (#464, PR #485): usable immediately as `expected_revision` on a following `update_page`/`delete_page`, without an intermediate read.
- **Extended structured error resolution hints** (#461, PR #484) and **preserved request context on write-tool errors** (#455, PR #483).
- **Proactive `build_site` health surfacing** (#467, PR #474) via `get_runtime_status`.

### Fixed
- **`StartupSync` duplicate FTS rows for the same page** (#475, PR #490): a page present in both the public (built) index and the source index was indexed twice, once under each slug form; now deduped, with orphan cleanup for any pre-existing legacy duplicate.
- **`lang` populated immediately on unbuilt pages** (#476, PR #491): `get_page_for_edit`/`get_page_frontmatter`/`build_agent_context` no longer report an empty `lang` for a page read back right after `create_page`, before the next build — it no longer lags behind `resolved_lang`.
- **`get_page` empty slug reports `invalid_params`, not `content_not_found`** (#470, PR #480).
- **`explain_structure` no longer reports language prefixes as sections** (#459, PR #479).
- **Hugo section-index files separated from creatable content types** (#457, PR #478).
- **`search_content`/`get_page_frontmatter` categories regression coverage** (#463, PR #477).

### Docs
- Near-duplicate read tools cross-referenced instead of adding new ones (#436, PR #481).
- Canonical vs. deprecated-alias sibling fields documented (#453, PR #482, superseded by the removal above).

## [v1.5.0] - 2026-07-18

Consolidates v1.5.0-pre1 and v1.5.0-pre2 (see below for full detail) plus two live-production fixes found during connector interoperability testing (Claude.ai, ChatGPT, Le Chat):

### Fixed
- **Live OAuth outage: "reader" scope rejected** (PR #448): `requestedScope`/`normalizeConfiguredScope` didn't recognize the published `reader` scope token, so any client (observed: Claude.ai) that echoed the full advertised `scopes_supported` list back as its `/authorize` request's `scope` parameter had the entire request rejected with `invalid_scope`. Fixed by accepting `reader` as its own distinct canonical scope, kept separate from `content.read` (same rank, different string — `site.IsReaderProfile` and the reader-safe gate key on the literal `"reader"` value).
- **CORS missing on `/register`, `/authorize`, `/token`** (PR #468): these three OAuth endpoints returned a plain 405 with no CORS headers on an `OPTIONS` preflight, blocking any browser-based OAuth client calling them directly via `fetch`/`XHR`. Now matches the CORS policy already used on discovery endpoints.

### Interop
- **Le Chat (Mistral) confirmed working end-to-end** (#424, #341): connects, discovers tools, and completes a full multi-tool session against production. Gemini CLI and GitHub Copilot support is deliberately deferred, not attempted in this release.

See v1.5.0-pre1 and v1.5.0-pre2 below for the full list of underlying fixes and features included in this release.

## [v1.5.0-pre2] - 2026-07-18

Prerelease covering a live connector audit (ChatGPT, 2026-07-17) of
v1.5.0-pre1, plus the Le Chat OAuth discovery fix and write-tool version-
reporting bug found while triaging it.

### Added
- **Structured error recovery hints** (#428, PR #429): `revision_conflict` and `content_not_found` tool errors now carry a `resolution.recommended_tool` (`get_page_for_edit`, `search_pages`) alongside the existing `resolution.action`, so an agent can act on a failure without guessing which tool to retry with.
- **`validate_site` pagination and `invalid_only` filter** (#431, PR #440): `limit`/`offset` paginate the per-page detail rows independently of `pages_checked`/`pages_passed`/`invalid` (which always describe the full scan); `invalid_only` filters the paginated view to failing pages only.
- **Taxonomy cross-language alias detection** (#183, PR #442): `get_site_health`'s near-duplicate tag/category detector now distinguishes a `translation_pair` (the same page bundle tagged in two languages — the site's own localization) from a genuine `possible_duplicate`/`alias_mismatch`, via a new `kind` field. Each finding also carries a `severity` (#419, see below).
- **Published schema `enum`/`maximum` constraints** (#418, PR #443): `search_pages.match`/`response_mode`, `build_agent_context.response_mode`, and every paginated tool's `limit` now publish real JSON Schema constraints in `tools/list`, so a well-behaved client discovers the valid range instead of learning it from a runtime rejection. Deliberately publishes only `maximum` for `limit`, never `minimum` — `clampLimit` treats `limit: 0` as "use the default," a real accepted request, and a `minimum` would break it.
- **`get_site_health` explainable score** (#419, PR #444): additive `score_breakdown` (`{frontmatter, taxonomy}`, each `{score, weight, issues, advisories?}`) and per-finding `severity` (`info`/`warning`) explain *why* `score`/`status` are what they are, without changing their existing formula for any input.
- **Bounded post-mutation publication tracking** (#421, PR #446): `verify_publication` accepts an optional `wait_seconds` (clamped server-side to 20s) to poll internally for build/reindex catch-up instead of requiring multiple round trips; omitting it preserves the original single-check behavior.
- **Docs: `lang` may be empty caveat** (#430, PR #439) on `get_page_frontmatter`, `build_agent_context`, and `get_page_for_edit`, matching the existing caveat on `get_page`.

### Fixed
- **Le Chat MCP server-card discovery** (#424, PR #425): added a `/.well-known/mcp/server-card/mcp` alias and embedded `authorization_servers`/`protected_resource_metadata` pointers in the card itself, after production logs showed Le Chat never reaching the standard OAuth discovery chain. Issue left open pending live re-test.
- **Write-tool `meta.server_version` reported the wrong version** (#426, PR #427): `create_page`/`update_page`/`delete_page`/`upload_page_asset`/image tools were reporting the response *schema* version (`toolcontract.ToolResultVersion`) instead of the actual build/commit version (`buildinfo.Version`).
- **`content_only` no longer includes theme chrome nested inside `<article>`** (#432, PR #441): `ExtractArticleHTML` now prefers `id="content"` over the `<article>`/`<main>`/`<body>` fallback chain, since the LoveIt theme's title/TOC/post-meta/share-buttons/tags/nav live as siblings of the real body inside the same `<article>` wrapper.
- **`inspect_rendered` hreflang check flags an empty `href`** (#420, PR #445): a `<link rel="alternate" hreflang="...">` with no `href` is now reported as incomplete instead of silently accepted; the underlying DOM-based detection was already immune to attribute order/case.

## [v1.5.0-pre1] - 2026-07-17

Prerelease for live OAuth connector testing (Gemini, Le Chat) ahead of v1.5.0.

### Added
- **OAuth flow observability** (PR #412): `HandleRegister`/`HandleAuthorize`/`HandleToken` now emit structured `oauth_register`/`oauth_authorize`/`oauth_token` log lines (`client_id`, redirect URI host, PKCE usage, scope, grant type), correlatable end-to-end by `client_id`, without ever logging secrets (client_secret, auth code, PKCE verifier, tokens). Added to let real connector behavior (which OAuth path a given client actually takes) be reconstructed from server logs.
- **Per-caller mutation rate limits** (#378, PR #422): `create_page`/`update_page`/`upload_page_asset` now share a per-caller-IP budget (`rate_limit.create_update_per_min`, default 60/min), independent of `delete_page`'s existing budget (`rate_limit.destructive_per_min`, now config-driven instead of hardcoded) and independent of the pre-existing per-scope OAuth HTTP rate limiter. A misconfigured `0`/negative limit now clamps to the safe default instead of silently disabling the limiter.
- **Automated path-leak audit + regression test for read-only tools** (#376, PR #423): confirmed no read-only tool (anonymous, `content.read`, or read-only `site.admin`) leaks absolute host filesystem paths, on both success and error response paths. New `internal/contracttests` regression test runs on every `go test ./...`, so future regressions fail CI instead of requiring manual re-audit.
- **Runtime input validation for write tools** (#380, PR #415): `create_page`/`update_page` now validate slug format, title length (255 runes), body size (1MB), and reject null bytes/control characters, before writing to disk. Schema-level (client-side) validation is a documented follow-up (#418), not yet implemented.
- **Git trust model and transactional-edit design docs** (#379, #338, #340, PRs #413, #414): normative documentation of rollback/commit semantics and a full design (not yet implemented) for future `plan_content_change`/`apply_content_plan` and `publish_changes`/`rollback_change` tools.

### Fixed
- **`diff_page`/`get_runtime_status` git dubious-ownership failure** (#416, PR #417): the production git checkout's owner (`jm`) differs from the MCP service account (`mcp-hugo-server-go`), which Git's CVE-2022-24765 mitigation was rejecting outright. New `internal/gitutil` package centralizes all git invocation with a server-resolved `safe.directory`, fixing both tools against the real production repository.

## [v1.4.9] - 2026-07-17

### Added
- **`get_page_for_edit` compact edit-oriented read surface** (#339, PR #408): bundles frontmatter + markdown + lifecycle `state` + `quality` (validity, per-page broken-link count) + `revision` in one call, replacing 2-3 separate reads before an edit. `include` selects a subset; `max_body_chars` truncates the markdown body with a `warnings` entry. `quality.broken_links` scopes the scan to the single page (`site.Index.Classifier()`, a new O(1) cached-classifier accessor, plus a new `brokenLinksForPage` helper extracted from the existing site-wide scan) rather than re-scanning the whole site on every edit. `quality` is omitted for the `reader` profile (source-derived).
- **`list_content_types` content-type/archetype discovery** (#347, PR #409): reports each Hugo content type/section, its archetype template (if any), and expected front matter fields — the union of the archetype's declared keys and the keys actually observed on existing pages of that type, so archetype-less sites (the common case) still get real field guidance instead of an empty list. `page_count` and observed-page-derived fields are omitted for `reader`; archetype metadata (filesystem templates, not page content) remains visible.
- **`list_page_assets` / `upload_page_asset` page-bundle asset management** (#348, PR #410): `list_page_assets` (`content.read`) lists sibling files in a page bundle directory; `upload_page_asset` (`content.write`) writes a new asset into one, with MIME sniffing (never trusts a caller-supplied content type), a 10MB size cap, filename sanitization, exclusive-create (never overwrites), and advisory duplicate-content detection by hash. Allowed types: png, jpg, jpeg, gif, webp — **SVG is intentionally not supported**, since SVG XSS can't be safely neutralized by an allowlist or a hand-rolled sanitizer; that needs a real parser and is deferred to a follow-up. Single-file pages (no per-page directory) are rejected with `not_a_bundle` for both tools. `list_page_assets`'s payload is entirely source-derived (a content-root directory listing); `reader` gets an empty list for a public page rather than an error, and `content_not_public` for a non-public one.

### Fixed
- **`get_site_health` taxonomy inconsistency details now name the affected pages** (#324, PR #407): the existing `taxonomy_inconsistencies` string list explained *that* two tag/category terms looked inconsistent but never *which pages* used them. New additive `taxonomy_inconsistency_details[*]` carries the affected page slugs per finding; the original string list is unchanged for backward compatibility. Omitted for `reader` (source-derived).

## [v1.4.8] - 2026-07-17

### Changed
- **BREAKING: 6 canonical tool names shortened to fit MCP client truncation limits** (#329, PR #405): at least one MCP client connector was observed silently truncating and hash-suffixing tool names of 21+ characters (e.g. `get_full_page_markdown` rendered to the model as `get_ful_7c6ab376aa24`), destroying tool-selection legibility. Renamed in place rather than aliased — MCP clients re-fetch `tools/list` every session, so nothing is hardcoded client-side, but any saved prompts/automation that reference the old names by string must be updated:
  - `generate_featured_image` → `generate_hero_image`
  - `suggest_internal_links` → `suggest_links`
  - `get_full_page_markdown` → `get_page_markdown`
  - `explain_site_structure` → `explain_structure`
  - `validate_front_matter` → `validate_frontmatter`
  - `inspect_rendered_page` → `inspect_rendered`

  Verified scope enforcement is safe across the rename (name-keyed lookup, but the registry is populated fresh at every server start from the same source, and no per-tool grants are persisted). The 20-character length ceiling is inferred from the observed failures, not independently reconfirmed against a live connector; `TestToolNamesWithinConnectorTruncationBudget` enforces it mechanically going forward. Full migration table in `docs/tools.md`.

### Added
- **`search_pages` match scoring and exact-title mode** (#332, PR #404): each result now carries `score` (count of matching query terms), and a new `match: "title_exact"` param returns a strict case-insensitive full-title match — zero results instead of loosely related hits when there's no exact match (e.g. verifying a page's absence after deletion). `site.Index.Search` refactored into a thin wrapper over a new `SearchScored` method; existing callers/tests unaffected.
- **`validate_front_matter`/`validate_site` pagination clarity** (#333, PR #403): added `has_more`/`next_offset` so a global validation call with a small `limit` no longer conflates the full scan scope (`pages_checked`, always the complete matched set regardless of pagination) with the paginated detail-row view (`pages`). Both tool descriptions now document explicitly which counters mean what.

### Documented
- **`search_pages` vs `search_content` tool selection guidance** (#326, PR #402): both tool descriptions now cross-reference each other so an agent with `content.read` scope knows to prefer `search_content` (also matches body text, supports type/language/sort filtering); `search_pages` is for anonymous callers. Docs-only, no behavior change.

## [v1.4.7] - 2026-07-17

### Added
- **`export_agent_context` size guard** (#325, PR #399): new `include_body` param (default `true`) caps `limit` at 10 pages when full Markdown bodies are included, since a 28-page tag previously returned ~900KB with no server-side size guard and MCP has no response streaming. `include_body=false` returns frontmatter + state only, at a higher cap of 50 pages. A `warnings` entry is emitted when a requested `limit` is silently capped. Behavior change: callers that previously passed `limit` 11–50 with the default body-included mode now get a 10-page cap instead.
- **Shared response-shaping contract** (#337, PR #400): new `internal/toolcontract` vocabulary (`response_mode`, `fields`, `include_body`, `max_body_chars`) so read tools can return smaller payloads on request without a proliferation of ad hoc per-tool knobs. `response_mode: compact` implemented on `search_pages` (list/search) and `build_agent_context` (page-read); `fields` selection on `search_pages`; `max_body_chars` (rune-aware truncation) on `build_agent_context`. `full`/`ids_only` modes are reserved vocabulary, rejected as `invalid_params` rather than silently downgraded to `standard`. Omitting all shaping params is a verified no-op — existing callers get byte-identical output. Documented in `docs/mcp-contract.md` §5.2.

### Documented
- **v1.x envelope-nesting compatibility decision recorded** (#328, PR #398): `docs/mcp-contract.md` §5.1 documents why the structured envelope's `data`-nesting (flagged by mcpscan as "Non-Standard Response Wrapping") is a known, accepted tradeoff — live clients depend on the uniform envelope. Decision: no v1.x flattening; any flattened payload ships as an explicit new contract version, never a stealth v1.x patch. Docs-only, no code changes; the shape is already mechanically enforced by `internal/contracttests`.

## [v1.4.6] - 2026-07-17

### Added
- **`get_theme_status` read-only theme diagnostic** (#350, PR #390): reports the active Hugo theme(s)/module imports, on-disk presence, and (for classic `themes/` installs) pinned Git commit + dirty state via `hugo config --format json` and bounded git probes. Read-only — never installs, updates, or fetches theme code.
- **Mutation coordination model documented and regression-tested** (#374, PR #391): `docs/mutation-coordination-model.md` formalizes the existing `hugosite.ContentMu` lock model (write-lock vs read-lock per tool, retry/timeout behavior, the `build_in_progress:` error convention, interaction with `expected_revision`). No production code changes were needed — the existing model already satisfied the acceptance criteria; four new concurrency regression tests (`internal/tools/write/mutation_coordination_test.go`) prove it under `-race`.
- **Structured security audit event trail** (#371, PR #392): new `internal/audit` package layers a consistent `event_type`/`result` vocabulary onto the existing `log/slog` pipeline (no new logging stack). Covers `auth_rejected`, `scope_denied`, `operator_milestone`, `mutation`, and `admin_operation` events; the latter two ride on the existing per-call `tool_call` log line rather than duplicating it. Design and event-shape reference in `docs/security-audit-trail.md`.
- **`inspect_rendered_page` rendered HTML/SEO/link validation** (#351, PR #393): validates a page's *rendered* public output — title/meta-description length, canonical URL (checked against an independently-derived expected URL, not the canonical tag itself), hreflang presence on multilingual sites, internal links, missing local images, and a heuristic scan for Hugo shortcode/render-error markers. Complements `validate_front_matter` (source-only) and `get_broken_links` (site-wide, not per-page).
- **`verify_publication` source/build/public/index freshness + live HTTP check** (#346, PR #394): proves a page's source, build, public output, and index all agree on the same revision, and that the public HTTP surface is actually serving it — without requiring SSH access. The HTTP probe always targets `cfg.SiteURL` + the page's own slug, never the page's own `<link rel="canonical">` tag, to avoid a lower-privileged `content.write` actor being able to steer the probe at an arbitrary host.
- **`create_preview` temporary token-gated preview surface** (#345, PR #395): builds source (optionally including drafts) into an isolated directory — never `cfg.SiteRoot` — and exposes it at `{issuer}/preview/{preview_id}/{token}/`. `preview_id` is opaque, the 192-bit `token` is the sole confidentiality boundary (constant-time compared, enforced on every access), the URL expires after `ttl_seconds` (default 900s, max 3600s), and every response carries `X-Robots-Tag: noindex`. New `internal/previewstore` package; design in `docs/preview-workflow.md`. The preview build passes `--baseURL` pointed at its own mount so assets resolve correctly, and the request-logging middleware redacts the token from logged paths.

## [v1.4.5] - 2026-07-16

### Added
- **`build_site` validation-oriented safety signals** (#343, PR #377): `build_site` now hashes the output tree (`output_revision`) and reports `publish_ready`/`partial_success` status distinctly from a hard failure, so agents can tell a successful-but-degraded build (e.g. a post-build callback failure) from one that's actually safe to publish.
- **Local Git baseline model design anchor** (#356, PR #375): `docs/git-baseline-model.md` defines the `git_baseline` config section (`mode: auto|configured|disabled`, `repo_path`, `branch`, `remote`) and the baseline-state vocabulary later issues build on.
- **`get_runtime_status` compact runtime/build/git/site status surface** (#344, PR #389): a single `site.admin` tool reporting server version/commit (via Go's embedded VCS build info, no new `-ldflags` needed), hugo/git availability, and a `degraded` list explaining why other tools (`build_site`, `diff_page`) may be failing — instead of agents having to infer environment health from scattered error messages. Revision hashes are opt-in via `include_revisions` to keep the common case cheap to poll.

### Fixed
- **Partial-failure semantics normalized across write/build/reindex/publication paths** (#372, PR #382): mutation tools now consistently distinguish full success, full failure, and partial success, per `docs/partial-failure-matrix.md`.
- **Build and post-build hook execution isolated** (#373, PR #381): `build_site`/hooks now run with a bounded environment (`boundedCommandEnv`), redirect-rejecting HTTP client for webhooks, and proper child-process group cleanup on timeout.
- **`diff_page` ambiguous `git_not_available` status** (#322, PR #388): now distinguishes `git_unavailable` (no usable Git baseline at all — surfaces the real underlying error) from `git_untracked` (file just isn't committed yet, e.g. right after `create_page`) from `unchanged`/`modified`/`deleted` (a real diff was computed). Also wires `git_baseline.mode: disabled` into `diff_page` so it actually short-circuits instead of always probing the host.

## [v1.4.4] - 2026-07-16

### Added
- **Reader-safe read policy for all read-only tools** (#354, PR #365): introduced `site.AccessProfile` context propagation and `ReaderSafeResolvedPage`, which projects `Source`/`SourcePath` out of resolved pages for the `reader` scope while preserving the full response for `content.read`/`operator`/`site.admin`. Applied consistently at the DTO boundary across all read tools.
- **Self-service reader registration** (#353, PR #366): `registerAgentAnonymous` issues the `reader` scope directly (bypassing the manual claim/approval flow) when `AllowReaderSelfRegistration` is enabled in config. Scope is always server-determined — the client cannot request a higher scope via the exchange request (regression-tested by attempting to inject `scope=site.admin`). `reader` shares `content.read`'s OAuth rate-limit bucket.
- **Operator tool parity tests across clients** (#355, PR #369): added contract tests asserting the same `operator`-scoped tool set is exposed consistently regardless of which MCP client surface (ChatGPT, Claude.ai, Gemini, Le Chat, generic MCP) is negotiating capabilities.

### Fixed
- **Runtime `mcp.Implementation.version` regression coverage** (#327, PR #387): the underlying fix (wiring `internal/buildinfo.Version` into both `serverInfo.version` and `meta.server_version`) shipped in #361/v1.4.3; this closes the issue with the regression test (`TestInitializeExposesRuntimeBuildVersion`) and doc note that #361 had deliberately left out of scope.

## [v1.4.3] - 2026-07-16

### Fixed
- **`meta.server_version` reported a hardcoded schema constant instead of the deployed build version** (#323, PR #361): extracted `internal/buildinfo` to separate the response schema version (`ToolResultVersion`, a stable constant) from the runtime build version (`buildinfo.Version`, set via `-ldflags`). `meta.server_version` now carries the real deployed build; the envelope `version` field is pinned to the schema version. ldflags wiring updated across CI, deploy workflow, Makefile, and local scripts.
- **Tool responses exposed absolute host filesystem paths** (#334, PR #362): added `fileutil.LogicalContentPath` to project resolved source paths to `content/...` at the DTO boundary, applied consistently across anonymous, read, write, and diff tool responses. Internal I/O still uses real paths; only client-facing fields are projected.

### Added
- **Access model design anchor** (#352, PR #364): `docs/access-model.md` documents the verified 31-tool scope matrix, the target `reader`/`operator` external model, and migration decisions for `site.admin`/`system.admin` aliases. Matrix is checked against the real tool registry by `TestVerifiedToolScopeMatrix`, not just prose.
- **Discovery metadata for reader/operator profiles** (#357, PR #383): `access_profiles` (`reader`/`operator`) added additively to both OAuth authorization-server and protected-resource discovery documents, alongside the existing real `scopes_supported`. No authorization or token-issuance logic changed.

## [v1.4.2] - 2026-07-16

### Fixed
- **`create_page` silently overwrote existing content on duplicate slug** (#330, PR #367): switched to an atomic exclusive-create primitive (temp file + `os.Link`, which fails if the destination exists) instead of a stat-then-write path. Duplicate creates now fail with `already_exists`. Also fixed `dry_run` mode, which previously reported a false-positive "would succeed" preview for slugs that already existed.
- **Write mutations had no optimistic-concurrency protection** (#335, PR #359): added a stable `sha256` `revision` to all page-oriented read surfaces; `update_page` and `delete_page` now require `expected_revision` and reject stale values with `revision_conflict`. `delete_page` recomputes the revision under the content lock (not before it) to close a race window while waiting for the lock.

### Added
- **`idempotency_key` replay safety for write mutations** (#336, PR #360): `create_page`, `update_page`, and `delete_page` accept an optional `idempotency_key`; replaying the same request returns the original result without reapplying the mutation, and reusing the key with different input returns `idempotency_conflict`. The replay check runs under the content lock so genuinely concurrent retries can't both miss the cache.

## [v1.4.1] - 2026-07-13

### Added
- **`get_related_content` four-way editorial response** (#273, PR #315): the tool now returns all four editorial surfaces — `related_pages`, `backlinks`, `suggested_links`, and `translations` — in a single response. A new `collectBacklinks` helper wraps `idx.GetBacklinks`; `scoreLinkSuggestions` is reused for link candidates. Golden contract fixture and unit tests updated.
- **Explicit `Prompts` and `Resources` capability declarations** (#318, PR #321): `defaultServerCapabilities()` helper extracted from `server.New`; `Prompts{ListChanged:true}` and `Resources{ListChanged:true,Subscribe:true}` now match the capabilities the SDK was already advertising at runtime. Unit test and server-card contract test added.

### Fixed
- **Agent-ready smoke scripts required legacy `system.admin` scope** (#317, PR #319): `check-agent-ready.sh` was asserting `system.admin` must be present in `scopes_supported`, inverting the canonical contract. Added `expect_not_contains` helper and a 135-line regression harness (`test-check-agent-ready.sh`) wired into CI.
- **Public `www.arleo.eu` discovery aliases returned 403** (#316, PR #320): `/.well-known/oauth-protected-resource/mcp` and `/.well-known/mcp/server-card.json` were missing from the OpenResty reference config. Added redirect `location` blocks in both HTTP and HTTPS server blocks. Removed `system.admin` from the static `oauth-protected-resource` artifact. CI lint (`test-agent-ready-www-surface.sh`) added to prevent future drift.

## [v1.4.0] - 2026-07-13

### Added
- **Shared contentmodel and toolcontract foundations** (#276, PR #289): extracted `contentmodel.PageIdentity`, `toolcontract.ToolResponse[T]`, and `toolcontract.NewMeta` into dedicated packages; all read tools now emit versioned structured envelopes with canonical `success/data/errors/warnings/meta` fields.
- **Canonical page identity across all tools** (#271 #272, PR #291): every page read tool now returns `resolved_source_path`, `resolved_lang`, and `State` (lifecycle state) consistently. The page resolver uses a 3-tier source lookup: slug+lang → default-lang → any-slug.
- **Self-descriptive pagination metadata** (#295): all list responses include `returned_count`, `has_more`, and `next_offset` to remove the need for clients to compute pagination state.
- **Lifecycle state for page reads and writes** (#296): `source_state`, `build_state`, `public_state`, and `index_state` exposed on all page reads (`get_page`, `get_full_page_markdown`, `get_page_frontmatter`, `diff_page`, `get_related_content`) and populated by write operations.
- **`diff_page` explicit fallback state** (#287, PR #294): when git is unavailable the tool now returns a structured `git_unavailable` state rather than propagating an error, matching the production-VM scenario.
- **Translations separated from editorial relations** (#273, PR #301): `translations` field carries same-content/different-language variants; `related_pages`, `backlinks`, and `suggested_links` are distinct editorial/structural surfaces.
- **MCP schema resources published** (#299, PR #307): the server exposes a `mcp://schemas/` resource prefix with machine-readable JSON Schema for each tool's input and output.
- **Write tool idempotency annotations** (#298, PR #303): `create_page`, `update_page`, and `delete_page` carry `idempotent`/`non-idempotent` annotations in their MCP descriptions for agent-side retry safety.
- **Structured agent-readable tool errors** (PR #309): all tool errors include a machine-readable error code prefix (`content_not_found:`, `invalid_params:`, etc.) before the human-readable message.
- **Unified read tool envelopes with v1 aliases** (#278, PR #310): `searchContentEnvelope`, `brokenLinkOutput`, `getBacklinksOutput`, and `suggestInternalLinksOutput` all embed `toolcontract.ToolResponse[T]` and expose top-level v1 compatibility aliases for smooth client migration.
- **Lifecycle state across rich read tools** (#290, PR #311): `explain_site_structure`, `search_content`, `list_pages`, `get_recent_posts`, `get_related_content` all populate `State` via `site.StateForResolvedPage`.
- **Golden contract fixtures** (#277, PR #312): `assertGoldenJSON` test harness validates `get_page`, `list_pages`, and `get_related_content` output stability across refactors.

### Fixed
- **`get_sitemap` taxonomy exclusion** (#208, PR #292): `exclude_taxonomies` option now correctly omits taxonomy list pages from the sitemap output.
- **Two-space YAML list indentation in `update_page`** (#288, PR #293): front matter tags/categories lists are now written with the Hugo-standard `  - value` style instead of `- value`.
- **Multilingual source resolution across read tools** (PR #300): `list_pages`, `get_recent_posts`, `search_content`, and `explain_site_structure` now pass `siteRoot` to source enrichment so multilingual bundle pages receive correct lifecycle state and source paths.
- **Sitemap taxonomy exclusion correctness** (PR #302): `IsContent` classifier now correctly excludes taxonomy term list pages (e.g. `/tags/go/`) from content counts and broken-link scans.
- **Preferred language source variant** (#271 #272, PR #313): `source_index.rebuildMaps` now maintains a dedicated `bySlugLang` map so the resolver picks the language-specific bundle (`index.fr.md`) over the default-language fallback when both exist.

### Changed
- `pageDTO` gains `resolved_lang`, `resolved_source_path`, and `state` fields (all tools).
- `RegisterWithSourceIndex` accepts a variadic `dbs ...*db.DB` parameter for the optional SQLite index.
- `write.Register` accepts `siteDB *db.DB` for write-triggered DB invalidation.

## [v1.3.9] - 2026-07-13

### Added
- **OAuth refresh-token renewal** (#270, PR #283): `HandleToken` now dispatches on `grant_type`;
  `exchangeRefreshToken` validates client authorization against a new `GrantTypes` field on the
  `client` struct (RFC 6749 §10.4). The hollow `exchangeToken` stub is removed. DCR-registered
  clients receive `["authorization_code","refresh_token"]`; static-registry clients (no `GrantTypes`
  field) are treated as supporting all standard grants for backwards compatibility.
- **`delete_page` dry-run** (#267, PR #284): `delete_page` now accepts `dry_run: true` and returns
  the page content and backlink list without deleting, matching the contract of `create_page` and
  `update_page`. The `backlinks` field is typed `*[]backlink` so an empty backlink list serialises as
  `[]` (not omitted) while the field is absent on non-dry-run responses.

### Changed
- **`get_page` source-index fallback contract documented** (#268, PR #286): `SourceSlugCandidates`
  now carries an explicit contract comment (priority order, language-prefix stripping, callers must
  break on first match). The `get_page` tool description spells out that `html`, `lang`, and `url`
  fields come from the public index and may be absent for drafts or source-only pages.

### Fixed
- **Slug normalisation across write tools** (#265, PR #284): `create_page`, `update_page`, and
  `delete_page` all strip leading/trailing slashes from the input slug via a shared
  `normalizeInputSlug` helper, so agents that pass `/posts/foo/` and `posts/foo` reach the same
  content directory and source-index entry.
- **`delete_page` silent success on missing slug** (#266, PR #284): previously returned an empty
  success when the target was already absent; now returns a structured `not_found` error.
- **Categories/tags empty for non-default-language pages** (#264, PR #280): `list_pages`,
  `get_recent_posts`, and `explain_site_structure` now enrich pages whose public path carries a
  language prefix by stripping the prefix before the source-index lookup.
- **`explain_site_structure` recent pages bypassed source enrichment** (#258, PR #281): recent-pages
  path in `explain_site_structure` now goes through the same source-index category/tag enrichment
  used by `list_pages`.
- **MCP session lifecycle observability** (#259, PRs #269 #282): structured log lines emitted on
  session connect and disconnect; `withDefaultLogger` test helper carries a `t.Parallel()` safety
  warning; SSE flush hygiene improved to avoid buffered-writer stalls.
- **`update_page` dry-run diff label** (#257, PR #262): `update_page` dry-run header no longer
  hard-codes `index.md`; the resolved multilingual path is used instead.
- **Explicit `InputSchema`/`OutputSchema` on all tools** (#253, PR #261): all MCP tools now declare
  both schemas explicitly so static scanners (mcpscan.dev) can inspect them.

### Tests
- **Property-based invariant tests** (#250, PR #254): replayable property checks for
  create/update/delete write coherence; public ⊆ source invariant verified on each mutation.
- **Fuzz smoke** (#251, PR #255): targeted fuzz corpora for path safety, taxonomy slugs, and
  front-matter parsing.
- **Local soak harness** (#249, PR #256): long-running mutation and build stability harness
  exercisable locally without CI.
- **Core benchmarks and invariant matrix** (#252, PR #260): `BenchmarkCreatePage`,
  `BenchmarkUpdatePage`, `BenchmarkDeletePage`, plus a reference table of expected invariants.

### Refactored
- **Write-tool test helpers consolidated** (PR #285): five near-identical `newTestServer*` functions
  replaced by a single `newTestServer(t, root, ...testServerOpts)` accepting optional
  `SiteRoot/SiteDB/SiteIdx` overrides and returning the source index for post-call inspection.
- **`normalizeInputSlug` extracted** (PR #285): the repeated `strings.Trim(slug, "/")` expression
  now lives in one named helper with a clear contract comment.

## [v1.3.8] - 2026-07-12

### Added
- **SQLite-backed derived index** (#221): optional persistent index controlled by `db_path` in
  config (falls back to existing in-memory behaviour when unset). Phase 1: core `pages`, `page_tags`,
  `page_categories`, and `links` tables with write-triggered invalidation (`create_page`,
  `update_page`, `delete_page` sync to DB in-process after file write). Phase 2: FTS5 virtual table
  (`page_fts`) makes `search_content` use ranked full-text search with `<<highlighted>>` snippets
  instead of a linear keyword scan. Phase 3: `site_health_snapshots` table for history (written by
  `build_site` post-build callback). Startup reindex is hash-gated — unchanged pages are skipped.
  `build_site` triggers incremental reindex of the public index after each successful Hugo build.
  DB is always re-derivable from scratch by deleting the file.
- **MCP tool-call observability** (#226): `NewToolCallMiddleware` wired as receiving middleware on
  all four MCP servers (anonymous, content.read, content.write, site.admin). Emits one structured
  log line per `tools/call` with `tool_name`, `scope`, `duration_ms`, `result_class`
  (`success`/`tool_error`/`protocol_error`), and `response_bytes`. Prometheus counters added to
  `/metrics`: `mcp_tool_calls_total{tool,scope,result}` and `mcp_tool_call_duration_ms_total{tool,scope}`.
  No request arguments, page content, or tokens are logged.
- **`suggest_internal_links`** (`content.read`) — new tool that recommends existing published pages
  to link from a draft or page, ranked by shared tags/categories. Accepts `slug` (merges that
  page's taxonomy, including source-only drafts), `tags`, `categories`, and optional `body` (detects
  title mentions using phrase-boundary matching to avoid false positives). Returns structured
  envelope with `anchor_text`, `shared_tags`, `shared_categories`, `score`, and `body_mention`
  (#220).
- **`docs/mcp-contract.md`** — explicit MCP contract document covering both response envelope
  shapes (flat and structured), error model with `snake_case_prefix:` codes, pagination, naming
  conventions, versioning, and per-tool inventory table (#224, #210).
- **`docs/agent-tool-matrix.md`** — agent-first tool-selection matrix: scenario→tool quick
  reference, common workflow sequences (create/edit/delete/validate/link), a decision tree, and a
  disambiguation table for commonly confused tool pairs (#225, #227).

### Changed
- **Tool annotations — `OpenWorldHint` corrected for write and build tools**: `create_page`,
  `update_page`, `delete_page`, and `build_site` now declare `OpenWorldHint: true`, accurately
  reflecting that these operations interact with external systems (Cloudflare CDN purge, IndexNow,
  Google Search Console, filesystem). Read-only and anonymous tools remain `false`. This resolves
  SPEC_006 on mcpscan.dev.
- **`server.New` accepts `...ScopeExtension` hooks**: operators can now register additional MCP
  tools per scope without modifying core packages. Pass one or more `ScopeExtension` functions to
  `server.New`; each receives the scope name and the `*mcp.Server` for that scope, enabling
  `mcp.AddTool` calls at startup. Resolves EASE_004 on mcpscan.dev.
- `list_pages` description: clarifies it returns content pages only (not taxonomy list pages) and
  cross-references `get_sitemap` for the full URL inventory.
- `search_pages` description: cross-references `search_content` for filtered/paginated search.
- `get_sitemap` description: clarifies it includes taxonomy pages by default; cross-references
  `list_pages` for content-only browsing.
- `search_content` description: cross-references `search_pages` for unauthenticated keyword search.
- `validate_site` description: notes equivalence to `validate_front_matter` with no slug filter.
- **Explicit `ServerCapabilities` in `mcp.NewServer`** (#250): all four scope servers (anonymous,
  content.read, content.write, site.admin) now pass `&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Logging, Tools}}`
  explicitly so static code analysis scanners (mcpscan.dev) can inspect declared capabilities.
  The SDK still auto-merges runtime-detected tool capabilities on top.

### Fixed
- **Build resilience** (#246): Hugo timeout/cancellation now kills the entire process group (not just
  the top-level process) so shell-wrapper scripts and their children are terminated. Post-build
  callbacks run in bounded goroutines with a 30s deadline; `partial_success` + warning is returned
  instead of blocking forever. Optional side-effect callbacks (Cloudflare purge, search indexing)
  swallow errors so only required callbacks can trigger `partial_success`. DB delete and
  public-output cleanup failures in `delete_page` are surfaced as `Warning` fields instead of being
  silently ignored (#238–#244).
- **TOCTOU symlink-swap** (#248): `create_page` and `update_page` now use `AtomicWriteChecked`
  which re-validates the parent directory via `pg.RevalidateForWrite` both before `os.CreateTemp`
  and before `os.Rename`, closing the write-time TOCTOU window. `generate_featured_image` uses a
  guard anchored at `HugoRoot` with `rejectSymlinks` forced `true`, validated before `MkdirAll`,
  to detect symlinked `static/images` regardless of the operator's `RejectSymlinks` config setting.
  `delete_page` audit-log failures are now surfaced as a `Warning` field instead of being silently
  discarded (#233–#235).
- **DCR anonymous scope default** (#249): RFC 7591 dynamic client registration now returns `""`
  (anonymous) scope when the requested redirect URIs don't match any pre-registered client, enabling
  MCP scanners to self-register and reach anonymous-only tools. The `if scope == "" { scope = "content.read" }`
  promotion in `exchangeToken` is removed so anonymous tokens remain anonymous through the full
  PKCE flow. Pre-registered clients (Claude.ai, ChatGPT) continue to inherit their configured scope
  via `resolveRegistrationScope`.

## [v1.3.7] - 2026-07-11

### Added
- **`get_backlinks`** (`content.read`) — new read tool that returns all pages linking to a given
  slug, built from a lazy reverse-link cache (`backlinkCache`) invalidated on every write mutation.
  Orphan pages (zero incoming links) are also surfaced in `get_site_health` (#217).
- **`get_page`**: new `allow_source_fallback` parameter (bool, default `false`) — opt-in to return
  source-index content for pages not yet built by Hugo (e.g. immediately after `create_page`).
  Draft pages are always excluded regardless of this flag. Default behaviour (published-only) is
  unchanged and the API contract is now explicit (#223).
- **`get_page`**: new `content_only` parameter (bool) — strips navigation, header, and footer from
  the rendered HTML of published pages, returning article-only HTML extracted from `<article>` /
  `<main>` (#209).
- **`update_page` / `create_page`**: new `dry_run` parameter (bool) — returns a unified diff
  preview without writing to disk. Uses in-process Wagner-Fischer LCS; no git dependency (#218).
- **`update_page`** now accepts `lang` parameter to target a specific language file on bilingual
  pages (e.g. `lang: "fr"` targets `index.fr.md`). Omitting `lang` on a page with multiple
  language files returns an explicit `ambiguous_language` error (#215).
- **`update_page`** now accepts `tags`, `categories`, `draft`, and `description` fields, enabling
  front matter updates without touching raw Markdown (#214).
- **`build_site`** now reloads the in-memory site index after a successful build so that
  `get_sitemap`, `get_broken_links`, and `search_pages` immediately reflect the rebuilt output
  without a server restart (#212).
- `site.Index.Reload(cfg)` with `sync.RWMutex` — atomic pointer swap of all index fields; read
  methods protected with `RLock` to eliminate data races during concurrent reload.
- Post-build webhooks: Cloudflare cache purge (full zone), IndexNow batch submission, and Google
  Indexing API `URL_UPDATED` notifications fire automatically after every successful `build_site`.
  All three are opt-in via host config only; credentials never committed to git. Taxonomy and
  search URLs are filtered before submission. Google plugin includes a daily quota guard (default
  180/day) with JSON state persistence (#216).
- CI: `TestTotalToolCount` asserts that `Defs()` sum across all packages equals the expected
  constant (30 tools) (#203).

### Fixed
- **`validate_front_matter`** returned silent success (`pages_checked: 0`) when a slug was
  provided but did not match any source page. Now returns `content_not_found` (#222).
- **`validate_front_matter`** false positive "missing date" immediately after `create_page` — the
  in-memory source-index entry now carries the correct `date` populated at creation time.
- **Public site index stale** after `update_page` / `delete_page` between Hugo builds.
  `update_page` now refreshes metadata in the public index when the entry already exists;
  `delete_page` removes it via `RemoveBySlug`; `create_page` no longer injects a premature stub
  (page is source-only until Hugo builds it) (#219).
- **`diff_page`** always returned an empty diff when git was unavailable in production. Fixed by
  falling back to in-process unified diff (#207).
- **`validateFrontmatterRoundTrip`** false positive: a Markdown thematic break (`---`) at the
  top of a body was incorrectly rejected as duplicated frontmatter. Now only triggers when a full
  YAML block (opening + closing `---` within 30 lines) is detected.

## [v1.3.6] - 2026-07-11

### Added
- `get_sitemap` now accepts `exclude_taxonomies: true` to omit Hugo-generated tag, category,
  and author listing pages, returning only content pages (#208).
- `generate_featured_image` uses local Go renderer by default (1200×675 JPEG, Unsplash photo
  background selected by title hash, dark gradient overlay). External API mode is optional.
  Output path corrected to `{hugo_root}/static/images/{slug}-featured.jpg` (#195).
- Operator guide: new "Known Pitfalls" section covering `generate_featured_image` write errors
  (`static/images` must be in `ReadWritePaths`) and stale index after `build_site` (#212).

### Fixed
- `update_page` now works on multilingual pages (`index.fr.md`, `index.en.md`). Previously it
  always resolved to `index.md` and failed with `read_error` on any bilingual bundle. Fixed by
  using `FilePath` from the source index, which is set to the actual discovered file path (#205).
- `delete_page` no longer leaves zombie pages in `public/` after a Hugo build. Previously,
  deleting a page removed the source but left the rendered `public/{slug}/` directory, which
  survived subsequent `build_site` calls because Hugo does not clean by default. Fixed by
  removing `cfg.SiteRoot/{slug}` atomically with the content dir (#213).
- `content/posts/csp-nonce/index.fr.md`: `aliases:` block was duplicated outside the YAML
  frontmatter, rendering as visible HTML text. Fixed on the live VM.
- `validate_front_matter` now returns `pages_checked: 80` (was 0 for valid published slugs) (#206).
- Taxonomy duplicate `postmortem`/`Post-mortems` resolved — list_categories no longer includes
  the stale `post-mortems` alias (#202).
- Broken Grav links in `migration-grav-hugo` article fixed (FR + EN) (#204).

## [v1.3.5] - 2026-07-10

### Added
- **Taxonomy alias map** (`taxonomy_aliases` in config): operators define a slug→slug map
  (e.g. `sécurité: security`) that folds alias terms to their canonical form in all listing
  and filter paths (`list_tags`, `list_categories`, `list_pages`, `search_pages`,
  `get_recent_posts`, `search_content`, `explain_site_structure`). Filtering by canonical
  tag/category now matches pages tagged with any alias form. Near-duplicate tag pairs are
  detected via Levenshtein distance ≤ 2 and reported in `get_site_health` (#183).
- `get_site_health` now includes a `taxonomy_inconsistencies` field listing alias-key terms
  in use and near-duplicate slug pairs that the operator should consolidate (#183).
- `validate_front_matter` now warns when a page's tags or categories use an alias slug
  instead of the canonical form (#183).
- `build_site` and `preview_build` now run a preflight write-check before invoking Hugo.
  A `build_precondition_failed` error is returned immediately when `public/` or
  `resources/_gen/` are not writable, with an `operator_hint` that names the missing
  `ReadWritePaths` entry and the exact `systemctl` command to fix it. Build errors caused
  by permission denial now also carry `suggestion` and `docs_url` fields pointing to the
  operator guide (#186).
- Added `docs/operator-guide.md#build-permissions` section documenting required writable
  paths per tool and the `ReadOnlyPaths` override precedence rule (#186, #190).

### Fixed
- `generate_featured_image` is no longer registered when `image_gen_url` is unset. MCP
  clients no longer see a confusing "available but broken" tool when image generation is
  not configured (#185).
- `list_pages`, `search_pages`, and `get_recent_posts` now populate `categories` from the
  Hugo source index frontmatter when the HTML index has none. Hugo does not emit
  `article:category` meta tags, so the HTML-only index always returned empty categories
  for per-page DTOs (#189).
- Systemd service `ReadWritePaths` configuration documented; deploy script template
  updated to include all paths Hugo needs to write (`content/`, `resources/`, `public/`)
  (#190).

## [v1.3.4] - 2026-07-06

### Added
- A secret-free staging profile is now versioned in-repo via `deploy/config-staging.yaml`,
  `deploy/systemd/mcp-hugo-server-go-staging.service.example`, `docs/staging-runbook.md`,
  and `scripts/staging-smoke-local.sh`. CI now exercises that synthetic staging profile before
  production deploys (#176).
- `internal/taxonomy` is now the shared normalization package for tags and categories. Read tools
  expose consistent `tag_terms` / `category_terms`, and the repo now documents the convention in
  `docs/taxonomy-convention.md` (#175).

### Fixed
- `build_site` and `preview_build` now work with the hardened systemd service layout and return
  actionable build diagnostics, including `exit_code`, `duration_ms`, `working_directory`,
  `build_id`, `log_hint`, and a useful `stderr_summary` even when Hugo only writes to stdout
  (#170).
- `check_sri_versions` now verifies data-driven SRI references correctly: it reads the configured
  SRI data source, decodes HTML entities, pairs hashes with the correct asset tags, and reports
  structured scan statistics instead of false `sri_checked=0` results (#171).
- `validate_front_matter` now computes aggregate counters before pagination, so `pages_checked` and
  `pages_passed` reflect the full scan instead of the current page size (#172).
- `export_agent_context` now uses the same source-markdown path as `build_agent_context`, removing
  theme chrome and HTML navigation artifacts from exported markdown (#173).
- `generate_featured_image` now returns structured, operator-actionable diagnostics when image
  generation is not configured or the output path is not writable, without changing the MCP tool
  contract (#174).
- The production deploy workflow now promotes refs without auto-creating a GitHub release, and the
  pre-release smoke gate runs from its own workflow instead of polluting push/PR checks with a
  skipped job state (#177, #178).

## [v1.3.3] - 2026-07-06

### Added
- `build_site` and `preview_build` now return a structured JSON error on Hugo failure containing
  `error`, `exit_code`, `command`, `working_directory`, `duration_ms`, `stderr_summary` (≤500 bytes,
  paths sanitised), `build_id` (`YYYYMMDD-HHMMSS-<4 hex chars>`), and `log_hint`. Full stderr is
  logged via `slog.Error` with the `build_id` key for log correlation (#160).
- `check_sri_versions` now returns a structured envelope `{files_scanned, sri_checked, summary,
  findings}` instead of a bare array. The `summary` field always contains a human-readable verdict
  ("No SRI attributes found", "All N passed", or "N/M passed, M mismatches"). **Breaking shape
  change:** existing code that destructures the flat `[]sriCheckEntry` array must be updated to
  access `.findings` (#162).
- `generate_featured_image` description in `tools/list` now appends
  `(not configured: set image_gen_url in config)` when `image_gen_url` is absent, so agents
  discover the configuration gap before calling. Operator guide documents `image_gen_url` and
  `image_gen_key` (#161).
- `get_page` accepts an optional `content_only=true` parameter that clears the `html` field
  (returns `html` as empty string) for lightweight metadata queries. Description now distinguishes
  `get_page` (rendered HTML) from `get_full_page_markdown` (raw Markdown, requires content.read)
  (#169).
- `frontMatterIssueDTO` (returned by `validate_front_matter` and `validate_site`) gains a `lang`
  field derived from the multilingual branch-bundle filename (`index.en.md` → `"en"`). `SourcePage`
  in the source index now carries a `Lang` field populated at index-build time (#168).

### Fixed
- `explain_site_structure` now uses `srcIdx.AllTags()` / `srcIdx.AllCategories()` when the source
  index is available, matching `get_site_health`. Previously reported 0 categories on sites where
  the HTML index carried no `article:section` meta tags (#163).
- `build_agent_context` now passes the raw public-index page to `computeRelated` (same pattern as
  `get_related_content`), preventing empty `related_pages` caused by source-merged tags not matching
  HTML-indexed sitemap entries (#164).
- `ContentClassifier` classifies `/404.html`, `/404/`, `/500.html`, `/500/` as `KindTechnical`,
  removing error pages from `get_feed` and `export_agent_context` output (#167).
- `get_broken_links` no longer reports false positives for `.md`-suffixed hrefs (LoveIt/PaperMod
  source-file links rendered as `<a href="./index.md">`) (#166).
- Smoke script `generate_featured_image` check now SKIPs instead of FAILing when the tool returns
  `config_error`, and the call now correctly includes the required `prompt` argument (#161).

### Changed
- **Breaking:** `validate_front_matter` and `validate_site` response `data` object field names
  renamed for clarity: `total` → `pages_checked`, `valid` → `pages_passed`. `invalid` unchanged.
  Update any agent prompts or custom tooling that references the old field names (#165).

## [v1.3.2] - 2026-07-06

### Fixed
- Rate limiter now only counts `tools/call` requests against the budget.
  Control-plane messages (`initialize`, `notifications/initialized`, `tools/list`,
  `resources/list`, etc.) pass through without consuming a token, so the
  configured rate limit reflects actual tool invocations rather than MCP
  handshake overhead (#156).
- When the rate limit fires inside an established MCP session
  (`Mcp-Session-Id` present), the server returns HTTP 200 with a JSON-RPC 2.0
  error body instead of HTTP 429. The go-sdk Streamable HTTP transport discards
  non-2xx response bodies before the MCP layer can surface the error; HTTP 200
  ensures the structured JSON-RPC error (`code: -32029`, `Retry-After`) reaches
  the MCP client (#155).
- `ContentClassifier` correctly classifies multilingual taxonomy slugs
  (`/en/tags/webhook/`, `/fr/categories/securite/`) via `stripLanguagePrefix`
  (added in v1.3.0); test coverage added in v1.3.1 confirms the fix. Closing
  #157 as resolved.
- `operator-guide.md`: new Pitfall 4 section documenting why OpenResty returns
  HTML 503 under rate-limit saturation and how to configure
  `proxy_intercept_errors` / `proxy_pass_header Retry-After` to forward the
  upstream JSON-RPC error body correctly (#158).
- `smoke-mcp-live.sh`: `generate_featured_image` is now called in the
  `MCP_SMOKE_ENABLE_WRITES=1` section (after `update_page`, while the page
  still exists); asserts `result.isError` via `classify_response` and verifies
  that `result.content[0].text` is non-empty (#159).

## [v1.3.1] - 2026-07-06

### Fixed
- Rate-limit 429 response body is now a valid JSON-RPC 2.0 error object
  (`code: -32029`, `message`, `data.retry_after_seconds`) so MCP clients can
  parse the structured error instead of seeing a generic "Error occurred during
  tool execution" (#153).
- Default rate limits raised to account for stateful Streamable HTTP transport
  consuming 2 HTTP requests per tool call: `site_admin_per_min` 10 → 60,
  `content_write_per_min` 30 → 60, `anonymous_per_min` 60 → 120,
  `content_read_per_min` 120 → 240 (#152, #140).
- `preview_build`, `create_page`, `update_page`, `delete_page` now use
  `TryLock`/`TryRLock` with a 10-second deadline instead of blocking
  indefinitely on `ContentMu`; lock events are logged via `slog` (#145).
- `get_related_content` resolves slugs through `PageResolver` instead of
  direct `idx.GetBySlug`, enabling correct multilingual branch-bundle lookup
  (#146).
- `matchContentFilters` in `search_content` no longer rebuilds
  `ContentClassifier` per page (O(n²) → O(n)) (#141).
- `isGitPathMissing` in `diff_page` now checks `exec.ExitError.ExitCode()==128`
  instead of locale-dependent English substring matching (#142).
- `get_sitemap` accepts `limit` (default/cap 200) and `offset`; returns an
  empty list when offset ≥ total instead of panicking (#147).
- Rate limiter bucket map now evicts idle entries (TTL 15 minutes, GC every
  5 minutes) and caps at 10,000 entries to prevent unbounded memory growth
  under sustained load from many distinct IPs (#150).
- `deploy.sh` no longer overwrites an existing `mcp-hugo-server-go.service`
  on upgrades — the distribution template carries no site-specific paths;
  a `service.d/override.conf` example is installed on first deploy and
  preserved on upgrades (#143).
- `--version` / `-version` / `version` flag prints the build version and
  exits without requiring the config file to be loaded (#148).
- Operator guide documents `ProtectSystem=strict`, the `ReadWritePaths`
  requirement, and the systemd drop-in override pattern (#149).
- `docs/client-compatibility.md` and `auth.md` document that
  `oauth.enabled: true` requires a Bearer token on all `/mcp` requests,
  including anonymous-scope tools (#154).
- `docs/client-compatibility.md` updated to v1.3.0 test results: Claude.ai
  admin token and stateful HTTP transport confirmed functional (#151).

### Added
- `smoke-agent-interop.sh` extended with `mcp_tool_call` helper (handles
  202+session-id two-phase flow) and live assertions for
  `get_site_information`, `get_recent_posts`, and optionally `get_site_health`
  (#144).

## [v1.3.0] - 2026-07-05

### Added
- `ContentClassifier` centralises Hugo page-kind detection (article, section, taxonomy, pagination, technical) replacing scattered `/posts/` prefix checks. Fixes `list_pages`, `get_feed`, `get_recent_posts`, `explain_site_structure`, and `get_broken_links` returning taxonomy and section pages as content (#127, #132, #133).
- `PageResolver` unifies public and source-index slug resolution. `diff_page`, `get_full_page_markdown`, `build_agent_context`, and `get_page` now look up pages through one code path: public HTML index for published metadata, SourceIndex for raw Markdown body (#130, #134, #137).

### Fixed
- Switch MCP transport from stateless to stateful mode. In stateless mode the server returned HTTP 405 for `GET /mcp`, causing Claude.ai and ChatGPT to immediately disconnect after tools discovery (tools briefly visible, then "not connected"). Stateful mode keeps the SSE session open so tool calls succeed. Sessions have a one-hour idle timeout for cleanup.
- `diff_page` and source-index lookup now correctly resolve multilingual branch-bundle slugs (`index.en.md`, `index.fr.md`) to the parent directory slug (`posts/slug`), matching how the public site index exposes those pages.
- `build_site` and `preview_build` now run Hugo from `hugo_root` (the Hugo project directory containing `hugo.toml`) instead of `site_root` (the generated `public/` output directory). Fixes `build_error: hugo exited with error` on every call (#135).
- `list_categories` and `list_tags` now return frontmatter taxonomies from the source index instead of the HTML `article:section` meta fallback, which was reporting "posts" as a category on sites without `article:category` meta tags (#136).
- `diff_page` returns `status: "git_not_available"` with raw source content instead of a hard error when the content directory is not inside a Git repository (#131).
- `get_broken_links` no longer reports false positives for pagination URLs (`/page/2/`), taxonomy term pages (`/tags/go/`), anchor-only links, `mailto:`, `tel:`, and non-HTTP scheme URIs (#139).
- `export_agent_context` now filters through `ContentPages()` (excluding taxonomy and section pages) and reads Markdown from the source index when available, consistent with `get_full_page_markdown`.
- Rate-limit `Retry-After` header and `retry_after_seconds` response field now reflect the actual token-bucket delay instead of a hardcoded 1-second value. For `site.admin` (10 req/min) the correct delay is 6 seconds (#140).
- Fixed a data race on the internal `ContentClassifier` pointer: `contentClassifier` is now initialised eagerly at index build time instead of lazily on first use, eliminating a concurrent-write hazard in the HTTP request goroutines.

## [v1.2.10] - 2026-07-05

### Changed
- Collapsed the former standalone `system.admin` tier into `site.admin`; `system.admin` remains accepted as a legacy alias.
- Simplified the active scope hierarchy to anonymous, `content.read`, `content.write`, and `site.admin`.

### Fixed
- Claude.ai authorization no longer fails with `invalid_scope` when it requests a wider historical scope list than the registered client ceiling.
- Admin and integrity tools, including `check_sri_versions`, are now served under `site.admin`.

## [v1.2.9] - 2026-07-05

### Fixed
- Added Claude.ai's observed `https://claude.ai/api/mcp/auth_callback` redirect URI to the admin client configuration path.

## [v1.2.8] - 2026-07-05

### Fixed
- Return a proper OAuth challenge for unauthenticated `/mcp` requests when OAuth is enabled, preventing authenticated clients from caching anonymous tool lists.

## [v1.2.7] - 2026-07-05

### Added
- Dynamic Client Registration scope inheritance from pre-registered clients when redirect URI policy matches.

### Fixed
- Hardened OAuth redirect handling and agent discovery metadata.
- Resolved CodeQL redirect findings with validated redirect sinks and documentation.

## [v1.2.6] - 2026-07-05

### Fixed
- Corrected `resource_documentation` metadata and added regression tests for the AgentReady scanner path.
- Added a regression test for the Auth.md backtick URL extraction issue.

## [v1.2.5] - 2026-07-05

### Fixed
- Resolved remaining AgentReady blockers for API/Auth/MCP/Skill Discovery 7/7.

## [v1.2.4] - 2026-07-05

### Fixed
- Added `register_uri` to agent auth discovery metadata.

## [v1.2.3] - 2026-07-05

### Added
- `scripts/verify-agent-ready.sh` for post-deploy discovery validation.
- RFC compliance documentation with live-tested discovery endpoint annotations.

## [v1.2.2] - 2026-07-05

### Fixed
- Applied `gofmt` to resolve CI formatting violations.

## [v1.2.1] - 2026-07-05

### Fixed
- Resolved remaining v1.2.0 follow-up issues around OAuth, client compatibility, and AgentReady discovery.

## [v1.2.0] - 2026-07-05

### Added
- Agent interop and AgentReady validation scripts.
- Secret scanning jobs for gitleaks and trufflehog in CI.

### Fixed
- Interop, security, and correctness issues found during the v1.2.0 hardening milestone.
- Deploy script now injects version ldflags so live binaries can report build version.

## [v1.1.0] - 2026-07-04

### Security
- Require `site.admin` or `system.admin` Bearer token on `POST /agent/identity/verify` — anonymous callers could previously self-claim and escalate to `content.read` ([#71](https://github.com/jmrGrav/mcp-hugo-server-go/issues/71))

### Added
- `internal/fileutil` package with shared `AtomicWrite`, `AtomicWriteBytes`, and `BoolPtr` helpers (#77)
- `Service.PurgeExpired()` cleans expired auth codes and agent registration maps every 5 minutes (#72, #74)
- Hourly reset of the per-IP OAuth allocation counter to prevent unbounded growth (#73)
- `security_contact` config field populates `/.well-known/security.txt` per RFC 9116
- `Canonical` line in `security.txt` falls back to `oauth.issuer` when `site_url` is blank (#94)
- Makefile with `build`, `test`, `cover`, `lint`, `vet`, `vuln`, and `check` targets (#96)
- API reference table in README (#98)
- Agent identity verification flow documented in README (#88)
- `security_contact` documented in README (#87)

### Changed
- `Version` in `internal/server` is now a `var` set at build time via `-ldflags` (defaults to `"dev"`) (#79)
- CI: staticcheck pinned to `2025.1.1` (#82)
- CI: `govulncheck` step added (#83)
- CI: `go build ./...` step added (#84)
- CI: coverage gate replaced `python3` with `awk` (#97)

### Fixed
- `handleSecurityTxt` no longer emits a relative `Canonical:` line when `site_url` is empty (#94)

## [v1.0.0] - 2026-06-01

Initial public release.

- Streamable HTTP MCP transport at `/mcp`
- OAuth 2.0 / PKCE authorization code flow
- Initial 5-tier scope hierarchy: anonymous → content.read → content.write → site.admin → system.admin
- Agent identity registration and claim flow
- SQLite and JSON token persistence backends
- Hugo content tools: `create_page`, `update_page`, `delete_page`
- Site admin tools: `build_site`, `preview_build`, `run_post_build_hooks`, `upload_asset`
- System tools: `check_sri_versions`
- PathGuard symlink and path traversal protection
- RFC 9116 security.txt, RFC 9116 robots.txt, llms.txt, MCP server card, agent card
