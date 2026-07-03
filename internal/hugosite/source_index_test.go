package hugosite_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func fixturesContentRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures", "content")
}

func TestNewSourceIndexEmpty(t *testing.T) {
	idx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	slugs := idx.AllSlugs()
	if len(slugs) != 0 {
		t.Fatalf("want 0 slugs, got %d", len(slugs))
	}
}

func TestNewSourceIndexInvalidRoot(t *testing.T) {
	_, err := hugosite.NewSourceIndex("/does/not/exist")
	if err == nil {
		t.Fatal("expected error for missing content root")
	}
}

func TestGetBySlug(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	page, ok := idx.GetBySlug("posts/hello")
	if !ok {
		t.Fatal("expected to find posts/hello")
	}
	if page.Title != "Hello World" {
		t.Fatalf("want title 'Hello World', got %q", page.Title)
	}
	if page.Date != "2024-01-15T10:00:00Z" {
		t.Fatalf("want date '2024-01-15T10:00:00Z', got %q", page.Date)
	}
	if page.Draft {
		t.Fatal("want draft=false")
	}
	if len(page.Tags) != 2 || page.Tags[0] != "go" || page.Tags[1] != "hugo" {
		t.Fatalf("unexpected tags: %v", page.Tags)
	}
	if len(page.Categories) != 1 || page.Categories[0] != "tutorials" {
		t.Fatalf("unexpected categories: %v", page.Categories)
	}
	if page.Body == "" {
		t.Fatal("want non-empty body")
	}
	if page.FrontmatterRaw == nil {
		t.Fatal("want non-nil FrontmatterRaw")
	}
}

func TestGetBySlugAbout(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	page, ok := idx.GetBySlug("about")
	if !ok {
		t.Fatal("expected to find about")
	}
	if page.Title != "About" {
		t.Fatalf("want title 'About', got %q", page.Title)
	}
	if !page.Draft {
		t.Fatal("want draft=true")
	}
}

func TestGetBySlugEmpty(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, ok := idx.GetBySlug("nonexistent/slug")
	if ok {
		t.Fatal("expected not found for nonexistent slug")
	}
}

func TestAllSlugs(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	slugs := idx.AllSlugs()
	if len(slugs) != 2 {
		t.Fatalf("want 2 slugs, got %d: %v", len(slugs), slugs)
	}
	slugSet := make(map[string]bool)
	for _, s := range slugs {
		slugSet[s] = true
	}
	if !slugSet["posts/hello"] {
		t.Errorf("missing slug 'posts/hello', got %v", slugs)
	}
	if !slugSet["about"] {
		t.Errorf("missing slug 'about', got %v", slugs)
	}
}

func TestListPages(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pages := idx.ListPages(10, 0)
	if len(pages) != 2 {
		t.Fatalf("want 2 pages, got %d", len(pages))
	}
}

func TestListPagesLimitOffset(t *testing.T) {
	root := fixturesContentRoot(t)
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pages := idx.ListPages(1, 0)
	if len(pages) != 1 {
		t.Fatalf("want 1 page, got %d", len(pages))
	}

	pages2 := idx.ListPages(10, 1)
	if len(pages2) != 1 {
		t.Fatalf("want 1 page at offset 1, got %d", len(pages2))
	}

	pages3 := idx.ListPages(10, 10)
	if len(pages3) != 0 {
		t.Fatalf("want 0 pages at offset 10, got %d", len(pages3))
	}
}
