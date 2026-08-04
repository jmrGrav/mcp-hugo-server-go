package anonymous

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 11 {
		t.Fatalf("Defs() = %d, want 11", len(defs))
	}
	if defs[0].RequiredScope != "" {
		t.Fatalf("Defs() first scope = %q, want empty", defs[0].RequiredScope)
	}
}

func TestRegisterPublishesExpectedToolCatalog(t *testing.T) {
	idx := mustAnonymousIndex(t)
	srcIdx := mustAnonymousSourceIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newAnonymousSession(t, idx, cfg, srcIdx)
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
		"get_changelog",
		"list_pages",
		"get_page",
		"search_pages",
		"get_recent_posts",
		"list_tags",
		"list_categories",
		"get_sitemap",
		"get_feed",
		"get_site_information",
		"get_capabilities",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("anonymous tools/list = %v, want %v", got, want)
	}
}

func mustAnonymousIndex(t *testing.T) *site.Index {
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

func mustAnonymousSourceIndex(t *testing.T) *hugosite.SourceIndex {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "content")
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	return idx
}

func newAnonymousSession(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	Register(s, idx, cfg, srcIdx)

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

func TestIsTaxonomyURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"/tags/hugo/", true},
		{"/tags/", true},
		{"/categories/infrastructure/", true},
		{"/categories/", true},
		{"/authors/jm/", true},
		{"/en/tags/hugo/", true},
		{"/fr/categories/infrastructure/", true},
		{"https://example.test/tags/hugo/", true},
		{"https://example.test/categories/infrastructure/", true},
		{"https://example.test/en/tags/hugo/", true},
		{"https://example.test/fr/categories/infrastructure/", true},
		{"/posts/tags/hugo/", false},
		{"/docs/categories/api/", false},
		{"/blog/authors/jm/", false},
		{"/posts/my-article/", false},
		{"/", false},
		{"/about/", false},
		{"/tagsnot/", false},
	}
	for _, c := range cases {
		if got := isTaxonomyURL(c.url); got != c.want {
			t.Errorf("isTaxonomyURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestToPageDTOsForProfileReaderSafeVsEnriched(t *testing.T) {
	pages := []site.Page{
		{
			Slug:       "/posts/demo/",
			Title:      "Demo",
			Tags:       []string{"Infrastructure"},
			Categories: []string{"public-category"},
			URL:        "https://example.test/posts/demo/",
			Lang:       "fr",
		},
	}
	srcIdx := &hugosite.SourceIndex{}
	srcIdx.Upsert(hugosite.SourcePage{
		Slug:       "posts/demo",
		Lang:       "fr",
		Categories: []string{"source-category"},
	})
	aliases := taxonomy.NormalizeAliasMap(map[string]string{
		"Infrastructure":  "infrastructure",
		"source-category": "normalized-category",
	})

	readerSafe := toPageDTOsForProfile(pages, srcIdx, aliases, true)
	if len(readerSafe) != 1 {
		t.Fatalf("readerSafe len = %d, want 1", len(readerSafe))
	}
	if readerSafe[0].SourceKey != "" {
		t.Fatalf("readerSafe SourceKey = %q, want empty", readerSafe[0].SourceKey)
	}
	if readerSafe[0].Categories[0] != "public-category" {
		t.Fatalf("readerSafe Categories = %#v, want public categories preserved", readerSafe[0].Categories)
	}
	if readerSafe[0].Tags[0] != "Infrastructure" {
		t.Fatalf("readerSafe Tags = %#v, want original tags preserved when no effective alias exists", readerSafe[0].Tags)
	}

	enriched := toPageDTOsForProfile(pages, srcIdx, aliases, false)
	if len(enriched) != 1 {
		t.Fatalf("enriched len = %d, want 1", len(enriched))
	}
	if enriched[0].SourceKey != "posts/demo" {
		t.Fatalf("enriched SourceKey = %q, want posts/demo", enriched[0].SourceKey)
	}
	if enriched[0].Categories[0] != "normalized-category" {
		t.Fatalf("enriched Categories = %#v, want source categories with aliases applied", enriched[0].Categories)
	}
}
