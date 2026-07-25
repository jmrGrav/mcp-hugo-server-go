package read_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// TestIndexStalenessWiring is a regression test for #583: get_backlinks,
// get_related_content, and get_broken_links must surface data.index_staleness
// when the in-memory index is behind on-disk content, and omit it entirely
// when the index is current.
func TestIndexStalenessWiring(t *testing.T) {
	siteRoot := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(siteRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/target/index.html", `<!doctype html><html><head>
<title>Target</title>
<link rel="canonical" href="https://example.test/posts/target/">
</head><body><article>Target body.</article></body></html>`)
	write("posts/linker/index.html", `<!doctype html><html><head>
<title>Linker</title>
<link rel="canonical" href="https://example.test/posts/linker/">
</head><body><article>See <a href="/posts/target/">target</a>.</article></body></html>`)

	restore := site.SetStaleCheckIntervalForTesting(1 * time.Millisecond)
	defer restore()

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, cfg, mustTestSourceIndex(t))
	defer done()

	assertNoStaleness := func(t *testing.T, data map[string]any, tool string) {
		t.Helper()
		if v, present := data["index_staleness"]; present {
			t.Errorf("%s: index_staleness present on a fresh index: %#v", tool, v)
		}
	}
	assertStaleness := func(t *testing.T, data map[string]any, tool string) {
		t.Helper()
		v, present := data["index_staleness"]
		if !present {
			t.Fatalf("%s: expected index_staleness to be present on a stale index", tool)
		}
		stalenessObj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%s: index_staleness = %#v, want object", tool, v)
		}
		if _, ok := stalenessObj["newest_edit"]; !ok {
			t.Errorf("%s: index_staleness missing newest_edit: %#v", tool, stalenessObj)
		}
		// #617: this scenario is an out-of-band disk edit bypassing
		// build_site/create_page/update_page entirely — no source page has
		// a BuildPending write recorded, so likely_source must read
		// external_or_unknown, not mcp_pending_build.
		if got := stalenessObj["likely_source"]; got != "external_or_unknown" {
			t.Errorf("%s: index_staleness.likely_source = %v, want external_or_unknown", tool, got)
		}
	}

	// Fresh index: no tool should surface index_staleness.
	backlinks := callTool(t, session, "get_backlinks", map[string]any{"slug": "/posts/target/"})
	assertNoStaleness(t, decodeContent(t, backlinks), "get_backlinks")

	related := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/target/"})
	assertNoStaleness(t, decodeContent(t, related), "get_related_content")

	broken := callTool(t, session, "get_broken_links", map[string]any{})
	assertNoStaleness(t, decodeContent(t, broken), "get_broken_links")

	// Simulate an out-of-band edit bypassing build_site/Reload entirely.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filepath.Join(siteRoot, "posts", "target", "index.html"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	time.Sleep(5 * time.Millisecond) // let the shrunk staleCheckInterval elapse

	backlinks = callTool(t, session, "get_backlinks", map[string]any{"slug": "/posts/target/"})
	assertStaleness(t, decodeContent(t, backlinks), "get_backlinks")

	related = callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/target/"})
	assertStaleness(t, decodeContent(t, related), "get_related_content")

	broken = callTool(t, session, "get_broken_links", map[string]any{})
	assertStaleness(t, decodeContent(t, broken), "get_broken_links")
}

// TestIndexStalenessLikelySourceReportsMCPPendingBuild is the counterpart
// regression test for #617: when the source index has a page marked
// BuildPending (i.e. this server itself made a write awaiting the next
// build_site/publish_changes), index_staleness.likely_source must read
// "mcp_pending_build", not "external_or_unknown" — even though, from the
// site index's own on-disk-staleness check alone, this scenario looks
// identical to an out-of-band edit.
func TestIndexStalenessLikelySourceReportsMCPPendingBuild(t *testing.T) {
	siteRoot := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(siteRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/target/index.html", `<!doctype html><html><head>
<title>Target</title>
<link rel="canonical" href="https://example.test/posts/target/">
</head><body><article>Target body.</article></body></html>`)

	restore := site.SetStaleCheckIntervalForTesting(1 * time.Millisecond)
	defer restore()

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	srcIdx := mustTestSourceIndex(t)
	srcIdx.Upsert(hugosite.SourcePage{
		Slug:           "pending-mcp-write",
		Title:          "Pending MCP Write",
		Body:           "hello",
		FrontmatterRaw: map[string]any{"title": "Pending MCP Write"},
		BuildPending:   true,
	})

	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filepath.Join(siteRoot, "posts", "target", "index.html"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	res := callTool(t, session, "get_broken_links", map[string]any{})
	data := decodeContent(t, res)
	v, present := data["index_staleness"]
	if !present {
		t.Fatal("expected index_staleness to be present on a stale index")
	}
	stalenessObj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("index_staleness = %#v, want object", v)
	}
	if got := stalenessObj["likely_source"]; got != "mcp_pending_build" {
		t.Errorf("index_staleness.likely_source = %v, want mcp_pending_build", got)
	}
}
