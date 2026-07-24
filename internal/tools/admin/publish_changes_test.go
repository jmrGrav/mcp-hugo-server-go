package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPublishChangesServer(t *testing.T, cfg config.Config, srcIdx *hugosite.SourceIndex, siteReload ...func() error) (*mcp.ClientSession, func()) {
	t.Helper()
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterPublishChanges(s, idx, srcIdx, cfg, siteReload...)

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

// TestPublishChangesPublishedWhenFresh verifies publish_changes reports
// data.status "published" only once the build succeeds *and*
// verify_publication's own check comes back "fresh" — the core contract
// from docs/transactional-edit-design.md §4's answer to #340's fifth
// question.
func TestPublishChangesPublishedWhenFresh(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

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

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = wantRoot
	cfg.SiteURL = upstream.URL
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newPublishChangesServer(t, cfg, nil)
	defer done()

	res, err := callTool(t, session, "publish_changes", map[string]any{"slug": "/posts/hello/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("publish_changes returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	if got := data["status"]; got != "published" {
		t.Fatalf("data.status = %v, want published (data=%v)", got, data)
	}
	build, ok := data["build"].(map[string]any)
	if !ok || build["build_id"] == "" || build["build_id"] == nil {
		t.Fatalf("data.build missing build_id: %v", data["build"])
	}
	pub, ok := data["publication"].(map[string]any)
	if !ok || pub["status"] != "fresh" {
		t.Fatalf("data.publication.status = %v, want fresh", data["publication"])
	}
}

// TestPublishChangesUnverifiedWhenPublicationNotFresh verifies a build that
// succeeds but a page that hasn't actually gone live yet (or is stale)
// reports "build_succeeded_unverified", not "published" — publish_changes
// is never allowed to claim success on build status alone.
// TestPublishChangesPartialSuccessBuildIsNeverPublished is a regression test
// for the case verify_publication's own file/HTTP checks can't see: a
// post-build callback failure (e.g. a CDN purge that leaves stale bytes
// cached at the edge) makes the build "partial_success" even though local
// source/build/public/index state and the HTTP probe can still read "fresh".
// publish_changes must not report "published" on build status alone.
func TestPublishChangesPartialSuccessBuildIsNeverPublished(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

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

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = wantRoot
	cfg.SiteURL = upstream.URL
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	failingCallback := func() error { return fmt.Errorf("cdn purge failed") }
	session, done := newPublishChangesServer(t, cfg, nil, failingCallback)
	defer done()

	res, err := callTool(t, session, "publish_changes", map[string]any{"slug": "/posts/hello/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("publish_changes returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	if got := data["status"]; got != "build_succeeded_unverified" {
		t.Fatalf("data.status = %v, want build_succeeded_unverified even though publication reads fresh (data=%v)", got, data)
	}
	pub, ok := data["publication"].(map[string]any)
	if !ok || pub["status"] != "fresh" {
		t.Fatalf("data.publication.status = %v, want fresh (this test's point is that fresh alone isn't enough)", data["publication"])
	}
	build, ok := data["build"].(map[string]any)
	if !ok || build["warning"] == "" || build["warning"] == nil {
		t.Fatalf("data.build.warning should surface the callback failure: %v", data["build"])
	}
}

func TestPublishChangesUnverifiedWhenPublicationNotFresh(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

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
	cfg.HugoRoot = wantRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	session, done := newPublishChangesServer(t, cfg, srcIdx)
	defer done()

	res, err := callTool(t, session, "publish_changes", map[string]any{"slug": "posts/draft"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("publish_changes returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	if got := data["status"]; got != "build_succeeded_unverified" {
		t.Fatalf("data.status = %v, want build_succeeded_unverified (data=%v)", got, data)
	}
	pub, ok := data["publication"].(map[string]any)
	if !ok || pub["status"] != "not_yet_published" {
		t.Fatalf("data.publication.status = %v, want not_yet_published", data["publication"])
	}
}

// TestPublishChangesBuildFailurePropagatesAsToolError verifies a failed
// build surfaces the same tool error build_site itself would produce,
// never a data.status value — publish_changes must not swallow a build
// failure into a "soft" success shape.
func TestPublishChangesBuildFailurePropagatesAsToolError(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\necho 'Error: TOML parse error' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = wantRoot

	session, done := newPublishChangesServer(t, cfg, nil)
	defer done()

	res, err := callTool(t, session, "publish_changes", map[string]any{"slug": "/posts/hello/"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("publish_changes should fail when the build fails")
	}
}

func TestPublishChangesRequiresSlug(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newPublishChangesServer(t, cfg, nil)
	defer done()

	res, err := callTool(t, session, "publish_changes", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("publish_changes without slug should fail")
	}
}
