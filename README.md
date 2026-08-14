# mcp-hugo-server-go

[![Go Version](https://img.shields.io/badge/go-1.25.11-00ADD8?logo=go&logoColor=white)](go.mod)
[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest)
[![CI](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml)
[![Production Go LOC](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2FjmrGrav%2Fmcp-hugo-server-go%2Fbadges%2Fsource-loc.json&logo=go&logoColor=white)](#production-go-loc)
[![Deploy to Production](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/deploy.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Security Policy](https://img.shields.io/badge/security-policy-green.svg)](SECURITY.md)
[![MCP](https://img.shields.io/badge/MCP-streamable--HTTP-purple.svg)](https://modelcontextprotocol.io)
[![MCP stdio](https://img.shields.io/badge/MCP-stdio-purple.svg)](https://github.com/jmrGrav/mcp-hugo-server-go#installation)
[![npx](https://img.shields.io/badge/npx-%40jmrgrav%2Fmcp--hugo--server--go-cb3837.svg?logo=npm&logoColor=white)](https://www.npmjs.com/package/@jmrgrav/mcp-hugo-server-go)
[![Claude Desktop](https://img.shields.io/badge/Claude%20Desktop-compatible-5f6bed.svg)](https://github.com/jmrGrav/mcp-hugo-server-go#installation)
[![ChatGPT](https://img.shields.io/badge/ChatGPT-compatible-10a37f.svg)](https://chatgpt.com/)
[![Claude](https://img.shields.io/badge/Claude.ai-compatible-5f6bed.svg)](https://claude.ai)
[![Le Chat](https://img.shields.io/badge/Le%20Chat-compatible-ff7000.svg)](https://chat.mistral.ai/)
[![Agent Ready](https://img.shields.io/badge/IsItAgentReady-100%25-brightgreen.svg)](https://isitagentready.com/www.arleo.eu)

Canonical unified MCP server for Hugo sites.

Public endpoint: `https://mcp.arleo.eu/mcp`

This MCP is far more than a remote Markdown editor for [Hugo](https://gohugo.io): it's an intelligent content-management interface. It gives AI agents structured understanding and safe operations on a Hugo site. Example site using this MCP: [www.arleo.eu](https://www.arleo.eu).

Ce MCP est bien plus qu'un éditeur à distance de Markdown pour [Hugo](https://gohugo.io) : il est une interface de gestion intelligente du contenu. Il donne aux agents IA une compréhension structurée et des opérations sûres sur un site Hugo. Exemple de site utilisant ce MCP : [www.arleo.eu](https://www.arleo.eu).

Content mostly written with Claude Code and Codex. / Contenu majoritairement codé avec Claude Code et Codex.

### Production Go LOC

The badge counts `scc` code lines from Git-tracked production `.go` files. It excludes comments, blank lines, untracked files, and every `*_test.go` file. CI recalculates and publishes the badge payload automatically after every push to `main`; contributors never edit the count in this README.

## What it does

`mcp-hugo-server-go` exposes a Hugo site through the Model Context Protocol with public discovery, OAuth-backed scopes, and strict separation between read, write, and admin operations.

It is the unified successor of:

- [`hugo-public-mcp`](https://github.com/jmrGrav/hugo-public-mcp) for public discovery, OAuth, and `auth.md`
- [`hugo-mcp-go`](https://github.com/jmrGrav/hugo-mcp-go) for content and administration tools
- [`mcp-runtime-go`](https://github.com/jmrGrav/mcp-runtime-go) for MCP transport/runtime behavior

## Installation

The same binary supports two transport modes. Pick based on who you are — they are not interchangeable and exist for different purposes.

### Local, single-user (most people): stdio transport

Use this if you manage your own Hugo site and want an MCP-capable client (Claude Desktop, or any other MCP host that can launch a local subprocess) to edit it directly on your machine. No OAuth, no server process to run or expose — the client launches the binary itself and talks to it over stdin/stdout.

**Via npx/npm** (`npm/` in this repository — [package README](npm/README.md)): downloads and checksum-verifies the matching release binary automatically, no manual OS/arch selection needed.

```bash
npx @jmrgrav/mcp-hugo-server-go
```

**Or download the binary directly**:

1. Download the `mcp-hugo-server-go` binary for your OS/arch from the [latest release](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest).
2. Configure it either via a `config.yaml` (see [docs/operator-guide.md](docs/operator-guide.md) for the full field reference) with `transport: stdio`, or — if your MCP host can only inject environment variables, not a config file, which is the case for MCPB-style desktop extension installs — via the `MCP_HUGO_SITE_ROOT` / `MCP_HUGO_HUGO_ROOT` / `MCP_HUGO_CONTENT_ROOT` / `MCP_HUGO_SITE_URL` / `MCP_HUGO_SITE_NAME` environment variables instead. A file's values always win; env vars only fill in whatever the file (or an absent file) leaves empty.
3. Point your MCP host at the binary as its launch command. See `manifest.json` in this repository for the shape a desktop-extension host expects.
4. See [Privacy policy](#privacy-policy) below for exactly what this mode does and does not do with your data — nothing leaves your machine by default.

**Or use the packaged `.mcpb` desktop extension**: download the `.mcpb` file attached to the [latest release](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest) and double-click it (or drag it into Claude Desktop's Settings → Extensions) — Claude Desktop prompts for the same `user_config` fields (`site_root`/`hugo_root`/`content_root`/`site_url`/`site_name`) described above. Submitted to the Claude Connectors Directory; not yet listed there pending review, but installable manually today via the release download. See the [wiki's Installation Guide](https://github.com/jmrGrav/mcp-hugo-server-go/wiki/Installation-Guide) for a deeper walkthrough of all three install paths side by side.

### Shared/remote, multi-user (advanced): HTTP + OAuth transport

Use this if you want a persistent, remotely-reachable instance — e.g. to let an agent running somewhere else (not on the machine with your Hugo site) manage the site, or to share access across multiple OAuth clients with `read`/`write` scoping. This is how this project's own instance at `https://mcp.arleo.eu/mcp` runs. It requires you to run and expose the server yourself (reverse proxy, TLS, OAuth client registration) — see [docs/operator-guide.md](docs/operator-guide.md) for a full deployment walkthrough. This is a materially higher setup cost than stdio and is meant for that more advanced use case, not the default choice.

## Access model

The server enforces exactly three internal scopes (#450, extended by #1039/#1050):

- `read`: full visibility, including drafts and other source-only/pre-publication
  content. Requires no secret and is auto-registrable (self-service, the same
  mechanism the old `reader` profile used).
- `write`: requires a registered OAuth client (`client_id` + `client_secret`).
  Implies `read` — a `write` token gets everything, including build/site/integrity/
  diagnostic operations that used to require a separate `site.admin` scope.
- `admin`: requires an explicitly approved administrator OAuth client. Implies
  `write` and additionally gates the four managed Hugo binary lifecycle tools
  (`stage_hugo_upgrade`, `activate_hugo`, `rollback_hugo`, `bootstrap_hugo`).

Legacy clients may still send any scope string from the pre-#450 four-tier model
(`reader`, `content.read`, `content.write`, `site.admin`, `system.admin`, ...) or the
original `mcp` alias. The server accepts all of them as deprecated compatibility
aliases, resolved to `read`/`write`/`admin` via `oauth.CanonicalScope`
(`site.admin`/`system.admin` resolve to `admin`), but only `read`, `write`, and
`admin` are ever advertised as canonical scopes. See
[docs/mcp-contract.md §6.12](docs/mcp-contract.md#612-3-scope-model-readwriteadmin-450)
for the full mapping and rationale.

## Tool inventory

The current tool inventory is documented in [docs/tools.md](docs/tools.md) and should be treated as the source of truth for scope mapping and tool naming.

## Slug formats: `slug` vs `source_key`

Tool payloads use two different shapes for the same page identity, and mixing them up is a recurring source of confusion (#610):

- **`slug` on read-tool outputs** (`list_pages`, `search_pages`, `get_recent_posts`, `get_sitemap`, `get_feed`, `get_page`, etc.) is the canonical **public URL form**, e.g. `/posts/my-article/` (or `/en/posts/my-article/` for a non-default language).
- **`slug` on write-tool inputs** (`create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`) expects the **source-relative `source_key` form**, e.g. `posts/my-article` — no leading/trailing slashes, no language prefix. (`suggest_links`, a read-scoped tool, is the exception: its `slug` input takes the public URL form, matching read-tool outputs.)
- To avoid reformatting by hand, every read tool that returns `slug` also returns `source_key` alongside it, in exactly the form the write tools' `slug` input expects. Feed `source_key` from a read-tool result straight into a write tool's `slug` parameter.

See [docs/mcp-contract.md](docs/mcp-contract.md) for the full per-tool field reference.

## Recommended authoring workflow

For a fresh article, the suggested call order is:

1. `list_content_types` — confirm the content type and required front matter.
2. `suggest_links(tags, categories, body)` — run against your draft tags/body *before* writing, to surface internal-linking candidates while the content is still easy to adjust (#623).
3. `create_page` — write the page (`create_page`'s own description also cross-references `suggest_links` as a pre-write step).
4. `verify_publication` — confirm the change actually went live after a build.

## Multi-page editorial changes

When a single logical change spans several pages (e.g. renaming a category across an entire series, or a coordinated cross-linking pass), don't call `publish_changes` after each page — it triggers a full site build, so publishing once per page instead of once for the whole batch costs a build per page for no benefit and makes a half-applied batch briefly visible on the live site between builds.

The recommended shape (#631):

1. For each page: `plan_content_change` → review the returned preview/diff → if it looks right, `apply_content_plan` immediately.
   Apply each plan right after previewing it rather than collecting previews for the whole batch first — `plan_content_change`'s `plan_id` is a single-use preview with a 5-minute TTL (`data.plan_expires_at`), so a plan-everything-then-apply-everything ordering risks the earliest plans expiring before you get to them on a large batch.
2. Track the `plan_id`/`revision` returned for each page as you go — `apply_content_plan` fails closed with `revision_conflict` if a page changed since its plan was made, and `rollback_change` (per page, using the tracked revision) is how you undo any single page in the batch if something downstream turns out wrong.
3. Once every page in the batch has been applied, call `publish_changes` **once** for the whole site.

No new orchestration tool is needed for this — `apply_content_plan`'s existing per-page revision pinning and `rollback_change`'s per-page undo already compose into this pattern; a batch-level primitive would just be a wrapper around the same three calls.

## Security model

- Anonymous callers and `read`-scoped callers see the same tool set — `read` carries no additional visibility restriction (#450).
- Reader-facing discovery is provider-neutral: capability differences depend on token trust, not on whether the client is ChatGPT, Claude, Gemini, Le Chat, Copilot, or another MCP consumer.
- An OAuth bearer token with `write` scope is required for mutating and operational tools.
- `write` is never exposed to anonymous or `read`-scoped callers.
- An OAuth bearer token with `admin` scope is required for the four managed Hugo binary lifecycle tools (`stage_hugo_upgrade`, `activate_hugo`, `rollback_hugo`, `bootstrap_hugo`); `admin` is never exposed to `read`- or `write`-scoped callers.
- Legacy scope aliases (`mcp`, `reader`, `content.read`, `content.write`, `site.admin`, `system.admin`, ...) are accepted for compatibility, but only `read`/`write`/`admin` are advertised as canonical.

## Privacy policy

This section applies specifically to the **stdio transport** (`transport: stdio`) — the mode used for a local, single-user install such as an MCPB desktop extension. It does not describe the operator-run `mcp.arleo.eu` HTTP+OAuth deployment, which is a separate, self-hosted service with its own operational practices.

**Data collection:** none. This server does not collect, transmit, or store any usage data, telemetry, or analytics about you or your content.

**Data processing:** all reads and writes happen entirely on your own machine, against the Hugo site directories (`site_root`/`hugo_root`/`content_root`) you configure. Content you create, edit, or delete through this server never leaves your machine as part of that operation.

**External network calls — none by default, opt-in only:** the base install (no optional config fields set) makes exactly one kind of external-adjacent call: invoking your local `hugo` CLI as a subprocess to build your site, which itself does not require network access. A handful of *optional*, individually-configured integrations do call external services, and only run if you explicitly set the corresponding config field:

| Feature | Config field | External service called |
|---|---|---|
| Post-build webhooks | `post_build_hooks` | Whatever URL(s) you configure |
| AI hero-image generation | `image_gen_url` / `image_gen_key` | Whatever image-generation API you configure |
| Preview ingress verification | `preview_external_verification` | The operator-configured OAuth issuer (`/preview/` only) |
| Cloudflare cache purge | `cloudflare.*` | Cloudflare's API |
| IndexNow search-engine ping | `indexnow.*` | IndexNow's API (or your configured endpoint) |
| Google Search Console indexing | `google_indexing.*` | Google's Indexing API |

None of these are set by default. If you never configure them, this server makes no outbound network calls at all beyond running your local `hugo` build.

**Data retention:** any local state this server keeps (SQLite indexes, rate-limit counters, idempotency keys) lives entirely in files you control (`db_path`, etc.) on your own machine, and is deleted whenever you delete those files.

**Third parties:** none, beyond the optional integrations you explicitly configure above, each of which is subject to that third party's own privacy practices.

**Contact:** see [Security contact](#security-contact) below, or open an issue on this repository.

## Claude and MCP

Claude Desktop and Claude.ai can connect directly to the public MCP endpoint above.

The server card and OAuth discovery advertise canonical internal scopes only:

- `read`
- `write`

They also publish additive `reader` / `operator` access-profile metadata so
clients can understand the simplified external contract without treating those
profile names as direct OAuth scope strings. (`reader`'s `internal_scopes` is
`["read"]` and `operator`'s is `["write"]` — `write` implies `read`, so no
second entry is needed.)

Public compatibility discovery for external scanners lives on the website
surface as well:

- `https://www.arleo.eu/auth.md`
- `https://www.arleo.eu/.well-known/oauth-protected-resource`

That `www` surface is served through Hugo static files plus OpenResty, not only
through the Go MCP runtime. The operator recovery notes live in
[docs/agent-ready-howto.md](docs/agent-ready-howto.md).

## Validation

The repository is expected to pass:

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
go build ./...
govulncheck ./...
gitleaks detect --no-banner --redact --source .
```

## Release flow

Production promotion is intentionally split into three explicit stages:

1. Merge to `main` and wait for `CI` to go green.
2. Run `Deploy to Production` for the exact `main` commit you want live.
3. Run `Release` only after production deployment succeeds. The release workflow refuses to publish unless:
   - the requested ref resolves to the current `origin/main` HEAD;
   - `CHANGELOG.md` contains the requested version;
   - `README.md` still uses dynamic latest-release metadata;
   - the target SHA already has a successful `production` deployment record.

## Project lineage

- [hugo-public-mcp](https://github.com/jmrGrav/hugo-public-mcp) - public agent-ready discovery, OAuth, and `auth.md`
- [hugo-mcp-go](https://github.com/jmrGrav/hugo-mcp-go) - Hugo content and administration tools
- [mcp-runtime-go](https://github.com/jmrGrav/mcp-runtime-go) - MCP runtime and transport foundation

`mcp-hugo-server-go` is the canonical unified successor of those repositories.

## Architecture

```
mcp.arleo.eu
├── read (anonymous or self-service token)   full content visibility, including drafts
└── write (registered OAuth client only)     content creation/editing plus build, site, integrity, and diagnostic operations
```

The MCP transport is streamable HTTP at `/mcp`.

## Security contact

To report a vulnerability, set `security_contact` in your server config (e.g., `security_contact: "mailto:security@example.com"`). This populates `/.well-known/security.txt` per RFC 9116. The server requires `Contact` and `Expires` — Canonical is set automatically from `site_url` (or `oauth.issuer` if `site_url` is blank).

## Agent identity flow

Agents authenticate via the identity assertion flow:

1. Agent POSTs to `/agent/identity` with `{"type":"anonymous"}`.
2. If `oauth.allow_reader_self_registration` is enabled, the response is immediately exchangeable at `/token` (`grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`) for a `read` Bearer token.
3. If self-registration is disabled, the response includes `claim_token` + `verification_uri`; the agent POSTs to `/agent/identity/claim`, then an operator visits the `verification_uri` (or POSTs to `/agent/identity/verify`) with a `write` Bearer token and the `claim_token` to approve.
4. The approved assertion then exchanges at `/token` for the configured read token.

This flow yields the internal `read` scope. The published `reader` / `operator`
profile language is an external contract layer over the same underlying
`read`/`write` scope strings, not a separate mechanism.

The POST to `/agent/identity/verify` requires operator authentication via the `Authorization: Bearer <admin-token>` header (or `admin_token` form field for browser submissions).

## API reference

| Endpoint | Method | Description |
|---|---|---|
| `/mcp` | GET/POST/DELETE | MCP Streamable HTTP transport |
| `/.well-known/oauth-authorization-server` | GET | OAuth 2.0 authorization server metadata (RFC 8414) |
| `/.well-known/oauth-protected-resource` | GET | Protected resource metadata (RFC 9728) |
| `/.well-known/mcp/server-card.json` | GET | MCP server card |
| `/.well-known/mcp.json` | GET | MCP server card (alias) |
| `/.well-known/agent.json` | GET | Agent card (Google A2A schema) |
| `/.well-known/security.txt` | GET | Security contact (RFC 9116) |
| `/robots.txt` | GET | Robots exclusion |
| `/llms.txt` | GET | LLM discovery |
| `/auth.md` | GET | Authentication guide |
| `/metrics` | GET | Prometheus metrics |
| `/register` | POST | OAuth dynamic client registration |
| `/authorize` | GET/POST | OAuth authorization endpoint |
| `/token` | POST | OAuth token endpoint |
| `/agent/identity` | POST | Register agent identity |
| `/agent/identity/claim` | POST | Initiate agent claim |
| `/agent/identity/verify` | GET/POST | Operator agent approval page |
| `/agent/event/notify` | POST | Agent event notifications |

## Documentation

- [Operator guide](docs/operator-guide.md)
- [AgentReady 100% HowTo](docs/agent-ready-howto.md)
- [MCP contract](docs/mcp-contract.md)
- [Agent tool matrix](docs/agent-tool-matrix.md)
- [Invariant matrix](docs/invariant-matrix.md)
- [Release checklist](docs/release-checklist.md)
- [Staging runbook](docs/staging-runbook.md)
- [Tool inventory](docs/tools.md)
- [Contributing guide](CONTRIBUTING.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Security policy](SECURITY.md)
- [Operations wiki](https://github.com/jmrGrav/mcp-hugo-server-go/wiki)
