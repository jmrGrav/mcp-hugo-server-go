# Access Model Design

This document is the design anchor for issue `#352`.

It does **not** change authorization semantics on its own. Its job is to pin
down the current verified model, the target external model, and the migration
invariants before follow-up implementation issues widen any visibility.

## Current verified internal model

The repository currently exposes 33 tools across four internal scope tiers:

| Internal scope | Count | Notes |
| --- | ---: | --- |
| `anonymous` | 9 | Public browse/read surfaces |
| `content.read` | 14 | Source-aware read and validation surfaces |
| `content.write` | 3 | Source mutations |
| `site.admin` | 7 | Build, hook, image, SRI, runtime status, and theme status operations |

Compatibility aliases that still exist today:

- `mcp` -> `content.read`
- `system.admin` -> `site.admin`

These are implementation details and should not remain the user-facing access
story.

## Target external model

The long-term external contract should expose two profiles only:

| External profile | Intended acquisition path | Effective capability bundle |
| --- | --- | --- |
| `reader` | anonymous or self-serve registration | public-safe read-only tools |
| `operator` | approved token present in the DB | `reader` + write + site operations |

The server may still keep finer-grained internal permissions:

- `content.read`
- `content.write`
- `site.operate` (target name for the current `site.admin`)

`operator` is expected to be a bundle of internal permissions, not a separate
parallel authorization model.

## Non-negotiable invariants

1. Capability differences must depend only on token trust, never on provider
   identity (`ChatGPT`, `Claude`, `Gemini`, `Le Chat`, `Copilot`, or another
   MCP client).
2. No implicit promotion path may exist from `reader` to `operator`.
3. No mutation, build, or hook capability may become visible to a `reader`.
4. Read-only tools may move to the `reader` profile only after the public-safe
   response policy is defined and enforced.
5. During v1.x, compatibility aliases may remain accepted internally, but the
   published contract should converge on `reader` / `operator` externally and
   `content.read` / `content.write` / `site.operate` internally.

## Verified current tool matrix

The following matrix is derived from the actual tool registry and runtime code,
not from README prose alone.

Legend used in `MCP annotations`:

- `RO` = `readOnlyHint=true`
- `IDEM` = `idempotentHint=true`
- `DEST` = `destructiveHint=true`
- `OPEN` = `openWorldHint=true`
- `CLOSED` = `openWorldHint=false`

| Tool | Real behavior | Current scope | Target external profile | Sensitive data risk | MCP annotations |
| --- | --- | --- | --- | --- | --- |
| `list_pages` | Published content listing | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_page` | Published page read with source fallback states | `anonymous` | `reader` | medium | `RO, IDEM, CLOSED` |
| `search_pages` | Anonymous keyword search | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_recent_posts` | Recent published posts | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `list_tags` | Published tag listing | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `list_categories` | Published category listing | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_sitemap` | Published URL inventory | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_feed` | Published feed slice | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_site_information` | Site metadata | `anonymous` | `reader` | low | `RO, IDEM, CLOSED` |
| `get_full_page_markdown` | Source markdown read | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `get_page_frontmatter` | Source/frontmatter metadata read | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `get_related_content` | Related-content analysis | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `build_agent_context` | Source-aware edit context bundle | `content.read` | `reader` after public-safe filtering | high | `RO, IDEM, CLOSED` |
| `export_agent_context` | Bulk source/context export | `content.read` | `reader` after public-safe filtering | high | `RO, IDEM, CLOSED` |
| `search_content` | Filtered source-aware search | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `explain_site_structure` | Site-structure summary | `content.read` | `reader` after public-safe filtering | low | `RO, IDEM, CLOSED` |
| `get_site_health` | Site health summary | `content.read` | `reader` after public-safe filtering | low | `RO, IDEM, CLOSED` |
| `get_broken_links` | Broken-link analysis | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `get_backlinks` | Backlink analysis | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `suggest_internal_links` | Link suggestion analysis | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `diff_page` | Source vs Git diff | `content.read` | `reader` after public-safe filtering | high | `RO, IDEM, CLOSED` |
| `validate_front_matter` | Source validation | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `validate_site` | Full-site source validation | `content.read` | `reader` after public-safe filtering | medium | `RO, IDEM, CLOSED` |
| `create_page` | Create source content | `content.write` | `operator` | high | `OPEN` |
| `update_page` | Mutate source content | `content.write` | `operator` | high | `IDEM, OPEN` |
| `delete_page` | Delete source content | `content.write` | `operator` | high | `DEST, OPEN` |
| `build_site` | Build/publish site output | `site.admin` | `operator` | high | `OPEN` |
| `preview_build` | Preview build execution | `site.admin` | `operator` | high | `CLOSED` |
| `run_post_build_hooks` | Outbound post-build webhooks | `site.admin` | `operator` | high | `OPEN` |
| `generate_featured_image` | Generate/write image asset | `site.admin` | `operator` | high | `CLOSED` |
| `check_sri_versions` | Live SRI verification against remote assets | `site.admin` | `operator` | medium | `RO, IDEM, OPEN` |
| `get_runtime_status` | Runtime/build/git/site status surface | `site.admin` | `operator` | medium | `RO, IDEM, OPEN` |
| `get_theme_status` | Active theme/module presence + Git commit/dirty state | `site.admin` | `operator` | medium | `RO, IDEM, OPEN` |

## Why `check_sri_versions` stays operator-only

`check_sri_versions` is read-only, but it is still not a good `reader` tool in
the target model:

- it scans operator-managed templates and SRI data files
- it performs live outbound fetches
- it can surface environment-specific diagnostics that are not part of the
  normal editorial read path

This is the canonical example of a tool that is read-only in the MCP sense but
still belongs to `operator` in the external access model.

## Migration decisions captured here

### `site.admin` -> `site.operate`

Recommended direction:

- keep `site.admin` accepted during v1.x
- introduce `site.operate` as the clearer internal name in follow-up work
- publish `operator` externally rather than surfacing either internal string to
  end users

### `system.admin`

Recommended direction:

- keep as compatibility alias during v1.x only
- do not advertise it in discovery or docs
- cover any remaining uses with compatibility tests before eventual removal

## Dependencies and rollout order

This document intentionally precedes the implementation issues that depend on
it:

1. `#354` — public-safe read response policy
2. `#353` — self-serve reader registration
3. `#355` — operator parity across MCP clients
4. `#357` — discovery metadata and OAuth docs aligned with the new model

No follow-up should widen reader visibility before `#354` is implemented.
