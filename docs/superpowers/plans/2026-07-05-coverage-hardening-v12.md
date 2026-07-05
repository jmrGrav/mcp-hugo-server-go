# Coverage Hardening Plan for v1.2.0

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Raise real test coverage toward 90% by covering high-risk behavior in storage, OAuth, server discovery, Hugo helpers, and MCP tool handlers without changing product semantics.

**Architecture:** Keep the production code stable unless a narrow dependency injection seam is required for a real test. Prefer table-driven tests against public APIs, handler-level HTTP tests for OAuth/MCP wiring, and fixture-backed tests for Hugo/index/storage paths. Use the existing packages and test helpers instead of inventing new abstractions.

**Tech Stack:** Go test, httptest, t.TempDir, SQLite, Hugo fixtures, MCP handlers, OAuth metadata, table-driven tests, race-enabled tests.

---

### Task 1: Measure baseline and identify coverage gaps

**Files:**
- Test: `coverage.out` (generated)

- [ ] **Step 1: Capture the baseline coverage**

Run: `go test ./... -coverprofile=coverage.out`

- [ ] **Step 2: Inspect uncovered branches**

Run: `go tool cover -func=coverage.out`
Run: `go tool cover -html=coverage.out`

- [ ] **Step 3: Record the highest-ROI targets**

Focus on `internal/storage`, `internal/server`, `internal/oauth`, `internal/tools/read`, `internal/tools/write`, `internal/tools/admin`, `internal/config`, `internal/security`, `internal/hugosite`, and `internal/fileutil`.

### Task 2: Expand storage and config coverage

**Files:**
- Modify: `internal/storage/json_test.go` or `internal/storage/storage_test.go`
- Modify: `internal/storage/sqlite_test.go` or `internal/oauth/client_registry_test.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add tests for JSON and SQLite success/error paths**

Cover file creation, load/save failure, token purge, SQLite open failure, and `ValidateAccessToken` edge cases.

- [ ] **Step 2: Add config validation tests**

Cover invalid external URLs, hook URLs, private/link-local rejection, missing required values, and successful defaults.

- [ ] **Step 3: Run the targeted package tests**

Run: `go test ./internal/storage ./internal/config`

### Task 3: Expand OAuth and server coverage

**Files:**
- Modify: `internal/oauth/oauth_test.go`
- Modify: `internal/oauth/redirect_registry_test.go`
- Modify: `internal/oauth/agent_auth_test.go`
- Modify: `internal/oauth/acl_test.go`
- Modify: `internal/oauth/helpers_test.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/discovery_test.go`
- Modify: `internal/server/server_internal_test.go`

- [ ] **Step 1: Add OAuth handler tests**

Cover authorization code issuance, redirect validation, PKCE, state handling, token exchange failures, client auth modes, bearer validation, and metadata responses.

- [ ] **Step 2: Add server discovery and tools-list tests**

Cover MCP server card, protected-resource metadata, authorization-server metadata, landing endpoints, and scope-based tools exposure.

- [ ] **Step 3: Run the targeted package tests**

Run: `go test ./internal/oauth ./internal/server`

### Task 4: Expand tool handler coverage

**Files:**
- Modify: `internal/tools/read/*.go` tests
- Modify: `internal/tools/write/*.go` tests
- Modify: `internal/tools/admin/*.go` tests
- Modify: `internal/tools/anonymous/*.go` tests

- [ ] **Step 1: Add table-driven handler tests**

Cover valid requests, invalid parameters, missing resources, permission denials, serialization, and internal errors for read/write/admin tools.

- [ ] **Step 2: Add branch coverage for filters and helper functions**

Cover pagination, sort/order normalization, diff paths, broken-link detection, front matter parsing, SRI scanning, hook dispatch, and featured-image generation helpers.

- [ ] **Step 3: Run the targeted package tests**

Run: `go test ./internal/tools/...`

### Task 5: Expand Hugo, security, and file utility coverage

**Files:**
- Modify: `internal/hugosite/source_index_test.go`
- Modify: `internal/site/index_test.go`
- Modify: `internal/site/index_more_test.go`
- Modify: `internal/site/markdown_test.go`
- Modify: `internal/security/pathguard_test.go`
- Modify: `internal/fileutil/atomic_test.go`

- [ ] **Step 1: Cover filesystem and parsing edge cases**

Cover missing pages, invalid front matter, slug conflicts, delete paths, symlink rejection, atomic write failures, markdown extraction, and site listing helpers.

- [ ] **Step 2: Add regression tests for rare branches**

Cover empty slices/maps, null-like values, duplicate handling, and context cancellation where the code already supports it.

- [ ] **Step 3: Run the targeted package tests**

Run: `go test ./internal/hugosite ./internal/site ./internal/security ./internal/fileutil`

### Task 6: Verify the full suite and summarize

**Files:**
- Generated: `coverage.out`

- [ ] **Step 1: Run the full verification set**

Run: `gofmt -w` on any changed test files, then `go test ./...`, `go test -race ./...`, `go vet ./...`, `staticcheck ./...`, `govulncheck ./...`

- [ ] **Step 2: Recompute coverage**

Run: `go test ./... -coverprofile=coverage.out` and `go tool cover -func=coverage.out`

- [ ] **Step 3: Summarize what improved**

Report global coverage, package coverage, the files with the largest gains, and any functions left below target with a reason they are hard to test.

---
