# mcp-hugo-server-go

[![Go Version](https://img.shields.io/badge/go-1.25.11-00ADD8?logo=go&logoColor=white)](go.mod)
[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest)
[![CI](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Security Policy](https://img.shields.io/badge/security-policy-green.svg)](SECURITY.md)
[![MCP](https://img.shields.io/badge/MCP-streamable--HTTP-purple.svg)](https://modelcontextprotocol.io)
[![ChatGPT](https://img.shields.io/badge/ChatGPT-compatible-10a37f.svg)](https://chatgpt.com/)
[![Claude](https://img.shields.io/badge/Claude.ai-compatible-5f6bed.svg)](https://claude.ai)
[![Agent Ready](https://img.shields.io/badge/IsItAgentReady-100%25-brightgreen.svg)](https://isitagentready.com/www.arleo.eu)

Canonical unified MCP server for Hugo sites.

Public endpoint: `https://mcp.arleo.eu/mcp`

## What it does

`mcp-hugo-server-go` exposes a Hugo site through the Model Context Protocol with public discovery, OAuth-backed scopes, and strict separation between read, write, admin, and system operations.

It is the unified successor of:

- `hugo-public-mcp` for public discovery, OAuth, and `auth.md`
- `hugo-mcp-go` for content and administration tools
- `mcp-runtime-go` for MCP transport/runtime behavior

## Scope model

- `anonymous`: public, safe, read-only discovery
- `content.read`: richer read-only access
- `content.write`: create, update, and delete operations
- `site.admin`: build and site-management operations
- `system.admin`: integrity and diagnostic operations

Legacy clients may still send `mcp` as a scope. The server accepts it as a deprecated compatibility alias for `content.read` only.

## Tool inventory

The current tool inventory is documented in [docs/tools.md](docs/tools.md) and should be treated as the source of truth for scope mapping and tool naming.

## Security model

- Anonymous callers only see public read-only tools.
- OAuth bearer tokens are required for non-public tiers.
- `content.write`, `site.admin`, and `system.admin` are never exposed to anonymous callers.
- The legacy `mcp` alias is accepted for compatibility, but it is not advertised as canonical.

## Claude and MCP

Claude Desktop and Claude.ai can connect directly to the public MCP endpoint above.

The server card and OAuth discovery advertise canonical scopes only:

- `content.read`
- `content.write`
- `site.admin`
- `system.admin`

## Validation

The repository is expected to pass:

```bash
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
gitleaks detect --no-banner --redact --source .
```

## Project lineage

- [hugo-public-mcp](https://github.com/jmrGrav/hugo-public-mcp) - public agent-ready discovery, OAuth, and `auth.md`
- [hugo-mcp-go](https://github.com/jmrGrav/hugo-mcp-go) - Hugo content and administration tools
- [mcp-runtime-go](https://github.com/jmrGrav/mcp-runtime-go) - MCP runtime and transport foundation

`mcp-hugo-server-go` is the canonical unified successor of those repositories.

## Architecture

```
mcp.arleo.eu
├── anonymous       public discovery and safe read-only tools
├── content.read    richer read-only content access
├── content.write   content creation and editing
├── site.admin      build and site operations
└── system.admin    diagnostic and integrity checks
```

The MCP transport is streamable HTTP at `/mcp`.

## Documentation

- [Operator guide](docs/operator-guide.md)
- [Tool inventory](docs/tools.md)
- [Security policy](SECURITY.md)
