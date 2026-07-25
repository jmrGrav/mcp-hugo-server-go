package write_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestForceDryRunAllOverridesCreatePage is the core regression test for
// #611: with cfg.ForceDryRunAll set, create_page must behave exactly like
// dry_run: true was passed — reporting data.dry_run=true and never writing
// to disk — even though the actual call omits dry_run entirely.
func TestForceDryRunAllOverridesCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{ForceDryRunAll: true})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/force-dry-run", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)
	if data["dry_run"] != true {
		t.Errorf("data.dry_run = %v, want true (force_dry_run_all must override an omitted dry_run)", data["dry_run"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "force-dry-run", "index.md")); !os.IsNotExist(err) {
		t.Error("create_page must not write to disk when force_dry_run_all is set, even with dry_run omitted")
	}
}

// TestForceDryRunAllOverridesExplicitFalse confirms the server-side
// override wins even when a caller explicitly passes dry_run: false —
// this is a server-wide safety switch, not a default a caller can bypass.
func TestForceDryRunAllOverridesExplicitFalse(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{ForceDryRunAll: true})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/explicit-false", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{}, "dry_run": false,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)
	if data["dry_run"] != true {
		t.Errorf("data.dry_run = %v, want true (force_dry_run_all must override an explicit dry_run:false too)", data["dry_run"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "explicit-false", "index.md")); !os.IsNotExist(err) {
		t.Error("create_page must not write to disk when force_dry_run_all is set, even with dry_run:false explicitly passed")
	}
}

// TestForceDryRunAllDoesNotConsumeRateLimitQuota confirms the override
// preserves dry-run's existing "never consumes quota" property (#588):
// with a budget of 1, an arbitrary number of forced-dry-run create_page
// calls must all succeed, never hitting rate_limit_exceeded.
func TestForceDryRunAllDoesNotConsumeRateLimitQuota(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 1
	session, _, done := newTestServer(t, contentRoot, testServerOpts{ForceDryRunAll: true, RateLimit: &rl})
	defer done()

	for i := 0; i < 5; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": "posts/quota-check", "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page call %d expected success under force_dry_run_all (must not consume quota), got error: %s", i, raw)
		}
	}
}

// TestForceDryRunAllOverridesDeletePage confirms the override also applies
// to delete_page — a page created outside force-dry-run mode must survive
// a delete_page call once force_dry_run_all is on.
func TestForceDryRunAllOverridesDeletePage(t *testing.T) {
	contentRoot := t.TempDir()
	setupSession, _, setupDone := newTestServer(t, contentRoot)
	res := callTool(t, setupSession, "create_page", map[string]any{
		"slug": "posts/survives-delete", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("setup create_page failed: %s", raw)
	}
	setupDone()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{ForceDryRunAll: true})
	defer done()

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/survives-delete",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)
	if data["dry_run"] != true {
		t.Errorf("data.dry_run = %v, want true", data["dry_run"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "survives-delete", "index.md")); err != nil {
		t.Errorf("delete_page must not have removed the page under force_dry_run_all: stat error = %v", err)
	}
}
