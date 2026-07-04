package site

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func minimalCfg(root string) config.Config {
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	return cfg
}

func mustNewIndex(t *testing.T) *Index {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	return idx
}

func TestNewIndexEmpty(t *testing.T) {
	root := t.TempDir()
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	if len(idx.Sitemap()) != 0 {
		t.Fatalf("expected 0 pages, got %d", len(idx.Sitemap()))
	}
}

func TestSearchPages(t *testing.T) {
	idx := mustNewIndex(t)

	got := idx.Search("security", 10)
	if len(got) == 0 {
		t.Fatal("Search('security') returned no results")
	}
	if got[0].Slug != "/posts/bonjour/" {
		t.Fatalf("Search('security') top slug = %q want /posts/bonjour/", got[0].Slug)
	}

	got2 := idx.Search("english", 10)
	if len(got2) == 0 {
		t.Fatal("Search('english') returned no results")
	}
	found := false
	for _, p := range got2 {
		if strings.Contains(p.Slug, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Search('english') did not find hello page")
	}
}

func TestGetBySlug(t *testing.T) {
	idx := mustNewIndex(t)

	p, ok := idx.GetBySlug("/posts/hello")
	if !ok {
		t.Fatal("GetBySlug('/posts/hello') not found")
	}
	if p.Lang != "en" {
		t.Fatalf("GetBySlug() lang = %q want en", p.Lang)
	}
	if p.URL != "https://example.test/posts/hello/" {
		t.Fatalf("GetBySlug() URL = %q", p.URL)
	}

	_, ok2 := idx.GetBySlug("/posts/does-not-exist")
	if ok2 {
		t.Fatal("GetBySlug() should not find missing slug")
	}
}

func TestRecentPosts(t *testing.T) {
	idx := mustNewIndex(t)

	posts := idx.RecentPosts(5)
	if len(posts) < 1 {
		t.Fatal("RecentPosts() returned no posts")
	}
	if posts[0].Slug != "/posts/hello/" {
		t.Fatalf("RecentPosts() first = %q want /posts/hello/", posts[0].Slug)
	}
	for i := 1; i < len(posts); i++ {
		if posts[i-1].Date < posts[i].Date {
			t.Fatalf("RecentPosts() not sorted by date desc")
		}
	}
}

func TestAllTags(t *testing.T) {
	idx := mustNewIndex(t)
	tags := idx.AllTags()
	if len(tags) == 0 {
		t.Fatal("AllTags() returned empty slice")
	}
	if !sort.StringsAreSorted(tags) {
		t.Fatalf("AllTags() not sorted: %v", tags)
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if seen[tag] {
			t.Fatalf("AllTags() duplicate: %q", tag)
		}
		seen[tag] = true
	}
}

func TestGetBySlugEmptySlug(t *testing.T) {
	idx := mustNewIndex(t)
	_, ok := idx.GetBySlug("")
	if ok {
		t.Fatal("GetBySlug('') should return not found")
	}
}
