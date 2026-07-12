# RFC Compliance Checklist

This document tracks the server's conformance with the relevant specifications. Each item is marked **✅ compliant**, **⚠️ partial**, or **❌ not implemented**.

Items marked **(live)** were verified against the production server with `scripts/verify-agent-ready.sh` or direct curl checks. Others were verified by code review only.

## RFC 6749 — OAuth 2.0 Authorization Framework

| Requirement | Status | Notes |
|---|---|---|
| Authorization Code grant | ✅ | `/authorize` → `/token` |
| PKCE extension (RFC 7636) required | ✅ | `RequirePKCE=true` default since v1.2.0 |
| Token endpoint authentication | ✅ | `client_secret_basic`, `client_secret_post`, `none` |
| Refresh Token grant | ✅ | `/token` accepts `grant_type=refresh_token` and returns a fresh bearer pair |
| `invalid_request`, `invalid_client`, `invalid_grant` error codes | ✅ | All returned correctly |
| Short-lived authorization codes | ✅ | Configurable TTL, purged on expiry |
| `redirect_uri` validation | ✅ | Exact match enforced |
| In-memory auth codes reset on restart | ⚠️ | By design (issue #26); access tokens are persisted |

## RFC 6750 — Bearer Token Usage

| Requirement | Status | Notes |
|---|---|---|
| `Authorization: Bearer <token>` header | ✅ | Required on `/mcp` for scoped tools |
| `WWW-Authenticate` on 401 | ✅ **(live)** | `Bearer realm=…, resource_metadata=…, error=invalid_token` |
| `Bearer` scheme only | ✅ | Other schemes rejected with 401 |
| Token must not appear in URL | ✅ | Only header accepted |

## RFC 7636 — PKCE

| Requirement | Status | Notes |
|---|---|---|
| `code_challenge` + `code_challenge_method=S256` | ✅ **(live)** | `/authorize` returns 400 when challenge absent |
| `code_verifier` validated at token exchange | ✅ | SHA-256 match enforced; unit-tested |
| `code_challenge_methods_supported` in metadata | ✅ **(live)** | `["S256"]` |
| `RequirePKCE` default | ✅ | `true` since v1.2.0 |

## RFC 8414 — OAuth Authorization Server Metadata

| Requirement | Status | Notes |
|---|---|---|
| `/.well-known/oauth-authorization-server` | ✅ **(live)** | Served at correct path |
| `issuer` matches request origin | ✅ **(live)** | Configurable via `oauth.issuer` |
| `authorization_endpoint` | ✅ **(live)** | |
| `token_endpoint` | ✅ **(live)** | |
| `registration_endpoint` | ✅ **(live)** | Always present when OAuth is enabled (v1.2.0 fix, issue #117) |
| `scopes_supported` | ✅ **(live)** | `["content.read","content.write","site.admin"]`; legacy `system.admin` normalizes to `site.admin` |
| `response_types_supported` | ✅ **(live)** | `["code"]` |
| `grant_types_supported` | ✅ | Includes `authorization_code`, `refresh_token`, and agent assertion grants on the current branch; re-verify live after deployment |
| `code_challenge_methods_supported` | ✅ **(live)** | `["S256"]` |

## RFC 7591 — Dynamic Client Registration

| Requirement | Status | Notes |
|---|---|---|
| `POST /register` endpoint | ✅ | Live when OAuth enabled |
| `client_name` required | ✅ | Validated at registration |
| `redirect_uris` required | ✅ | |
| `client_id` + `client_secret` returned | ✅ | |
| Public clients (`none` auth method) | ✅ | When `dynamic_client_registration: true` |
| Confidential clients (secret auth) | ✅ | Loaded from `client_registry_path` |

## RFC 9728 — OAuth Protected Resource Metadata

| Requirement | Status | Notes |
|---|---|---|
| `/.well-known/oauth-protected-resource` | ✅ **(live)** | |
| `resource` field | ✅ **(live)** | `{issuer}/mcp` |
| `authorization_servers` field | ✅ **(live)** | Points to issuer |
| `bearer_methods_supported` | ✅ **(live)** | `["header"]` |
| `scopes_supported` | ✅ **(live)** | Canonical scopes listed: `content.read`, `content.write`, `site.admin` |

## MCP Streamable HTTP Transport

| Requirement | Status | Notes |
|---|---|---|
| `POST /mcp` for JSON-RPC requests | ✅ | |
| `GET /mcp` for SSE streams | ✅ | |
| `DELETE /mcp` for session teardown | ✅ | |
| `405` for unsupported methods | ✅ | `Allow: GET, POST, DELETE` |
| `Cache-Control: no-store` on `/mcp` | ✅ | Added v1.2.0 — prevents tool list caching |
| `Vary: Authorization` on `/mcp` | ✅ | Added v1.2.0 |
| Stateless mode | ✅ | No server-side session state |

## Discovery Endpoints (agent-readiness)

| Endpoint | Status | Notes |
|---|---|---|
| `/.well-known/mcp/server-card.json` | ✅ **(live)** | `protocolVersion: 2025-06-18`, `transport.type: streamable-http` |
| `/.well-known/agent.json` (A2A) | ✅ **(live)** | `$schema: a2a.google.com/…`, `name`, `url`, `capabilities` |
| `/auth.md` | ✅ **(live)** | Correct scopes, `/register` URL, no stale `mcp` scope |
| `llms.txt` | ✅ | MCP endpoint and description |

## Agent-to-Agent (A2A / WorkOS agent identity)

| Requirement | Status | Notes |
|---|---|---|
| `POST /agent/identity` | ✅ | Registers anonymous agent |
| `POST /agent/identity/claim` | ✅ | Initiates approval flow |
| `GET /agent/identity/verify` | ✅ | HTML approval form for operators |
| `POST /agent/identity/verify` | ✅ | Operator approves with admin token |
| JWT-bearer grant at `/token` | ✅ | `urn:ietf:params:oauth:grant-type:jwt-bearer` |
| `Retry-After: 0` on assertion-not-found | ✅ | Added v1.2.0 |
| In-memory assertion state | ⚠️ | Lost on restart by design; Retry-After signals re-registration |
| `/.well-known/agent.json` | ✅ | A2A agent card |

## Deferred / Out of Scope

- Token introspection (RFC 7662) — not implemented
- Token revocation (RFC 7009) — not implemented
- Pushed Authorization Requests (RFC 9126) — not implemented
