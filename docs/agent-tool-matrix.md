# Agent Tool-Selection Matrix

This document maps common editorial agent scenarios to the right tool.
Use it to choose the correct tool on the first call rather than exploring
by trial and error.

Access terminology in this document:

- `reader` = read-only access profile (internal scope `read`; full
  visibility, drafts included — see `docs/mcp-contract.md` §6.12)
- `operator` = reader profile plus write and site operations (internal
  scope `write`, which implies `read`)

The per-tool notes below still reference the current internal scopes where that
detail matters for runtime setup.

---

## Quick-reference: scenario → tool

| Scenario | Tool | Notes |
|---|---|---|
| Read a published page (summary + HTML) | `get_page` | Reader tool; on OAuth-enabled deployments, obtain a read Bearer token first |
| Read a page's source Markdown for editing | `get_page_markdown` | Reader tool; on OAuth-enabled deployments, use a read Bearer token; includes `page.state` |
| Get a full context bundle before editing | `build_agent_context` | Frontmatter + Markdown + related + `context.state` |
| Get a compact edit bundle in one call | `get_page_for_edit` | Frontmatter + Markdown + `state` + `quality` + `revision`; shape with `include`/`max_body_chars` |
| Bulk-export content for analysis | `export_agent_context` | Tag/category filter + pagination + per-page `state` |
| Simple keyword search | `search_pages` | Published-page search; on OAuth-enabled deployments, obtain a read Bearer token first |
| Filtered search (type, tag, language, sort) | `search_content` | Full filter set + pagination + per-result `state` |
| List all published pages with pagination | `list_pages` | No auth, metadata only |
| Get the full URL list (including taxonomy) | `get_sitemap` | No auth, all slugs |
| Read recent posts for a digest | `get_recent_posts` | No auth |
| List all tags | `list_tags` | No auth |
| List all categories | `list_categories` | No auth |
| Read site name/URL/language | `get_site_information` | No auth |
| Get page metadata only (no body) | `get_page_frontmatter` | Reading time, tags, categories + `frontmatter.state` |
| Find pages related to a slug | `get_related_content` | Shared tags/categories |
| **Suggest links to add in a draft** | `suggest_links` | Tags/categories → ranked suggestions |
| Check what links to a page (before delete) | `get_backlinks` | Impact analysis |
| Show what changed since last Git commit | `diff_page` | Requires local Git; includes `data.state` even in fallback mode |
| Validate frontmatter before publishing | `validate_frontmatter` | One slug or all pages |
| Full-site validation pass | `validate_site` | All source pages |
| Audit all internal broken links | `get_broken_links` | Published index only |
| Understand site structure / onboard | `explain_structure` | Sections, languages, recent |
| Get a health score before publishing | `get_site_health` | Counts + taxonomy warnings |
| **Create a new page** | `create_page` | → then `build_site` |
| **Edit an existing page** | `update_page` | → then `build_site` |
| **Delete a page** | `delete_page` | Rate-limited: 5/min |
| **Build (publish changes)** | `build_site` | Required after write ops |
| Preview the build output | `preview_build` | Dry-run build |
| Run post-build hooks (CDN purge, etc.) | `run_post_build_hooks` | After `build_site` |
| Generate a featured image | `generate_hero_image` | write scope |

---

## Common workflows

### Create and publish a new article

```
create_page(slug, title, tags, categories, body)
  → build_site()
  → [optional] run_post_build_hooks()
```

### Edit an existing article

```
get_page_markdown(slug)          ← read current source
update_page(slug, title?, body?, tags?, ...)
  → build_site()
```

### Full editorial review before editing

```
build_agent_context(slug)             ← frontmatter + Markdown + related pages
diff_page(slug)                       ← check uncommitted changes
update_page(slug, ...)
  → build_site()
```

### Internal linking pass on a draft

```
suggest_links(tags, categories, body)   ← ranked link suggestions
  → update_page(slug, body_with_links_added)
```

### Safe page deletion

```
get_backlinks(slug)                   ← find pages that link here
  → fix backlinks via update_page(...)
  → delete_page(slug)
  → build_site()
```

### Pre-publish quality check

```
get_site_health()                     ← health score + taxonomy warnings
validate_frontmatter(slug)           ← frontmatter issues for one page
validate_site()                       ← full-site validation pass
get_broken_links()                    ← internal link audit
```

---

## Tool-choice decision tree

```
Need to READ a page?
├── No Bearer token yet     → obtain a reader token first on OAuth-enabled deployments
└── Bearer token available
    ├── Just metadata       → get_page_frontmatter
    ├── Markdown (editing)  → get_page_markdown
    └── Full bundle         → build_agent_context

Need to SEARCH?
├── Simple keyword, no auth → search_pages
└── Filtered + paginated    → search_content (type/tag/category/language/sort)

Need to DISCOVER related content?
├── Related to an indexed slug → get_related_content
└── Suggest outgoing links     → suggest_links

Need to WRITE?
├── New page    → create_page → build_site
├── Edit        → update_page → build_site
└── Delete      → get_backlinks first, then delete_page → build_site

Need to VALIDATE?
├── One page    → validate_frontmatter(slug)
└── All pages   → validate_site
```

---

## Tool disambiguation: pairs often confused

### `search_pages` vs `search_content`

| | `search_pages` | `search_content` |
|---|---|---|
| Auth | Reader tool; on OAuth-enabled deployments, use a read Bearer token | Reader tool; on OAuth-enabled deployments, use a read Bearer token |
| Filters | Query only | type, tag, category, language, sort, order |
| Pagination | None | limit, offset, total in response |
| Envelope | Flat `{pages}` | Structured `{success, data, warnings, errors}` |
| Use when | Quick keyword lookup, no token | Precise filtering, pagination, agent workflows |

### `get_page` vs `get_page_markdown` vs `build_agent_context`

| | `get_page` | `get_page_markdown` | `build_agent_context` |
|---|---|---|---|
| Auth | Reader tool; on OAuth-enabled deployments, use a read Bearer token | Reader tool; on OAuth-enabled deployments, use a read Bearer token | Reader tool; on OAuth-enabled deployments, use a read Bearer token |
| Returns | Published HTML + metadata | Source Markdown + frontmatter | Frontmatter + Markdown + related pages |
| Source fallback | Yes (`allow_source_fallback`) | Published only | Published only |
| Use when | Reading/display | About to edit | Full context before editing/summarizing |

### `list_pages` vs `get_sitemap`

| | `list_pages` | `get_sitemap` |
|---|---|---|
| Auth | None | None |
| Scope | Content pages (articles/pages) | All slugs including taxonomy (`/tags/go/`, `/categories/docs/`) |
| Pagination | Yes (limit, offset) | Yes (limit, offset, exclude_taxonomies) |
| Response fields | title, summary, tags, categories, date, URL | slug, URL, date only |
| Use when | Browse content | Full URL inventory, sitemap generation |

### `validate_frontmatter` vs `validate_site`

Both validate Hugo source front matter. `validate_frontmatter` accepts an optional `slug` to target one page; `validate_site` always runs over all pages (it is an alias for `validate_frontmatter` without a slug). Use `validate_frontmatter(slug)` when checking a specific page; use `validate_site` for a full sweep.

---

## Description audit findings (resolved in v1.3.8)

The following description improvements were made during the v1.3.8 audit:

- `get_page`: clarified that `content_only` applies to published pages; added guidance on `allow_source_fallback` for pre-build verification.
- `search_pages` vs `search_content`: descriptions now explicitly cross-reference each other to help agents choose.
- `list_pages` vs `get_sitemap`: descriptions now note the scope difference (content only vs all slugs).
- `validate_site`: noted it is equivalent to `validate_frontmatter` with no slug filter.
- `suggest_links`: new tool, description covers all three input modes (slug, tags/categories, body).
