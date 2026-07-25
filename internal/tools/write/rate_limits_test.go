package write_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestGetRateLimitsReportsFullBudgetBeforeAnyMutation is the basic
// regression test for #614: a caller must be able to check its quota
// before ever making a mutating call, and see the full configured budget
// on both buckets since nothing has been consumed yet.
func TestGetRateLimitsReportsFullBudgetBeforeAnyMutation(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 10
	rl.DestructivePerMin = 4
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	res := callTool(t, session, "get_rate_limits", map[string]any{})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("get_rate_limits expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)

	cuu, ok := data["create_update_upload"].(map[string]any)
	if !ok {
		t.Fatalf("data.create_update_upload missing or wrong type: %#v", data)
	}
	if got := int(cuu["remaining"].(float64)); got != 10 {
		t.Errorf("create_update_upload.remaining = %d, want 10 (full budget, nothing consumed yet)", got)
	}
	if got := int(cuu["limit"].(float64)); got != 10 {
		t.Errorf("create_update_upload.limit = %d, want 10", got)
	}
	if got := cuu["retry_after_seconds"].(float64); got != 0 {
		t.Errorf("create_update_upload.retry_after_seconds = %v, want 0 (quota available now)", got)
	}

	destructive, ok := data["destructive"].(map[string]any)
	if !ok {
		t.Fatalf("data.destructive missing or wrong type: %#v", data)
	}
	if got := int(destructive["remaining"].(float64)); got != 4 {
		t.Errorf("destructive.remaining = %d, want 4", got)
	}
	if got := int(destructive["limit"].(float64)); got != 4 {
		t.Errorf("destructive.limit = %d, want 4", got)
	}
}

// TestGetRateLimitsReflectsConsumedQuota confirms get_rate_limits reports
// the caller's actual remaining budget after real mutations have consumed
// part of it — not just the static configured limit — and that calling
// get_rate_limits itself never consumes any quota (calling it twice in a
// row reports the same remaining value both times).
func TestGetRateLimitsReflectsConsumedQuota(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 5
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	for i := 0; i < 2; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("quota-check-%d", i), "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d expected success, got error: %s", i, raw)
		}
	}

	res := callTool(t, session, "get_rate_limits", map[string]any{})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("get_rate_limits expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)
	cuu := data["create_update_upload"].(map[string]any)
	if got := int(cuu["remaining"].(float64)); got != 3 {
		t.Fatalf("create_update_upload.remaining = %d, want 3 (5 - 2 consumed by create_page calls)", got)
	}

	// Calling get_rate_limits again must report the identical remaining
	// value — checking quota must never itself consume quota.
	res2 := callTool(t, session, "get_rate_limits", map[string]any{})
	if res2.IsError {
		raw, _ := json.Marshal(res2.Content)
		t.Fatalf("get_rate_limits (2nd) expected success, got error: %s", raw)
	}
	data2 := decodeWriteData(t, res2)
	cuu2 := data2["create_update_upload"].(map[string]any)
	if got := int(cuu2["remaining"].(float64)); got != 3 {
		t.Fatalf("create_update_upload.remaining after a 2nd get_rate_limits call = %d, want still 3 — get_rate_limits must not consume quota", got)
	}
}

// TestGetRateLimitsDestructiveAndCreateUpdateAreIndependent mirrors
// TestDeleteAndCreateRateLimitsAreIndependent: the two buckets
// get_rate_limits reports must reflect the same independence the mutation
// tools themselves already enforce — exhausting one must not affect the
// other's reported remaining value.
func TestGetRateLimitsDestructiveAndCreateUpdateAreIndependent(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.DestructivePerMin = 1
	rl.CreateUpdatePerMin = 60
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "indep-rl", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page expected success, got error: %s", raw)
	}
	if res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "indep-rl",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "indep-rl", "index.md")),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page expected success, got error: %s", raw)
	}

	res = callTool(t, session, "get_rate_limits", map[string]any{})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("get_rate_limits expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)

	cuu := data["create_update_upload"].(map[string]any)
	if got := int(cuu["remaining"].(float64)); got != 59 {
		t.Errorf("create_update_upload.remaining = %d, want 59 (60 - 1 create_page, unaffected by the exhausted destructive budget)", got)
	}
	destructive := data["destructive"].(map[string]any)
	if got := int(destructive["remaining"].(float64)); got != 0 {
		t.Errorf("destructive.remaining = %d, want 0 (DestructivePerMin=1, fully consumed by the one delete_page)", got)
	}
	if got := destructive["retry_after_seconds"].(float64); got <= 0 {
		t.Errorf("destructive.retry_after_seconds = %v, want > 0 since the destructive budget is exhausted", got)
	}
}
