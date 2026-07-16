package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newVerifyPublicationServer(t *testing.T, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterVerifyPublication(s, idx, srcIdx, cfg)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

func writePublicHTML(t *testing.T, siteRoot, rel, body string) {
	t.Helper()
	full := filepath.Join(siteRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestVerifyPublicationFreshPageReportsFreshAndHTTPOK(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	siteRoot := t.TempDir()
	writePublicHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html><head><title>Hello</title><link rel="canonical" href="`+upstream.URL+`/posts/hello/"></head>
<body>Hello.</body></html>`)
	if err := os.WriteFile(filepath.Join(siteRoot, "sitemap.xml"), []byte("<xml/>"), 0o644); err != nil {
		t.Fatalf("write sitemap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "index.xml"), []byte("<rss/>"), 0o644); err != nil {
		t.Fatalf("write feed: %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = upstream.URL
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, nil)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "/posts/hello/"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("verify_publication returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)

	if got := data["status"]; got != "fresh" {
		t.Fatalf("status = %v, want fresh (data=%v)", got, data)
	}
	if got := data["http_checked"]; got != true {
		t.Fatalf("http_checked = %v, want true", got)
	}
	if got := data["http_status"]; got != float64(http.StatusOK) {
		t.Fatalf("http_status = %v, want 200", got)
	}
	if got := data["sitemap_present"]; got != true {
		t.Fatalf("sitemap_present = %v, want true", got)
	}
	if got := data["feed_present"]; got != true {
		t.Fatalf("feed_present = %v, want true", got)
	}
}

// TestVerifyPublicationProbesConfiguredSiteURLNotCanonicalTag proves the
// HTTP probe targets cfg.SiteURL + the page's own slug, never the value
// lifted from the page's own <link rel="canonical"> tag. A content.write
// actor controls that tag; if the probe trusted it, a lower-privileged
// actor could steer this site.admin tool's outbound request at an arbitrary
// host (SSRF), and the check would also verify the wrong site entirely.
func TestVerifyPublicationProbesConfiguredSiteURLNotCanonicalTag(t *testing.T) {
	var probedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	siteRoot := t.TempDir()
	// The canonical tag deliberately points somewhere other than cfg.SiteURL
	// (the upstream test server) — a stand-in for attacker-controlled or
	// drifted content.
	writePublicHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html><head><title>Hello</title><link rel="canonical" href="http://attacker.invalid/posts/hello/"></head>
<body>Hello.</body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = upstream.URL
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, nil)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "/posts/hello/"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("verify_publication returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)

	if probedHost == "" {
		t.Fatal("upstream (cfg.SiteURL) never received a request — probe target may have gone elsewhere")
	}
	if got := data["http_status"]; got != float64(http.StatusOK) {
		t.Fatalf("http_status = %v, want 200 from cfg.SiteURL host, not the canonical tag's host", got)
	}
	if got, _ := data["url"].(string); !strings.Contains(got, upstream.URL) {
		t.Fatalf("reported url = %q, want it derived from cfg.SiteURL %q, not the canonical tag", got, upstream.URL)
	}
}

func TestVerifyPublicationSourceOnlyReportsNotYetPublished(t *testing.T) {
	contentRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "draft", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Draft\ndate: 2026-07-14\n---\nDraft body.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir() // empty — nothing built yet
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, srcIdx)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "posts/draft"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("verify_publication returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)

	if got := data["status"]; got != "not_yet_published" {
		t.Fatalf("status = %v, want not_yet_published (data=%v)", got, data)
	}
	if got := data["http_checked"]; got != false {
		t.Fatalf("http_checked = %v, want false (page isn't public yet, no request should be made)", got)
	}
}

func TestVerifyPublicationStalePageDetectsSourceNewerThanPublic(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Hello\ndate: 2026-07-14\n---\nUpdated body.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	writePublicHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html><head><title>Hello</title></head><body>Stale.</body></html>`)

	// Ensure the source file's mtime is unambiguously after the public
	// output's mtime (site.StateForResolvedPage compares mtimes to decide
	// staleness).
	publicPath := filepath.Join(siteRoot, "posts", "hello", "index.html")
	longAgo := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(publicPath, longAgo, longAgo); err != nil {
		t.Fatalf("chtimes public output: %v", err)
	}

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, srcIdx)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "posts/hello"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("verify_publication returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)

	if got := data["status"]; got != "stale" {
		t.Fatalf("status = %v, want stale (data=%v)", got, data)
	}
	if got, _ := data["explanation"].(string); got == "" {
		t.Fatal("explanation should be non-empty when stage is stale")
	}
}

func TestVerifyPublicationUnknownSlugReturnsNotFound(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, nil)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "does/not/exist"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("verify_publication on unknown slug: want error, got success")
	}
}

func TestVerifyPublicationEmptySlugIsInvalidParams(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.MaxIndexEntries = 1000

	session, done := newVerifyPublicationServer(t, cfg, nil)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify_publication", Arguments: map[string]any{"slug": "  "}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("verify_publication with blank slug: want error, got success")
	}
}
