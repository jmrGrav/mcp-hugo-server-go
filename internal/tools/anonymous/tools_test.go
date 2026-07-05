package anonymous_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	anonymous.Register(s, idx, config.Default(), srcIdx)

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
	session, done := newTestClient(t, idx)
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

	res := callTool(t, session, "get_page", map[string]any{"slug": "/drafts/fresh/"})
	if res.IsError {
		t.Fatalf("get_page source-only returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page := m["page"].(map[string]any)
	if page["title"] != "Fresh" || page["html"] != "Fresh body" {
		t.Fatalf("get_page source-only page = %#v", page)
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

func TestGetPageNotFound(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/does-not-exist"})
	if !res.IsError {
		t.Fatal("get_page for missing slug should return error result")
	}
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
