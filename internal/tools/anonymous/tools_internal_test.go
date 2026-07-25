package anonymous

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 10 {
		t.Fatalf("Defs() = %d, want 10", len(defs))
	}
	if defs[0].RequiredScope != "" {
		t.Fatalf("Defs() first scope = %q, want empty", defs[0].RequiredScope)
	}
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
		"Infrastructure": "infrastructure",
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
