# MCP Client Compatibility Matrix

Tested against `https://mcp.arleo.eu` (v1.2.0). Each client is tested for: discovery, OAuth flow, anonymous tool access, and scoped tool access.

## Summary

| Client | Discovery | OAuth | Anonymous tools | Write/Admin tools | Notes |
|---|---|---|---|---|---|
| Claude.ai (custom connector) | ✅ | ✅ | ✅ 9 tools | ⚠️ Needs re-test with v1.2.0 Cache-Control fix | Admin token showed 9 anon tools in v1.1.0 |
| ChatGPT (custom connector) | ✅ | ✅ | ✅ | ✅ write-scope tools visible | Spinner/reconnect on initial connect (cosmetic) |
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
- **Status:** ⚠️ pending re-validation with v1.2.0

### ChatGPT

- **Connector type:** Custom GPT action / MCP connector
- **Discovery:** OAuth auth server metadata read correctly
- **OAuth:** Completes with read/write scope; writes `update_page`, `validate_front_matter` visible
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
