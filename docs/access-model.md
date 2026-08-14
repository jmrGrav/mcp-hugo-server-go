# Access Model

This document describes the **current runtime contract**. It replaces the
older pre-`#450` design notes that assumed a future "public-safe reader"
profile distinct from full source-aware read access.

For the authoritative per-tool contract, see:

- [docs/mcp-contract.md](docs/mcp-contract.md)
- [docs/tools.md](docs/tools.md)
- [docs/operator-guide.md](docs/operator-guide.md)

## Current runtime summary

The live server exposes exactly three canonical scopes:

| Canonical scope | Meaning today |
| --- | --- |
| `read` | Full source-aware visibility, including drafts and other source-only / pre-publication content |
| `write` | `read` plus every mutation and site-operation tool |
| `admin` | `write` plus the four managed Hugo binary lifecycle tools: `stage_hugo_upgrade`, `activate_hugo`, `rollback_hugo`, `bootstrap_hugo` |

Important consequences:

- There is **no separate public-only read scope** in the current runtime.
- A `write` token **implies `read`**; an `admin` token **implies `write`** (and
  therefore `read`).
- `revoke_all_previews` stays `write`-scoped, deliberately: it is a bulk
  action scoped to previews owned by the calling caller
  (`RevokeAllOwned(owner)`), not a cross-tenant administrative operation, so
  it does not belong in `admin` alongside tools that touch the shared Hugo
  binary.
- OAuth discovery and docs may still use `reader` / `operator` /
  `administrator` as human-facing labels, but they are descriptive names
  layered over the same three canonical scopes:
  - `reader` -> `read`
  - `operator` -> `write`
  - `administrator` -> `admin`

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
but they are normalized to the canonical three-scope model and are never
advertised as canonical. `admin` itself is not a legacy alias — it is one of
the three canonical scope names (see `internal/oauth/scope_alias.go`), listed
in the table below only to show which *other* legacy strings resolve to it.

Current normalization summary:

| Accepted input scope(s) | Canonical runtime scope |
| --- | --- |
| `mcp`, `read`, `content.read`, `reader` | `read` |
| `write`, `content.write` | `write` |
| `admin`, `site.admin`, `site_admin`, `siteadmin`, `system.admin`, `system_admin`, `systemadmin` | `admin` |

This is a compatibility layer only. New docs, discovery metadata, OAuth
clients, tests, and operator guidance should speak in terms of `read`,
`write`, and `admin`.

## Tool-family overview

At a high level:

| Tool family | Effective scope today |
| --- | --- |
| Public browse/read tools (`list_pages`, `get_page`, `search_pages`, etc.) | anonymous surface |
| Source-aware read tools (`get_page_markdown`, `get_page_frontmatter`, `get_page_for_edit`, `build_agent_context`, `export_agent_context`, `search_content`, `validate_*`, etc.) | `read` |
| Mutations (`create_page`, `update_page`, `delete_page`, plans, rollback, asset writes/deletes) | `write` |
| Site operations (`create_preview`, `build_site`, `verify_publication`, `generate_hero_image`, `get_runtime_status`, `get_theme_status`, `check_sri_versions`) | `write` |
| Managed Hugo binary lifecycle (`stage_hugo_upgrade`, `activate_hugo`, `rollback_hugo`, `bootstrap_hugo`) | `admin` |

## Historical note

Earlier design work described a different target model:

- a public-safe `reader` profile
- a broader `operator` profile
- multiple internal tiers such as `content.read`, `content.write`, and
  `site.admin`

That four-tier model was collapsed to two scopes in `#450`: `content.read`
folded into `read`, and `content.write`/`site.admin` folded together into a
single `write` tier. `#1039`/`#1050` later reintroduced a third tier, but not
by reinstating a narrower `site.admin`-style write tier or a restricted
public-only read tier — the new `admin` scope sits **above** `write`, gating
only the four Hugo binary lifecycle tools. That shape was a deliberate choice:
the two-scope model existed specifically to avoid the "reader outage" class of
incident (`#448`/`#449`), where a caller presenting a scope string the server
no longer recognized could fail to resolve at all. Re-narrowing `read` (or
inserting a distinct restricted-write tier below `write`) would have
reintroduced exactly that risk for existing `read`/`write` callers; adding a
strictly-additive `admin` tier on top does not, since every existing `read`
and `write` caller keeps working unchanged. The live runtime contract is the
three-scope model summarized above.
