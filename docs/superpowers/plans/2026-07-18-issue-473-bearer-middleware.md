# Issue 473 Bearer Middleware Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route `/mcp` bearer-token verification through `go-sdk/auth.RequireBearerToken` while preserving the deployed OAuth challenge, ACL, logging, and rate-limit behavior.

**Architecture:** Add a local adapter middleware in `internal/server` that delegates token verification to the SDK, captures SDK-auth failures, and rewrites only the compatibility-sensitive 401 response details. Keep tool ACL and scope injection in project code.

**Tech Stack:** Go, net/http middleware, github.com/modelcontextprotocol/go-sdk/auth, existing internal/oauth + internal/server test suite.

---

### Task 1: Add regression tests for preserved `/mcp` auth behavior

**Files:**
- Modify: `internal/server/server_test.go`

- [ ] Add/adjust tests for missing bearer, invalid bearer, authenticated tools/list, and scope-denied tool calls to assert the preserved challenge/body/ACL behavior.
- [ ] Run: `go test ./internal/server -run 'TestUnauthenticatedMCPReturns401WithWWWAuthenticate|TestInSessionInvalidBearerEmitsStructuredLog|TestScopeDeniedToolCallEmitsStructuredAuditLog|TestToolsListAuthenticatedReturnsTwentyOneTools'`
- [ ] Confirm at least one new/updated assertion fails before implementation.

### Task 2: Introduce the SDK-backed bearer adapter

**Files:**
- Modify: `internal/server/server.go`

- [ ] Add a small `/mcp` auth adapter using `auth.RequireBearerToken` plus a response-capture shim.
- [ ] Preserve scope injection, legacy metrics, caller IP context, ACL, and cache headers after successful auth.
- [ ] Keep `/agent/identity/verify` untouched.

### Task 3: Verify server tests go green

**Files:**
- Modify: `internal/server/server_test.go` if needed for exact on-wire expectations

- [ ] Run: `go test ./internal/server ./internal/oauth`
- [ ] Fix only implementation/test mismatches grounded in the compatibility design above.

### Task 4: Document the approach and decisions

**Files:**
- Modify: `docs/mcp-contract.md`
- Modify: `docs/operator-guide.md`
- Modify: `docs/client-compatibility.md`

- [ ] Add a short note that `/mcp` bearer verification now uses the SDK primitive through a local compatibility adapter because the project still owns ACL and client-facing challenge shape.
- [ ] Document any consciously retained custom behavior.

### Task 5: Full verification and publish for Claude review

**Files:**
- Modify: issue/PR comments only after validation

- [ ] Run: `go test ./...`
- [ ] Run: `go vet ./...`
- [ ] Run: `staticcheck ./...`
- [ ] Run: `go build ./...`
- [ ] Commit with a focused message.
- [ ] Push branch and open a draft PR.
- [ ] Comment issue `#473` with approach, rejected ideas, and compatibility reasoning.
