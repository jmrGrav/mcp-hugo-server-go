package admin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPreviewInspectionServer(t *testing.T, cfg config.Config) (*mcp.ClientSession, *previewstore.Store, func()) {
	t.Helper()
	store := previewstore.New()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	var err error
	if strings.TrimSpace(cfg.ContentRoot) == "" {
		cfg.ContentRoot = t.TempDir()
	}
	if strings.TrimSpace(cfg.SiteRoot) == "" {
		cfg.SiteRoot = t.TempDir()
	}
	if strings.TrimSpace(cfg.HugoRoot) == "" {
		cfg.HugoRoot = t.TempDir()
	}
	var srcIdx *hugosite.SourceIndex
	if strings.TrimSpace(cfg.ContentRoot) != "" {
		srcIdx, err = hugosite.NewSourceIndex(cfg.ContentRoot)
		if err != nil {
			t.Fatalf("NewSourceIndex(%q): %v", cfg.ContentRoot, err)
		}
	}
	admin.Register(s, cfg, srcIdx, nil)
	admin.RegisterCreatePreview(s, cfg, store, "https://mcp.example.test")
	admin.RegisterPreviewAccessTools(s, cfg, store, "https://mcp.example.test")
	read.RegisterInspectPreviewRenderedPage(s, nil, srcIdx, cfg, store, "https://mcp.example.test")

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
	return session, store, func() { _ = session.Close() }
}

func writePreviewInspectionPage(t *testing.T, contentRoot string) {
	t.Helper()
	pagePath := filepath.Join(contentRoot, "posts", "draft", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Draft\ndate: 2026-08-04\ndraft: true\n---\nDraft body.\n"), 0o644); err != nil {
		t.Fatalf("write source page: %v", err)
	}
}

func writePreviewInspectionHTML(t *testing.T, root string) {
	t.Helper()
	full := filepath.Join(root, "posts", "draft", "index.html")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir preview output: %v", err)
	}
	body := `<!DOCTYPE html>
<html lang="en">
<head>
<title>Draft</title>
<meta name="description" content="Draft preview description.">
<link rel="canonical" href="https://mcp.example.test/preview/abc123/posts/draft/">
</head>
<body><p>Draft preview body.</p></body>
</html>`
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write preview html: %v", err)
	}
}

func TestInspectPreviewRenderedSchemaAndToolPresence(t *testing.T) {
	cfg := config.Default()
	cfg.ContentRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, _, done := newPreviewInspectionServer(t, cfg)
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "inspect_preview" {
			return
		}
	}
	t.Fatal("inspect_preview_rendered missing from tools/list")
}

func TestInspectPreviewRenderedSupportsDraftPage(t *testing.T) {
	contentRoot := t.TempDir()
	writePreviewInspectionPage(t, contentRoot)
	previewRoot := t.TempDir()
	writePreviewInspectionHTML(t, previewRoot)

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"

	session, store, done := newPreviewInspectionServer(t, cfg)
	defer done()
	store.Put("abc123", &previewstore.Entry{
		Dir:         previewRoot,
		Token:       "entry-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		BuildStatus: "passed",
		Owner:       "audit",
	})

	res, err := callTool(t, session, "inspect_preview", map[string]any{
		"slug":       "posts/draft",
		"preview_id": "abc123",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("inspect_preview_rendered returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	if got := data["inspection_scope"]; got != "preview" {
		t.Fatalf("inspection_scope = %v, want preview", got)
	}
	if got := data["preview_id"]; got != "abc123" {
		t.Fatalf("preview_id = %v, want abc123", got)
	}
	if got := data["preview_build"]; got != "passed" {
		t.Fatalf("preview_build = %v, want passed", got)
	}
	if got := data["preview_expires_at"]; got == "" || got == nil {
		t.Fatalf("preview_expires_at = %v, want non-empty", got)
	}
	if got := data["url"]; got == nil || !strings.Contains(got.(string), "/preview/abc123/posts/draft/") {
		t.Fatalf("url = %v, want preview-scoped URL", got)
	}
	assertPreviewCheckStatus(t, data, "internal_links", "pass")
}

func TestInspectPreviewRenderedPreservesNonDefaultLanguagePath(t *testing.T) {
	contentRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "draft", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: English draft\ndate: 2026-08-10\ndraft: true\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previewRoot := t.TempDir()
	full := filepath.Join(previewRoot, "en", "posts", "draft", "index.html")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	html := `<!doctype html><html lang="en"><head><title>English draft</title><meta name="description" content="Preview"><link rel="canonical" href="https://mcp.example.test/preview/abc123/en/posts/draft/"></head><body>Body.</body></html>`
	if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.DefaultLanguage = "fr"
	session, store, done := newPreviewInspectionServer(t, cfg)
	defer done()
	store.Put("abc123", &previewstore.Entry{Dir: previewRoot, Token: "entry-token", ExpiresAt: time.Now().Add(time.Hour), BuildStatus: "passed", Owner: "audit"})

	res, err := callTool(t, session, "inspect_preview", map[string]any{"slug": "/en/posts/draft/", "preview_id": "abc123"})
	if err != nil {
		t.Fatalf("inspect_preview error=%v", err)
	}
	if res.IsError {
		t.Fatalf("inspect_preview result=%s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	if got := data["url"]; got != "https://mcp.example.test/preview/abc123/en/posts/draft/" {
		t.Fatalf("url = %v, want language-prefixed preview URL", got)
	}
	if got := data["output_path"]; got != "en/posts/draft/index.html" {
		t.Fatalf("output_path = %v, want language-prefixed output", got)
	}
	assertPreviewCheckStatus(t, data, "internal_links", "pass")
}

func assertPreviewCheckStatus(t *testing.T, data map[string]any, name, want string) {
	t.Helper()
	checks, ok := data["checks"].([]any)
	if !ok {
		t.Fatalf("checks = %T, want []any", data["checks"])
	}
	for _, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok || check["check"] != name {
			continue
		}
		if got := check["status"]; got != want {
			t.Fatalf("%s status = %v, want %s", name, got, want)
		}
		return
	}
	t.Fatalf("checks missing %q", name)
}

func TestInspectPreviewRenderedRejectsReversedLanguagePreviewPrefix(t *testing.T) {
	contentRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "draft", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: English draft\ndate: 2026-08-10\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previewRoot := t.TempDir()
	full := filepath.Join(previewRoot, "en", "posts", "draft", "index.html")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	html := `<!doctype html><html lang="en"><head><title>English draft</title><meta name="description" content="Preview"><link rel="canonical" href="https://mcp.example.test/en/preview/abc123/posts/draft/"></head><body><a href="/en/preview/abc123/tags/test/">tag</a></body></html>`
	if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.DefaultLanguage = "fr"
	session, store, done := newPreviewInspectionServer(t, cfg)
	defer done()
	store.Put("abc123", &previewstore.Entry{Dir: previewRoot, Token: "entry-token", ExpiresAt: time.Now().Add(time.Hour), BuildStatus: "passed"})
	res, err := callTool(t, session, "inspect_preview", map[string]any{"slug": "/en/posts/draft/", "preview_id": "abc123"})
	if err != nil || res.IsError {
		t.Fatalf("inspect_preview = %v, %s", err, resultText(res))
	}
	checks := decodeStructuredResult(t, res)["data"].(map[string]any)["checks"].([]any)
	for _, raw := range checks {
		check := raw.(map[string]any)
		if check["check"] == "preview_url_prefixes" && check["status"] == "fail" {
			return
		}
	}
	t.Fatal("inspect_preview must fail preview_url_prefixes for /{lang}/preview/{id}/")
}

func TestInspectPreviewRenderedPreviewExpiredReturnsStableCode(t *testing.T) {
	contentRoot := t.TempDir()
	writePreviewInspectionPage(t, contentRoot)

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, store, done := newPreviewInspectionServer(t, cfg)
	defer done()
	store.Put("expired", &previewstore.Entry{
		Dir:       t.TempDir(),
		Token:     "entry-token",
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	res, err := callTool(t, session, "inspect_preview", map[string]any{
		"slug":       "posts/draft",
		"preview_id": "expired",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("inspect_preview_rendered should fail for expired preview")
	}
	if raw := resultText(res); !strings.Contains(raw, "preview_expired") {
		t.Fatalf("error = %q, want stable preview_expired code", raw)
	}
}

func TestInspectPreviewRenderedPreviewMissingReturnsStableCode(t *testing.T) {
	contentRoot := t.TempDir()
	writePreviewInspectionPage(t, contentRoot)

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, _, done := newPreviewInspectionServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "inspect_preview", map[string]any{
		"slug":       "posts/draft",
		"preview_id": "missing",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("inspect_preview_rendered should fail for missing preview")
	}
	if raw := resultText(res); !strings.Contains(raw, "preview_not_found") {
		t.Fatalf("error = %q, want stable preview_not_found code", raw)
	}
}

func TestInspectPreviewRenderedRevokedPreviewReturnsStableCode(t *testing.T) {
	contentRoot := t.TempDir()
	writePreviewInspectionPage(t, contentRoot)
	previewRoot := t.TempDir()
	writePreviewInspectionHTML(t, previewRoot)

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, store, done := newPreviewInspectionServer(t, cfg)
	defer done()
	store.Put("revoked", &previewstore.Entry{
		Dir:         previewRoot,
		Token:       "entry-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		BuildStatus: "passed",
	})
	if !store.Revoke("revoked") {
		t.Fatal("Revoke(revoked) = false, want true")
	}

	res, err := callTool(t, session, "inspect_preview", map[string]any{
		"slug":       "posts/draft",
		"preview_id": "revoked",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("inspect_preview_rendered should fail for revoked preview")
	}
	if raw := resultText(res); !strings.Contains(raw, "preview_not_found") {
		t.Fatalf("error = %q, want stable preview_not_found code", raw)
	}
}

func TestInspectPreviewRenderedAcceptsPreviewScopedLinksAssetsAndBenignPreloadOnload(t *testing.T) {
	contentRoot := t.TempDir()
	writePreviewInspectionPage(t, contentRoot)

	publicRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(publicRoot, "posts", "hello"), 0o755); err != nil {
		t.Fatalf("mkdir public hello: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "posts", "hello", "index.html"), []byte(`<!doctype html><html><head><title>Hello</title><link rel="canonical" href="https://example.test/posts/hello/"></head><body>Hello.</body></html>`), 0o644); err != nil {
		t.Fatalf("write public hello: %v", err)
	}
	idxCfg := config.Default()
	idxCfg.SiteRoot = publicRoot
	idxCfg.SiteURL = "https://example.test"
	idxCfg.SiteName = "example.test"
	idxCfg.DefaultLanguage = "en"
	idx, err := site.NewIndex(idxCfg)
	if err != nil {
		t.Fatalf("NewIndex(): %v", err)
	}

	previewRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(previewRoot, "posts", "draft"), 0o755); err != nil {
		t.Fatalf("mkdir preview page: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(previewRoot, "svg"), 0o755); err != nil {
		t.Fatalf("mkdir preview svg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(previewRoot, "svg", "loading.min.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o644); err != nil {
		t.Fatalf("write preview svg: %v", err)
	}
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<title>Draft</title>
<meta name="description" content="Draft preview description.">
<link rel="canonical" href="https://mcp.example.test/preview/abc123/posts/draft/">
<link rel="preload" as="style" href="/preview/abc123/css/site.css" onload="this.onload=null;this.rel='stylesheet'">
</head>
<body>
  <a href="/preview/abc123/posts/hello/">Hello</a>
  <img src="/preview/abc123/svg/loading.min.svg" alt="loading">
</body>
</html>`
	if err := os.WriteFile(filepath.Join(previewRoot, "posts", "draft", "index.html"), []byte(html), 0o644); err != nil {
		t.Fatalf("write preview html: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"

	store := previewstore.New()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex(%q): %v", contentRoot, err)
	}
	admin.Register(s, cfg, srcIdx, nil)
	admin.RegisterCreatePreview(s, cfg, store, "https://mcp.example.test")
	admin.RegisterPreviewAccessTools(s, cfg, store, "https://mcp.example.test")
	read.RegisterInspectPreviewRenderedPage(s, idx, srcIdx, cfg, store, "https://mcp.example.test")

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
	defer func() { _ = session.Close() }()

	store.Put("abc123", &previewstore.Entry{
		Dir:         previewRoot,
		Token:       "entry-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		BuildStatus: "passed",
		Owner:       "audit",
	})

	res, err := callTool(t, session, "inspect_preview", map[string]any{
		"slug":       "posts/draft",
		"preview_id": "abc123",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("inspect_preview returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	checks, ok := data["checks"].([]any)
	if !ok {
		t.Fatalf("checks type = %T", data["checks"])
	}
	byName := map[string]map[string]any{}
	for _, raw := range checks {
		m, _ := raw.(map[string]any)
		byName[m["check"].(string)] = m
	}
	for _, name := range []string{"internal_links", "missing_images", "security_inline_event_handlers"} {
		check := byName[name]
		if got := check["status"]; got != "pass" {
			t.Fatalf("%s status = %v detail=%v, want pass", name, got, check["detail"])
		}
	}
}
