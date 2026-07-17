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

Public documentation uses two external profiles:

- `reader`: all public-safe read-only tools
- `operator`: reader tools plus write and site operations

The registry below still lists the current internal scope tiers enforced by the
runtime during v1.x so the mapping stays explicit and auditable.

## Search tool selection (#326)

Two overlapping search tools exist: `search_pages` (anonymous) and
`search_content` (`content.read`). If an agent has `content.read` scope,
prefer `search_content` — it also matches body text and supports
type/language/sort filtering that `search_pages` doesn't (both tools
support `limit`/`offset` pagination).
`search_pages` exists for callers with no authentication at all; it is not
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

## `content.read`

- `get_page_markdown` - Get full page Markdown
- `get_page_frontmatter` - Get page frontmatter
- `get_related_content` - Get related content
- `build_agent_context` - Build agent context
- `export_agent_context` - Export agent context
- `get_page_for_edit` - Get page for edit (compact edit bundle: frontmatter + markdown + state + quality + revision in one call; see #339)
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

## `content.write`

- `create_page` - Publish page
- `update_page` - Update page
- `delete_page` - Delete page

Write tools also accept an optional `idempotency_key` on non-dry-run calls.
Replaying the exact same mutation with the same key returns the original result
without applying the write again. Reusing the same key for materially different
input returns a structured `idempotency_conflict` error.

## `site.admin`

- `build_site` - Build website
- `preview_build` - Preview build
- `run_post_build_hooks` - Run post-build hooks
- `generate_hero_image` - Generate hero image
- `check_sri_versions` - Verify SRI integrity
- `get_runtime_status` - Get runtime status (server version/commit, hugo/git availability, source/public revision hashes)
- `get_theme_status` - Get theme status (active theme/module name, on-disk presence, Git commit/dirty state for classic themes)
- `verify_publication` - Verify publication (compares source/build/public/index freshness for a page and checks the live public HTTP status; no SSH required)
- `create_preview` - Create preview (builds source, optionally including drafts, into an isolated directory exposed at a temporary token-gated, non-indexable URL; see `docs/preview-workflow.md`)

`system.admin` is accepted as a legacy compatibility alias for `site.admin`, but it is not advertised as a canonical tool tier.

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
