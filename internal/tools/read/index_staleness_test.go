package read_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
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
