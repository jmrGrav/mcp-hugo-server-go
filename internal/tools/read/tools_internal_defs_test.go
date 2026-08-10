package read

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 21 {
		t.Fatalf("Defs() = %d, want 21", len(defs))
	}
	if defs[0].RequiredScope != "" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, nil, config.Default())
	RegisterWithSourceIndex(nil, nil, nil, config.Default())
}

func TestRegisterPublishesExpectedBaseToolCatalog(t *testing.T) {
	idx := mustReadIndex(t)
	srcIdx := mustReadSourceIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newReadSession(t, idx, cfg, srcIdx, false)
	defer done()

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	want := []string{
		"get_page_markdown",
		"get_page_frontmatter",
		"get_related_content",
		"build_agent_context",
		"export_agent_context",
		"get_page_for_edit",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("read Register tools/list = %v, want %v", got, want)
	}
}

func TestRegisterWithSourceIndexPublishesExpectedExtendedToolCatalog(t *testing.T) {
	idx := mustReadIndex(t)
	srcIdx := mustReadSourceIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newReadSession(t, idx, cfg, srcIdx, true)
	defer done()

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	want := []string{
		"get_page_markdown",
		"get_page_frontmatter",
		"get_related_content",
		"build_agent_context",
		"export_agent_context",
		"get_page_for_edit",
		"diff_page",
		"inspect_rendered",
		"list_content_types",
		"list_page_assets",
		"check_ai_readiness",
		"plan_page",
		"list_page_revisions",
		"search_content",
		"explain_structure",
		"get_site_health",
		"validate_frontmatter",
		"validate_site",
		"get_broken_links",
		"get_backlinks",
		"suggest_links",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("read full tools/list = %v, want %v", got, want)
	}
}

func mustReadIndex(t *testing.T) *site.Index {
	t.Helper()
	root := filepath.Join("testdata", "public", "minimal")
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

func mustReadSourceIndex(t *testing.T) *hugosite.SourceIndex {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "content")
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	return idx
}

func newReadSession(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex, withSourceTools bool) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	Register(s, idx, cfg, srcIdx)
	if withSourceTools {
		RegisterWithSourceIndex(s, idx, srcIdx, cfg)
	}

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

func TestSourcePageAsPublic(t *testing.T) {
	if got := sourcePageAsPublic(nil); got.Slug != "" {
		t.Fatalf("sourcePageAsPublic(nil) = %#v", got)
	}
	src := &hugosite.SourcePage{
		Slug:       "posts/hello",
		Title:      "Hello",
		Date:       "2026-07-11",
		Tags:       []string{"go"},
		Categories: []string{"blog"},
		Lang:       "en",
	}
	got := sourcePageAsPublic(src)
	if got.Slug != "/posts/hello/" || got.Title != "Hello" || len(got.Tags) != 1 || got.Lang != "en" {
		t.Fatalf("sourcePageAsPublic() = %#v", got)
	}
}
