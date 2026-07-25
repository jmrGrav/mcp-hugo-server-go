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

// TestGetPageFrontmatterLifecycleStateTracksOutOfBandSourceEdits closes the
// remaining client-visible gap from #686: index_staleness was already covered,
// but page-oriented lifecycle state still needed proof under an out-of-band
// source edit. A source file changed outside MCP must flip the page read to
// pending/stale/stale against the built public HTML, then return to
// built/available/fresh after a simulated rebuild + Reload.
func TestGetPageFrontmatterLifecycleStateTracksOutOfBandSourceEdits(t *testing.T) {
	siteRoot := t.TempDir()
	contentRoot := t.TempDir()

	write := func(path, raw string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	publicPath := filepath.Join(siteRoot, "posts", "hello", "index.html")
	write(publicPath, `<!doctype html><html><head>
<title>Hello</title>
<link rel="canonical" href="https://example.test/posts/hello/">
</head><body><article>Hello public body.</article></body></html>`)

	sourcePath := filepath.Join(contentRoot, "posts", "hello.md")
	write(sourcePath, "---\ntitle: Hello\ndate: 2026-07-25T10:00:00Z\ncategories:\n  - Tutorials\n---\nHello source body.\n")

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
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
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	fresh := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello/"})
	if fresh.IsError {
		t.Fatalf("fresh get_page_frontmatter returned error: %v", fresh.Content)
	}
	freshFM, ok := decodeContent(t, fresh)["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("fresh frontmatter type = %T", decodeContent(t, fresh)["frontmatter"])
	}
	assertReadPageState(t, freshFM["state"], "present", "built", "available", "fresh")

	sourceFuture := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(sourcePath, sourceFuture, sourceFuture); err != nil {
		t.Fatalf("chtimes source: %v", err)
	}

	stale := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello/"})
	if stale.IsError {
		t.Fatalf("stale get_page_frontmatter returned error: %v", stale.Content)
	}
	staleFM, ok := decodeContent(t, stale)["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("stale frontmatter type = %T", decodeContent(t, stale)["frontmatter"])
	}
	assertReadPageState(t, staleFM["state"], "present", "pending", "stale", "stale")

	publicFuture := sourceFuture.Add(1 * time.Minute)
	if err := os.Chtimes(publicPath, publicFuture, publicFuture); err != nil {
		t.Fatalf("chtimes public: %v", err)
	}
	if err := idx.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	rebuilt := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello/"})
	if rebuilt.IsError {
		t.Fatalf("rebuilt get_page_frontmatter returned error: %v", rebuilt.Content)
	}
	rebuiltFM, ok := decodeContent(t, rebuilt)["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("rebuilt frontmatter type = %T", decodeContent(t, rebuilt)["frontmatter"])
	}
	assertReadPageState(t, rebuiltFM["state"], "present", "built", "available", "fresh")
}
