package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"golang.org/x/net/html"
)

func TestSiteIndexHelpers(t *testing.T) {
	t.Run("slug and path helpers", func(t *testing.T) {
		if got := slugFromRel("posts/hello/index.html"); got != "/posts/hello/" {
			t.Fatalf("slugFromRel() = %q", got)
		}
		if got := slugFromRel("about.html"); got != "/about/" {
			t.Fatalf("slugFromRel() = %q", got)
		}
		if got := slugFromCanonical("https://example.test/posts/hello"); got != "/posts/hello/" {
			t.Fatalf("slugFromCanonical() = %q", got)
		}
		if got := normalizeSlug("posts/hello"); got != "/posts/hello/" {
			t.Fatalf("normalizeSlug() = %q", got)
		}
		if got := normalizeSlug(""); got != "/" {
			t.Fatalf("normalizeSlug(\"\") = %q", got)
		}
		if got := pathClean("posts//hello/../world"); got != "/posts/world" {
			t.Fatalf("pathClean() = %q", got)
		}
		if got := joinURL("https://example.test/", "/posts/hello/"); got != "https://example.test/posts/hello/" {
			t.Fatalf("joinURL() = %q", got)
		}
		if got := slugTitleFallback("/posts/my_first-post/"); got != "My first post" {
			t.Fatalf("slugTitleFallback() = %q", got)
		}
		if !isHTMLFile("index.HTM") || isHTMLFile("index.md") {
			t.Fatal("isHTMLFile() failed expected cases")
		}
		classifier := NewClassifier(nil)
		if !classifier.IsArticle(Page{Slug: "/posts/hello/"}) || classifier.IsArticle(Page{Slug: "/about/"}) {
			t.Fatal("ContentClassifier.IsArticle() failed expected cases")
		}
	})

	t.Run("string helpers", func(t *testing.T) {
		if got := firstNonEmptyStr(" ", "hello", "world"); got != "hello" {
			t.Fatalf("firstNonEmptyStr() = %q", got)
		}
		if got := firstNonZeroTime(time.Time{}, time.Unix(100, 0)); got.Unix() != 100 {
			t.Fatalf("firstNonZeroTime() = %v", got)
		}
		if got := taxonomy.DeduplicateRaw([]string{"Go", " go ", "", "Rust", "rust"}); len(got) != 2 || got[0] != "Go" || got[1] != "Rust" {
			t.Fatalf("DeduplicateRaw() = %#v", got)
		}
		if got := splitCSV("alpha, beta, ,gamma"); len(got) != 3 || got[1] != "beta" {
			t.Fatalf("splitCSV() = %#v", got)
		}
	})

	t.Run("HTML helpers", func(t *testing.T) {
		raw := []byte(`
<!doctype html>
<html lang="fr">
  <head>
    <title>Example title</title>
    <meta name="description" content="Example summary">
    <meta property="og:title" content="OG title">
    <meta property="og:description" content="OG summary">
    <meta property="article:section" content="News">
    <meta property="article:published_time" content="2026-07-04T06:00:00Z">
    <meta property="article:modified_time" content="2026-07-04">
    <meta property="article:tag" content="Go">
    <meta property="article:tag" content="go">
    <meta name="keywords" content="alpha, beta">
    <link rel="canonical" href="https://example.test/posts/hello/">
  </head>
  <body>
    <article>
      <h1>Body heading</h1>
      <p>Body text</p>
    </article>
  </body>
</html>`)
		pg, parsed, err := parseHTMLPage(raw, "posts/hello/index.html", time.Unix(0, 0), "https://example.test", "en")
		if err != nil {
			t.Fatalf("parseHTMLPage() error = %v", err)
		}
		if pg.Slug != "/posts/hello/" || pg.Title != "OG title" || pg.Summary != "OG summary" {
			t.Fatalf("parseHTMLPage() page = %#v", pg)
		}
		if pg.Lang != "fr" || pg.URL != "https://example.test/posts/hello/" {
			t.Fatalf("parseHTMLPage() lang/url = %#v", pg)
		}
		if len(pg.Tags) != 1 || pg.Tags[0] != "Go" {
			t.Fatalf("parseHTMLPage() tags = %#v", pg.Tags)
		}
		if len(pg.Categories) != 2 || pg.Categories[0] != "alpha" || pg.Categories[1] != "beta" {
			t.Fatalf("parseHTMLPage() categories = %#v", pg.Categories)
		}
		if parsed.IsZero() {
			t.Fatal("parseHTMLPage() parsed date should not be zero")
		}
		if body := bodyHTML(raw); !strings.Contains(body, "<h1>Body heading</h1>") {
			t.Fatalf("bodyHTML() = %q", body)
		}
		doc, err := htmlParseForTest(raw)
		if err != nil {
			t.Fatalf("html parse helper: %v", err)
		}
		meta := collectMeta(doc)
		if meta.title != "Example title" || meta.description != "Example summary" {
			t.Fatalf("collectMeta() = %#v", meta)
		}
		if nodeAttr(findElement(doc, "html"), "lang") != "fr" {
			t.Fatal("nodeAttr() did not read lang")
		}
		if txt := textContent(findElement(doc, "title")); txt != "Example title" {
			t.Fatalf("textContent() = %q", txt)
		}
	})

	t.Run("article section is not category fallback", func(t *testing.T) {
		raw := []byte(`
<!doctype html>
<html>
  <head>
    <title>Post section only</title>
    <meta property="article:section" content="posts">
    <link rel="canonical" href="https://example.test/posts/section-only/">
  </head>
  <body><article><p>Body</p></article></body>
</html>`)
		pg, _, err := parseHTMLPage(raw, "posts/section-only/index.html", time.Unix(0, 0), "https://example.test", "en")
		if err != nil {
			t.Fatalf("parseHTMLPage() error = %v", err)
		}
		if len(pg.Categories) != 0 {
			t.Fatalf("parseHTMLPage() categories = %#v, want none", pg.Categories)
		}
	})
}

func TestSiteIndexCollectionsAndBoundaries(t *testing.T) {
	idx := &Index{
		entries: []entry{
			{page: Page{Slug: "/posts/b/", Date: "2026-07-02", Tags: []string{"go"}, Categories: []string{"docs"}}},
			{page: Page{Slug: "/posts/a/", Date: "2026-07-03", Tags: []string{"mcp"}, Categories: []string{"docs"}}},
			{page: Page{Slug: "/about/", Date: "2026-07-01", Tags: []string{"about"}, Categories: []string{"pages"}}},
			{page: Page{Slug: "/posts/", Date: "2026-07-04"}},
			{page: Page{Slug: "/tags/go/", Date: "2026-07-05"}},
		},
		bySlug: map[string]int{
			"/posts/b/": 0,
			"/posts/a/": 1,
			"/about/":   2,
		},
		tags:       []string{"about", "go", "mcp"},
		categories: []string{"docs", "pages"},
		info:       map[string]string{"name": "example", "url": "https://example.test", "lang": "en"},
	}

	if got := idx.AllCategories(); len(got) != 2 || got[0] != "docs" || got[1] != "pages" {
		t.Fatalf("AllCategories() = %#v", got)
	}
	if got := idx.SiteInfo(); got["name"] != "example" {
		t.Fatalf("SiteInfo() = %#v", got)
	}
	if got := idx.GetFeed(2); len(got) != 2 {
		t.Fatalf("GetFeed() = %#v", got)
	}
	if got := idx.GetFeed(0); len(got) != 3 {
		t.Fatalf("GetFeed(0) should return all pages, got %#v", got)
	}
	if got := idx.RecentPosts(1); len(got) != 1 || got[0].Slug != "/posts/b/" {
		t.Fatalf("RecentPosts() = %#v", got)
	}
	if got := idx.Search("", 1); len(got) != 1 {
		t.Fatalf("Search() with empty query should still return ranked results, got %#v", got)
	}
	if got := idx.Sitemap(); len(got) != 5 {
		t.Fatalf("Sitemap() = %#v", got)
	}
}

func TestCanonicalDirAndHiddenPath(t *testing.T) {
	root := t.TempDir()
	if _, err := canonicalDir(root); err != nil {
		t.Fatalf("canonicalDir() error = %v", err)
	}
	if !isHiddenPath(".well-known/robots.txt") {
		t.Fatal("isHiddenPath() should detect hidden component")
	}
	if isHiddenPath("public/robots.txt") {
		t.Fatal("isHiddenPath() should not flag visible path")
	}

	symlink := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, symlink); err == nil {
		if _, err := canonicalDir(symlink); err == nil {
			t.Fatal("canonicalDir() should reject symlinks")
		}
	}
}

func TestNilIndexMethods(t *testing.T) {
	var idx *Index
	if got := idx.AllTags(); got != nil {
		t.Fatalf("AllTags(nil) = %#v", got)
	}
	if got := idx.AllCategories(); got != nil {
		t.Fatalf("AllCategories(nil) = %#v", got)
	}
	if got := idx.Sitemap(); got != nil {
		t.Fatalf("Sitemap(nil) = %#v", got)
	}
	if got := idx.GetFeed(5); got != nil {
		t.Fatalf("GetFeed(nil) = %#v", got)
	}
	if got := idx.RecentPosts(5); got != nil {
		t.Fatalf("RecentPosts(nil) = %#v", got)
	}
	if got := idx.Search("query", 5); got != nil {
		t.Fatalf("Search(nil) = %#v", got)
	}
	if got := idx.SiteInfo(); len(got) != 0 {
		t.Fatalf("SiteInfo(nil) = %#v", got)
	}
}

func TestIndexBoundariesAndDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example"
	cfg.DefaultLanguage = ""
	cfg.MaxIndexEntries = 0
	idx, err := NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	if got := idx.SiteInfo(); got["lang"] != "en" {
		t.Fatalf("SiteInfo().lang = %q want en", got["lang"])
	}

	custom := &Index{
		entries: []entry{
			{page: Page{Slug: "/posts/a/", Date: "2026-07-03"}},
			{page: Page{Slug: "/posts/b/", Date: "2026-07-04"}},
		},
		bySlug: map[string]int{"/posts/a/": 0, "/posts/b/": 1},
		info:   map[string]string{"name": "example", "url": "https://example.test", "lang": "en"},
	}
	if got := custom.GetFeed(-1); len(got) != 2 {
		t.Fatalf("GetFeed(-1) = %#v", got)
	}
	if got := custom.RecentPosts(0); len(got) != 2 {
		t.Fatalf("RecentPosts(0) = %#v", got)
	}
	if got := custom.Search("missing", 3); len(got) != 0 {
		t.Fatalf("Search(missing) = %#v", got)
	}
	if got := custom.Search("", 1); len(got) != 1 {
		t.Fatalf("Search(empty) = %#v", got)
	}
}

func htmlParseForTest(raw []byte) (*html.Node, error) {
	return html.Parse(strings.NewReader(string(raw)))
}

func TestBacklinkCache(t *testing.T) {
	// buildReverseMap with nil index returns empty map
	if got := buildReverseMap(nil); len(got) != 0 {
		t.Fatalf("buildReverseMap(nil) = %v", got)
	}

	// Build an index where page A links to page B
	pageA := Page{
		Slug:    "/posts/a/",
		Title:   "Page A",
		URL:     "https://example.test/posts/a/",
		RawHTML: `<article><a href="/posts/b/">go to B</a></article>`,
		Lang:    "en",
	}
	pageB := Page{
		Slug:    "/posts/b/",
		Title:   "Page B",
		URL:     "https://example.test/posts/b/",
		RawHTML: `<article><p>no outgoing links</p></article>`,
		Lang:    "en",
	}
	idx := &Index{
		entries: []entry{{page: pageA}, {page: pageB}},
		bySlug:  map[string]int{"/posts/a/": 0, "/posts/b/": 1},
		info:    map[string]string{"url": "https://example.test"},
	}
	idx.contentClassifier = NewClassifier(idx)

	// GetBacklinks on B should return A
	bls := idx.GetBacklinks("/posts/b/")
	if len(bls) != 1 || bls[0].FromSlug != "/posts/a/" {
		t.Fatalf("GetBacklinks(/posts/b/) = %#v", bls)
	}
	// GetBacklinks on A returns nothing (B does not link to A)
	if got := idx.GetBacklinks("/posts/a/"); len(got) != 0 {
		t.Fatalf("GetBacklinks(/posts/a/) = %#v", got)
	}
	// Unknown slug returns nil/empty
	if got := idx.GetBacklinks("/posts/missing/"); len(got) != 0 {
		t.Fatalf("GetBacklinks(missing) = %#v", got)
	}

	// invalidate clears the cache
	idx.blCache.invalidate()
	if idx.blCache.index != nil {
		t.Fatal("invalidate() did not nil the cache")
	}
	// rebuild on next call
	if bls2 := idx.GetBacklinks("/posts/b/"); len(bls2) != 1 {
		t.Fatalf("GetBacklinks after invalidate = %#v", bls2)
	}
}

func TestExtractLinksHTML(t *testing.T) {
	links := extractLinksHTML(`<a href="/posts/a/">link</a><a href="mailto:x@x.com">mail</a>`)
	if len(links) != 2 {
		t.Fatalf("extractLinksHTML() = %v", links)
	}
	if extractLinksHTML("") != nil {
		t.Fatal("extractLinksHTML(empty) should return nil")
	}
	if extractLinksHTML("<p>no links</p>") != nil {
		t.Fatal("extractLinksHTML(no links) should return nil")
	}
}
