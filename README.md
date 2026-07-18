# mcp-hugo-server-go

[![Go Version](https://img.shields.io/badge/go-1.25.11-00ADD8?logo=go&logoColor=white)](go.mod)
[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest)
[![CI](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml)
[![Deploy to Production](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/deploy.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Security Policy](https://img.shields.io/badge/security-policy-green.svg)](SECURITY.md)
[![MCP](https://img.shields.io/badge/MCP-streamable--HTTP-purple.svg)](https://modelcontextprotocol.io)
[![ChatGPT](https://img.shields.io/badge/ChatGPT-compatible-10a37f.svg)](https://chatgpt.com/)
[![Claude](https://img.shields.io/badge/Claude.ai-compatible-5f6bed.svg)](https://claude.ai)
[![Le Chat](https://img.shields.io/badge/Le%20Chat-compatible-ff7000.svg)](https://chat.mistral.ai/)
[![Agent Ready](https://img.shields.io/badge/IsItAgentReady-100%25-brightgreen.svg)](https://isitagentready.com/www.arleo.eu)

Canonical unified MCP server for Hugo sites.

Public endpoint: `https://mcp.arleo.eu/mcp`

## What it does

`mcp-hugo-server-go` exposes a Hugo site through the Model Context Protocol with public discovery, OAuth-backed scopes, and strict separation between read, write, and admin operations.

It is the unified successor of:

- `hugo-public-mcp` for public discovery, OAuth, and `auth.md`
- `hugo-mcp-go` for content and administration tools
- `mcp-runtime-go` for MCP transport/runtime behavior

## Access model

The external access story is intentionally converging toward two profiles:

- `reader`: public-safe read-only discovery and content inspection
- `operator`: reader capabilities plus write and site operations

The runtime still enforces the current internal capability scopes during v1.x:

- `anonymous`: public, safe, read-only discovery
- `reader`: self-service safe read-only access for agent registrations
- `content.read`: richer approved read-only access
- `content.write`: create, update, and delete operations
- `site.admin`: build, site-management, integrity, and diagnostic operations

Legacy clients may still send `mcp` as a scope. The server accepts it as a deprecated compatibility alias for `content.read` only.
Legacy clients may still send `system.admin`; the server accepts it as a compatibility alias for `site.admin`, but it is not advertised as a canonical scope.

## Tool inventory

The current tool inventory is documented in [docs/tools.md](docs/tools.md) and should be treated as the source of truth for scope mapping and tool naming.

## Security model

- Anonymous callers only see public read-only tools.
- Reader-facing discovery is provider-neutral: capability differences depend on token trust, not on whether the client is ChatGPT, Claude, Gemini, Le Chat, Copilot, or another MCP consumer.
- OAuth bearer tokens are required for non-public tiers.
- `content.write` and `site.admin` are never exposed to anonymous callers.
- The legacy `mcp` alias is accepted for compatibility, but it is not advertised as canonical.

## Claude and MCP

Claude Desktop and Claude.ai can connect directly to the public MCP endpoint above.

The server card and OAuth discovery advertise canonical internal scopes only:

- `reader`
- `content.read`
- `content.write`
- `site.admin`

They also publish additive `reader` / `operator` access-profile metadata so
clients can understand the simplified external contract without treating those
profile names as direct OAuth scope strings.

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
├── anonymous       public discovery and safe read-only tools
├── reader          self-service safe read-only tokens
├── content.read    richer approved read-only content access
├── content.write   content creation and editing
└── site.admin      build, site, integrity, and diagnostic operations
```

The MCP transport is streamable HTTP at `/mcp`.

## Security contact

To report a vulnerability, set `security_contact` in your server config (e.g., `security_contact: "mailto:security@example.com"`). This populates `/.well-known/security.txt` per RFC 9116. The server requires `Contact` and `Expires` — Canonical is set automatically from `site_url` (or `oauth.issuer` if `site_url` is blank).

## Agent identity flow

Agents authenticate via the identity assertion flow:

1. Agent POSTs to `/agent/identity` with `{"type":"anonymous"}`.
2. If `oauth.allow_reader_self_registration` is enabled, the response is immediately exchangeable at `/token` (`grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`) for a `reader` Bearer token.
3. If self-registration is disabled, the response includes `claim_token` + `verification_uri`; the agent POSTs to `/agent/identity/claim`, then an operator visits the `verification_uri` (or POSTs to `/agent/identity/verify`) with a `site.admin` Bearer token and the `claim_token` to approve.
4. The approved assertion then exchanges at `/token` for the configured read token.

Today this flow still yields the internal `content.read` capability. The
published `reader` / `operator` profile language is an external contract layer,
not yet a replacement for the underlying scope strings.

Today this flow still yields the internal `content.read` capability. The
published `reader` / `operator` profile language is an external contract layer,
not yet a replacement for the underlying scope strings.

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
