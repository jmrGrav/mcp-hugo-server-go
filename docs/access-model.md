# Access Model

This document describes the **current runtime contract**. It replaces the
older pre-`#450` design notes that assumed a future "public-safe reader"
profile distinct from full source-aware read access.

For the authoritative per-tool contract, see:

- [docs/mcp-contract.md](docs/mcp-contract.md)
- [docs/tools.md](docs/tools.md)
- [docs/operator-guide.md](docs/operator-guide.md)

## Current runtime summary

The live server exposes exactly two canonical scopes:

| Canonical scope | Meaning today |
| --- | --- |
| `read` | Full source-aware visibility, including drafts and other source-only / pre-publication content |
| `write` | `read` plus every mutation and site-operation tool |

Important consequences:

- There is **no separate public-only read scope** in the current runtime.
- A `write` token **implies `read`**.
- Former `site.admin` operations are now part of `write`.
- OAuth discovery and docs may still use `reader` / `operator` as
  human-facing labels, but they are descriptive names layered over the same
  two canonical scopes:
  - `reader` -> `read`
  - `operator` -> `write`

## Anonymous vs authenticated read

Anonymous callers and `read`-scoped callers do **not** have the same
effective access:

- anonymous callers only see the anonymous tool surface
- authenticated `read` callers can invoke the read-tier tools and receive full
  source-aware visibility

On OAuth-enabled deployments, `/mcp` still requires a Bearer token even to
call anonymous-tier tools. "Anonymous" in the tool matrix therefore means
"no additional scope beyond transport authentication", not "unauthenticated
HTTP access to `/mcp`".

## Compatibility aliases

Legacy scope strings are still accepted as input for backward compatibility,
but they are normalized to the canonical two-scope model and are never
advertised as canonical.

Current normalization summary:

| Accepted input scope(s) | Canonical runtime scope |
| --- | --- |
| `mcp`, `read`, `content.read`, `reader` | `read` |
| `write`, `content.write`, `site.admin`, `site_admin`, `siteadmin`, `system.admin`, `admin`, `system_admin`, `systemadmin` | `write` |

This is a compatibility layer only. New docs, discovery metadata, OAuth
clients, tests, and operator guidance should speak in terms of `read` and
`write`.

## Tool-family overview

At a high level:

| Tool family | Effective scope today |
| --- | --- |
| Public browse/read tools (`list_pages`, `get_page`, `search_pages`, etc.) | anonymous surface |
| Source-aware read tools (`get_page_markdown`, `get_page_frontmatter`, `get_page_for_edit`, `build_agent_context`, `export_agent_context`, `search_content`, `validate_*`, etc.) | `read` |
| Mutations (`create_page`, `update_page`, `delete_page`, plans, rollback, asset writes/deletes) | `write` |
| Site operations (`create_preview`, `build_site`, `verify_publication`, `generate_hero_image`, runtime/theme/SRI/admin diagnostics) | `write` |

If a future product decision intentionally introduces a third
published-content-only read scope, that must be specified and tested as a new
runtime contract. It must not be implied by legacy `reader` wording.

## Historical note

Earlier design work described a different target model:

- a public-safe `reader` profile
- a broader `operator` profile
- multiple internal tiers such as `content.read`, `content.write`, and
  `site.admin`

That is **not** the shipped contract anymore. The repository kept some helper
names and historical issue references from that period, but the live runtime
contract is the two-scope model summarized above.
