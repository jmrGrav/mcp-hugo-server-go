# MCP Client Compatibility Matrix

Tested against `https://mcp.arleo.eu` (v1.3.0). Each client is tested for: discovery, OAuth flow, anonymous tool access, and scoped tool access.

## Summary

| Client | Discovery | OAuth | Anonymous tools | Write/Admin tools | Notes |
|---|---|---|---|---|---|
| Claude.ai (custom connector) | ✅ | ✅ | ✅ 9 tools | ✅ admin tools confirmed | Stateful HTTP transport; v1.3.0 ContentClassifier fixes correct taxonomy noise |
| ChatGPT (custom connector) | ✅ | ✅ | ✅ | ✅ write-scope tools visible | Stateful transport; spinner on first connect is cosmetic |
| MCP Inspector | ✅ | N/A | ✅ | N/A | Works with no auth |
| Cursor | Not tested | Not tested | Not tested | Not tested | Planned |
| VS Code Copilot | Not tested | Not tested | Not tested | Not tested | Planned |
| OpenAI Codex | Not tested | Not tested | Not tested | Not tested | Planned |

## Detail

### Claude.ai

- **Connector type:** Custom MCP connector
- **Discovery:** Reads `/.well-known/mcp/server-card.json` correctly
- **OAuth:** Authorization Code + PKCE flow completes; admin-scope token obtained
- **v1.1.0 issue:** `tools/list` called before auth was cached; admin token still showed 9 anonymous tools
- **v1.2.0 fix:** `Cache-Control: no-store` + `Vary: Authorization` added; re-test required to confirm
- **Transport:** Stateful Streamable HTTP (`POST /mcp`); sessions have 24-hour idle timeout
- **Status:** ✅ functional — v1.3.0 re-validated with stateful HTTP transport. Admin token correctly shows expanded tool set.

### ChatGPT

- **Connector type:** Custom GPT action / MCP connector
- **Discovery:** OAuth auth server metadata read correctly
- **OAuth:** Completes with read/write scope; writes `update_page`, `validate_frontmatter` visible
- **Known quirk:** Spinner + reconnect prompt on first connection (cosmetic, not a failure)
- **Status:** ✅ functional

### MCP Inspector

- **Tool:** `npx @modelcontextprotocol/inspector`
- **Discovery:** Full tool list visible at anonymous scope
- **OAuth:** Not used in typical inspector workflow
- **Status:** ✅ functional

### Cursor, VS Code, Codex

- **Status:** Not yet tested. Track at issue #101.

## Regression Signals

Run `scripts/check-agent-ready.sh` before each release to catch discovery regressions (issue #117).

The agent-readiness scan at `isitagentready.com` targets `https://www.arleo.eu/` and should score ≥95/100 overall and 7/7 on `API/Auth/MCP/Skill Discovery`.

Run `scripts/smoke-mcp-live.sh` after deploys to catch interop regressions that
discovery-only checks cannot see. It verifies `tools/list`, representative
`tools/call` responses, JSON-RPC errors, `result.isError`, rate-limit behavior,
and reverse-proxy HTML failures. The script is safe by default and skips write
tools unless `MCP_SMOKE_ENABLE_WRITES=1` is explicitly set.

## Known Behavior: OAuth Enabled Requires Bearer for All Requests

When `oauth.enabled: true`, **every** `/mcp` request must carry a valid Bearer
token in the `Authorization` header — including requests for anonymous-scope
tools (`get_site_information`, `list_pages`, etc.).

Without a Bearer token the server returns `HTTP 401` with a
`WWW-Authenticate: Bearer` challenge. This is intentional: OAuth discovery
forces the client through the PKCE flow so that consent is captured once,
even for read-only access.

**Implication for tool developers:** If you are testing against a server with
`oauth.enabled: true`, you cannot call anonymous tools without first completing
the authorization code + PKCE flow. Use a server with `oauth.enabled: false`
for unauthenticated integration tests.
