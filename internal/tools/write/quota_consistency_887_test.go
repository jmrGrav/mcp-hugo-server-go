package write_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// quota_consistency_887_test.go proves the unified quota-consumption rule
// (#887, documented on quotaConsumptionRule in rate_limits.go) holds
// *consistently* across all five write/destructive tools. Before #887 the
// same failure class cost quota on one tool but was free on another — most
// notably a not_found rejection consumed the destructive quota on
// delete_page_asset but not on delete_page (the live v1.7.7 audit's top
// finding), and not_a_bundle consumed on upload_page_asset but not on
// delete_page_asset.
//
// Each case measures the *delta* in the caller's reported quota across a
// single tool call, read via get_rate_limits (which itself never consumes),
// so the assertion is on real token movement, not just the error code. A
// small per-minute limit keeps refill negligible within the sub-millisecond
// window between the two snapshots, so a floored reading is stable.
//
// Fail-red verification (performed by the implementing engineer against the
// pre-#887 tree): the not_found rows for update_page and delete_page_asset,
// and the not_a_bundle row for upload_page_asset, each asserted delta 0 but
// observed delta 1 (quota consumed) before the fix; they pass at delta 0
// after it. The consume rows (revision_conflict, already_exists, success)
// stayed green throughout, which is the evidence the chosen rule is (b) — a
// rule that freed those state-conflict rejections would have flipped them.

const (
	scopeCreate      = "create_update_upload"
	scopeDestructive = "destructive"
)

// quotaRemaining reads the caller's current remaining budget on the named
// bucket via get_rate_limits, which is documented to never consume quota.
func quotaRemaining(t *testing.T, session *mcp.ClientSession, scope string) int {
	t.Helper()
	res := callTool(t, session, "get_rate_limits", map[string]any{})
	if res.IsError {
		t.Fatalf("get_rate_limits failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	bucket, ok := data[scope].(map[string]any)
	if !ok {
		t.Fatalf("get_rate_limits data[%q] type = %T, want object", scope, data[scope])
	}
	rem, ok := bucket["remaining"].(float64)
	if !ok {
		t.Fatalf("get_rate_limits data[%q].remaining type = %T, want number", scope, bucket["remaining"])
	}
	return int(rem)
}

// assertQuotaDelta runs op and asserts it moved the named bucket by wantDelta
// tokens (0 = free, 1 = consumed).
func assertQuotaDelta(t *testing.T, session *mcp.ClientSession, scope string, wantDelta int, wantErr bool, op func() *mcp.CallToolResult) {
	t.Helper()
	before := quotaRemaining(t, session, scope)
	res := op()
	if res.IsError != wantErr {
		t.Fatalf("call IsError = %v, want %v; body = %s", res.IsError, wantErr, marshalContent(t, res))
	}
	after := quotaRemaining(t, session, scope)
	if got := before - after; got != wantDelta {
		t.Fatalf("quota delta on %q bucket = %d (before=%d after=%d), want %d", scope, got, before, after, wantDelta)
	}
}

func smallRateLimit() config.RateLimitConfig {
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 20
	rl.DestructivePerMin = 20
	return rl
}

func TestQuotaConsistency887_CreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// early-validation (blocked shortcode) — FREE
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "create_page", map[string]any{
			"slug": "posts/blocked", "title": "T",
			"body": "{{< script >}}", "tags": []any{}, "categories": []any{},
		})
	})
	// successful mutation — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "create_page", map[string]any{
			"slug": "posts/fresh", "title": "T",
			"body": "Body", "tags": []any{}, "categories": []any{},
		})
	})
	// already_exists (write-time collision on an eligible target) — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, true, func() *mcp.CallToolResult {
		return callTool(t, session, "create_page", map[string]any{
			"slug": "posts/fresh", "title": "T",
			"body": "Body", "tags": []any{}, "categories": []any{},
		})
	})
}

func TestQuotaConsistency887_UpdatePage(t *testing.T) {
	contentRoot := t.TempDir()
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// not_found — FREE (the #887 fix: was CONSUMES pre-fix)
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "update_page", map[string]any{
			"slug": "posts/ghost", "title": "X", "expected_revision": "sha256:whatever",
		})
	})
	// early-validation (blocked shortcode) — FREE
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "update_page", map[string]any{
			"slug": "posts/ghost", "body": "{{< script >}}", "expected_revision": "sha256:x",
		})
	})

	// Setup a real page (consumes; done before the measured op below).
	create := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/live", "title": "T", "body": "Body", "tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("setup create_page failed: %s", marshalContent(t, create))
	}
	rev, _ := decodeWriteData(t, create)["new_revision"].(string)

	// missing expected_revision — FREE (invalid_params input-shape; matches delete_page)
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "update_page", map[string]any{
			"slug": "posts/live", "title": "New",
		})
	})
	// stale revision_conflict on an existing page — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, true, func() *mcp.CallToolResult {
		return callTool(t, session, "update_page", map[string]any{
			"slug": "posts/live", "title": "New", "expected_revision": "sha256:stale",
		})
	})
	// successful update — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "update_page", map[string]any{
			"slug": "posts/live", "title": "New", "expected_revision": rev,
		})
	})
}

func TestQuotaConsistency887_DeletePage(t *testing.T) {
	contentRoot := t.TempDir()
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// not_found — FREE (delete_page is the reference; must stay free)
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page", map[string]any{"slug": "posts/ghost"})
	})
	// early-validation (invalid lang) — FREE
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page", map[string]any{"slug": "posts/ghost", "lang": "../x"})
	})

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/doomed", "title": "T", "body": "Body", "tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("setup create_page failed: %s", marshalContent(t, create))
	}
	rev, _ := decodeWriteData(t, create)["new_revision"].(string)

	// missing expected_revision — FREE
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page", map[string]any{"slug": "posts/doomed"})
	})
	// stale revision_conflict — CONSUMES
	assertQuotaDelta(t, session, scopeDestructive, 1, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page", map[string]any{"slug": "posts/doomed", "expected_revision": "sha256:stale"})
	})
	// successful delete — CONSUMES
	assertQuotaDelta(t, session, scopeDestructive, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page", map[string]any{"slug": "posts/doomed", "expected_revision": rev})
	})
}

func TestQuotaConsistency887_UploadPageAsset(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// not_a_bundle (target bundle absent) — FREE (the #887 fix: was CONSUMES pre-fix)
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "upload_page_asset", map[string]any{
			"slug": "posts/nonexistent", "filename": "a.png", "content_base64": b64(minimalPNG),
		})
	})
	// early-validation (bad filename) — FREE
	assertQuotaDelta(t, session, scopeCreate, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "upload_page_asset", map[string]any{
			"slug": "posts/article", "filename": "../evil.png", "content_base64": b64(minimalPNG),
		})
	})
	// successful upload — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "upload_page_asset", map[string]any{
			"slug": "posts/article", "filename": "cover.png", "content_base64": b64(minimalPNG),
		})
	})
	// already_exists (write-time collision) — CONSUMES
	assertQuotaDelta(t, session, scopeCreate, 1, true, func() *mcp.CallToolResult {
		return callTool(t, session, "upload_page_asset", map[string]any{
			"slug": "posts/article", "filename": "cover.png", "content_base64": b64(minimalPNG),
		})
	})
}

func TestQuotaConsistency887_DeletePageAsset(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// not_found (bundle exists, asset absent) — FREE (the #887 fix: was CONSUMES pre-fix)
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "missing.png", "expected_sha256": "sha256:whatever",
		})
	})
	// early-validation (bad filename) — FREE
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "../evil.png", "expected_sha256": "sha256:x",
		})
	})

	// Setup: upload an asset (consumes create quota, not destructive; done before measurement).
	up := callTool(t, session, "upload_page_asset", map[string]any{
		"slug": "posts/article", "filename": "doomed.png", "content_base64": b64(minimalPNG),
	})
	if up.IsError {
		t.Fatalf("setup upload_page_asset failed: %s", marshalContent(t, up))
	}
	sha, _ := decodeWriteData(t, up)["sha256"].(string)

	// stale revision_conflict on an existing asset — CONSUMES
	assertQuotaDelta(t, session, scopeDestructive, 1, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "doomed.png", "expected_sha256": "sha256:stale",
		})
	})
	// successful delete — CONSUMES
	assertQuotaDelta(t, session, scopeDestructive, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "doomed.png", "expected_sha256": sha,
		})
	})
}

func TestQuotaConsistency963_ReferencedAssetIsFreeUntilForced(t *testing.T) {
	contentRoot := t.TempDir()
	dir := filepath.Join(contentRoot, "posts", "article")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("---\ntitle: Article\n---\n![cover](cover.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rl := smallRateLimit()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	up := callTool(t, session, "upload_page_asset", map[string]any{
		"slug": "posts/article", "filename": "cover.png", "content_base64": b64(minimalPNG),
	})
	if up.IsError {
		t.Fatalf("setup upload_page_asset failed: %s", marshalContent(t, up))
	}
	sha := decodeWriteData(t, up)["sha256"].(string)

	// The reference guard blocks without consuming destructive quota (#963).
	assertQuotaDelta(t, session, scopeDestructive, 0, true, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "cover.png", "expected_sha256": sha,
		})
	})
	// force=true turns the same request into a genuine deletion attempt.
	assertQuotaDelta(t, session, scopeDestructive, 1, false, func() *mcp.CallToolResult {
		return callTool(t, session, "delete_page_asset", map[string]any{
			"slug": "posts/article", "filename": "cover.png", "expected_sha256": sha, "force": true,
		})
	})
}
