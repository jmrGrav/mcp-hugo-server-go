# mcp-hugo-server-go

Canonical unified MCP server for Hugo sites.

**Status**: bootstrap — migration in progress from:
- [`hugo-public-mcp`](https://github.com/jmrGrav/hugo-public-mcp) — public agent-ready surface, OAuth, auth.md (100/100 IsItAgentReady)
- [`hugo-mcp-go`](https://github.com/jmrGrav/hugo-mcp-go) — content/admin tools (write, rebuild, deploy)
- [`mcp-runtime-go`](https://github.com/jmrGrav/mcp-runtime-go) — MCP runtime/transport base

## Architecture

```
mcp.arleo.eu
│
├─ anonymous          → public read-only tools (no auth)
├─ content.read       → enhanced read tools (OAuth bearer)
├─ content.write      → create/edit/delete tools (OAuth + scope)
├─ site.admin         → build/deploy/publish tools (OAuth + scope)
└─ system.admin       → config/diagnostics/maintenance (OAuth + scope)
```

## Roadmap

- `v0.1` — bootstrap: module structure, runtime, OAuth, public tools (100/100 AgentReady)
- `v0.2` — public read: all anonymous tools from hugo-public-mcp
- `v0.3` — enhanced read: content.read scope tools
- `v0.4` — write tools: content.write + site.admin scope tools
- `v1.0` — stable unified MCP: all tiers, full test coverage, deprecation of predecessor repos
