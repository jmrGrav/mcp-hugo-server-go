package write_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testServerOpts struct {
	SiteRoot string
	SiteDB   *db.DB
	SiteIdx  *site.Index
}

// newTestServer builds a write-tool MCP server over an in-memory transport and
// returns the client session, the source index (for post-call inspection), and
// a cleanup function. Callers that don't need the source index can ignore it.
func newTestServer(t *testing.T, contentRoot string, opts ...testServerOpts) (*mcp.ClientSession, *hugosite.SourceIndex, func()) {
	t.Helper()
	var o testServerOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = o.SiteRoot

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg, o.SiteDB, o.SiteIdx)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, idx, func() { _ = session.Close() }
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func decodeWriteContent(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return m
}

func assertWritePageState(t *testing.T, raw any, source, build, public, index string) {
	t.Helper()
	state, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("state type = %T", raw)
	}
	if got := state["source_state"]; got != source {
		t.Fatalf("source_state = %v, want %q", got, source)
	}
	if got := state["build_state"]; got != build {
		t.Fatalf("build_state = %v, want %q", got, build)
	}
	if got := state["public_state"]; got != public {
		t.Fatalf("public_state = %v, want %q", got, public)
	}
	if got := state["index_state"]; got != index {
		t.Fatalf("index_state = %v, want %q", got, index)
	}
}

func TestCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "my-post",
		"title":      "My Post",
		"body":       "Hello world.",
		"tags":       []any{"go", "hugo"},
		"categories": []any{"tutorials"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	out := decodeWriteContent(t, res)
	assertWritePageState(t, out["state"], "present", "pending", "not_yet_available", "source_only")

	path := filepath.Join(contentRoot, "my-post", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not found at %s: %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, "My Post") {
		t.Errorf("frontmatter missing title: %s", content)
	}
	if !strings.Contains(content, "Hello world.") {
		t.Errorf("body missing: %s", content)
	}
	if !strings.Contains(content, "go") {
		t.Errorf("tags missing: %s", content)
	}
	if !strings.Contains(content, "draft") {
		t.Errorf("frontmatter missing draft field: %s", content)
	}
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != path {
		t.Fatalf("create_page resolved_source_path = %v, want %s", got, path)
	}
	if got := decoded["resolved_lang"]; got != "" {
		t.Fatalf("create_page resolved_lang = %v, want empty default lang", got)
	}
}

func TestCreatePageSymlinkBlocked(t *testing.T) {
	contentRoot := t.TempDir()

	target := t.TempDir()
	symlinkPath := filepath.Join(contentRoot, "bad-slug")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "bad-slug",
		"title": "Bad Slug",
	})
	if !res.IsError {
		t.Fatal("expected error for symlink slug, got success")
	}
}

func TestCreatePageReservedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "_index",
		"title": "Index",
	})
	if !res.IsError {
		t.Fatal("expected error for reserved slug _index, got success")
	}
}

func TestDeletePageRateLimit(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Create 6 pages. Delete the first 5 (each succeeds). The 6th delete targets
	// a page that still exists but must be blocked by the rate limiter.
	for i := 0; i < 6; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("rate-post-%d", i), "title": "Rate Post",
			"body": "body", "tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d failed: %s", i, raw)
		}
	}
	for i := 0; i < 5; i++ {
		res := callTool(t, session, "delete_page", map[string]any{"slug": fmt.Sprintf("rate-post-%d", i)})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("delete %d expected success, got error: %s", i+1, raw)
		}
	}

	res := callTool(t, session, "delete_page", map[string]any{"slug": "rate-post-5"})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 6th delete, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded error, got: %s", raw)
	}
}

func TestDeletePageExposesLifecycleState(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(filepath.Join(siteRoot, "to-delete"), 0o755); err != nil {
		t.Fatalf("MkdirAll(siteRoot): %v", err)
	}
	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "to-delete",
		"title":      "Delete Me",
		"body":       "",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		raw, _ := json.Marshal(createRes.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "delete_page", map[string]any{"slug": "to-delete"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}
	out := decodeWriteContent(t, res)
	assertWritePageState(t, out["state"], "deleted", "not_applicable", "removed", "removed")
}

func TestUpdatePageNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "nonexistent",
		"title": "New Title",
	})
	if !res.IsError {
		t.Fatal("expected not_found error for nonexistent page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found error, got: %s", raw)
	}
}

func TestUpdatePageExposesLifecycleState(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(filepath.Join(siteRoot, "my-post"), 0o755); err != nil {
		t.Fatalf("MkdirAll(siteRoot): %v", err)
	}
	cfg := config.Default()
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/my-post/", Title: "My Post", URL: "https://example.test/my-post/"})

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot, SiteIdx: siteIdx})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "my-post",
		"title":      "My Post",
		"body":       "Hello world.",
		"tags":       []any{"go"},
		"categories": []any{"tutorials"},
	})
	if createRes.IsError {
		raw, _ := json.Marshal(createRes.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "my-post",
		"title": "New Title",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}
	out := decodeWriteContent(t, res)
	assertWritePageState(t, out["state"], "present", "pending", "stale", "stale")
}

func TestCreatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestCreatePageEmptyTitle(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "valid-slug", "title": ""})
	if !res.IsError {
		t.Fatal("expected error for empty title")
	}
}

func TestUpdatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestDeletePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{"slug": ""})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestUpdatePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// create first
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-me",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// update title only
	res = callTool(t, session, "update_page", map[string]any{
		"slug":  "update-me",
		"title": "New Title",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	data, err := os.ReadFile(filepath.Join(contentRoot, "update-me", "index.md"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if !strings.Contains(string(data), "New Title") {
		t.Errorf("updated file missing new title: %s", data)
	}
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != filepath.Join(contentRoot, "update-me", "index.md") {
		t.Fatalf("update_page resolved_source_path = %v, want %s", got, filepath.Join(contentRoot, "update-me", "index.md"))
	}
}

func TestDeletePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "to-delete",
		"title":      "Delete Me",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "to-delete"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	// The entire directory must be removed, not just index.md.
	if _, err := os.Stat(filepath.Join(contentRoot, "to-delete")); !os.IsNotExist(err) {
		t.Error("expected page directory to be fully removed")
	}
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != filepath.Join(contentRoot, "to-delete", "index.md") {
		t.Fatalf("delete_page resolved_source_path = %v, want %s", got, filepath.Join(contentRoot, "to-delete", "index.md"))
	}
}

func TestDeletePageRemovesWholeDirectory(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "extra-files",
		"title":      "Extra Files",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Add an extra file inside the page directory (e.g. an uploaded asset).
	extra := filepath.Join(contentRoot, "extra-files", "image.png")
	if err := os.WriteFile(extra, []byte("fake png"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "extra-files"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "extra-files")); !os.IsNotExist(err) {
		t.Error("expected directory with extra files to be fully removed")
	}
}

// TestUpdatePageMultilingualFile ensures update_page works when the page only
// has index.fr.md (no index.md) — the real-world case for bilingual sites.
func TestUpdatePageMultilingualFile(t *testing.T) {
	contentRoot := t.TempDir()

	// Write an index.fr.md directly (no index.md counterpart).
	pageDir := filepath.Join(contentRoot, "posts", "csp-nonce")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	original := "---\ntitle: Titre original\ndate: \"2026-04-15T00:00:00Z\"\n---\nContenu original."
	if err := os.WriteFile(frFile, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "posts/csp-nonce",
		"title": "Nouveau titre",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed on multilingual page: %s", raw)
	}

	// The fr file must be updated; no index.md should have been created.
	data, err := os.ReadFile(frFile)
	if err != nil {
		t.Fatalf("index.fr.md not found: %v", err)
	}
	if !strings.Contains(string(data), "Nouveau titre") {
		t.Errorf("index.fr.md not updated, got: %s", data)
	}
	if _, err := os.Stat(filepath.Join(pageDir, "index.md")); !os.IsNotExist(err) {
		t.Error("update_page must not create index.md when only index.fr.md exists")
	}
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != frFile {
		t.Fatalf("update_page multilingual resolved_source_path = %v, want %s", got, frFile)
	}
	if got := decoded["resolved_lang"]; got != "fr" {
		t.Fatalf("update_page multilingual resolved_lang = %v, want fr", got)
	}
}

func TestCreatePageAcceptsExplicitLang(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/bilingual",
		"lang":       "fr",
		"title":      "Bonjour",
		"body":       "Contenu",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page with explicit lang failed: %s", raw)
	}

	frPath := filepath.Join(contentRoot, "posts", "bilingual", "index.fr.md")
	if _, err := os.Stat(frPath); err != nil {
		t.Fatalf("expected explicit lang file at %s: %v", frPath, err)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "bilingual", "index.md")); !os.IsNotExist(err) {
		t.Fatal("create_page with explicit lang must not create default index.md")
	}
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != frPath {
		t.Fatalf("create_page resolved_source_path = %v, want %s", got, frPath)
	}
	if got := decoded["resolved_lang"]; got != "fr" {
		t.Fatalf("create_page resolved_lang = %v, want fr", got)
	}
}

func TestCreatePageRejectsInvalidLang(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, lang := range []string{"../escape", "zh-Hant"} {
		res := callTool(t, session, "create_page", map[string]any{
			"slug":       "posts/bad-lang",
			"lang":       lang,
			"title":      "Bad",
			"body":       "body",
			"tags":       []any{},
			"categories": []any{},
		})
		if !res.IsError {
			t.Fatalf("create_page with invalid lang %q should fail", lang)
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "invalid_params") {
			t.Fatalf("create_page invalid lang %q must return invalid_params, got: %s", lang, raw)
		}
	}
}

func TestDeletePageMultilingualBundleStillSucceeds(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual-delete")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{"slug": "posts/bilingual-delete"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page on multilingual bundle failed: %s", raw)
	}
	if _, err := os.Stat(pageDir); !os.IsNotExist(err) {
		t.Fatal("delete_page must remove multilingual bundle directory")
	}
}

// TestUpdatePageDryRunMultilingualPath verifies that the dry_run diff header
// names the resolved file (index.fr.md) not the hardcoded fallback (index.md).
// Regression for issue #257.
func TestUpdatePageDryRunMultilingualPath(t *testing.T) {
	contentRoot := t.TempDir()

	pageDir := filepath.Join(contentRoot, "posts", "csp-nonce")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"),
		[]byte("---\ntitle: Titre\ndate: \"2026-01-01T00:00:00Z\"\n---\nCorps."), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":    "posts/csp-nonce",
		"title":   "Nouveau titre",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run failed: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	body := string(raw)
	if !strings.Contains(body, "index.fr.md") {
		t.Errorf("dry_run diff header must reference index.fr.md, got: %s", body)
	}
	if strings.Contains(body, "posts/csp-nonce/index.md\"") {
		t.Errorf("dry_run diff header must not hardcode index.md for multilingual pages, got: %s", body)
	}
}

// TestDeletePageCleansPublicDir verifies that delete_page also removes the
// rendered output directory from public/ so no zombie page survives.
func TestDeletePageCleansPublicDir(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	// Create source page.
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/zombie-test",
		"title":      "Zombie Test",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Simulate a prior Hugo build by creating the public output directory.
	publicPageDir := filepath.Join(siteRoot, "posts", "zombie-test")
	if err := os.MkdirAll(publicPageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll public dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicPageDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("WriteFile public html: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/zombie-test"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "zombie-test")); !os.IsNotExist(err) {
		t.Error("source directory must be removed")
	}
	if _, err := os.Stat(publicPageDir); !os.IsNotExist(err) {
		t.Error("public directory must be removed by delete_page to prevent zombie")
	}
}

// TestCreatePageSlugNormalization verifies that create_page strips leading and
// trailing slashes from the slug so agents that pass /posts/foo/ and posts/foo
// both reach the same content directory (#265).
func TestCreatePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "/posts/normalized/", "title": "Normalized", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page with leading/trailing slashes failed: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "normalized", "index.md")); err != nil {
		t.Errorf("expected file at posts/normalized/index.md after slug normalization: %v", err)
	}
}

// TestUpdatePageSlugNormalization verifies that update_page accepts a slug with
// leading and trailing slashes and resolves to the same page (#265).
func TestUpdatePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/update-me", "title": "Update Me", "body": "original",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug": "/posts/update-me/", "title": "Updated",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page with leading/trailing slashes failed: %s", raw)
	}
}

// TestDeletePageSlugNormalization verifies that delete_page accepts a slug with
// leading and trailing slashes and removes the correct directory (#265).
func TestDeletePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/slash-test", "title": "Slash Test", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "/posts/slash-test/"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page with leading/trailing slashes failed: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "slash-test")); !os.IsNotExist(err) {
		t.Error("expected page directory to be removed after slug-normalized delete")
	}
}

// TestDeletePageNotFoundOnDoubleDeletion verifies that a second delete on an
// already-deleted slug returns not_found instead of silent success (#266).
func TestDeletePageNotFoundOnDoubleDeletion(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/double-delete", "title": "Double Delete", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/double-delete"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first delete_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/double-delete"})
	if !res.IsError {
		t.Fatal("second delete_page should return not_found, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found error on double deletion, got: %s", raw)
	}
}

// TestDeletePageDryRun verifies that delete_page with dry_run=true returns the
// page content and an empty backlinks list without removing the file (#267).
func TestDeletePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-me", "title": "Dry Run", "body": "preview body",
		"tags": []any{"go"}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dry-run-me", "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run failed: %s", raw)
	}

	m := decodeWriteContent(t, res)
	if m["dry_run"] != true {
		t.Errorf("expected dry_run=true in response, got %v", m["dry_run"])
	}
	content, _ := m["content"].(string)
	if !strings.Contains(content, "Dry Run") {
		t.Errorf("dry_run content should contain page frontmatter, got: %q", content)
	}
	if _, ok := m["backlinks"]; !ok {
		t.Error("dry_run response must include backlinks key")
	}

	// File must not have been removed.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "dry-run-me", "index.md")); err != nil {
		t.Errorf("dry_run must not delete the file: %v", err)
	}
}

// TestDeletePageDryRunNotFound verifies that dry_run on a non-existent slug
// returns not_found (#267).
func TestDeletePageDryRunNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/does-not-exist", "dry_run": true,
	})
	if !res.IsError {
		t.Fatal("delete_page dry_run on non-existent slug should return not_found")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found, got: %s", raw)
	}
}

// TestDeletePageSlugNormalizationSourceIndex verifies that delete_page with a
// slash-wrapped slug correctly removes the source-index entry. Without the
// strings.Trim fix, idx.Delete("/posts/x/") would miss the key "posts/x" that
// create_page stored, leaving a stale index entry (#265).
func TestDeletePageSlugNormalizationSourceIndex(t *testing.T) {
	contentRoot := t.TempDir()
	session, srcIdx, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/norm-idx-test", "title": "Norm Idx", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	if _, ok := srcIdx.GetBySlug("posts/norm-idx-test"); !ok {
		t.Fatal("source index should contain page after create_page")
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "/posts/norm-idx-test/"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page with slashed slug failed: %s", raw)
	}

	if _, ok := srcIdx.GetBySlug("posts/norm-idx-test"); ok {
		t.Error("source index must not retain entry after delete_page with slashed slug")
	}
}

// TestDeletePageDryRunWithBacklinks verifies that dry_run returns actual
// backlinks when a site.Index is wired in (#267).
func TestDeletePageDryRunWithBacklinks(t *testing.T) {
	contentRoot := t.TempDir()

	// Build a minimal site.Index: target page + a page that links to it.
	// Both pages must be in the index: buildReverseMap only stores a backlink
	// if the target page is found via GetBySlug AND classified as content.
	cfg := config.Default()
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:    "/posts/dry-run-bl/",
		Title:   "BL Target",
		URL:     "https://example.test/posts/dry-run-bl/",
		RawHTML: `<article><p>no outgoing links</p></article>`,
	})
	siteIdx.UpsertPage(site.Page{
		Slug:    "/posts/linker/",
		Title:   "Linker Page",
		URL:     "https://example.test/posts/linker/",
		RawHTML: `<article><a href="/posts/dry-run-bl/">go to target</a></article>`,
	})

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteIdx: siteIdx})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-bl", "title": "BL Target", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dry-run-bl", "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run failed: %s", raw)
	}

	m := decodeWriteContent(t, res)
	bls, ok := m["backlinks"].([]any)
	if !ok {
		t.Fatalf("dry_run response must include backlinks array, got %T: %v", m["backlinks"], m["backlinks"])
	}
	if len(bls) != 1 {
		t.Fatalf("expected 1 backlink, got %d: %v", len(bls), bls)
	}
	bl, _ := bls[0].(map[string]any)
	if bl["slug"] != "/posts/linker/" {
		t.Errorf("backlink slug = %q, want /posts/linker/", bl["slug"])
	}
}

// TestDeletePagePublicCleanupWarning verifies that when the public output
// directory cannot be removed (e.g. parent dir is read-only), delete_page
// still succeeds but surfaces a warning in the response (#239).
func TestDeletePagePublicCleanupWarning(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod tricks don't apply as root")
	}
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/read-only-zombie", "title": "RO Zombie",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Create the public output dir then make its parent read-only so RemoveAll fails.
	publicPageDir := filepath.Join(siteRoot, "posts", "read-only-zombie")
	if err := os.MkdirAll(publicPageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	postsDir := filepath.Join(siteRoot, "posts")
	if err := os.Chmod(postsDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(postsDir, 0o755) })

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/read-only-zombie"})

	// Must restore before any further assertions so t.TempDir cleanup can proceed.
	_ = os.Chmod(postsDir, 0o755)

	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not hard-fail on public cleanup error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Errorf("expected a warning in response when public cleanup fails, got: %s", raw)
	}
}

// TestDeletePageDBWarning verifies that when the derived DB cannot be updated
// (e.g. the connection is closed), delete_page still removes the source file
// and surfaces a warning rather than failing hard (#242).
func TestDeletePageDBWarning(t *testing.T) {
	contentRoot := t.TempDir()

	siteDB, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	// Close the DB so any operation on it returns "sql: database is closed".
	siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/db-warning-test", "title": "DB Warning",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/db-warning-test"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not hard-fail on DB error: %s", raw)
	}

	// Source must be gone.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "db-warning-test")); !os.IsNotExist(err) {
		t.Error("source directory must be removed even when DB update fails")
	}

	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Errorf("expected a warning in response when DB delete fails, got: %s", raw)
	}
}

func TestCreatePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "dry-post",
		"title":      "Dry Post",
		"body":       "Preview only.",
		"tags":       []any{},
		"categories": []any{},
		"dry_run":    true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page dry_run returned error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "dry_run") && !strings.Contains(string(raw), "Dry Post") {
		t.Fatalf("create_page dry_run missing content preview: %s", raw)
	}
	// File must NOT exist on disk
	if _, err := os.Stat(filepath.Join(contentRoot, "dry-post", "index.md")); !os.IsNotExist(err) {
		t.Error("create_page dry_run must not write file to disk")
	}
}

func TestUpdatePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Create a real page first
	if r := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-dry",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":    "update-dry",
		"title":   "New Title",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run returned error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	// Diff should show the title change
	if !strings.Contains(string(raw), "New Title") {
		t.Fatalf("update_page dry_run diff missing new title: %s", raw)
	}
	// On-disk file must still have the original title
	data, err := os.ReadFile(filepath.Join(contentRoot, "update-dry", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Original Title") {
		t.Errorf("update_page dry_run must not write to disk; file = %q", data)
	}
}

// TestCreatePageAtomicWriteCheckedRejectsSymlink verifies that create_page
// fails (and does not write outside content_root) when the target slug
// directory is a symlink pointing outside — protecting the T2/T3 write window
// addressed by AtomicWriteChecked (#233).
func TestCreatePageAtomicWriteCheckedRejectsSymlink(t *testing.T) {
	contentRoot := t.TempDir()
	target := t.TempDir()

	// Pre-create the slug dir as a symlink to a dir outside contentRoot.
	symlinkDir := filepath.Join(contentRoot, "escape-post")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "escape-post",
		"title": "Escape Post",
	})
	if !res.IsError {
		t.Fatal("expected error when slug dir is a symlink, got success")
	}
	// No file must be written to the symlink target.
	if _, err := os.Stat(filepath.Join(target, "index.md")); !os.IsNotExist(err) {
		t.Error("index.md was written to symlink target — content root escape not prevented")
	}
}

// TestUpdatePageAtomicWriteCheckedRejectsSymlink verifies that update_page
// fails and does not write outside content_root when the slug directory is
// a symlink (#233).
func TestUpdatePageAtomicWriteCheckedRejectsSymlink(t *testing.T) {
	contentRoot := t.TempDir()

	// Create a real page first so it appears in the source index.
	realDir := filepath.Join(contentRoot, "symlink-me")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := "---\ntitle: Original\ndate: \"2026-01-01T00:00:00Z\"\n---\nBody."
	if err := os.WriteFile(filepath.Join(realDir, "index.md"), []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Confirm update succeeds before swapping.
	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "symlink-me",
		"title": "Updated",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page setup failed: %s", raw)
	}

	// Replace the real dir with a symlink pointing outside contentRoot.
	target := t.TempDir()
	if err := os.RemoveAll(realDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.Symlink(target, realDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":  "symlink-me",
		"title": "Should Not Write",
	})
	if !res.IsError {
		t.Fatal("expected error when slug dir swapped to symlink, got success")
	}
	// No file must be written to the symlink target.
	if _, err := os.Stat(filepath.Join(target, "index.md")); !os.IsNotExist(err) {
		t.Error("index.md was written to symlink target — content root escape not prevented")
	}
}

// TestDeletePageAuditLogErrorSurfacedAsWarning verifies that when the audit log
// cannot be written (e.g. it exists as a directory), delete_page still succeeds
// and surfaces the failure in the response Warning field rather than returning
// an error (#235).
func TestDeletePageAuditLogErrorSurfacedAsWarning(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	// Create a page to delete.
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "audit-test-page",
		"title":      "Audit Test",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	// Simulate a public output directory to verify it is cleaned up too.
	publicDir := filepath.Join(siteRoot, "audit-test-page")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll public dir: %v", err)
	}

	// Create .mcp-audit.log as a directory to make it unusable as a file.
	auditLogPath := filepath.Join(contentRoot, ".mcp-audit.log")
	if err := os.MkdirAll(auditLogPath, 0o755); err != nil {
		t.Fatalf("MkdirAll audit log dir: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "audit-test-page"})

	// Must NOT return an error — deletion is committed.
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not fail when audit log write fails: %s", raw)
	}

	// Source directory must be gone.
	if _, err := os.Stat(filepath.Join(contentRoot, "audit-test-page")); !os.IsNotExist(err) {
		t.Error("source directory must be removed")
	}

	// Public directory must be gone.
	if _, err := os.Stat(publicDir); !os.IsNotExist(err) {
		t.Error("public directory must be removed")
	}

	// Response must contain a warning mentioning audit_error.
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "audit_error") {
		t.Errorf("expected 'audit_error' in response warning, got: %s", raw)
	}
}
