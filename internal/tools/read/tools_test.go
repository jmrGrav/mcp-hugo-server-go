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
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")
	read.Register(s, idx, cfg, srcIdx)
	read.RegisterWithSourceIndex(s, idx, srcIdx, cfg)

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
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
}

func newTestClientWithCfg(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	read.Register(s, idx, cfg, srcIdx)
	read.RegisterWithSourceIndex(s, idx, srcIdx, cfg)

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

func decodeErrorEnvelope(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent != nil {
		return decodeContent(t, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	return m
}

func TestGetFullPageMarkdown(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_markdown", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page_markdown returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	pageVal, ok := m["page"]
	if !ok {
		t.Fatal("get_page_markdown: missing 'page' key")
	}
	page, ok := pageVal.(map[string]any)
	if !ok {
		t.Fatalf("get_page_markdown: 'page' is %T, want map", pageVal)
	}
	markdownVal, ok := page["markdown"]
	if !ok {
		t.Fatal("get_page_markdown: missing 'markdown' key in page")
	}
	markdown, ok := markdownVal.(string)
	if !ok || markdown == "" {
		t.Fatalf("get_page_markdown: markdown is empty or not a string: %v", markdownVal)
	}
	if markdown != "This is the hello world post body." {
		t.Fatalf("get_page_markdown markdown = %q, want source body", markdown)
	}
	if got := page["resolved_source_path"]; got != "content/posts/hello.md" {
		t.Fatalf("get_page_markdown resolved_source_path = %v, want content/posts/hello.md", got)
	}
	assertReadPageState(t, page["state"], "present", "built", "available", "fresh")
}

func TestGetFullPageMarkdownUnknown(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_markdown", map[string]any{"slug": "/posts/does-not-exist"})
	if !res.IsError {
		t.Fatal("get_page_markdown with unknown slug should return error")
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
	if got := fm["resolved_source_path"]; got != "content/posts/hello.md" {
		t.Fatalf("get_page_frontmatter resolved_source_path = %v, want content/posts/hello.md", got)
	}
}

// TestGetPageFrontmatterLangMatchesResolvedLangBeforeBuild is a regression
// test for #476: a page that exists only in the source index (e.g. right
// after create_page, before the next Hugo build) must report a non-empty
// `lang` that agrees with `resolved_lang`, rather than leaving `lang` empty
// until the page is built and gains a site.Index entry.
func TestGetPageFrontmatterLangMatchesResolvedLangBeforeBuild(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "posts", "unbuilt", "index.fr.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\ntitle: Pas Encore Construit\n---\nContenu non construit.\n"
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	cfg.ContentRoot = contentRoot
	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/unbuilt"})
	if res.IsError {
		t.Fatalf("get_page_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	fm, ok := m["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_frontmatter frontmatter type = %T", m["frontmatter"])
	}
	if got := fm["resolved_lang"]; got != "fr" {
		t.Fatalf("get_page_frontmatter resolved_lang = %v, want fr", got)
	}
	if got := fm["lang"]; got != "fr" {
		t.Fatalf("get_page_frontmatter lang = %v, want fr (must agree with resolved_lang before any build)", got)
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
	relVal, ok := m["related_pages"]
	if !ok {
		t.Fatal("get_related_content: missing 'related_pages' key")
	}
	related, ok := relVal.([]any)
	if !ok {
		t.Fatalf("get_related_content: 'related_pages' is %T, want []any", relVal)
	}
	if len(related) == 0 {
		t.Fatal("get_related_content: expected at least one related page (bonjour shares Hugo tag)")
	}
}

// newImpactFixtureSession builds a two-page site for #434's impact facet:
// /posts/hello/ carries a shared tag ("Hugo", also on /posts/guide/) and an
// unshared tag ("UniqueOrphanTag", nowhere else) plus a shared category
// ("Infrastructure") and a redirect alias, so a single get_related_content
// call can assert orphan detection, non-orphan exclusion, sitemap/feed
// presence, and the aliases passthrough all at once.
func newImpactFixtureSession(t *testing.T) (*mcp.ClientSession, func()) {
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
	writeHTML(filepath.Join("posts", "hello", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/hello/">
<meta property="og:title" content="Hello">
<meta property="article:tag" content="Hugo">
<meta property="article:tag" content="UniqueOrphanTag">
<meta property="article:section" content="Infrastructure">
</head><body><article>Hello public body</article></body></html>`)
	writeHTML(filepath.Join("posts", "guide", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/guide/">
<meta property="og:title" content="Guide">
<meta property="article:tag" content="Hugo">
<meta property="article:section" content="Infrastructure">
</head><body><article>Guide public body</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = htmlDir
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
	writeSource("posts/hello/index.md", "---\ntitle: Hello\ntags: [Hugo, UniqueOrphanTag]\ncategories: [Infrastructure]\naliases: [/old-hello/]\n---\nHello body.\n")
	writeSource("posts/guide/index.md", "---\ntitle: Guide\ntags: [Hugo]\ncategories: [Infrastructure]\n---\nGuide body.\n")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	cfg.ContentRoot = contentRoot
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
}

// Both tests below read m["impact"] via decodeContent's full-envelope
// unwrap, i.e. the top-level duplicate of data.impact — get_related_content
// still has that duplication (deliberately out of scope for #433/#494).
// When #495 removes it, these become m["data"].(map[string]any)["impact"].
func TestGetRelatedContentImpactDetectsTaxonomyOrphans(t *testing.T) {
	session, done := newImpactFixtureSession(t)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/", "include": []any{"impact"}})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	impact, ok := m["impact"].(map[string]any)
	if !ok {
		t.Fatalf("get_related_content impact type = %T, want map[string]any", m["impact"])
	}
	orphans, ok := impact["taxonomy_orphans"].([]any)
	if !ok || len(orphans) != 1 || orphans[0] != "UniqueOrphanTag" {
		t.Fatalf("impact.taxonomy_orphans = %#v, want exactly [UniqueOrphanTag] (Hugo/Infrastructure are shared with /posts/guide/)", impact["taxonomy_orphans"])
	}
	if got, ok := impact["sitemap_present"].(bool); !ok || !got {
		t.Fatalf("impact.sitemap_present = %v, want true", impact["sitemap_present"])
	}
	if got, ok := impact["feed_present"].(bool); !ok || !got {
		t.Fatalf("impact.feed_present = %v, want true", impact["feed_present"])
	}
	aliases, ok := impact["aliases"].([]any)
	if !ok || len(aliases) != 1 || aliases[0] != "/old-hello/" {
		t.Fatalf("impact.aliases = %#v, want exactly [/old-hello/]", impact["aliases"])
	}
}

func TestGetRelatedContentOmitsImpactByDefault(t *testing.T) {
	session, done := newImpactFixtureSession(t)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	if _, ok := m["impact"]; ok {
		t.Fatalf("get_related_content impact = %#v, want omitted when include is not requested (#434)", m["impact"])
	}
}

func TestGetRelatedContentInvalidIncludeRejected(t *testing.T) {
	session, done := newImpactFixtureSession(t)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/", "include": []any{"bogus"}})
	if !res.IsError {
		t.Fatal("get_related_content with invalid include value should return an error")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("expected invalid_params error, got: %s", raw)
	}
}

func TestListContentTypesMergesArchetypeAndObservedSections(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// "posts": has both an archetype and observed source pages.
	write("archetypes/posts.md", "---\ntitle: \"\"\nsubtitle: \"\"\ntags: []\n---\n")
	write("content/posts/a/index.md", "---\ntitle: A\n---\nBody A.\n")
	write("content/posts/b/index.md", "---\ntitle: B\n---\nBody B.\n")
	// "notes": observed only, no archetype.
	write("content/notes/c/index.md", "---\ntitle: C\n---\nBody C.\n")
	// "landing": archetype only, no source pages yet.
	write("archetypes/landing.md", "---\nhero_image: \"\"\n---\n")
	// default.md is Hugo's fallback archetype, not a content type itself.
	write("archetypes/default.md", "---\ntitle: \"\"\n---\n")

	src, err := hugosite.NewSourceIndex(filepath.Join(root, "content"))
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join(root, "content")
	cfg.HugoRoot = root
	session, done := newTestClientWithCfg(t, idx, cfg, src)
	defer done()

	res := callTool(t, session, "list_content_types", map[string]any{})
	if res.IsError {
		t.Fatalf("list_content_types returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	types, ok := m["content_types"].([]any)
	if !ok {
		t.Fatalf("list_content_types: 'content_types' is %T, want []any", m["content_types"])
	}
	byName := make(map[string]map[string]any, len(types))
	for _, raw := range types {
		ct, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("list_content_types: entry = %T, want map", raw)
		}
		name, _ := ct["name"].(string)
		byName[name] = ct
	}
	if _, ok := byName["default"]; ok {
		t.Fatal("list_content_types: 'default' archetype must not appear as a content type")
	}

	posts, ok := byName["posts"]
	if !ok {
		t.Fatal("list_content_types: missing 'posts'")
	}
	if posts["source"] != "archetype+observed" {
		t.Fatalf("list_content_types posts.source = %v, want archetype+observed", posts["source"])
	}
	if count, _ := posts["page_count"].(float64); count != 2 {
		t.Fatalf("list_content_types posts.page_count = %v, want 2", posts["page_count"])
	}
	fields, _ := posts["expected_fields"].([]any)
	if len(fields) == 0 {
		t.Fatal("list_content_types posts.expected_fields is empty, want archetype's front matter keys")
	}

	notes, ok := byName["notes"]
	if !ok {
		t.Fatal("list_content_types: missing 'notes'")
	}
	if notes["source"] != "observed" {
		t.Fatalf("list_content_types notes.source = %v, want observed", notes["source"])
	}
	if _, present := notes["archetype_path"]; present {
		t.Fatal("list_content_types notes: unexpected archetype_path, notes has no archetype")
	}
	notesFields, _ := notes["expected_fields"].([]any)
	if len(notesFields) != 1 || notesFields[0] != "title" {
		t.Fatalf("list_content_types notes.expected_fields = %v, want [title] inferred from observed pages", notesFields)
	}

	landing, ok := byName["landing"]
	if !ok {
		t.Fatal("list_content_types: missing 'landing'")
	}
	if landing["source"] != "archetype" {
		t.Fatalf("list_content_types landing.source = %v, want archetype", landing["source"])
	}
	if _, present := landing["page_count"]; present {
		t.Fatal("list_content_types landing: unexpected page_count, landing has no source pages")
	}
}

// TestListContentTypesExcludesSectionIndexFiles is a regression test for
// #457: _index.en/_index.fr section-index files must not appear in
// content_types (an agent shouldn't be able to infer they're a creatable
// content type), but must still be discoverable, now under special_files.
func TestListContentTypesExcludesSectionIndexFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Root-level homepage section index, two languages.
	write("content/_index.en.md", "---\ntitle: Home\n---\nHome EN.\n")
	write("content/_index.fr.md", "---\ntitle: Accueil\n---\nHome FR.\n")
	// "posts" section index, two languages, plus one real post.
	write("content/posts/_index.en.md", "---\ntitle: Posts\n---\n")
	write("content/posts/_index.fr.md", "---\ntitle: Articles\n---\n")
	write("content/posts/a/index.en.md", "---\ntitle: A\n---\nBody A.\n")

	src, err := hugosite.NewSourceIndex(filepath.Join(root, "content"))
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join(root, "content")
	cfg.HugoRoot = root
	session, done := newTestClientWithCfg(t, idx, cfg, src)
	defer done()

	res := callTool(t, session, "list_content_types", map[string]any{})
	if res.IsError {
		t.Fatalf("list_content_types returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	types, _ := m["content_types"].([]any)
	for _, raw := range types {
		ct, _ := raw.(map[string]any)
		if name, _ := ct["name"].(string); strings.HasPrefix(name, "_index") {
			t.Fatalf("list_content_types: %q must not appear as a content type", name)
		}
	}
	posts, ok := func() (map[string]any, bool) {
		for _, raw := range types {
			ct, _ := raw.(map[string]any)
			if ct["name"] == "posts" {
				return ct, true
			}
		}
		return nil, false
	}()
	if !ok {
		t.Fatal("list_content_types: missing 'posts' (its section index must not have swallowed it)")
	}
	if count, _ := posts["page_count"].(float64); count != 1 {
		t.Fatalf("list_content_types posts.page_count = %v, want 1 (section index excluded)", posts["page_count"])
	}

	special, ok := m["special_files"].([]any)
	if !ok || len(special) != 2 {
		t.Fatalf("list_content_types special_files = %v, want 2 entries (root + posts)", m["special_files"])
	}
	bySection := make(map[string]map[string]any, len(special))
	for _, raw := range special {
		sf, _ := raw.(map[string]any)
		bySection[sf["section"].(string)] = sf
	}
	root0, ok := bySection[""]
	if !ok {
		t.Fatal("list_content_types special_files: missing root/home section index")
	}
	if root0["kind"] != "section_index" {
		t.Fatalf("list_content_types special_files root.kind = %v, want section_index", root0["kind"])
	}
	rootLangs, _ := root0["languages"].([]any)
	if len(rootLangs) != 2 {
		t.Fatalf("list_content_types special_files root.languages = %v, want [en fr]", rootLangs)
	}
	postsIdx, ok := bySection["posts"]
	if !ok {
		t.Fatal("list_content_types special_files: missing posts section index")
	}
	postsLangs, _ := postsIdx["languages"].([]any)
	if len(postsLangs) != 2 {
		t.Fatalf("list_content_types special_files posts.languages = %v, want [en fr]", postsLangs)
	}
}

func TestListPageAssetsListsSiblingFilesInBundle(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, data []byte) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("content/posts/article/index.md", []byte("---\ntitle: Article\n---\nBody.\n"))
	write("content/posts/article/cover.webp", []byte("cover bytes"))
	write("content/posts/article/notes.txt", []byte("notes"))

	src, err := hugosite.NewSourceIndex(filepath.Join(root, "content"))
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join(root, "content")
	session, done := newTestClientWithCfg(t, idx, cfg, src)
	defer done()

	res := callTool(t, session, "list_page_assets", map[string]any{"slug": "/posts/article"})
	if res.IsError {
		t.Fatalf("list_page_assets returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	assets, ok := m["assets"].([]any)
	if !ok {
		t.Fatalf("list_page_assets: 'assets' is %T, want []any", m["assets"])
	}
	names := make(map[string]bool, len(assets))
	for _, raw := range assets {
		a, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("list_page_assets: entry = %T, want map", raw)
		}
		names[a["name"].(string)] = true
		if a["name"] == "cover.webp" {
			if size, _ := a["size_bytes"].(float64); size != float64(len("cover bytes")) {
				t.Fatalf("list_page_assets cover.webp size_bytes = %v, want %d", a["size_bytes"], len("cover bytes"))
			}
		}
	}
	if !names["cover.webp"] || !names["notes.txt"] {
		t.Fatalf("list_page_assets missing expected siblings, got %v", names)
	}
	if names["index.md"] {
		t.Fatal("list_page_assets must not list the page's own index.md")
	}
}

func TestListPageAssetsSingleFilePageReturnsNotABundle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content", "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "pages", "about.md"), []byte("---\ntitle: About\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := hugosite.NewSourceIndex(filepath.Join(root, "content"))
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join(root, "content")
	session, done := newTestClientWithCfg(t, idx, cfg, src)
	defer done()

	res := callTool(t, session, "list_page_assets", map[string]any{"slug": "/pages/about"})
	if !res.IsError {
		t.Fatal("list_page_assets: want error for single-file page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_a_bundle") {
		t.Fatalf("list_page_assets error = %s, want not_a_bundle", raw)
	}
}

func TestListPageAssetsNotFound(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "list_page_assets", map[string]any{"slug": "/no/such/page"})
	if !res.IsError {
		t.Fatal("list_page_assets: want error for missing page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "content_not_found") {
		t.Fatalf("list_page_assets error = %s, want content_not_found", raw)
	}
}

func TestGetPageForEditDefaultReturnsFullBundle(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/hello"})
	if res.IsError {
		t.Fatalf("get_page_for_edit returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page, ok := m["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit: 'page' is %T, want map", m["page"])
	}
	for _, field := range []string{"slug", "revision", "frontmatter", "markdown", "state", "quality"} {
		if _, present := page[field]; !present {
			t.Errorf("get_page_for_edit default: missing field %q, got keys %v", field, mapKeysRead(page))
		}
	}
	if rev, _ := page["revision"].(string); rev == "" {
		t.Error("get_page_for_edit default: revision is empty, want a stable revision (#335 dependency)")
	}
	quality, ok := page["quality"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit: 'quality' is %T, want map", page["quality"])
	}
	if _, present := quality["valid"]; !present {
		t.Error("get_page_for_edit: quality.valid missing")
	}
	if _, present := quality["broken_links"]; !present {
		t.Error("get_page_for_edit: quality.broken_links missing")
	}
}

func TestGetPageForEditIncludeShapesResponse(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/hello", "include": []string{"markdown"}})
	if res.IsError {
		t.Fatalf("get_page_for_edit returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page, ok := m["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit: 'page' is %T, want map", m["page"])
	}
	if _, present := page["markdown"]; !present {
		t.Error("get_page_for_edit include=[markdown]: missing markdown")
	}
	for _, field := range []string{"frontmatter", "state", "quality"} {
		if _, present := page[field]; present {
			t.Errorf("get_page_for_edit include=[markdown]: unexpected field %q present", field)
		}
	}
}

func TestGetPageForEditMaxBodyCharsTruncatesAndWarns(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/hello", "max_body_chars": 10})
	if res.IsError {
		t.Fatalf("get_page_for_edit returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	page, ok := m["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit: 'page' is %T, want map", m["page"])
	}
	md, _ := page["markdown"].(string)
	if len(md) != 10 {
		t.Fatalf("get_page_for_edit max_body_chars=10: markdown length = %d, want 10", len(md))
	}
	warnings, _ := m["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("get_page_for_edit max_body_chars=10: expected a truncation warning")
	}
}

func TestGetPageForEditInvalidIncludeRejected(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/hello", "include": []string{"bogus"}})
	if !res.IsError {
		t.Fatal("get_page_for_edit include=[bogus]: expected error")
	}
}

// TestGetPageForEditBacklinksIncludeMatchesStandaloneGetBacklinks is a
// regression test for #465: include=["backlinks"] must return the exact
// same data a standalone get_backlinks call would for the same slug, and
// must NOT be present when omitted (backlinks is opt-in only, not part of
// the default four-section bundle).
func TestGetPageForEditBacklinksIncludeMatchesStandaloneGetBacklinks(t *testing.T) {
	siteRoot := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(siteRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/target/index.html", `<!doctype html><html><head>
<title>Target</title>
<link rel="canonical" href="https://example.test/posts/target/">
</head><body><article>Target body.</article></body></html>`)
	write("posts/linker/index.html", `<!doctype html><html><head>
<title>Linker</title>
<link rel="canonical" href="https://example.test/posts/linker/">
</head><body><article>See <a href="/posts/target/">target</a>.</article></body></html>`)

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
	session, done := newTestClient(t, idx)
	defer done()

	standalone := callTool(t, session, "get_backlinks", map[string]any{"slug": "/posts/target/"})
	if standalone.IsError {
		t.Fatalf("get_backlinks returned error: %v", standalone.Content)
	}
	standaloneData := decodeContent(t, standalone)
	standaloneBacklinks, ok := standaloneData["backlinks"].([]any)
	if !ok || len(standaloneBacklinks) == 0 {
		t.Fatalf("get_backlinks backlinks = %#v, want at least one entry from posts/linker", standaloneData["backlinks"])
	}

	withBacklinks := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/target/", "include": []string{"backlinks"}})
	if withBacklinks.IsError {
		t.Fatalf("get_page_for_edit returned error: %v", withBacklinks.Content)
	}
	withBacklinksData := decodeContent(t, withBacklinks)
	page, ok := withBacklinksData["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit: 'page' is %T, want map", withBacklinksData["page"])
	}
	editBacklinks, ok := page["backlinks"].([]any)
	if !ok {
		t.Fatalf("get_page_for_edit include=[backlinks]: 'backlinks' is %T, want array", page["backlinks"])
	}
	standaloneJSON, _ := json.Marshal(standaloneBacklinks)
	editJSON, _ := json.Marshal(editBacklinks)
	if string(standaloneJSON) != string(editJSON) {
		t.Fatalf("get_page_for_edit backlinks = %s, want identical to get_backlinks = %s", editJSON, standaloneJSON)
	}
	for _, field := range []string{"frontmatter", "markdown", "state", "quality"} {
		if _, present := page[field]; present {
			t.Errorf("get_page_for_edit include=[backlinks]: unexpected field %q present (backlinks is additive to explicit include, not exclusive)", field)
		}
	}

	// Default (omitted include) must NOT carry backlinks.
	defaultRes := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/target/"})
	if defaultRes.IsError {
		t.Fatalf("get_page_for_edit default returned error: %v", defaultRes.Content)
	}
	defaultData := decodeContent(t, defaultRes)
	defaultPage, ok := defaultData["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit default: 'page' is %T, want map", defaultData["page"])
	}
	if _, present := defaultPage["backlinks"]; present {
		t.Fatalf("get_page_for_edit default: unexpected 'backlinks' present, want omitted since it's opt-in only")
	}

	// Requested-but-empty must stay present as `[]`, distinct from
	// not-requested (absent) — this is the whole reason Backlinks is a
	// pointer field rather than a plain slice.
	noInboundRes := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/posts/linker/", "include": []string{"backlinks"}})
	if noInboundRes.IsError {
		t.Fatalf("get_page_for_edit include=[backlinks] (no inbound links) returned error: %v", noInboundRes.Content)
	}
	noInboundPage, ok := decodeContent(t, noInboundRes)["page"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit include=[backlinks] (no inbound links): 'page' is not a map")
	}
	noInboundBacklinks, present := noInboundPage["backlinks"]
	if !present {
		t.Fatal("get_page_for_edit include=[backlinks] (no inbound links): 'backlinks' absent, want present as []")
	}
	arr, ok := noInboundBacklinks.([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("get_page_for_edit include=[backlinks] (no inbound links): backlinks = %#v, want empty array", noInboundBacklinks)
	}
}

func TestGetPageForEditNotFound(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_page_for_edit", map[string]any{"slug": "/does/not/exist/"})
	if !res.IsError {
		t.Fatal("get_page_for_edit(missing slug): expected error")
	}
}

func mapKeysRead(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
	if got := fm["resolved_source_path"]; got != "content/posts/hello.md" {
		t.Fatalf("build_agent_context resolved_source_path = %v, want content/posts/hello.md", got)
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

func TestBuildAgentContextResponseModeCompact(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello", "response_mode": "compact"})
	if res.IsError {
		t.Fatalf("build_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	ctx, ok := m["context"].(map[string]any)
	if !ok {
		t.Fatalf("build_agent_context: 'context' is %T, want map", m["context"])
	}
	for _, field := range []string{"frontmatter", "markdown", "state"} {
		if _, present := ctx[field]; !present {
			t.Errorf("build_agent_context compact: missing field %q", field)
		}
	}
	for _, field := range []string{"translations", "related_pages"} {
		if _, present := ctx[field]; present {
			t.Errorf("build_agent_context compact: unexpected field %q present, want reduced shape", field)
		}
	}

	standard := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello"})
	standardBytes, err := json.Marshal(decodeContent(t, standard)["context"])
	if err != nil {
		t.Fatalf("marshal standard context: %v", err)
	}
	compactBytes, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal compact context: %v", err)
	}
	if len(compactBytes) >= len(standardBytes) {
		t.Errorf("build_agent_context compact payload (%d bytes) not smaller than standard (%d bytes)", len(compactBytes), len(standardBytes))
	}
}

func TestBuildAgentContextMaxBodyCharsTruncatesAndWarns(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello", "max_body_chars": 10})
	if res.IsError {
		t.Fatalf("build_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	ctx, ok := m["context"].(map[string]any)
	if !ok {
		t.Fatalf("build_agent_context: 'context' is %T, want map", m["context"])
	}
	md, _ := ctx["markdown"].(string)
	if len(md) != 10 {
		t.Fatalf("build_agent_context max_body_chars=10: markdown length = %d, want 10", len(md))
	}
	warnings, _ := m["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("build_agent_context max_body_chars=10: expected a truncation warning")
	}
}

func TestBuildAgentContextResponseModeReservedRejected(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "build_agent_context", map[string]any{"slug": "/posts/hello", "response_mode": "full"})
	if !res.IsError {
		t.Fatal("build_agent_context response_mode=full: expected error, reserved mode is not yet implemented")
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

func TestExportAgentContextDefaultCapsLimitWhenIncludeBody(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"limit": 15})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context export type = %T", m["export"])
	}
	if got := exportVal["limit"]; got != float64(10) {
		t.Fatalf("export_agent_context limit=15 with include_body default: effective limit = %v, want 10 (capped)", got)
	}
	if got := exportVal["include_body"]; got != true {
		t.Fatalf("export_agent_context include_body = %v, want true (default)", got)
	}
	warnings, _ := m["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("export_agent_context limit=15 with include_body default: expected a warning that the limit was capped")
	}
}

func TestExportAgentContextIncludeBodyFalseOmitsMarkdownAndRaisesCap(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "export_agent_context", map[string]any{"limit": 15, "include_body": false})
	if res.IsError {
		t.Fatalf("export_agent_context returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	exportVal, ok := m["export"].(map[string]any)
	if !ok {
		t.Fatalf("export_agent_context export type = %T", m["export"])
	}
	if got := exportVal["limit"]; got != float64(15) {
		t.Fatalf("export_agent_context limit=15 with include_body=false: effective limit = %v, want 15 (not capped)", got)
	}
	if got := exportVal["include_body"]; got != false {
		t.Fatalf("export_agent_context include_body = %v, want false", got)
	}
	warnings, _ := m["warnings"].([]any)
	if len(warnings) != 0 {
		t.Fatalf("export_agent_context limit=15 with include_body=false: unexpected warnings %v", warnings)
	}
	pages, _ := exportVal["pages"].([]any)
	if len(pages) == 0 {
		t.Fatal("export_agent_context include_body=false: expected at least one page")
	}
	for _, raw := range pages {
		page, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("export_agent_context page = %T, want map", raw)
		}
		if md, present := page["markdown"]; present {
			t.Fatalf("export_agent_context include_body=false: page still carries markdown field: %v", md)
		}
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

	cfg.ContentRoot = contentRoot
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
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

	cfg.ContentRoot = contentRoot
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
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
	if got := asString(t, fm["resolved_source_path"]); got != "content/posts/hello/index.en.md" {
		t.Fatalf("frontmatter.resolved_source_path = %q, want content/posts/hello/index.en.md", got)
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
		if got := asString(t, fm["resolved_source_path"]); got != "content/posts/hello/index.en.md" {
			t.Fatalf("resolved_source_path = %q, want content/posts/hello/index.en.md", got)
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

	t.Run("get_page_markdown", func(t *testing.T) {
		res := callTool(t, session, "get_page_markdown", map[string]any{"slug": "/en/posts/hello/"})
		if res.IsError {
			t.Fatalf("get_page_markdown returned error: %v", res.Content)
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
	backlinks, ok := m["backlinks"].([]any)
	if !ok {
		t.Fatalf("backlinks = %#v, want array field present", m["backlinks"])
	}
	if len(backlinks) != 0 {
		t.Fatalf("backlinks = %#v, want empty array for fixture without inbound links", backlinks)
	}
	suggestedLinks, ok := m["suggested_links"].([]any)
	if !ok || len(suggestedLinks) == 0 {
		t.Fatalf("suggested_links = %#v, want populated editorial suggestions", m["suggested_links"])
	}
	// The deprecated "related" alias (#453) was removed once #433/#454 were
	// resolved (related_pages is canonical and was never actually removed).
	if _, ok := m["related"]; ok {
		t.Fatalf("related = %#v, want the deprecated alias removed", m["related"])
	}
	for _, raw := range append(relatedPages, suggestedLinks...) {
		related := raw.(map[string]any)
		if got := related["slug"]; got == "/en/posts/hello/" {
			t.Fatalf("translation leaked into related content: %#v", related)
		}
	}
	if got := relatedPages[0].(map[string]any)["slug"]; got != "/posts/guide/" {
		t.Fatalf("top related slug = %v, want /posts/guide/", got)
	}
	if got := suggestedLinks[0].(map[string]any)["slug"]; got != "/posts/guide/" {
		t.Fatalf("top suggested link slug = %v, want /posts/guide/", got)
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

	res := callTool(t, session, "suggest_links", map[string]any{"slug": "/posts/hello/", "limit": 10})
	if res.IsError {
		t.Fatalf("suggest_links returned error: %v", res.Content)
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
	suggestedLinks, ok := data["suggested_links"].([]any)
	if !ok || len(suggestedLinks) == 0 {
		t.Fatalf("suggested_links = %#v, want at least one suggestion", data["suggested_links"])
	}
	// The deprecated "suggestions" alias (#453) was removed once #433/#454
	// were resolved (suggested_links is canonical and was never removed).
	if _, ok := data["suggestions"]; ok {
		t.Fatalf("suggestions = %#v, want the deprecated alias removed", data["suggestions"])
	}
	for _, raw := range suggestedLinks {
		suggestion := raw.(map[string]any)
		if got := suggestion["slug"]; got == "/en/posts/hello/" {
			t.Fatalf("translation leaked into suggested links: %#v", suggestion)
		}
	}
	if got := suggestedLinks[0].(map[string]any)["slug"]; got != "/posts/guide/" {
		t.Fatalf("top suggestion slug = %v, want /posts/guide/", got)
	}
}

// TestSuggestLinksEmptyResultIncludesExplanation is a regression test for
// #458: when suggested_links comes back empty, the response must explain
// why (candidates evaluated, minimum score required) instead of just an
// empty array with no context.
func TestSuggestLinksEmptyResultIncludesExplanation(t *testing.T) {
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
	writeHTML(filepath.Join("posts", "alpha", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/alpha/">
<meta property="og:title" content="Alpha">
<meta property="article:tag" content="go">
</head><body><article>Alpha body</article></body></html>`)
	writeHTML(filepath.Join("posts", "beta", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/beta/">
<meta property="og:title" content="Beta">
<meta property="article:tag" content="rust">
</head><body><article>Beta body</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = htmlDir
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
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "suggest_links", map[string]any{"tags": []string{"no-such-tag"}, "limit": 10})
	if res.IsError {
		t.Fatalf("suggest_links returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	suggestedLinks, ok := data["suggested_links"].([]any)
	if !ok || len(suggestedLinks) != 0 {
		t.Fatalf("suggested_links = %#v, want empty array", data["suggested_links"])
	}
	emptyReason, ok := data["empty_reason"].(map[string]any)
	if !ok {
		t.Fatalf("empty_reason = %#v, want present alongside empty suggested_links", data["empty_reason"])
	}
	if got := emptyReason["reason"]; got != "no_candidates_with_sufficient_taxonomy_affinity" {
		t.Fatalf("empty_reason.reason = %v, want no_candidates_with_sufficient_taxonomy_affinity", got)
	}
	if got := emptyReason["candidates_evaluated"]; got != float64(2) {
		t.Fatalf("empty_reason.candidates_evaluated = %v, want 2", got)
	}
	if got := emptyReason["minimum_score"]; got != float64(1) {
		t.Fatalf("empty_reason.minimum_score = %v, want 1", got)
	}
}

// TestGetRelatedContentEmptyResultIncludesExplanationForSoleContentPage is a
// regression test for #458's "no other content to compare" branch: with only
// one published page in the whole site, related_pages must come back empty
// with candidates_evaluated=0, distinguishing "nothing else exists" from
// "other pages exist but none matched".
func TestGetRelatedContentEmptyResultIncludesExplanationForSoleContentPage(t *testing.T) {
	htmlDir := t.TempDir()
	write := func(rel, html string) {
		t.Helper()
		full := filepath.Join(htmlDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// A home page and a /posts/ section-list page are structural, not
	// content candidates — they must NOT count toward candidates_evaluated
	// (#458), so this fixture deliberately includes both alongside the sole
	// real article to prove the classifier filter actually excludes them.
	write(filepath.Join("index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/">
<meta property="og:title" content="Home">
</head><body><article>Home body</article></body></html>`)
	write(filepath.Join("posts", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/">
<meta property="og:title" content="Posts">
</head><body><article>Posts section list</article></body></html>`)
	write(filepath.Join("posts", "solo", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/solo/">
<meta property="og:title" content="Solo">
<meta property="article:tag" content="go">
</head><body><article>Solo body</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = htmlDir
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
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/solo/", "limit": 10})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	relatedPages, ok := m["related_pages"].([]any)
	if !ok || len(relatedPages) != 0 {
		t.Fatalf("related_pages = %#v, want empty array", m["related_pages"])
	}
	emptyReason, ok := m["empty_reason"].(map[string]any)
	if !ok {
		t.Fatalf("empty_reason = %#v, want present alongside empty related_pages", m["empty_reason"])
	}
	if got := emptyReason["reason"]; got != "no_other_published_content_to_compare" {
		t.Fatalf("empty_reason.reason = %v, want no_other_published_content_to_compare", got)
	}
	if got := emptyReason["candidates_evaluated"]; got != float64(0) {
		t.Fatalf("empty_reason.candidates_evaluated = %v, want 0", got)
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
	if got := hello["resolved_source_path"]; got != "content/posts/hello.md" {
		t.Fatalf("search_content resolved_source_path = %v, want content/posts/hello.md", got)
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
	m := decodeErrorEnvelope(t, res)
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

// TestSearchContentCategoriesMatchGetPageFrontmatter is a regression test for
// #463: search_content previously risked returning empty categories for a
// page whose public/rendered HTML carries no category metadata (Hugo never
// emits article:category), even when get_page_frontmatter correctly enriches
// from the source index for the same slug. Both tools must agree — this is
// the "shared regression test" #463's acceptance criteria asked for, so a
// fifth endpoint doesn't need a fourth live audit to catch the same class of
// bug (see #182/#264/#163 for the prior endpoint-by-endpoint recurrences).
func TestSearchContentCategoriesMatchGetPageFrontmatter(t *testing.T) {
	idx := mustTestIndex(t)
	srcIdx := mustTestSourceIndex(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	fm := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/posts/hello/"})
	if fm.IsError {
		t.Fatalf("get_page_frontmatter returned error: %v", fm.Content)
	}
	fmData := decodeContent(t, fm)
	fmFrontmatter, ok := fmData["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_frontmatter frontmatter type = %T", fmData["frontmatter"])
	}
	fmCategories, _ := fmFrontmatter["categories"].([]any)
	if len(fmCategories) == 0 {
		t.Fatal("get_page_frontmatter categories empty — fixture content/posts/hello.md declares categories: [tutorials]")
	}

	res := callTool(t, session, "search_content", map[string]any{"query": "hello", "limit": 5})
	if res.IsError {
		t.Fatalf("search_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("search_content data type = %T", m["data"])
	}
	pages, _ := data["pages"].([]any)
	var hello map[string]any
	for _, raw := range pages {
		page, _ := raw.(map[string]any)
		if page["slug"] == "/posts/hello/" {
			hello = page
			break
		}
	}
	if hello == nil {
		t.Fatalf("search_content expected /posts/hello/ result, got %v", pages)
	}
	searchCategories, _ := hello["categories"].([]any)
	if len(searchCategories) == 0 {
		t.Fatalf("search_content categories empty for /posts/hello/, want to match get_page_frontmatter's %v", fmCategories)
	}
	if len(searchCategories) != len(fmCategories) || searchCategories[0] != fmCategories[0] {
		t.Fatalf("search_content categories = %v, get_page_frontmatter categories = %v — must match", searchCategories, fmCategories)
	}
}

// TestSearchContentCategoriesMatchGetPageFrontmatterNonDefaultLang covers the
// specific class of bug #463 originally reported: a non-default-language,
// language-prefixed slug (e.g. /en/posts/foo/) where the source index stores
// the page without the language prefix (posts/foo). This is the exact shape
// that historically broke source enrichment (#182/#264/#163) — the fixture
// used by TestSearchContentCategoriesMatchGetPageFrontmatter above uses a
// default-language slug and would not catch a regression in the lang-prefix
// stripping path.
func TestSearchContentCategoriesMatchGetPageFrontmatterNonDefaultLang(t *testing.T) {
	htmlDir := t.TempDir()
	htmlPage := filepath.Join(htmlDir, "en", "posts", "hello")
	if err := os.MkdirAll(htmlPage, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const html = `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/en/posts/hello/">
<meta property="og:title" content="Hello">
</head><body><article>Body</article></body></html>`
	if err := os.WriteFile(filepath.Join(htmlPage, "index.html"), []byte(html), 0o644); err != nil {
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
	cfg.ContentRoot = contentRoot

	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	fm := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": "/en/posts/hello/"})
	if fm.IsError {
		t.Fatalf("get_page_frontmatter returned error: %v", fm.Content)
	}
	fmData := decodeContent(t, fm)
	fmFrontmatter, ok := fmData["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_frontmatter frontmatter type = %T", fmData["frontmatter"])
	}
	fmCategories, _ := fmFrontmatter["categories"].([]any)
	if len(fmCategories) == 0 {
		t.Fatal("get_page_frontmatter categories empty — expected [tutorials, go] from source")
	}

	res := callTool(t, session, "search_content", map[string]any{"query": "hello", "limit": 5})
	if res.IsError {
		t.Fatalf("search_content returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("search_content data type = %T", m["data"])
	}
	pages, _ := data["pages"].([]any)
	var hello map[string]any
	for _, raw := range pages {
		page, _ := raw.(map[string]any)
		if page["slug"] == "/en/posts/hello/" {
			hello = page
			break
		}
	}
	if hello == nil {
		t.Fatalf("search_content expected /en/posts/hello/ result, got %v", pages)
	}
	searchCategories, _ := hello["categories"].([]any)
	if len(searchCategories) == 0 {
		t.Fatalf("search_content categories empty for /en/posts/hello/, want to match get_page_frontmatter's %v", fmCategories)
	}
	if len(searchCategories) != len(fmCategories) || searchCategories[0] != fmCategories[0] {
		t.Fatalf("search_content categories = %v, get_page_frontmatter categories = %v — must match", searchCategories, fmCategories)
	}
}

func TestExplainSiteStructure(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
	}
	if _, ok := data["sections"]; !ok {
		t.Fatal("explain_structure missing sections")
	}
	if _, ok := data["summary"]; !ok {
		t.Fatal("explain_structure missing summary")
	}
	recentPages, ok := data["recent_pages"].([]any)
	if !ok || len(recentPages) == 0 {
		t.Fatalf("explain_structure recent_pages = %#v, want at least one page", data["recent_pages"])
	}
	assertReadPageState(t, recentPages[0].(map[string]any)["state"], "present", "built", "available", "fresh")
}

func TestValidateFrontMatter(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_frontmatter", map[string]any{"limit": 10, "offset": 0})
	if res.IsError {
		t.Fatalf("validate_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_frontmatter data type = %T", m["data"])
	}
	if _, ok := data["pages"]; !ok {
		t.Fatal("validate_frontmatter missing pages")
	}
}

func TestValidateFrontMatterGlobalPaginationDistinguishesScanFromDetailPage(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "validate_frontmatter", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("validate_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_frontmatter data type = %T", m["data"])
	}
	pagesChecked, _ := data["pages_checked"].(float64)
	returnedCount, _ := data["returned_count"].(float64)
	pages, _ := data["pages"].([]any)
	if pagesChecked < 2 {
		t.Fatalf("validate_frontmatter limit=1: pages_checked = %v, want the full scan scope (>=2), not capped by limit", pagesChecked)
	}
	if returnedCount != 1 || len(pages) != 1 {
		t.Fatalf("validate_frontmatter limit=1: returned_count=%v len(pages)=%d, want exactly 1 detail row", returnedCount, len(pages))
	}
	if int(pagesChecked) <= int(returnedCount) {
		t.Fatalf("validate_frontmatter limit=1: pages_checked (%v) should exceed returned_count (%v) so has_more is meaningful", pagesChecked, returnedCount)
	}
	hasMore, _ := data["has_more"].(bool)
	if !hasMore {
		t.Fatal("validate_frontmatter limit=1: has_more = false, want true (more detail rows exist beyond this page)")
	}
	if data["next_offset"] == nil {
		t.Fatal("validate_frontmatter limit=1: next_offset missing, want a value to continue pagination")
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

// mustSiteWithOneInvalidPage builds a small fixture with one page missing
// title/date (invalid) and one clean page, for #431's pagination/invalid_only
// tests below.
func mustSiteWithOneInvalidPage(t *testing.T) (*site.Index, *hugosite.SourceIndex) {
	t.Helper()
	contentRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "valid"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "valid", "index.md"), []byte("---\ntitle: Valid\ndate: 2026-01-01\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "broken"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "broken", "index.md"), []byte("---\n---\nNo title or date.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}
	return idx, srcIdx
}

func TestValidateSiteInvalidOnlyFiltersPassingPages(t *testing.T) {
	idx, srcIdx := mustSiteWithOneInvalidPage(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"invalid_only": true})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	if pagesChecked, _ := data["pages_checked"].(float64); pagesChecked != 2 {
		t.Fatalf("pages_checked = %v, want 2 (full scan scope unaffected by invalid_only)", pagesChecked)
	}
	if invalid, _ := data["invalid"].(float64); invalid != 1 {
		t.Fatalf("invalid = %v, want 1", invalid)
	}
	pages, _ := data["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("pages = %v, want exactly the 1 invalid page's detail row", pages)
	}
	page, ok := pages[0].(map[string]any)
	if !ok || page["slug"] != "posts/broken" {
		t.Fatalf("pages[0] = %v, want the broken page", pages[0])
	}
	if hasMore, _ := data["has_more"].(bool); hasMore {
		t.Fatalf("has_more = true, want false (only 1 invalid page total, all returned)")
	}
}

// TestValidateSiteDefaultsToInvalidOnly is a regression test for #456: with
// no arguments at all, validate_site's default flipped from "return every
// page's detail row" to "return only invalid ones" — the common case (most
// pages pass) no longer pays full response cost to confirm nothing is wrong.
func TestValidateSiteDefaultsToInvalidOnly(t *testing.T) {
	idx, srcIdx := mustSiteWithOneInvalidPage(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	if pagesChecked, _ := data["pages_checked"].(float64); pagesChecked != 2 {
		t.Fatalf("pages_checked = %v, want 2 (full scan scope unaffected by the invalid-only default)", pagesChecked)
	}
	if pagesPassed, _ := data["pages_passed"].(float64); pagesPassed != 1 {
		t.Fatalf("pages_passed = %v, want 1 (full scan scope unaffected by the invalid-only default)", pagesPassed)
	}
	pages, _ := data["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("pages = %v, want only the 1 invalid page by default (#456)", pages)
	}
	page, ok := pages[0].(map[string]any)
	if !ok || page["slug"] != "posts/broken" {
		t.Fatalf("pages[0] = %v, want the broken page", pages[0])
	}
}

// TestValidateSiteIncludeValidOptsIntoFullListing is a regression test for
// #456: include_valid=true is the new opt-in for the pre-#456 behavior of
// returning every page's detail row, not just the invalid ones.
func TestValidateSiteIncludeValidOptsIntoFullListing(t *testing.T) {
	idx, srcIdx := mustSiteWithOneInvalidPage(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"include_valid": true})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	pages, _ := data["pages"].([]any)
	if len(pages) != 2 {
		t.Fatalf("pages = %v, want both pages when include_valid=true", pages)
	}
}

// TestValidateSiteExplicitInvalidOnlyFalsePreservesFullListing is a
// regression test for #456: a caller that already explicitly passed
// invalid_only=false under the old default must keep getting the full
// listing after the default flip — only omitting the field entirely picks
// up the new default.
func TestValidateSiteExplicitInvalidOnlyFalsePreservesFullListing(t *testing.T) {
	idx, srcIdx := mustSiteWithOneInvalidPage(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"invalid_only": false})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	pages, _ := data["pages"].([]any)
	if len(pages) != 2 {
		t.Fatalf("pages = %v, want both pages when invalid_only is explicitly false", pages)
	}
}

func TestValidateSitePaginatesDetailRows(t *testing.T) {
	idx, srcIdx := mustSiteWithOneInvalidPage(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"limit": 1, "offset": 0, "include_valid": true})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	if pagesChecked, _ := data["pages_checked"].(float64); pagesChecked != 2 {
		t.Fatalf("pages_checked = %v, want 2 (full scan scope unaffected by limit)", pagesChecked)
	}
	pages, _ := data["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("pages = %v, want exactly 1 detail row for limit=1", pages)
	}
	if hasMore, _ := data["has_more"].(bool); !hasMore {
		t.Fatal("has_more = false, want true (1 more page beyond limit=1)")
	}
}

func TestGetSiteHealthTaxonomyInconsistencyDetailsIncludeAffectedPages(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.md", "---\ntitle: A\ncategories: [security]\n---\n")
	write("posts/b/index.md", "---\ntitle: B\ncategories: [securite]\n---\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	idx := mustTestIndex(t)
	session, done := newTestClientWithSourceIndex(t, idx, src)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("get_site_health data type = %T", m["data"])
	}
	strs, ok := data["taxonomy_inconsistencies"].([]any)
	if !ok || len(strs) == 0 {
		t.Fatalf("get_site_health: taxonomy_inconsistencies = %#v, want at least one legacy string entry (#210/#328 backward compat)", data["taxonomy_inconsistencies"])
	}
	details, ok := data["taxonomy_inconsistency_details"].([]any)
	if !ok || len(details) == 0 {
		t.Fatalf("get_site_health: taxonomy_inconsistency_details = %#v, want at least one structured entry (#324)", data["taxonomy_inconsistency_details"])
	}
	if len(details) != len(strs) {
		t.Fatalf("get_site_health: %d structured details vs %d legacy strings, want same count and order", len(details), len(strs))
	}
	detail, ok := details[0].(map[string]any)
	if !ok {
		t.Fatalf("get_site_health: taxonomy_inconsistency_details[0] = %T, want map", details[0])
	}
	for _, field := range []string{"message", "term_a", "term_b", "pages_with_term_a", "pages_with_term_b"} {
		if _, present := detail[field]; !present {
			t.Errorf("get_site_health: taxonomy_inconsistency_details[0] missing %q", field)
		}
	}
	pagesA, _ := detail["pages_with_term_a"].([]any)
	pagesB, _ := detail["pages_with_term_b"].([]any)
	if len(pagesA) == 0 && len(pagesB) == 0 {
		t.Fatal("get_site_health: taxonomy_inconsistency_details[0] has no affected pages on either side — the actionability #324 asked for")
	}
}

// TestGetSiteHealthTranslationPairInfoFindingDoesNotMoveScore covers #419:
// a translation_pair finding (info severity) must not affect score or
// score_breakdown.taxonomy.issues at all — only score_breakdown.taxonomy.
// advisories, which exists precisely so an agent can see the finding was
// counted but intentionally excluded from the score.
func TestGetSiteHealthTranslationPairInfoFindingDoesNotMoveScore(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.en.md", "---\ntitle: A\ndate: 2026-07-01\ntags: [security]\n---\n")
	write("posts/a/index.fr.md", "---\ntitle: A\ndate: 2026-07-01\ntags: [sécurité]\n---\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	idx := mustTestIndex(t)
	session, done := newTestClientWithSourceIndex(t, idx, src)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)

	score, _ := data["score"].(float64)
	if score != 100 {
		t.Fatalf("score = %v, want 100 (a translation_pair finding must not penalize score)", score)
	}
	details, _ := data["taxonomy_inconsistency_details"].([]any)
	if len(details) == 0 {
		t.Fatal("taxonomy_inconsistency_details is empty, want the security/sécurité translation_pair finding")
	}
	detail := details[0].(map[string]any)
	if detail["kind"] != "translation_pair" {
		t.Fatalf("taxonomy_inconsistency_details[0].kind = %v, want translation_pair", detail["kind"])
	}
	if detail["severity"] != "info" {
		t.Fatalf("taxonomy_inconsistency_details[0].severity = %v, want info", detail["severity"])
	}
	breakdown, ok := data["score_breakdown"].(map[string]any)
	if !ok {
		t.Fatalf("score_breakdown missing or wrong type: %#v", data["score_breakdown"])
	}
	taxonomy := breakdown["taxonomy"].(map[string]any)
	if issues, _ := taxonomy["issues"].(float64); issues != 0 {
		t.Fatalf("score_breakdown.taxonomy.issues = %v, want 0 (info findings aren't issues)", issues)
	}
	if advisories, _ := taxonomy["advisories"].(float64); advisories != 1 {
		t.Fatalf("score_breakdown.taxonomy.advisories = %v, want 1", advisories)
	}
	if taxScore, _ := taxonomy["score"].(float64); taxScore != 100 {
		t.Fatalf("score_breakdown.taxonomy.score = %v, want 100", taxScore)
	}
}

// TestGetSiteHealthPossibleDuplicateWarningReducesCategoryScoreOnly covers
// #419: a warning-severity finding (possible_duplicate) applies a real,
// visible penalty within score_breakdown.taxonomy.score — proving the
// field isn't decorative — but per the issue's own scope note ("not a
// scoring algorithm change") must NOT move the top-level `score`, which is
// still computed exactly as it was before #419 (frontmatter signals only).
func TestGetSiteHealthPossibleDuplicateWarningReducesCategoryScoreOnly(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.md", "---\ntitle: A\ndate: 2026-07-01\ntags: [postmortem]\n---\n")
	write("posts/b/index.md", "---\ntitle: B\ndate: 2026-07-01\ntags: [post-mortems]\n---\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	idx := mustTestIndex(t)
	session, done := newTestClientWithSourceIndex(t, idx, src)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)

	status, _ := data["status"].(string)
	if status != "healthy" {
		t.Fatalf("status = %q, want healthy", status)
	}
	score, _ := data["score"].(float64)
	if score != 100 {
		t.Fatalf("score = %v, want 100 — a taxonomy warning must not move the top-level score (#419 is presentation-only)", score)
	}
	breakdown := data["score_breakdown"].(map[string]any)
	taxonomy := breakdown["taxonomy"].(map[string]any)
	if issues, _ := taxonomy["issues"].(float64); issues != 1 {
		t.Fatalf("score_breakdown.taxonomy.issues = %v, want 1", issues)
	}
	if weight, _ := taxonomy["weight"].(float64); weight != 0 {
		t.Fatalf("score_breakdown.taxonomy.weight = %v, want 0 (taxonomy never contributes to the top-level score)", weight)
	}
	if taxScore, _ := taxonomy["score"].(float64); taxScore >= 100 {
		t.Fatalf("score_breakdown.taxonomy.score = %v, want < 100 (the finding must show up somewhere, even though it doesn't move the top-level score)", taxScore)
	}
}

// TestGetSiteHealthFrontmatterIssueReducesScore covers #419's acceptance
// criterion of "an error/critical finding that changes status": a
// frontmatter validation error is this server's only category that has
// ever driven `status` (unchanged by #419), and one missing-date page
// already drops it from "healthy" to "degraded" under the pre-existing
// formula (a missing field is flagged twice — once by the free-text check,
// once by the raw front-matter-key check — for 20 points of penalty);
// that's pre-existing behavior this PR doesn't touch.
func TestGetSiteHealthFrontmatterIssueReducesScore(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.md", "---\ntitle: A\ndate: 2026-07-01\n---\n")
	write("posts/b/index.md", "---\ntitle: B\n---\n") // missing date: a real frontmatter finding
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	idx := mustTestIndex(t)
	session, done := newTestClientWithSourceIndex(t, idx, src)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)

	status, _ := data["status"].(string)
	if status != "degraded" {
		t.Fatalf("status = %q, want degraded (one missing-date finding is enough to change status under the pre-existing formula)", status)
	}
	score, _ := data["score"].(float64)
	if score >= 100 {
		t.Fatalf("score = %v, want < 100 (a frontmatter finding must apply a real penalty)", score)
	}
	breakdown := data["score_breakdown"].(map[string]any)
	frontmatter := breakdown["frontmatter"].(map[string]any)
	if weight, _ := frontmatter["weight"].(float64); weight != 100 {
		t.Fatalf("score_breakdown.frontmatter.weight = %v, want 100 (it's the sole driver of the top-level score)", weight)
	}
	if fmScore, _ := frontmatter["score"].(float64); fmScore != score {
		t.Fatalf("score_breakdown.frontmatter.score = %v, want to equal top-level score %v (weight=100 means they must reconcile exactly)", fmScore, score)
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
	for _, name := range []string{"search_content", "explain_structure", "get_site_health", "get_broken_links", "diff_page", "validate_frontmatter", "validate_site"} {
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
		{tool: "get_page_markdown", keys: []string{"success", "data", "errors", "warnings", "meta", "page"}},
		{tool: "get_page_frontmatter", keys: []string{"success", "data", "errors", "warnings", "meta", "frontmatter"}},
		{tool: "get_related_content", keys: []string{"success", "data", "errors", "warnings", "meta", "translations", "related_pages"}},
		{tool: "build_agent_context", keys: []string{"success", "data", "errors", "warnings", "meta", "context"}},
		{tool: "export_agent_context", keys: []string{"success", "data", "errors", "warnings", "meta", "export", "pages", "total", "limit", "offset", "returned_count", "has_more"}},
		{tool: "search_content", keys: []string{"success", "data", "errors", "warnings", "meta", "pages", "total", "limit", "offset", "returned_count", "has_more"}},
		{tool: "explain_structure", keys: []string{"success", "data", "errors", "warnings", "meta", "summary", "sections", "languages"}},
		{tool: "get_site_health", keys: []string{"success", "data", "errors", "warnings", "meta", "status", "score", "published_pages"}},
		{tool: "get_broken_links", keys: []string{"success", "data", "errors", "warnings", "meta", "links", "broken_links", "total_pages"}},
		{tool: "get_backlinks", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "count", "backlinks"}},
		{tool: "suggest_links", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "total", "translations", "suggested_links"}},
		{tool: "diff_page", keys: []string{"success", "data", "errors", "warnings", "meta", "slug", "path", "status", "diff_available"}},
		{tool: "validate_frontmatter", keys: []string{"success", "data", "errors", "warnings", "meta", "pages", "pages_checked", "pages_passed", "invalid"}},
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

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
	}
	gotCats, ok := data["categories"].(float64)
	if !ok {
		t.Fatalf("explain_structure categories type = %T, value = %v", data["categories"], data["categories"])
	}
	if int(gotCats) != wantCats {
		t.Fatalf("explain_structure categories = %d, want %d (source index count)", int(gotCats), wantCats)
	}
}

func TestExplainSiteStructureRecentPagesUseSourceCategories(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
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

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
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

// TestExplainSiteStructureSectionsExcludeLanguagePrefix is a regression test
// for #459: a non-default-language page's slug carries its language route
// prefix (e.g. /en/posts/hello/ on an fr-default site), which must not be
// reported as if "en" were itself a content section.
func TestExplainSiteStructureSectionsExcludeLanguagePrefix(t *testing.T) {
	siteRoot := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(siteRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/bonjour/index.html", `<!doctype html><html lang="fr"><head>
<title>Bonjour</title>
<link rel="canonical" href="https://example.test/posts/bonjour/">
</head><body><article>Bonjour</article></body></html>`)
	write("en/posts/hello/index.html", `<!doctype html><html lang="en"><head>
<title>Hello</title>
<link rel="canonical" href="https://example.test/en/posts/hello/">
</head><body><article>Hello</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
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

	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
	}
	sections, ok := data["sections"].([]any)
	if !ok {
		t.Fatalf("explain_structure sections type = %T", data["sections"])
	}
	var sawPosts, sawEn bool
	var postsCount float64
	for _, raw := range sections {
		sec, _ := raw.(map[string]any)
		switch sec["name"] {
		case "posts":
			sawPosts = true
			postsCount, _ = sec["count"].(float64)
		case "en":
			sawEn = true
		}
	}
	if sawEn {
		t.Fatalf("explain_structure sections = %v, must not report the language prefix 'en' as a section", sections)
	}
	if !sawPosts {
		t.Fatalf("explain_structure sections = %v, want 'posts' counted for both the fr and en pages", sections)
	}
	if postsCount != 2 {
		t.Fatalf("explain_structure posts count = %v, want 2 (fr + en pages both merged under posts)", postsCount)
	}
	languages, _ := data["languages"].([]any)
	var sawEnLang bool
	for _, l := range languages {
		if l == "en" {
			sawEnLang = true
		}
	}
	if !sawEnLang {
		t.Fatalf("explain_structure languages = %v, want 'en' surfaced there instead", languages)
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

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
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

	res := callTool(t, session, "explain_structure", map[string]any{})
	if res.IsError {
		t.Fatalf("explain_structure returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("explain_structure data type = %T", m["data"])
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

	res := callTool(t, session, "validate_frontmatter", map[string]any{"limit": 1, "offset": 0})
	if res.IsError {
		t.Fatalf("validate_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_frontmatter data type = %T", m["data"])
	}
	if _, ok := data["pages_checked"]; !ok {
		t.Fatal("validate_frontmatter: pages_checked field missing (was 'total')")
	}
	if _, ok := data["pages_passed"]; !ok {
		t.Fatal("validate_frontmatter: pages_passed field missing (was 'valid')")
	}
	if _, ok := data["invalid"]; !ok {
		t.Fatal("validate_frontmatter: invalid field missing")
	}
	if _, ok := data["total"]; ok {
		t.Fatal("validate_frontmatter: old 'total' field must not be present")
	}
	if _, ok := data["valid"]; ok {
		t.Fatal("validate_frontmatter: old 'valid' field must not be present")
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

	res := callTool(t, session, "validate_frontmatter", map[string]any{})
	if res.IsError {
		t.Fatalf("validate_frontmatter returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("validate_frontmatter data type = %T", m["data"])
	}
	pages, ok := data["pages"].([]any)
	if !ok || len(pages) == 0 {
		t.Skip("no pages in validate_frontmatter output; cannot check DTO shape")
	}
	firstDTO, ok := pages[0].(map[string]any)
	if !ok {
		t.Fatalf("validate_frontmatter pages[0] type = %T", pages[0])
	}
	if _, ok := firstDTO["lang"]; !ok {
		t.Fatal("validate_frontmatter page DTO: 'lang' field missing")
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
