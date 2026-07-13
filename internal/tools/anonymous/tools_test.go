package anonymous_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mustTestIndex(t *testing.T) *site.Index {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	return idx
}

func newTestClient(t *testing.T, idx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	anonymous.Register(s, idx, config.Default())

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

func newTestClientWithSourceIndex(t *testing.T, idx *site.Index, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	return newTestClientWithCfg(t, idx, config.Default(), srcIdx)
}

func newTestClientWithCfg(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	anonymous.Register(s, idx, cfg, srcIdx)

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

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func decodeContent(t *testing.T, res *mcp.CallToolResult) map[string]any {
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

func TestListPages(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 10, "offset": 0})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pagesVal, ok := m["pages"]
	if !ok {
		t.Fatal("list_pages: missing 'pages' key")
	}
	pages, ok := pagesVal.([]any)
	if !ok {
		t.Fatalf("list_pages: 'pages' is %T, want []any", pagesVal)
	}
	if len(pages) == 0 {
		t.Fatal("list_pages: returned 0 pages")
	}
}

func TestListPagesPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 2, 1, 0, 1, true, 1, true)
}

func TestListPagesLimitCap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 200, "offset": 0})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)
	if len(pages) > 50 {
		t.Fatalf("list_pages limit=200 returned %d pages, want ≤50", len(pages))
	}
}

func TestListPagesOffset(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 10, "offset": 1000})
	if res.IsError {
		t.Fatalf("list_pages high offset returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)
	if len(pages) != 0 {
		t.Fatalf("list_pages high offset: expected 0 results, got %d", len(pages))
	}
}

func TestGetPageBySlug(t *testing.T) {
	idx := mustTestIndex(t)
	srcIdx, err := hugosite.NewSourceIndex(filepath.Join("..", "..", "..", "testdata", "fixtures", "content"))
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pageVal, ok := m["page"]
	if !ok {
		t.Fatal("get_page: missing 'page' key")
	}
	page, ok := pageVal.(map[string]any)
	if !ok {
		t.Fatalf("get_page: 'page' is %T, want map", pageVal)
	}
	if page["slug"] != "/posts/hello/" {
		t.Fatalf("get_page: slug = %v, want /posts/hello/", page["slug"])
	}
	if page["lang"] != "en" {
		t.Fatalf("get_page: lang = %v, want en", page["lang"])
	}
	if page["resolved_lang"] != "" {
		t.Fatalf("get_page: resolved_lang = %v, want empty default source lang for hello.md fixture", page["resolved_lang"])
	}
	if got, _ := page["resolved_source_path"].(string); !strings.HasSuffix(got, filepath.ToSlash("testdata/fixtures/content/posts/hello.md")) {
		t.Fatalf("get_page: resolved_source_path = %v, want suffix testdata/fixtures/content/posts/hello.md", page["resolved_source_path"])
	}
}

func TestGetPageUsesSourceIndexForCreatedPageBeforeBuild(t *testing.T) {
	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "drafts", "fresh", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Fresh\ntags: [draft]\ncategories: [notes]\n---\nFresh body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &site.Index{}
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	// Without allow_source_fallback the source-only page must not be accessible.
	resDefault := callTool(t, session, "get_page", map[string]any{"slug": "/drafts/fresh/"})
	if !resDefault.IsError {
		t.Fatal("get_page source-only without allow_source_fallback should return error")
	}
	raw, _ := json.Marshal(resDefault.Content)
	if !strings.Contains(string(raw), "content_not_found") {
		t.Fatalf("get_page source-only default error missing 'content_not_found': %s", raw)
	}

	// With allow_source_fallback the source-only non-draft page is returned.
	res := callTool(t, session, "get_page", map[string]any{"slug": "/drafts/fresh/", "allow_source_fallback": true})
	if res.IsError {
		t.Fatalf("get_page source-only with allow_source_fallback returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page := m["page"].(map[string]any)
	if page["title"] != "Fresh" || page["html"] != "Fresh body" {
		t.Fatalf("get_page source-only page = %#v", page)
	}
	assertPageState(t, page["state"], "present", "pending", "not_yet_available", "source_only")
	if page["lang"] != "" {
		t.Fatalf("get_page source-only lang = %#v, want empty string", page["lang"])
	}
	if page["url"] != "" {
		t.Fatalf("get_page source-only url = %#v, want empty string", page["url"])
	}
	if page["resolved_lang"] != "" {
		t.Fatalf("get_page source-only resolved_lang = %#v, want empty string", page["resolved_lang"])
	}
	if page["resolved_source_path"] != full {
		t.Fatalf("get_page source-only resolved_source_path = %#v, want %s", page["resolved_source_path"], full)
	}

	resContentOnly := callTool(t, session, "get_page", map[string]any{
		"slug":                  "/drafts/fresh/",
		"allow_source_fallback": true,
		"content_only":          true,
	})
	if resContentOnly.IsError {
		t.Fatalf("get_page source-only with content_only returned error: %v", resContentOnly.Content)
	}
	contentOnlyPage := decodeContent(t, resContentOnly)["page"].(map[string]any)
	if contentOnlyPage["html"] != "" {
		t.Fatalf("get_page source-only with content_only html = %#v, want empty string", contentOnlyPage["html"])
	}
}

func TestGetPageDraftBlockedEvenWithSourceFallback(t *testing.T) {
	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "drafts", "wip", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: WIP\ndraft: true\n---\nSecret body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &site.Index{}
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/drafts/wip/", "allow_source_fallback": true})
	if !res.IsError {
		t.Fatal("get_page draft with allow_source_fallback should still return error")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "content_not_found") {
		t.Fatalf("get_page draft error missing 'content_not_found': %s", raw)
	}
}

func TestGetPageEmptySlug(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": ""})
	if !res.IsError {
		t.Fatal("get_page with empty slug should return error result")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "content_not_found") {
		t.Fatalf("get_page empty slug error missing 'content_not_found': %s", raw)
	}
}

// TestGetPageDateGates verifies that source-fallback get_page blocks pages
// whose publishDate is in the future or whose expiryDate is in the past (#232).
func TestGetPageDateGates(t *testing.T) {
	contentRoot := t.TempDir()

	write := func(slug, front string) {
		p := filepath.Join(contentRoot, filepath.FromSlash(slug), "index.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte("---\n"+front+"---\nbody\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	write("posts/future", "title: Future\npublishDate: 2099-01-01\n")
	write("posts/expired", "title: Expired\nexpiryDate: 2000-01-01\n")
	write("posts/live", "title: Live\npublishDate: 2000-01-01\nexpiryDate: 2099-01-01\n")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	session, done := newTestClientWithSourceIndex(t, &site.Index{}, srcIdx)
	defer done()

	for _, slug := range []string{"/posts/future/", "/posts/expired/"} {
		res := callTool(t, session, "get_page", map[string]any{"slug": slug, "allow_source_fallback": true})
		if !res.IsError {
			t.Errorf("get_page %s should be blocked by date gate but returned success", slug)
			continue
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "content_not_found") {
			t.Errorf("get_page %s date-gate error missing 'content_not_found': %s", slug, raw)
		}
	}

	// Page with valid window must be accessible.
	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/live/", "allow_source_fallback": true})
	if res.IsError {
		t.Fatalf("get_page live page should be accessible but returned error: %v", res.Content)
	}
}

func TestGetPageNotFound(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/does-not-exist"})
	if !res.IsError {
		t.Fatal("get_page for missing slug should return error result")
	}
}

func TestGetPagePublishedExposesLifecycleState(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page := m["page"].(map[string]any)
	assertPageState(t, page["state"], "absent", "built", "available", "fresh")
}

func TestGetPagePublishedSourceDriftExposesStaleLifecycleState(t *testing.T) {
	contentRoot := t.TempDir()
	publicRoot := t.TempDir()

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "hello"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content): %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "hello", "index.md"), []byte("---\ntitle: Hello\ncategories: [tutorials]\ntags: [hugo]\ndate: 2024-01-01\n---\nNew source body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(content): %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	idx.UpsertPage(site.Page{
		Slug:       "/posts/hello/",
		Title:      "Hello",
		Summary:    "Summary",
		Tags:       []string{"hugo"},
		Categories: []string{"tutorials"},
		Date:       "2024-01-01",
		URL:        "https://example.test/posts/hello/",
		Lang:       "en",
		RawHTML:    "<article>Any built body</article>",
	})
	publicPath := filepath.Join(publicRoot, "posts", "hello", "index.html")
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(public): %v", err)
	}
	if err := os.WriteFile(publicPath, []byte("<article>Any built body</article>"), 0o644); err != nil {
		t.Fatalf("WriteFile(public): %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(publicPath, old, old); err != nil {
		t.Fatalf("Chtimes(public): %v", err)
	}

	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page := m["page"].(map[string]any)
	assertPageState(t, page["state"], "present", "pending", "stale", "stale")
}

func TestSearchPagesMinLength(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_pages", map[string]any{"query": "", "limit": 10})
	if !res.IsError {
		t.Fatal("search_pages with empty query should return error result")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("search_pages empty query error missing 'invalid_params': %s", raw)
	}
}

func TestSearchPagesResults(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_pages", map[string]any{"query": "hello", "limit": 5})
	if res.IsError {
		t.Fatalf("search_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pagesVal, ok := m["pages"]
	if !ok {
		t.Fatal("search_pages: missing 'pages' key")
	}
	pages, ok := pagesVal.([]any)
	if !ok {
		t.Fatalf("search_pages: 'pages' is %T, want []any", pagesVal)
	}
	if len(pages) == 0 {
		t.Fatal("search_pages('hello'): expected results, got 0")
	}
}

func TestSearchPagesPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_pages", map[string]any{"query": "hugo", "limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("search_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 2, 1, 0, 1, true, 1, true)
}

func TestSearchPagesLimitCap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_pages", map[string]any{"query": "hugo", "limit": 200})
	if res.IsError {
		t.Fatalf("search_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)
	if len(pages) > 50 {
		t.Fatalf("search_pages limit=200 returned %d results, want ≤50", len(pages))
	}
}

func TestGetRecentPosts(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_recent_posts", map[string]any{"limit": 5})
	if res.IsError {
		t.Fatalf("get_recent_posts returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pagesVal, ok := m["pages"]
	if !ok {
		t.Fatal("get_recent_posts: missing 'pages' key")
	}
	pages, ok := pagesVal.([]any)
	if !ok {
		t.Fatalf("get_recent_posts: 'pages' is %T, want []any", pagesVal)
	}
	if len(pages) == 0 {
		t.Fatal("get_recent_posts: expected at least one post")
	}
}

func TestGetRecentPostsPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_recent_posts", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("get_recent_posts returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 2, 1, 0, 1, true, 1, true)
}

func TestGetRecentPostsDefaultLimit(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_recent_posts", map[string]any{})
	if res.IsError {
		t.Fatalf("get_recent_posts (default) returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	_, ok := m["pages"]
	if !ok {
		t.Fatal("get_recent_posts: missing 'pages' key")
	}
}

func TestGetRecentPostsLimitCap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_recent_posts", map[string]any{"limit": 200})
	if res.IsError {
		t.Fatalf("get_recent_posts returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)
	if len(pages) > 50 {
		t.Fatalf("get_recent_posts limit=200 returned %d results, want ≤50", len(pages))
	}
}

func TestListTags(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_tags", map[string]any{})
	if res.IsError {
		t.Fatalf("list_tags returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	tagsVal, ok := m["tags"]
	if !ok {
		t.Fatal("list_tags: missing 'tags' key")
	}
	tags, ok := tagsVal.([]any)
	if !ok {
		t.Fatalf("list_tags: 'tags' is %T, want []any", tagsVal)
	}
	if len(tags) == 0 {
		t.Fatal("list_tags: expected at least one tag")
	}
}

func TestListCategories(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_categories", map[string]any{})
	if res.IsError {
		t.Fatalf("list_categories returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	_, ok := m["categories"]
	if !ok {
		t.Fatal("list_categories: missing 'categories' key")
	}
}

func TestListPagesPrefersSourceCategories(t *testing.T) {
	// HTML index: minimal fixture — hello page has no categories in HTML meta.
	// Hugo does not emit article:category or keywords meta tags for taxonomy categories.
	idx := mustTestIndex(t)

	// Source index: the same page with categories in frontmatter.
	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "posts", "hello", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Hello\ncategories: [go, infrastructure]\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 10})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)

	var helloPage map[string]any
	for _, p := range pages {
		pm, _ := p.(map[string]any)
		if pm["slug"] == "/posts/hello/" {
			helloPage = pm
			break
		}
	}
	if helloPage == nil {
		t.Fatal("list_pages did not return /posts/hello/")
	}
	cats, _ := helloPage["categories"].([]any)
	if len(cats) == 0 {
		t.Fatal("list_pages: categories empty — expected source categories [go, infrastructure]")
	}
	if cats[0] != "go" || cats[1] != "infrastructure" {
		t.Fatalf("list_pages: categories = %v, want [go infrastructure]", cats)
	}
}

// TestListPagesEnrichesNonDefaultLangCategories reproduces the production bug where
// non-default-language pages (e.g. /en/posts/foo/) had empty categories because the
// source-index lookup used the slug with the language prefix ("en/posts/foo") but the
// source index stores slugs without it ("posts/foo").
func TestListPagesEnrichesNonDefaultLangCategories(t *testing.T) {
	// Build a public HTML index with an English page at /en/posts/hello/
	// (no article:category tag — Hugo never emits one).
	htmlDir := t.TempDir()
	htmlPage := filepath.Join(htmlDir, "en", "posts", "hello")
	if err := os.MkdirAll(htmlPage, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	htmlFile := filepath.Join(htmlPage, "index.html")
	const html = `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/en/posts/hello/">
<meta property="og:title" content="Hello">
<meta property="article:tag" content="Hugo">
</head><body><article>Body</article></body></html>`
	if err := os.WriteFile(htmlFile, []byte(html), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := config.Default()
	cfg.SiteRoot = htmlDir
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	// Source index: same page stored at posts/hello/index.en.md (no lang prefix).
	contentRoot := t.TempDir()
	src := filepath.Join(contentRoot, "posts", "hello", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(src, []byte("---\ntitle: Hello\ncategories: [tutorials, go]\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 10})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pages, _ := m["pages"].([]any)

	var found map[string]any
	for _, p := range pages {
		pm, _ := p.(map[string]any)
		if pm["slug"] == "/en/posts/hello/" {
			found = pm
			break
		}
	}
	if found == nil {
		slugs := make([]string, 0, len(pages))
		for _, p := range pages {
			pm, _ := p.(map[string]any)
			slugs = append(slugs, pm["slug"].(string))
		}
		t.Fatalf("list_pages: /en/posts/hello/ not found; got %v", slugs)
	}
	cats, _ := found["categories"].([]any)
	if len(cats) == 0 {
		t.Fatal("list_pages: EN page categories empty — expected [tutorials go] from source frontmatter")
	}
	if cats[0] != "tutorials" {
		t.Fatalf("list_pages: EN page categories[0] = %v, want tutorials", cats[0])
	}
}

func TestTaxonomyAliasesNormalizeListTagsAndListPages(t *testing.T) {
	// End-to-end test: with taxonomy_aliases={sécurité:security}, list_tags must
	// return the canonical "security" slug and not the alias "sécurité".
	contentRoot := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(contentRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	write("posts/a/index.md", "---\ntitle: A\ntags: [sécurité, docker]\n---\nBody A\n")
	write("posts/b/index.md", "---\ntitle: B\ntags: [security]\n---\nBody B\n")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	cfg := config.Default()
	cfg.TaxonomyAliases = map[string]string{"sécurité": "security"}

	session, done := newTestClientWithCfg(t, &site.Index{}, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "list_tags", map[string]any{})
	if res.IsError {
		t.Fatalf("list_tags error: %v", res.Content)
	}
	m := decodeContent(t, res)
	tags, ok := m["tags"].([]any)
	if !ok {
		t.Fatalf("list_tags: tags is %T", m["tags"])
	}
	tagSet := make(map[string]bool, len(tags))
	for _, v := range tags {
		tagSet[v.(string)] = true
	}
	if tagSet["sécurité"] {
		t.Error("list_tags: alias 'sécurité' must be folded into canonical 'security', but it appeared in the result")
	}
	if !tagSet["security"] {
		t.Errorf("list_tags: canonical 'security' must be present, got %v", tags)
	}
}

func TestListCategoriesPrefersSourceFrontmatter(t *testing.T) {
	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Hello\ncategories: [dev, security]\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &site.Index{}
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "list_categories", map[string]any{})
	if res.IsError {
		t.Fatalf("list_categories returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	categories := m["categories"].([]any)
	if len(categories) != 2 || categories[0] != "dev" || categories[1] != "security" {
		t.Fatalf("categories = %#v, want dev/security", categories)
	}
}

func TestGetSitemap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_sitemap", map[string]any{})
	if res.IsError {
		t.Fatalf("get_sitemap returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	entriesVal, ok := m["entries"]
	if !ok {
		t.Fatal("get_sitemap: missing 'entries' key")
	}
	entries, ok := entriesVal.([]any)
	if !ok {
		t.Fatalf("get_sitemap: 'entries' is %T, want []any", entriesVal)
	}
	if len(entries) == 0 {
		t.Fatal("get_sitemap: expected at least one entry")
	}
}

func TestGetSitemapExcludeTaxonomies(t *testing.T) {
	root := t.TempDir()
	writeHTML := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	writeHTML("en/tags/webhook/index.html", `<!doctype html><html><head><title>Webhook tag</title><link rel="canonical" href="https://example.test/en/tags/webhook/"></head><body><main>Tag page</main></body></html>`)
	writeHTML("fr/categories/securite/index.html", `<!doctype html><html><head><title>Securite category</title><link rel="canonical" href="https://example.test/fr/categories/securite/"></head><body><main>Category page</main></body></html>`)
	writeHTML("authors/jm/index.html", `<!doctype html><html><head><title>JM author</title><link rel="canonical" href="https://example.test/authors/jm/"></head><body><main>Author page</main></body></html>`)
	writeHTML("posts/hello/index.html", `<!doctype html><html><head><title>Hello</title><meta property="og:type" content="article"><link rel="canonical" href="https://example.test/posts/hello/"></head><body><article>Hello</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, cfg, nil)
	defer done()

	res := callTool(t, session, "get_sitemap", map[string]any{"exclude_taxonomies": true})
	if res.IsError {
		t.Fatalf("get_sitemap exclude_taxonomies returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	entries, ok := m["entries"].([]any)
	if !ok {
		t.Fatalf("get_sitemap exclude_taxonomies entries type = %T", m["entries"])
	}
	if len(entries) == 0 {
		t.Fatal("get_sitemap exclude_taxonomies expected content entries")
	}
	if len(entries) != 1 {
		t.Fatalf("get_sitemap exclude_taxonomies returned %d entries, want 1 content page after filtering", len(entries))
	}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		url, _ := entry["url"].(string)
		if strings.Contains(url, "/tags/") || strings.Contains(url, "/categories/") || strings.Contains(url, "/authors/") {
			t.Fatalf("get_sitemap exclude_taxonomies returned taxonomy URL %q", url)
		}
	}
}

func TestGetSitemapPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_sitemap", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("get_sitemap returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 3, 1, 0, 1, true, 1, true)
}

func TestGetFeed(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_feed", map[string]any{})
	if res.IsError {
		t.Fatalf("get_feed returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	_, ok := m["items"]
	if !ok {
		t.Fatal("get_feed: missing 'items' key")
	}
}

func TestGetFeedPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_feed", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("get_feed returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 2, 1, 0, 1, true, 1, true)
}

func TestListPagesPaginationMetadataTerminalPage(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 10, "offset": 1})
	if res.IsError {
		t.Fatalf("list_pages returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assertPaginationMetadata(t, m, 2, 10, 1, 1, false, 0, false)
}

func TestGetFeedLimitCap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_feed", map[string]any{"limit": 200})
	if res.IsError {
		t.Fatalf("get_feed returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	items, _ := m["items"].([]any)
	if len(items) > 50 {
		t.Fatalf("get_feed limit=200 returned %d items, want ≤50", len(items))
	}
}

func TestGetSiteInformation(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_site_information", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_information returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	siteVal, ok := m["site"]
	if !ok {
		t.Fatal("get_site_information: missing 'site' key")
	}
	siteMap, ok := siteVal.(map[string]any)
	if !ok {
		t.Fatalf("get_site_information: 'site' is %T, want map", siteVal)
	}
	if siteMap["name"] != "example.test" {
		t.Fatalf("get_site_information: name = %v, want example.test", siteMap["name"])
	}
	if siteMap["url"] != "https://example.test" {
		t.Fatalf("get_site_information: url = %v, want https://example.test", siteMap["url"])
	}
}

func TestGetPageContentOnly(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello", "content_only": true})
	if res.IsError {
		t.Fatalf("get_page content_only returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page, ok := m["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page: 'page' is %T, want map", m["page"])
	}
	if slug, _ := page["slug"].(string); slug == "" {
		t.Fatal("get_page content_only: slug must be non-empty (metadata present)")
	}
	html, _ := page["html"].(string)
	if html == "" {
		t.Fatal("get_page content_only: html must be non-empty (article content expected)")
	}
	// content_only must not carry full page chrome — no <nav>, <header>, <footer>.
	for _, tag := range []string{"<nav", "<header", "<footer"} {
		if strings.Contains(html, tag) {
			t.Fatalf("get_page content_only: html contains theme chrome tag %q: %s", tag, html)
		}
	}
}

func TestGetPageFullHTML(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page, ok := m["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page: 'page' is %T, want map", m["page"])
	}
	if html, _ := page["html"].(string); html == "" {
		t.Fatal("get_page: html must be non-empty when content_only is not set")
	}
}

func TestReadOnlyAnnotations(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := map[string]*mcp.Tool{}
	for i := range tools.Tools {
		got[tools.Tools[i].Name] = tools.Tools[i]
	}
	names := []string{"list_pages", "get_page", "search_pages", "get_recent_posts", "list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information"}
	for _, name := range names {
		tool, ok := got[name]
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		assertObjectSchema(t, tool, "inputSchema")
		assertObjectSchema(t, tool, "outputSchema")
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q: ReadOnlyHint not set", name)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("tool %q: DestructiveHint should be false", name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Fatalf("tool %q: IdempotentHint should be true", name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %q: OpenWorldHint should be false", name)
		}
	}
}

func TestGetPageDescriptionDocumentsSourceFallbackContract(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	for i := range tools.Tools {
		tool := tools.Tools[i]
		if tool.Name != "get_page" {
			continue
		}
		for _, want := range []string{
			"raw Markdown rather than rendered HTML",
			"`lang` and `url` are empty",
			"`content_only=true` is also set, the `html` field is returned empty for source-only fallback results",
		} {
			if !strings.Contains(tool.Description, want) {
				t.Fatalf("get_page description missing %q:\n%s", want, tool.Description)
			}
		}
		return
	}
	t.Fatal("get_page tool not found")
}

func assertObjectSchema(t *testing.T, tool *mcp.Tool, field string) {
	t.Helper()
	var schema any
	switch field {
	case "inputSchema":
		schema = tool.InputSchema
	case "outputSchema":
		schema = tool.OutputSchema
	default:
		t.Fatalf("unknown schema field %q", field)
	}
	if schema == nil {
		t.Fatalf("tool %q: %s is nil", tool.Name, field)
	}
	m, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: %s type = %T, want map[string]any", tool.Name, field, schema)
	}
	if m["type"] != "object" {
		t.Fatalf("tool %q: %s.type = %v, want object", tool.Name, field, m["type"])
	}
}

func assertPaginationMetadata(t *testing.T, m map[string]any, total, limit, offset, returned int, hasMore bool, nextOffset int, hasNextOffset bool) {
	t.Helper()
	if got := int(m["total"].(float64)); got != total {
		t.Fatalf("total = %d, want %d", got, total)
	}
	if got := int(m["limit"].(float64)); got != limit {
		t.Fatalf("limit = %d, want %d", got, limit)
	}
	if got := int(m["offset"].(float64)); got != offset {
		t.Fatalf("offset = %d, want %d", got, offset)
	}
	if got := int(m["returned_count"].(float64)); got != returned {
		t.Fatalf("returned_count = %d, want %d", got, returned)
	}
	if got := m["has_more"].(bool); got != hasMore {
		t.Fatalf("has_more = %v, want %v", got, hasMore)
	}
	gotNext, ok := m["next_offset"]
	if hasNextOffset {
		if !ok {
			t.Fatal("next_offset missing")
		}
		if got := int(gotNext.(float64)); got != nextOffset {
			t.Fatalf("next_offset = %d, want %d", got, nextOffset)
		}
		return
	}
	if ok {
		t.Fatalf("next_offset = %v, want omitted", gotNext)
	}
}

func assertPageState(t *testing.T, raw any, source, build, public, index string) {
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
