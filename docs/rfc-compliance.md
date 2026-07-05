# RFC Compliance Checklist

This document tracks the server's conformance with the relevant specifications. Each item is marked **✅ compliant**, **⚠️ partial**, or **❌ not implemented**.

## RFC 6749 — OAuth 2.0 Authorization Framework

| Requirement | Status | Notes |
|---|---|---|
| Authorization Code grant | ✅ | `/authorize` → `/token` |
| PKCE extension (RFC 7636) required | ✅ | `RequirePKCE=true` default since v1.2.0 |
| Token endpoint authentication | ✅ | `client_secret_basic`, `client_secret_post`, `none` |
| `invalid_request`, `invalid_client`, `invalid_grant` error codes | ✅ | All returned correctly |
| Short-lived authorization codes | ✅ | Configurable TTL, purged on expiry |
| `redirect_uri` validation | ✅ | Exact match enforced |
| In-memory auth codes reset on restart | ⚠️ | By design (issue #26); access tokens are persisted |

## RFC 6750 — Bearer Token Usage

| Requirement | Status | Notes |
|---|---|---|
| `Authorization: Bearer <token>` header | ✅ | Required on `/mcp` for scoped tools |
| `WWW-Authenticate` on 401 | ✅ | Includes `realm`, `resource_metadata`, `error` |
| `Bearer` scheme only | ✅ | Other schemes rejected with 401 |
| Token must not appear in URL | ✅ | Only header accepted |

## RFC 7636 — PKCE

| Requirement | Status | Notes |
|---|---|---|
| `code_challenge` + `code_challenge_method=S256` | ✅ | Required when `RequirePKCE=true` |
| `code_verifier` validated at token exchange | ✅ | SHA-256 match enforced |
| `code_challenge_methods_supported` in metadata | ✅ | `["S256"]` |
| `RequirePKCE` default | ✅ | `true` since v1.2.0 |

## RFC 8414 — OAuth Authorization Server Metadata

| Requirement | Status | Notes |
|---|---|---|
| `/.well-known/oauth-authorization-server` | ✅ | Served at correct path |
| `issuer` matches request origin | ✅ | Configurable via `oauth.issuer` |
| `authorization_endpoint` | ✅ | |
| `token_endpoint` | ✅ | |
| `registration_endpoint` | ✅ | Always present when OAuth is enabled (v1.2.0 fix) |
| `scopes_supported` | ✅ | All 4 scopes listed |
| `response_types_supported` | ✅ | `["code"]` |
| `grant_types_supported` | ✅ | Includes `authorization_code` and agent assertion grants |
| `code_challenge_methods_supported` | ✅ | `["S256"]` |

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
| `/.well-known/oauth-protected-resource` | ✅ | |
| `resource` field | ✅ | Defaults to `{issuer}/mcp` |
| `authorization_servers` field | ✅ | Points to issuer |
| `bearer_methods_supported` | ✅ | `["header"]` |
| `scopes_supported` | ✅ | |

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

- Refresh tokens (stateless server; tokens expire and agents re-authenticate)
- Token introspection (RFC 7662) — not implemented
- Token revocation (RFC 7009) — not implemented
- Pushed Authorization Requests (RFC 9126) — not implemented
