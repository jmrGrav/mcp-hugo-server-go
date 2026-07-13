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
	assertReadPageState(t, page["state"], "present", "built", "available", "fresh")
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
	assertReadPageState(t, fm["state"], "present", "built", "available", "fresh")
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
	assertReadPageState(t, fm["state"], "present", "built", "available", "fresh")
	assertReadPageState(t, ctx["state"], "present", "built", "available", "fresh")
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
	assertReadPageState(t, pages0[0].(map[string]any)["state"], "present", "built", "available", "fresh")
}

func TestExportAgentContextPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context export type = %T", m["export"])
	}
	assertReadPaginationMetadata(t, exportVal, 2, 1, 0, 1, true, 1, true)
}

func TestExportAgentContextPaginationMetadataTerminalPage(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"limit": 10, "offset": 1})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context export type = %T", m["export"])
	}
	assertReadPaginationMetadata(t, exportVal, 2, 10, 1, 1, false, 0, false)
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

func newMultilingualHelloReadSession(t *testing.T) (*mcp.ClientSession, func()) {
	t.Helper()
	htmlDir := t.TempDir()
	htmlPage := filepath.Join(htmlDir, "en", "posts", "hello")
	if err := os.MkdirAll(htmlPage, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	htmlFile := filepath.Join(htmlPage, "index.html")
	const html = `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/en/posts/hello/">
<meta property="og:title" content="Hello EN">
<meta property="article:tag" content="Hugo">
</head><body><article>English public body</article></body></html>`
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

	contentRoot := t.TempDir()
	writeSource := func(rel, body string) {
		t.Helper()
		full := filepath.Join(contentRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	writeSource("posts/hello/index.fr.md", "---\ntitle: Bonjour FR\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nBonjour depuis la source francaise.\n")
	writeSource("posts/hello/index.en.md", "---\ntitle: Hello EN\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nHello from the English source.\n")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	return newTestClientWithSourceIndex(t, idx, srcIdx)
}

func newEditorialGraphSession(t *testing.T) (*mcp.ClientSession, func()) {
	t.Helper()
	htmlDir := t.TempDir()
	writeHTML := func(rel, html string) {
		t.Helper()
		full := filepath.Join(htmlDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	writeHTML(filepath.Join("posts", "hello", "index.html"), `<!DOCTYPE html><html lang="fr"><head>
<link rel="canonical" href="https://example.test/posts/hello/">
<meta property="og:title" content="Bonjour FR">
<meta property="article:tag" content="Hugo">
<meta property="article:section" content="Infrastructure">
</head><body><article>Bonjour FR public body</article></body></html>`)
	writeHTML(filepath.Join("en", "posts", "hello", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/en/posts/hello/">
<meta property="og:title" content="Hello EN">
<meta property="article:tag" content="Hugo">
<meta property="article:section" content="Infrastructure">
</head><body><article>Hello EN public body</article></body></html>`)
	writeHTML(filepath.Join("posts", "guide", "index.html"), `<!DOCTYPE html><html lang="fr"><head>
<link rel="canonical" href="https://example.test/posts/guide/">
<meta property="og:title" content="Guide FR">
<meta property="article:tag" content="Hugo">
<meta property="article:section" content="Infrastructure">
</head><body><article>Guide FR public body</article></body></html>`)

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

	contentRoot := t.TempDir()
	writeSource := func(rel, body string) {
		t.Helper()
		full := filepath.Join(contentRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	writeSource("posts/hello/index.fr.md", "---\ntitle: Bonjour FR\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nBonjour depuis la source francaise.\n")
	writeSource("posts/hello/index.en.md", "---\ntitle: Hello EN\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nHello from the English source.\n")
	writeSource("posts/guide/index.fr.md", "---\ntitle: Guide FR\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nGuide associe en francais.\n")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	return newTestClientWithSourceIndex(t, idx, srcIdx)
}

func TestExportAgentContextPrefersMatchingLanguageSource(t *testing.T) {
	session, done := newMultilingualHelloReadSession(t)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"tag": "Hugo", "limit": 10, "offset": 0})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context export type = %T", m["export"])
	}
	pages, ok := exportVal["pages"].([]any)
	if !ok || len(pages) != 1 {
		t.Fatalf("export_agent_context pages = %#v, want one page", exportVal["pages"])
	}
	page, ok := pages[0].(map[string]any)
	if !ok {
		t.Fatalf("export page type = %T", pages[0])
	}
	fm, ok := page["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("frontmatter type = %T", page["frontmatter"])
	}
	if got := fm["lang"]; got != "en" {
		t.Fatalf("frontmatter.lang = %v, want en", got)
	}
	if got := fm["resolved_lang"]; got != "en" {
		t.Fatalf("frontmatter.resolved_lang = %v, want en", got)
	}
	if got := asString(t, fm["resolved_source_path"]); !strings.HasSuffix(got, filepath.ToSlash("posts/hello/index.en.md")) {
		t.Fatalf("frontmatter.resolved_source_path = %q, want suffix posts/hello/index.en.md", got)
	}
	md, _ := page["markdown"].(string)
	if !strings.Contains(md, "Hello from the English source.") {
		t.Fatalf("markdown = %q, want English source content", md)
	}
	if strings.Contains(md, "Bonjour depuis la source francaise.") {
		t.Fatalf("markdown unexpectedly used French source content: %q", md)
	}
}

func TestRichReadToolsPreferMatchingLanguageSource(t *testing.T) {
	session, done := newMultilingualHelloReadSession(t)
	defer done()

	checkFrontmatter := func(t *testing.T, fm map[string]any) {
		t.Helper()
		if got := fm["lang"]; got != "en" {
			t.Fatalf("lang = %v, want en", got)
		}
		if got := fm["resolved_lang"]; got != "en" {
			t.Fatalf("resolved_lang = %v, want en", got)
		}
		if got := asString(t, fm["resolved_source_path"]); !strings.HasSuffix(got, filepath.ToSlash("posts/hello/index.en.md")) {
			t.Fatalf("resolved_source_path = %q, want suffix posts/hello/index.en.md", got)
		}
	}

	t.Run("get_page_frontmatter", func(t *testing.T) {
		res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/en/posts/hello/"})
		if res.IsError {
			t.Fatalf("get_page_frontmatter returned error: %v", res.Content)
		}
		m := decodeContent(t, res)
		fm, ok := m["frontmatter"].(map[string]any)
		if !ok {
			t.Fatalf("frontmatter type = %T", m["frontmatter"])
		}
		checkFrontmatter(t, fm)
	})

	t.Run("get_full_page_markdown", func(t *testing.T) {
		res := callTool(t, session, "get_full_page_markdown", map[string]any{"slug": "/en/posts/hello/"})
		if res.IsError {
			t.Fatalf("get_full_page_markdown returned error: %v", res.Content)
		}
		m := decodeContent(t, res)
		page, ok := m["page"].(map[string]any)
		if !ok {
			t.Fatalf("page type = %T", m["page"])
		}
		checkFrontmatter(t, page)
		md, _ := page["markdown"].(string)
		if !strings.Contains(md, "Hello from the English source.") {
			t.Fatalf("markdown = %q, want English source content", md)
		}
	})

	t.Run("build_agent_context", func(t *testing.T) {
		res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/en/posts/hello/"})
		if res.IsError {
			t.Fatalf("build_agent_context returned error: %v", res.Content)
		}
		m := decodeContent(t, res)
		ctx, ok := m["context"].(map[string]any)
		if !ok {
			t.Fatalf("context type = %T", m["context"])
		}
		fm, ok := ctx["frontmatter"].(map[string]any)
		if !ok {
			t.Fatalf("context.frontmatter type = %T", ctx["frontmatter"])
		}
		checkFrontmatter(t, fm)
		md, _ := ctx["markdown"].(string)
		if !strings.Contains(md, "Hello from the English source.") {
			t.Fatalf("markdown = %q, want English source content", md)
		}
	})
}

func TestGetRelatedContentSeparatesTranslations(t *testing.T) {
	session, done := newEditorialGraphSession(t)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/", "limit": 10})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	translations, ok := m["translations"].([]any)
	if !ok || len(translations) != 1 {
		t.Fatalf("translations = %#v, want one translation", m["translations"])
	}
	translation := translations[0].(map[string]any)
	if got := translation["slug"]; got != "/en/posts/hello/" {
		t.Fatalf("translation slug = %v, want /en/posts/hello/", got)
	}
	if got := translation["lang"]; got != "en" {
		t.Fatalf("translation lang = %v, want en", got)
	}

	relatedPages, ok := m["related_pages"].([]any)
	if !ok || len(relatedPages) == 0 {
		t.Fatalf("related_pages = %#v, want at least one real related page", m["related_pages"])
	}
	legacyRelated, ok := m["related"].([]any)
	if !ok || len(legacyRelated) == 0 {
		t.Fatalf("related = %#v, want legacy compatibility alias", m["related"])
	}
	for _, raw := range append(relatedPages, legacyRelated...) {
		related := raw.(map[string]any)
		if got := related["slug"]; got == "/en/posts/hello/" {
			t.Fatalf("translation leaked into related content: %#v", related)
		}
	}
	if got := relatedPages[0].(map[string]any)["slug"]; got != "/posts/guide/" {
		t.Fatalf("top related slug = %v, want /posts/guide/", got)
	}
}

func TestBuildAgentContextSeparatesTranslations(t *testing.T) {
	session, done := newEditorialGraphSession(t)
	defer done()

	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("build_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	ctx, ok := m["context"].(map[string]any)
	if !ok {
		t.Fatalf("context type = %T", m["context"])
	}
	translations, ok := ctx["translations"].([]any)
	if !ok || len(translations) != 1 {
		t.Fatalf("translations = %#v, want one translation", ctx["translations"])
	}
	relatedPages, ok := ctx["related_pages"].([]any)
	if !ok || len(relatedPages) == 0 {
		t.Fatalf("related_pages = %#v, want at least one related page", ctx["related_pages"])
	}
	for _, raw := range relatedPages {
		related := raw.(map[string]any)
		if got := related["slug"]; got == "/en/posts/hello/" {
			t.Fatalf("translation leaked into build_agent_context related_pages: %#v", related)
		}
	}
}

func TestSuggestInternalLinksSeparatesTranslations(t *testing.T) {
	session, done := newEditorialGraphSession(t)
	defer done()

	res := callTool(t, session, "suggest_internal_links", map[string]any{"slug": "/posts/hello/", "limit": 10})
	if res.IsError {
		t.Fatalf("suggest_internal_links returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	translations, ok := data["translations"].([]any)
	if !ok || len(translations) != 1 {
		t.Fatalf("translations = %#v, want one translation", data["translations"])
	}
	legacySuggestions, ok := data["suggestions"].([]any)
	if !ok || len(legacySuggestions) == 0 {
		t.Fatalf("suggestions = %#v, want at least one suggestion", data["suggestions"])
	}
	suggestedLinks, ok := data["suggested_links"].([]any)
	if !ok || len(suggestedLinks) == 0 {
		t.Fatalf("suggested_links = %#v, want compatibility alias", data["suggested_links"])
	}
	for _, raw := range append(legacySuggestions, suggestedLinks...) {
		suggestion := raw.(map[string]any)
		if got := suggestion["slug"]; got == "/en/posts/hello/" {
			t.Fatalf("translation leaked into suggested links: %#v", suggestion)
		}
	}
	if got := legacySuggestions[0].(map[string]any)["slug"]; got != "/posts/guide/" {
		t.Fatalf("top suggestion slug = %v, want /posts/guide/", got)
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
	var hello map[string]any
	for _, raw := range pages {
		page, _ := raw.(map[string]any)
		if page["slug"] == "/posts/hello/" {
			hello = page
			break
		}
	}
	if hello == nil {
		t.Fatal("search_content expected /posts/hello/ result")
	}
	if got := hello["resolved_lang"]; got != "" {
		t.Fatalf("search_content resolved_lang = %v, want empty default source lang for hello.md fixture", got)
	}
	if got := hello["resolved_source_path"]; !strings.HasSuffix(asString(t, got), filepath.ToSlash("testdata/fixtures/content/posts/hello.md")) {
		t.Fatalf("search_content resolved_source_path = %v, want suffix testdata/fixtures/content/posts/hello.md", got)
	}
	assertReadPageState(t, hello["state"], "present", "built", "available", "fresh")
}

func TestSearchContentPaginationMetadata(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_content", map[string]any{
		"query":  "hugo",
		"limit":  1,
		"offset": 0,
		"sort":   "relevance",
		"order":  "desc",
	})
	if res.IsError {
		t.Fatalf("search_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("search_content data type = %T", m["data"])
	}
	assertReadPaginationMetadata(t, data, 2, 1, 0, 1, true, 1, true)
}

func TestSearchContentInvalidTypeStructuredError(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "search_content", map[string]any{
		"type": "wrong",
	})
	if !res.IsError {
		t.Fatal("search_content with invalid type should return error result")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if m["success"] != false {
		t.Fatalf("search_content error success = %v, want false", m["success"])
	}
	errors, ok := m["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("search_content errors = %#v", m["errors"])
	}
	err0 := errors[0].(map[string]any)
	if got := err0["code"]; got != "invalid_params" {
		t.Fatalf("search_content error code = %v, want invalid_params", got)
	}
	if got := err0["field"]; got != "type" {
		t.Fatalf("search_content error field = %v, want type", got)
	}
	resolution, ok := err0["resolution"].(map[string]any)
	if !ok {
		t.Fatalf("search_content resolution = %T", err0["resolution"])
	}
	allowed, ok := resolution["allowed_values"].([]any)
	if !ok || len(allowed) == 0 {
		t.Fatalf("search_content allowed_values = %#v", resolution["allowed_values"])
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
	recentPages, ok := data["recent_pages"].([]any)
	if !ok || len(recentPages) == 0 {
		t.Fatalf("explain_site_structure recent_pages = %#v, want at least one page", data["recent_pages"])
	}
	assertReadPageState(t, recentPages[0].(map[string]any)["state"], "present", "built", "available", "fresh")
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
		assertSchemaHasProperties(t, tool, "outputSchema", "success", "data", "errors", "warnings", "meta")
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
	for _, tc := range []struct {
		tool string
		keys []string
	}{
		{tool: "get_full_page_markdown", keys: []string{"success", "data", "errors", "warnings", "meta", "page"}},
		{tool: "get_page_frontmatter", keys: []string{"success", "data", "errors", "warnings", "meta", "frontmatter"}},
		{tool: "get_related_content", keys: []string{"success", "data", "errors", "warnings", "meta", "translations", "related_pages", "related"}},
		{tool: "build_agent_context", keys: []string{"success", "data", "errors", "warnings", "meta", "context"}},
		{tool: "export_agent_context", keys: []string{"success", "data", "errors", "warnings", "meta", "export", "pages", "total", "limit", "offset", "returned_count", "has_more"}},
		{tool: "search_content", keys: []string{"success", "data", "errors", "warnings", "meta", "pages", "total", "limit", "offset", "returned_count", "has_more"}},
		{tool: "explain_site_structure", keys: []string{"success", "data", "errors", "warnings", "meta", "summary", "sections", "languages"}},
		{tool: "get_site_health", keys: []string{"success", "data", "errors", "warnings", "meta", "status", "score", "published_pages"}},
		{tool: "get_broken_links", keys: []string{"success", "data", "errors", "warnings", "meta", "links", "broken_links", "total_pages"}},
		{tool: "get_backlinks", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "count", "backlinks"}},
		{tool: "suggest_internal_links", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "total", "translations", "suggestions", "suggested_links"}},
		{tool: "diff_page", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "path", "status", "diff_available"}},
		{tool: "validate_front_matter", keys: []string{"success", "data", "errors", "warnings", "meta", "pages", "pages_checked", "pages_passed", "invalid"}},
		{tool: "validate_site", keys: []string{"success", "data", "errors", "warnings", "meta", "pages", "pages_checked", "pages_passed", "invalid"}},
	} {
		tool, ok := got[tc.tool]
		if !ok {
			t.Fatalf("missing tool %q", tc.tool)
		}
		assertSchemaHasProperties(t, tool, "outputSchema", tc.keys...)
		assertSchemaHasProperties(t, tool, "outputSchema.meta", "generated_at", "server_version")
	}
}

func assertReadPageState(t *testing.T, raw any, source, build, public, index string) {
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

func assertSchemaHasProperties(t *testing.T, tool *mcp.Tool, field string, want ...string) {
	t.Helper()
	schema := schemaAt(t, tool, field)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q: %s.properties type = %T, want map[string]any", tool.Name, field, schema["properties"])
	}
	for _, key := range want {
		if _, ok := props[key]; !ok {
			t.Fatalf("tool %q: %s.properties missing %q", tool.Name, field, key)
		}
	}
}

func schemaAt(t *testing.T, tool *mcp.Tool, field string) map[string]any {
	t.Helper()
	parts := strings.Split(field, ".")
	var current any
	switch parts[0] {
	case "inputSchema":
		current = tool.InputSchema
	case "outputSchema":
		current = tool.OutputSchema
	default:
		t.Fatalf("unknown schema field %q", field)
	}
	for _, part := range parts[1:] {
		m, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("tool %q: schema path %q type = %T, want map[string]any", tool.Name, field, current)
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q: schema path %q missing properties map", tool.Name, field)
		}
		current, ok = props[part]
		if !ok {
			t.Fatalf("tool %q: schema path %q missing property %q", tool.Name, field, part)
		}
	}
	m, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: schema path %q final type = %T, want map[string]any", tool.Name, field, current)
	}
	return m
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

func assertReadPaginationMetadata(t *testing.T, m map[string]any, total, limit, offset, returned int, hasMore bool, nextOffset int, hasNextOffset bool) {
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
