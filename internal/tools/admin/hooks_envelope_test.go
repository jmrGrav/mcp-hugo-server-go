package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestRunPostBuildHooksDryRunListsConfiguredHooksWithoutContactingThem(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv.URL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{"dry_run": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks dry_run returned error: %s", resultText(res))
	}
	if called {
		t.Fatal("run_post_build_hooks dry_run must not contact configured endpoints")
	}

	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	if data["dry_run"] != true {
		t.Fatalf("data.dry_run = %v, want true", data["dry_run"])
	}
	if data["configured_count"] != float64(1) {
		t.Fatalf("data.configured_count = %v, want 1", data["configured_count"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("data.results = %#v, want one configured hook preview", data["results"])
	}
	entry := results[0].(map[string]any)
	if entry["url"] != srv.URL {
		t.Fatalf("preview url = %v, want %q", entry["url"], srv.URL)
	}
	if _, present := entry["status"]; present {
		t.Fatalf("dry-run preview entry must not include status, got %#v", entry)
	}
	if got := data["status"]; got != "dry_run" {
		t.Fatalf("data.status = %v, want dry_run", got)
	}
}

func TestRunPostBuildHooksDryRunReportsNoHooksConfigured(t *testing.T) {
	cfg := config.Default()
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{"dry_run": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks dry_run returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	if data["dry_run"] != true {
		t.Fatalf("data.dry_run = %v, want true", data["dry_run"])
	}
	if data["configured_count"] != float64(0) {
		t.Fatalf("data.configured_count = %v, want 0", data["configured_count"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 0 {
		t.Fatalf("data.results = %#v, want empty array when no hooks are configured", data["results"])
	}
	if got := data["status"]; got != "no_hooks_configured" {
		t.Fatalf("data.status = %v, want no_hooks_configured", got)
	}
}

func TestRunPostBuildHooksRealExecutionReportsNoHooksConfigured(t *testing.T) {
	cfg := config.Default()
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	if data["configured_count"] != float64(0) {
		t.Fatalf("data.configured_count = %v, want 0", data["configured_count"])
	}
	if got := data["status"]; got != "no_hooks_configured" {
		t.Fatalf("data.status = %v, want no_hooks_configured", got)
	}
}
