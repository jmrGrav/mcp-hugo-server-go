package read_test

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	return idx
}

func newTestClient(t *testing.T, idx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	read.Register(s, idx, config.Default())
	read.RegisterWithSourceIndex(s, idx, mustTestSourceIndex(t), config.Default())

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
	if _, ok := ctx["markdown"]; !ok {
		t.Fatal("build_agent_context: missing 'markdown' in context")
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
	if _, ok := data["total"]; !ok {
		t.Fatal("validate_site missing total")
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
	for _, name := range []string{"search_content", "explain_site_structure", "get_site_health", "validate_front_matter", "validate_site"} {
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
