package admin_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildSiteWith registers build_site with the given srcIdx and callbacks over a
// mock hugo that succeeds, calls the tool, and returns the decoded response.
func buildSiteWith(t *testing.T, srcIdx *hugosite.SourceIndex, callbacks ...admin.PostBuildCallback) map[string]any {
	t.Helper()
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, srcIdx, callbacks...)
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("build_site call: %v", err)
	}
	if res.IsError {
		t.Fatalf("build_site error: %s", resultText(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("decode response: %v — %s", err, resultText(res))
	}
	return out
}

// TestBuildSiteCallbackFreeStages — AC4 "callback-free deployments": stages are
// reported with callbacks_status "skipped", and the pre-existing top-level
// fields remain present (backward compatibility).
func TestBuildSiteCallbackFreeStages(t *testing.T) {
	out := buildSiteWith(t, nil)

	// Backward-compat: pre-existing fields still present.
	for _, f := range []string{"status", "duration_ms", "build_id", "output_revision", "publish_ready"} {
		if _, ok := out[f]; !ok {
			t.Fatalf("backward-compat field %q missing from response", f)
		}
	}
	data, _ := out["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response missing data envelope: %v", out)
	}
	stages, _ := data["stages"].(map[string]any)
	if stages == nil {
		t.Fatalf("data.stages missing: %v", data)
	}
	if stages["hugo_build"] != "ok" {
		t.Errorf("stages.hugo_build = %v, want ok", stages["hugo_build"])
	}
	if stages["callbacks_status"] != "skipped" {
		t.Errorf("stages.callbacks_status = %v, want skipped", stages["callbacks_status"])
	}
	if stages["source_index_reload"] != "skipped" {
		t.Errorf("stages.source_index_reload = %v, want skipped", stages["source_index_reload"])
	}
	pages, _ := data["pages"].(map[string]any)
	if pages == nil {
		t.Fatalf("data.pages missing: %v", data)
	}
	for _, k := range []string{"included", "excluded_drafts", "deleted_outputs"} {
		if _, ok := pages[k]; !ok {
			t.Errorf("data.pages missing key %q", k)
		}
	}
}

// TestBuildSitePageAwareChangedSet — AC1/AC3 end to end: a pending normal page
// is reported as included and a pending draft as excluded_drafts, captured
// before the index_reload callback clears the pending flags.
func TestBuildSitePageAwareChangedSet(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	hugosite.ContentMu.Lock()
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/live", Lang: "fr", BuildPending: true})
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/wip", Lang: "en", Draft: true, BuildPending: true})
	hugosite.ContentMu.Unlock()

	// An index_reload callback that clears pending flags, mirroring production;
	// the report must still reflect the pre-callback changed set.
	reload := admin.PostBuildCallback{Name: "index_reload", Fn: func() error {
		srcIdx.ClearAllBuildPending()
		return nil
	}}
	out := buildSiteWith(t, srcIdx, reload)

	data := out["data"].(map[string]any)
	stages := data["stages"].(map[string]any)
	if stages["source_index_reload"] != "ok" || stages["callbacks_status"] != "ok" {
		t.Fatalf("stages = %v, want source_index_reload/callbacks_status ok", stages)
	}
	pages := data["pages"].(map[string]any)
	included := toStringSet(pages["included"])
	excluded := toStringSet(pages["excluded_drafts"])
	if !included["posts/live:fr"] {
		t.Errorf("included = %v, want posts/live:fr", pages["included"])
	}
	if !excluded["posts/wip:en"] {
		t.Errorf("excluded_drafts = %v, want posts/wip:en", pages["excluded_drafts"])
	}
	if included["posts/wip:en"] {
		t.Errorf("draft page leaked into included: %v", pages["included"])
	}
}

func toStringSet(v any) map[string]bool {
	out := map[string]bool{}
	if arr, ok := v.([]any); ok {
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
