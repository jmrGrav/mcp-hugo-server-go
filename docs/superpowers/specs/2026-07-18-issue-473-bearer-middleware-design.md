# 2026-07-18 issue-473 bearer middleware design

## Goal
Adopt `github.com/modelcontextprotocol/go-sdk/auth.RequireBearerToken` for `/mcp` bearer-token verification without changing observable behavior already validated with ChatGPT, Claude, Le Chat, `isitagentready`, and `mcptest`.

## Non-negotiable compatibility constraints

1. Unauthenticated `/mcp` still returns `401` with a `WWW-Authenticate` challenge containing `realm` and `resource_metadata`.
2. Invalid bearer tokens still return `401` with `error="invalid_token"` in the challenge.
3. Scope-based MCP ACL remains body-aware and continues to reject forbidden `tools/call` requests with JSON-RPC `403`.
4. Existing audit logging, legacy-scope metrics, caller-IP context injection, and rate limiting remain active.
5. OAuth discovery behavior used by ChatGPT, Claude, and Le Chat does not regress.

## Rejected approaches

### 1. Direct replacement of the `/mcp` auth block with raw `auth.RequireBearerToken`
Rejected because the SDK middleware by itself:
- does not emit our current `realm=...` challenge parameter;
- does not add `error="invalid_token"` on invalid bearer responses;
- does not know our per-tool ACL model;
- does not surface our canonical scope / legacy-alias information in the request context.

That path would reduce local code, but it would knowingly regress live client compatibility.

### 2. Keep the hand-rolled bearer parser and only add comments referencing the SDK
Rejected because it does not satisfy the issue's purpose: we would still be maintaining our own header parsing/challenge flow instead of depending on the SDK primitive.

## Chosen approach

Introduce a thin local adapter around `auth.RequireBearerToken`:

- use the SDK middleware as the transport-layer bearer verifier;
- provide a custom `TokenVerifier` backed by `oauthSvc.ValidateBearerDetails(...)`;
- carry canonical scope and legacy-alias metadata through `auth.TokenInfo.Extra`;
- wrap the SDK middleware with a small response-capture shim that normalizes the final 401 challenge/body back to the already-supported on-wire shape.

This keeps the risky parsing/verification path on the SDK while preserving the compatibility details our deployed clients already rely on.

## Scope of change

In scope:
- `/mcp` bearer-token verification path
- request-context scope injection after successful SDK auth
- regression tests around challenge shape and ACL continuity
- issue comment / PR notes documenting exactly what was preserved and what was deliberately left custom

Out of scope:
- changing OAuth discovery endpoints
- changing scope semantics (`read` / `write`)
- moving JSON-RPC ACL into the SDK
- changing admin/reader registration routes
- changing non-`/mcp` bearer handling such as `/agent/identity/verify`

## Verification plan

Red/green tests first for:
- missing bearer -> same `401` + challenge shape
- invalid bearer -> same `401` + `error="invalid_token"`
- authenticated read token can still `tools/list`
- authenticated read token still gets `403` JSON-RPC on write tool
- `Vary: Authorization` and `Cache-Control: no-store` still present on `/mcp`

Then full verification:
- `go test ./internal/server ./internal/oauth`
- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- `go build ./...`
