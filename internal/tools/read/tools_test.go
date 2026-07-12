package read_test

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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
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

func mustTestSourceIndex(t *testing.T) *hugosite.SourceIndex {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "content")
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	return idx
}

func newTestClient(t *testing.T, idx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	srcIdx := mustTestSourceIndex(t)
	read.Register(s, idx, config.Default(), srcIdx)
	read.RegisterWithSourceIndex(s, idx, srcIdx, config.Default())

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
	read.Register(s, idx, config.Default(), srcIdx)
	read.RegisterWithSourceIndex(s, idx, srcIdx, config.Default())

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

func TestGetFullPageMarkdown(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_full_page_markdown", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_full_page_markdown returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pageVal, ok := m["page"]
	if !ok {
		t.Fatal("get_full_page_markdown: missing 'page' key")
	}
	page, ok := pageVal.(map[string]any)
	if !ok {
		t.Fatalf("get_full_page_markdown: 'page' is %T, want map", pageVal)
	}
	markdownVal, ok := page["markdown"]
	if !ok {
		t.Fatal("get_full_page_markdown: missing 'markdown' key in page")
	}
	markdown, ok := markdownVal.(string)
	if !ok || markdown == "" {
		t.Fatalf("get_full_page_markdown: markdown is empty or not a string: %v", markdownVal)
	}
	if markdown != "This is the hello world post body." {
		t.Fatalf("get_full_page_markdown markdown = %q, want source body", markdown)
	}
	if got := page["resolved_source_path"]; got != "testdata/fixtures/content/posts/hello.md" &&
		!strings.HasSuffix(asString(t, got), filepath.ToSlash("testdata/fixtures/content/posts/hello.md")) {
		t.Fatalf("get_full_page_markdown resolved_source_path = %v, want suffix testdata/fixtures/content/posts/hello.md", got)
	}
}

func TestGetFullPageMarkdownUnknown(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_full_page_markdown", map[string]any{"slug": "/posts/does-not-exist"})
	if !res.IsError {
		t.Fatal("get_full_page_markdown with unknown slug should return error")
	}
	raw, _ := json.Marshal(res.Content)
	if len(raw) == 0 {
		t.Fatal("expected error content")
	}
}

func TestGetPageFrontmatter(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	fmVal, ok := m["frontmatter"]
	if !ok {
		t.Fatal("get_page_frontmatter: missing 'frontmatter' key")
	}
	fm, ok := fmVal.(map[string]any)
	if !ok {
		t.Fatalf("get_page_frontmatter: 'frontmatter' is %T, want map", fmVal)
	}
	rtVal, ok := fm["reading_time_minutes"]
	if !ok {
		t.Fatal("get_page_frontmatter: missing 'reading_time_minutes'")
	}
	rt, ok := rtVal.(float64)
	if !ok || rt < 1 {
		t.Fatalf("get_page_frontmatter: reading_time_minutes = %v, want >= 1", rtVal)
	}
	cats, ok := fm["categories"].([]any)
	if !ok || len(cats) != 1 || cats[0] != "tutorials" {
		t.Fatalf("get_page_frontmatter categories = %#v, want source frontmatter category", fm["categories"])
	}
	categoryTerms, ok := fm["category_terms"].([]any)
	if !ok || len(categoryTerms) != 1 {
		t.Fatalf("get_page_frontmatter category_terms = %#v, want one normalized term", fm["category_terms"])
	}
	term := categoryTerms[0].(map[string]any)
	if term["source"] != "tutorials" || term["slug"] != "tutorials" || term["label"] != "Tutorials" {
		t.Fatalf("category term = %#v, want source/slug/label for tutorials", term)
	}
}

func TestGetPageFrontmatterExposesStableMetadataContract(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	fm, ok := m["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_frontmatter frontmatter type = %T", m["frontmatter"])
	}
	for _, key := range []string{"slug", "lang", "url", "title", "reading_time_minutes", "tag_terms", "category_terms", "resolved_lang", "resolved_source_path"} {
		if _, ok := fm[key]; !ok {
			t.Fatalf("get_page_frontmatter missing %q in frontmatter: %#v", key, fm)
		}
	}
	if got := fm["resolved_lang"]; got != "" {
		t.Fatalf("get_page_frontmatter resolved_lang = %v, want empty default lang for hello.md fixture", got)
	}
	if got := fm["resolved_source_path"]; !strings.HasSuffix(asString(t, got), filepath.ToSlash("testdata/fixtures/content/posts/hello.md")) {
		t.Fatalf("get_page_frontmatter resolved_source_path = %v, want suffix testdata/fixtures/content/posts/hello.md", got)
	}
}

func TestGetRelatedContent(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello", "limit": 5})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	relVal, ok := m["related"]
	if !ok {
		t.Fatal("get_related_content: missing 'related' key")
	}
	related, ok := relVal.([]any)
	if !ok {
		t.Fatalf("get_related_content: 'related' is %T, want []any", relVal)
	}
	if len(related) == 0 {
		t.Fatal("get_related_content: expected at least one related page (bonjour shares Hugo tag)")
	}
}

func TestBuildAgentContext(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("build_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	ctxVal, ok := m["context"]
	if !ok {
		t.Fatal("build_agent_context: missing 'context' key")
	}
	ctx, ok := ctxVal.(map[string]any)
	if !ok {
		t.Fatalf("build_agent_context: 'context' is %T, want map", ctxVal)
	}
	if _, ok := ctx["frontmatter"]; !ok {
		t.Fatal("build_agent_context: missing 'frontmatter' in context")
	}
	fm := ctx["frontmatter"].(map[string]any)
	if got := fm["resolved_source_path"]; !strings.HasSuffix(asString(t, got), filepath.ToSlash("testdata/fixtures/content/posts/hello.md")) {
		t.Fatalf("build_agent_context resolved_source_path = %v, want suffix testdata/fixtures/content/posts/hello.md", got)
	}
	markdown, ok := ctx["markdown"].(string)
	if !ok {
		t.Fatal("build_agent_context: missing 'markdown' in context")
	}
	if markdown != "This is the hello world post body." {
		t.Fatalf("build_agent_context markdown = %q, want source body", markdown)
	}
	if _, ok := ctx["related_pages"]; !ok {
		t.Fatal("build_agent_context: missing 'related_pages' in context")
	}
}

func TestExportAgentContext(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res0 := callTool(t, session, "export_agent_context", map[string]any{"limit": 1, "offset": 0})
	if res0.IsError {
		t.Fatalf("export_agent_context offset=0 returned error: %v", res0.Content)
	}
	m0 := decodeContent(t, res0)
	exportVal0, ok := m0["export"]
	if !ok {
		t.Fatal("export_agent_context: missing 'export' key")
	}
	exp0, ok := exportVal0.(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context: 'export' is %T, want map", exportVal0)
	}
	pages0, _ := exp0["pages"].([]any)
	if len(pages0) != 1 {
		t.Fatalf("export_agent_context limit=1 offset=0: expected 1 page, got %d", len(pages0))
	}

	res1 := callTool(t, session, "export_agent_context", map[string]any{"limit": 1, "offset": 1})
	if res1.IsError {
		t.Fatalf("export_agent_context offset=1 returned error: %v", res1.Content)
	}
	m1 := decodeContent(t, res1)
	exp1, _ := m1["export"].(map[string]any)
	pages1, _ := exp1["pages"].([]any)
	if len(pages1) == 0 {
		t.Fatal("export_agent_context offset=1: expected at least one page (fixture has 2+ pages)")
	}

	slug0 := pages0[0].(map[string]any)["frontmatter"].(map[string]any)["slug"]
	slug1 := pages1[0].(map[string]any)["frontmatter"].(map[string]any)["slug"]
	if slug0 == slug1 {
		t.Fatalf("export_agent_context offset should skip pages: got same slug %v at offset 0 and 1", slug0)
	}
}

func TestExportAgentContextUsesSourceMarkdownForPublicLanguageSlug(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"tag": "Hugo", "limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context: 'export' is %T, want map", m["export"])
	}
	pages, ok := exportVal["pages"].([]any)
	if !ok || len(pages) != 1 {
		t.Fatalf("export_agent_context pages = %#v, want one page", exportVal["pages"])
	}
	first, ok := pages[0].(map[string]any)
	if !ok {
		t.Fatalf("export page type = %T", pages[0])
	}
	md, _ := first["markdown"].(string)
	if !strings.Contains(md, "This is the hello world post body.") {
		t.Fatalf("markdown = %q, want source body", md)
	}
	for _, bad := range []string{"javascript:void(0)", "Read Markdown", "Share"} {
		if strings.Contains(md, bad) {
			t.Fatalf("markdown contains theme artifact %q: %q", bad, md)
		}
	}
}

func TestSearchContent(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_content", map[string]any{
		"query": "hello",
		"limit": 5,
		"sort":  "relevance",
		"order": "desc",
	})
	if res.IsError {
		t.Fatalf("search_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	if m["success"] != true {
		t.Fatalf("search_content success = %v, want true", m["success"])
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("search_content data type = %T", m["data"])
	}
	if data["total"] == nil {
		t.Fatal("search_content missing total")
	}
	pages, ok := data["pages"].([]any)
	if !ok {
		t.Fatalf("search_content pages type = %T", data["pages"])
	}
	if len(pages) == 0 {
		t.Fatal("search_content expected results")
	}
}

func TestExplainSiteStructure(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	if _, ok := data["sections"]; !ok {
		t.Fatal("explain_site_structure missing sections")
	}
	if _, ok := data["summary"]; !ok {
		t.Fatal("explain_site_structure missing summary")
	}
}

func TestValidateFrontMatter(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_front_matter", map[string]any{"limit": 10, "offset": 0})
	if res.IsError {
		t.Fatalf("validate_front_matter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_front_matter data type = %T", m["data"])
	}
	if _, ok := data["pages"]; !ok {
		t.Fatal("validate_front_matter missing pages")
	}
}

func TestValidateSite(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_site data type = %T", m["data"])
	}
	if _, ok := data["pages_checked"]; !ok {
		t.Fatal("validate_site missing pages_checked")
	}
}

func TestExtendedReadAnnotations(t *testing.T) {
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
	for _, name := range []string{"search_content", "explain_site_structure", "get_site_health", "get_broken_links", "diff_page", "validate_front_matter", "validate_site"} {
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

func TestExplainSiteStructureUsesSourceIndexCategories(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	srcIdx := mustTestSourceIndex(t)
	wantCats := len(srcIdx.AllCategories())
	if wantCats == 0 {
		t.Fatal("test precondition: source index must have at least one category")
	}

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	gotCats, ok := data["categories"].(float64)
	if !ok {
		t.Fatalf("explain_site_structure categories type = %T, value = %v", data["categories"], data["categories"])
	}
	if int(gotCats) != wantCats {
		t.Fatalf("explain_site_structure categories = %d, want %d (source index count)", int(gotCats), wantCats)
	}
}

func TestExplainSiteStructureRecentPagesUseSourceCategories(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	recentPages, ok := data["recent_pages"].([]any)
	if !ok {
		t.Fatalf("recent_pages type = %T", data["recent_pages"])
	}
	for _, raw := range recentPages {
		page, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("recent page type = %T", raw)
		}
		if page["slug"] != "/posts/hello/" {
			continue
		}
		cats, ok := page["categories"].([]any)
		if !ok {
			t.Fatalf("categories type = %T", page["categories"])
		}
		if len(cats) != 1 || cats[0] != "tutorials" {
			t.Fatalf("recent page categories = %#v, want [tutorials]", cats)
		}
		terms, ok := page["category_terms"].([]any)
		if !ok {
			t.Fatalf("category_terms type = %T", page["category_terms"])
		}
		if len(terms) != 1 {
			t.Fatalf("category_terms = %#v, want one normalized term", terms)
		}
		term := terms[0].(map[string]any)
		if term["slug"] != "tutorials" || term["label"] != "Tutorials" || term["source"] != "tutorials" {
			t.Fatalf("category_terms[0] = %#v, want tutorials normalized term", term)
		}
		return
	}
	t.Fatal("recent_pages does not include /posts/hello/ test fixture")
}

func TestExplainSiteStructureRecentPagesUseSourceCategoriesForLanguagePrefixedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "hello")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.md"), []byte("---\ntitle: Hello\ntags:\n  - Hugo\ncategories:\n  - tutorials\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	siteRoot := t.TempDir()
	publicDir := filepath.Join(siteRoot, "en", "posts", "hello")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll publicDir: %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <title>Hello</title>
  <meta name="description" content="Rendered summary">
  <link rel="canonical" href="https://example.test/en/posts/hello/">
</head>
<body><article>Hello</article></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile public HTML: %v", err)
	}
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

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	recentPages, ok := data["recent_pages"].([]any)
	if !ok || len(recentPages) != 1 {
		t.Fatalf("recent_pages = %#v, want one page", data["recent_pages"])
	}
	page := recentPages[0].(map[string]any)
	cats, ok := page["categories"].([]any)
	if !ok {
		t.Fatalf("categories type = %T", page["categories"])
	}
	if len(cats) != 1 || cats[0] != "tutorials" {
		t.Fatalf("language-prefixed recent page categories = %#v, want [tutorials]", cats)
	}
}

func TestExplainSiteStructureRecentPagesPreferSourceCategoriesOverStalePublicCategories(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "hello")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.md"), []byte("---\ntitle: Hello\ncategories:\n  - tutorials\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	siteRoot := t.TempDir()
	publicDir := filepath.Join(siteRoot, "posts", "hello")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll publicDir: %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <title>Hello</title>
  <meta name="description" content="Rendered summary">
  <meta name="keywords" content="LegacyCat">
  <link rel="canonical" href="https://example.test/posts/hello/">
</head>
<body><article>Hello</article></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile public HTML: %v", err)
	}
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

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	recentPages, ok := data["recent_pages"].([]any)
	if !ok || len(recentPages) != 1 {
		t.Fatalf("recent_pages = %#v, want one page", data["recent_pages"])
	}
	page := recentPages[0].(map[string]any)
	cats, ok := page["categories"].([]any)
	if !ok {
		t.Fatalf("categories type = %T", page["categories"])
	}
	if len(cats) != 1 || cats[0] != "tutorials" {
		t.Fatalf("stale public categories should be overridden by source categories, got %#v", cats)
	}
}

func TestExplainSiteStructureRecentPagesPreferEmptySourceCategoriesOverStalePublicCategories(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "hello")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.md"), []byte("---\ntitle: Hello\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	siteRoot := t.TempDir()
	publicDir := filepath.Join(siteRoot, "posts", "hello")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll publicDir: %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <title>Hello</title>
  <meta name="description" content="Rendered summary">
  <meta name="keywords" content="LegacyCat">
  <link rel="canonical" href="https://example.test/posts/hello/">
</head>
<body><article>Hello</article></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile public HTML: %v", err)
	}
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

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "explain_site_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_site_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_site_structure data type = %T", m["data"])
	}
	recentPages, ok := data["recent_pages"].([]any)
	if !ok || len(recentPages) != 1 {
		t.Fatalf("recent_pages = %#v, want one page", data["recent_pages"])
	}
	page := recentPages[0].(map[string]any)
	cats, ok := page["categories"].([]any)
	if !ok {
		t.Fatalf("categories type = %T", page["categories"])
	}
	if len(cats) != 0 {
		t.Fatalf("empty source categories should override stale public categories, got %#v", cats)
	}
}

func TestBuildAgentContextRelatedPagesMatchSitemap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	// /posts/hello has HTML-indexed tags ["Hugo", "Read-only"]; /posts/bonjour also has "Hugo".
	// Without the fix, build_agent_context would use source-merged tags (["go", "hugo"]) which
	// do not case-match the HTML-indexed "Hugo" in the sitemap, yielding empty related_pages.
	// With the fix, the raw public tags are used for matching, so bonjour is found.
	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("build_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	ctx, ok := m["context"].(map[string]any)
	if !ok {
		t.Fatalf("build_agent_context context type = %T", m["context"])
	}
	related, ok := ctx["related_pages"].([]any)
	if !ok {
		t.Fatalf("build_agent_context related_pages type = %T", ctx["related_pages"])
	}
	if len(related) == 0 {
		t.Fatal("build_agent_context: expected non-empty related_pages (bonjour shares 'Hugo' tag via HTML index)")
	}
}

func TestGetBrokenLinks(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_broken_links", map[string]any{"limit": 5, "offset": 0})
	if res.IsError {
		t.Fatalf("get_broken_links returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("get_broken_links data type = %T", m["data"])
	}
	if _, ok := data["total_pages"]; !ok {
		t.Fatal("get_broken_links missing total_pages")
	}
	if _, ok := data["broken_links"]; !ok {
		t.Fatal("get_broken_links missing broken_links")
	}
}

func TestValidateFrontMatterOutputFields(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_front_matter", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("validate_front_matter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_front_matter data type = %T", m["data"])
	}
	if _, ok := data["pages_checked"]; !ok {
		t.Fatal("validate_front_matter: pages_checked field missing (was 'total')")
	}
	if _, ok := data["pages_passed"]; !ok {
		t.Fatal("validate_front_matter: pages_passed field missing (was 'valid')")
	}
	if _, ok := data["invalid"]; !ok {
		t.Fatal("validate_front_matter: invalid field missing")
	}
	if _, ok := data["total"]; ok {
		t.Fatal("validate_front_matter: old 'total' field must not be present")
	}
	if _, ok := data["valid"]; ok {
		t.Fatal("validate_front_matter: old 'valid' field must not be present")
	}
	pagesChecked := int(data["pages_checked"].(float64))
	pagesPassed := int(data["pages_passed"].(float64))
	invalid := int(data["invalid"].(float64))
	if pagesPassed+invalid != pagesChecked {
		t.Fatalf("aggregate counters are paginated: pages_passed(%d)+invalid(%d) != pages_checked(%d)", pagesPassed, invalid, pagesChecked)
	}
	pages := data["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("limit=1 should return exactly one page detail, got %d", len(pages))
	}
}

func TestValidateFrontMatterDTOHasLangField(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_front_matter", map[string]any{})
	if res.IsError {
		t.Fatalf("validate_front_matter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_front_matter data type = %T", m["data"])
	}
	pages, ok := data["pages"].([]any)
	if !ok || len(pages) == 0 {
		t.Skip("no pages in validate_front_matter output; cannot check DTO shape")
	}
	firstDTO, ok := pages[0].(map[string]any)
	if !ok {
		t.Fatalf("validate_front_matter pages[0] type = %T", pages[0])
	}
	if _, ok := firstDTO["lang"]; !ok {
		t.Fatal("validate_front_matter page DTO: 'lang' field missing")
	}
}
