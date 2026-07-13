package hugosite_test

import (
	"os"
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
	wantPath := filepath.Join(root, "posts", "hello.md")
	if page.FilePath != wantPath {
		t.Fatalf("FilePath = %q want %q", page.FilePath, wantPath)
	}
	if page.FrontmatterRaw == nil {
		t.Fatal("want non-nil FrontmatterRaw")
	}
}

func TestSlugFromRelHandlesBundlesAndMultilingualBundles(t *testing.T) {
	tests := []struct {
		rel  string
		want string
	}{
		{rel: "posts/hello/index.md", want: "posts/hello"},
		{rel: "posts/hello/index.fr.md", want: "posts/hello"},
		{rel: "posts/hello/index.en-US.md", want: "posts/hello"},
		{rel: "about.md", want: "about"},
	}
	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			if got := hugosite.SlugFromRel(tt.rel); got != tt.want {
				t.Fatalf("SlugFromRel(%q) = %q want %q", tt.rel, got, tt.want)
			}
		})
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

func TestSourceIndexTaxonomiesComeFromFrontmatter(t *testing.T) {
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
	write("posts/a/index.md", "---\ntitle: A\ntags: [go]\ncategories: [dev, security]\n---\nA\n")
	write("posts/b/index.md", "---\ntitle: B\ntags: [hugo, go]\ncategories: [security]\n---\nB\n")
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.AllTags(); len(got) != 2 || got[0] != "go" || got[1] != "hugo" {
		t.Fatalf("AllTags() = %#v", got)
	}
	if got := idx.AllCategories(); len(got) != 2 || got[0] != "dev" || got[1] != "security" {
		t.Fatalf("AllCategories() = %#v", got)
	}
}

// TestSourceIndexUpsertAndDelete verifies that mutations are reflected without
// restart (issue #35).
func TestSourceIndexUpsertAndDelete(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "fixtures", "content")
	idx, err := hugosite.NewSourceIndex(dir)
	if err != nil {
		t.Fatal(err)
	}

	newPage := hugosite.SourcePage{
		Slug:           "new-post",
		Title:          "New Post",
		Body:           "hello",
		FrontmatterRaw: map[string]any{"title": "New Post"},
	}
	idx.Upsert(newPage)

	got, ok := idx.GetBySlug("new-post")
	if !ok {
		t.Fatal("Upsert: page not found after insert")
	}
	if got.Title != "New Post" {
		t.Fatalf("Upsert: title = %q want New Post", got.Title)
	}

	updated := *got
	updated.Title = "Updated"
	idx.Upsert(updated)

	got2, ok := idx.GetBySlug("new-post")
	if !ok {
		t.Fatal("Upsert(update): page not found")
	}
	if got2.Title != "Updated" {
		t.Fatalf("Upsert(update): title = %q want Updated", got2.Title)
	}

	idx.Delete("new-post")
	if _, ok := idx.GetBySlug("new-post"); ok {
		t.Fatal("Delete: page still found after deletion")
	}
}

// TestSourceIndexUpsertCreateThenUpdate mimics the create->update multi-step
// workflow that used to fail when the index was never refreshed (issue #35).
func TestSourceIndexUpsertCreateThenUpdate(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "fixtures", "content")
	idx, err := hugosite.NewSourceIndex(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate create_page
	idx.Upsert(hugosite.SourcePage{Slug: "fresh", Title: "Fresh", FrontmatterRaw: map[string]any{"title": "Fresh"}})

	// Simulate update_page using same index (no restart)
	existing, ok := idx.GetBySlug("fresh")
	if !ok {
		t.Fatal("create->update: page not visible in index after create")
	}
	existing.Title = "Refreshed"
	idx.Upsert(*existing)

	got, _ := idx.GetBySlug("fresh")
	if got.Title != "Refreshed" {
		t.Fatalf("title = %q want Refreshed", got.Title)
	}
}

func TestSourceIndexMaintainsLanguageIndexesAcrossUpsertAndDelete(t *testing.T) {
	idx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	fr := hugosite.SourcePage{
		Slug:           "posts/hello",
		Lang:           "fr",
		Title:          "Bonjour",
		FilePath:       "/tmp/posts/hello/index.fr.md",
		Body:           "Bonjour FR",
		FrontmatterRaw: map[string]any{"title": "Bonjour"},
	}
	en := hugosite.SourcePage{
		Slug:           "posts/hello",
		Lang:           "en",
		Title:          "Hello",
		FilePath:       "/tmp/posts/hello/index.en.md",
		Body:           "Hello EN",
		FrontmatterRaw: map[string]any{"title": "Hello"},
	}

	idx.Upsert(fr)
	idx.Upsert(en)

	if got, ok := idx.GetBySlugLang("posts/hello", "fr"); !ok || got.FilePath != fr.FilePath {
		t.Fatalf("GetBySlugLang(fr) = %#v, %v", got, ok)
	}
	if got, ok := idx.GetBySlugLang("posts/hello", "en"); !ok || got.FilePath != en.FilePath {
		t.Fatalf("GetBySlugLang(en) = %#v, %v", got, ok)
	}

	enUpdated := en
	enUpdated.Title = "Hello Updated"
	enUpdated.Body = "Hello EN updated"
	idx.Upsert(enUpdated)

	if got, ok := idx.GetBySlugLang("posts/hello", "en"); !ok || got.Title != "Hello Updated" {
		t.Fatalf("GetBySlugLang(en updated) = %#v, %v", got, ok)
	}

	idx.Delete("posts/hello")
	if _, ok := idx.GetBySlug("posts/hello"); ok {
		t.Fatal("GetBySlug() should miss deleted multilingual slug")
	}
	if _, ok := idx.GetBySlugLang("posts/hello", "fr"); ok {
		t.Fatal("GetBySlugLang(fr) should miss deleted multilingual slug")
	}
	if _, ok := idx.GetBySlugLang("posts/hello", "en"); ok {
		t.Fatal("GetBySlugLang(en) should miss deleted multilingual slug")
	}
}
