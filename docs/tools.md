# Tool Inventory

This document reflects the current MCP registry. Tool IDs are stable; titles and descriptions are tuned for Claude and other MCP clients.

## Tool name migration (#329)

At least one MCP client connector was observed silently truncating and
hash-suffixing canonical tool names of 21+ characters for uniqueness (e.g.
`get_full_page_markdown` rendered to the model as `get_ful_7c6ab376aa24`),
which destroys legibility for tool selection. Six tools with names over a
20-character budget were shortened. Agents re-fetch the tool list via MCP's
`tools/list` each session, so no client-side caching migration is required
— this is a rename, not a deprecation:

| Old name                  | New name              |
|----------------------------|------------------------|
| `generate_featured_image`  | `generate_hero_image`  |
| `suggest_internal_links`   | `suggest_links`        |
| `get_full_page_markdown`   | `get_page_markdown`    |
| `explain_site_structure`   | `explain_structure`    |
| `validate_front_matter`    | `validate_frontmatter` |
| `inspect_rendered_page`    | `inspect_rendered`     |

The 20-character budget is inferred from the observed failures (all 21–22
characters); it has not been independently reconfirmed against a live
connector this session. `TestToolNamesWithinConnectorTruncationBudget`
(`internal/tools/toolcount_test.go`) enforces the budget mechanically for
every registered tool going forward.

## External access profiles

Public documentation uses two external profiles, which map directly onto the
internal `read`/`write` scopes (#450, see `docs/mcp-contract.md` §6.12):

- `reader`: all read tools (full visibility, drafts included)
- `operator`: reader tools plus write and site operations

The registry below lists the current internal scope tiers enforced by the
runtime so the mapping stays explicit and auditable. Since #450, there are
only two: `read` (the reader tier; on OAuth-enabled deployments `/mcp`
still requires a Bearer token, but no additional per-tool scope split
exists below `read`) and `write` (requires a registered OAuth client).

## Search tool selection (#326)

Two overlapping search tools exist: `search_pages` (published-page search)
and `search_content` (`read`). If calling with any
Bearer token, prefer `search_content` — it also matches body text and
supports type/language/sort filtering that `search_pages` doesn't (both
tools support `limit`/`offset` pagination).
`search_pages` exists as the smaller published-content search surface; it is not
a lighter-weight alternative to reach for when `search_content` is
available.

## Anonymous

- `list_pages` - Browse pages
- `get_page` - Read page
- `search_pages` - Search content
- `get_recent_posts` - Read recent posts
- `list_tags` - Browse tags
- `list_categories` - Browse categories
- `get_sitemap` - Read sitemap
- `get_feed` - Read feed
- `get_site_information` - Read site metadata
- `get_changelog` - Get changelog
- `get_capabilities` - Get capabilities (machine-readable runtime limits and feature discovery, e.g. rate-limit windows, TTL/body-size bounds, enabled optional features; see #859)

## `read` (reader tier; on OAuth-enabled deployments, obtain a Bearer token first)

- `get_page_markdown` - Get full page Markdown
- `get_page_frontmatter` - Get page frontmatter
- `get_related_content` - Get related content
- `build_agent_context` - Build agent context
- `export_agent_context` - Export agent context
- `get_page_for_edit` - Get page for edit (compact edit bundle: frontmatter + markdown + state + quality + revision in one call; opt-in `include` facets also expose standalone-equivalent `backlinks`, `impact`, and `preview` data for pre-mutation review; see #339, #527)
- `list_content_types` - List content types (site's Hugo content types/sections, archetype template + expected front matter fields [union of archetype-declared and observed-page fields] + observed page count per type; see #347)
- `list_page_assets` - List page assets (sibling files stored alongside a page bundle's index.md, e.g. images; only leaf bundles have an asset directory, single-file pages fail with `not_a_bundle`; see #348)
- `check_ai_readiness` - Validate AI readiness (deterministic Markdown/frontmatter audit for heading hierarchy, section and paragraph length outliers, metadata presence, internal-link density, and citation structure; intentionally not an SEO/render/build validator; see #437)
- `search_content` - Search content
- `explain_structure` - Explain site structure
- `get_site_health` - Get site health
- `get_broken_links` - Get broken links
- `get_backlinks` - Get backlinks
- `suggest_links` - Suggest internal links
- `diff_page` - Diff page (depends on a readable local Git baseline; see `docs/git-baseline-model.md`)
- `inspect_rendered` - Inspect rendered page (title/meta description/canonical/hreflang/internal links/missing images/render-error checks against the current public build output)
- `validate_frontmatter` - Validate front matter
- `validate_site` - Validate site
- `plan_page` - Plan page scaffold (proposes a slug/frontmatter/vocabulary scaffold for a new page before create_page; see #622)
- `list_page_revisions` - List page revisions (git-baseline revision history for a page; see #615)

## `write` (requires a registered OAuth client)

Per #450, `write` implies `read` and folds in every tool that used to
require a separate `site.admin` scope, with no exceptions.

Successful write-tool responses currently use a **v1.x compatibility**
convention (#520):

- the canonical machine payload lives under `data`
- a mirrored copy of the same write-result fields still exists at the root

New clients should read `data.*` first. The root write fields remain accepted
as compatibility aliases during v1.x; they are not the preferred contract for
new integrations.

- `create_page` - Publish page
- `update_page` - Update page (accepts optional `expected_bundle_revision` alongside `expected_revision` to additionally reject the write if a sibling translation or bundle-local asset changed since the caller last read the bundle; omitting it is a no-op — see #857)
- `delete_page` - Delete page
- `delete_page_asset` - Delete page asset
- `upload_page_asset` - Upload page asset (write a new file into an existing leaf page bundle directory; allowed types png/jpg/jpeg/gif/webp/svg, content is sniffed against the declared extension for raster types and structurally validated for SVG, never overwrites — see #348, #571)
- `get_mutation_status` - Get mutation status (idempotency-key lookup for a previous mutation's result)
- `get_rate_limits` - Get rate limits (check remaining per-caller mutation quota before acting; never itself consumes quota)
- `plan_content_change` - Plan content change
- `apply_content_plan` - Apply content plan
- `rollback_change` - Rollback change (recoverable, not data-destroying — restores a prior revision of a single page)
- `plan_bundle_change` - Plan bundle change (bundle-scoped analog of plan_content_change/apply_content_plan for atomic multi-translation edits; see #854)
- `apply_bundle_plan` - Apply bundle plan (validates every translation before writing any; rolls back already-written files on a mid-apply failure — runtime, not crash, atomicity)
- `rollback_bundle` - Rollback bundle (restores every translation touched by a previous apply_bundle_plan)
- `publish_changes` - Publish changes

Write tools also accept an optional `idempotency_key` on non-dry-run calls.
Replaying the exact same mutation with the same key returns the original result
without applying the write again. Reusing the same key for materially different
input returns a structured `idempotency_conflict` error.

- `build_site` - Build website (reports stage-aware detail — Hugo build/output swap/source+public index reload/per-callback outcomes — and page-aware detail — included/excluded_drafts/deleted_outputs; see #858)
- `preview_build` - Preview build
- `run_post_build_hooks` - Run post-build hooks (supports `dry_run:true` to inspect configured targets without contacting them)
- `generate_hero_image` - Generate hero image (response includes `source_key`/`delete_slug`/`delete_scope`/`delete_filename`, ready to feed directly into delete_page_asset for cleanup — see #845)
- `check_sri_versions` - Verify SRI integrity
- `get_runtime_status` - Get runtime status (server version/commit, hugo/git availability, source/public revision hashes, `changed_files_count` when the git baseline is dirty, overdue `test_content` advisories)
- `get_theme_status` - Get theme status (active theme/module name, on-disk presence, Git commit/dirty state for classic themes)
- `verify_publication` - Verify publication (compares source/build/public/index freshness for a page and checks the live public HTTP status; no SSH required)
- `create_preview` - Create preview (builds source, optionally including drafts, into an isolated directory exposed at a temporary token-gated, non-indexable URL; the entry token is single-use, retired once the resulting session is confirmed in active use; see `docs/preview-workflow.md`, #853, #871)
- `list_previews` - List previews
- `revoke_preview` - Revoke preview
- `revoke_all_previews` - Revoke all previews
- `inspect_preview` - Inspect preview rendered page (same rendered-output security/SEO checks as `inspect_rendered`, run against an isolated preview build so draft/test_content pages can be audited before publish; requires `preview_id` from `create_preview`; see #863)
- `get_storage_health` - Get storage health (advisory-only detection of orphaned generated assets, expired preview residue, and source/index/public inconsistencies; never deletes anything itself; see #861)

Legacy scope strings (`content.write`, `site.admin`, `system.admin`, `admin`,
and others — see `docs/mcp-contract.md` §6.12 for the full table) are
accepted as compatibility aliases for `write`, but only `read`/`write` are
advertised as canonical tool tiers.

## Taxonomy Fields

Existing `tags` and `categories` arrays are preserved for backward compatibility. Read tools that return page/frontmatter DTOs may also include:

- `tag_terms`
- `category_terms`

Each term contains:

```json
{
  "source": "postmortem",
  "slug": "postmortem",
  "label": "Postmortem"
}
```

Use `slug` for stable filtering/grouping and `label` for display. The original `source` value remains available for auditing content taxonomy drift.

## Lifecycle State Fields

Page-oriented read and mutation tools may also include a shared additive `state`
object:

```json
{
  "source_state": "present",
  "build_state": "pending",
  "public_state": "not_yet_available",
  "index_state": "source_only"
}
```

Meaning:

- `source_state` - whether source markdown currently exists on disk
- `build_state` - whether Hugo output is up to date with the source view
- `public_state` - whether public HTML is currently available, stale, or removed
- `index_state` - whether the read/index view is fresh, stale, source-only, or removed

Use this instead of inferring lifecycle from empty `html`, `url`, or diff fields.

## Discovery

- `/.well-known/agent.json` - A2A agent card for Google-compatible discovery

## Shared Resources

The server also publishes a small additive MCP resource catalog for reusable shared schemas. Agents that need a canonical entity shape can inspect these via `resources/list` and `resources/read` instead of reverse-engineering the same DTO from multiple tool schemas.

- `schema://mcp-hugo-server-go/contentmodel/page-identity`
- `schema://mcp-hugo-server-go/toolcontract/pagination-meta`
- `schema://mcp-hugo-server-go/site/lifecycle-state`

Use these resources when you need the stable shared contract behind multiple tools; use per-tool input/output schemas when you need the exact shape of one specific call.
