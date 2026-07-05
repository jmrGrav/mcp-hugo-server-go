package read

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestContentHelperFunctions(t *testing.T) {
	pages := []site.Page{
		{Slug: "/posts/a/", Title: "Alpha", Summary: "first", Tags: []string{"go"}, Categories: []string{"docs"}, Date: "2026-07-03", URL: "https://example.test/posts/a/", Lang: "en"},
		{Slug: "/posts/b/", Title: "Beta", Summary: "second", Tags: []string{"mcp"}, Categories: []string{"docs"}, Date: "2026-07-04", URL: "https://example.test/posts/b/", Lang: "fr"},
		{Slug: "/about/", Title: "About", Summary: "third", Tags: []string{"go"}, Categories: []string{"pages"}, Date: "2026-07-02", URL: "https://example.test/about/", Lang: "en"},
	}

	if got := canonicalSort(""); got != "date" {
		t.Fatalf("canonicalSort(\"\") = %q", got)
	}
	if got := canonicalSort("title"); got != "title" {
		t.Fatalf("canonicalSort(title) = %q", got)
	}
	if got := canonicalOrder("ASC"); got != "asc" {
		t.Fatalf("canonicalOrder(ASC) = %q", got)
	}
	if got := effectiveSort(searchContentInput{Query: "alpha"}); got != "relevance" {
		t.Fatalf("effectiveSort(query) = %q", got)
	}

	filtered := filterContentPages(pages, searchContentInput{Query: "go", Type: "post", Order: "desc"})
	if len(filtered) != 1 || filtered[0].Slug != "/posts/a/" {
		t.Fatalf("filterContentPages() = %#v", filtered)
	}
	if !matchContentFilters(pages[0], searchContentInput{Tag: "go", Category: "docs", Language: "en", Type: "posts"}) {
		t.Fatal("matchContentFilters() should match expected page")
	}
	if matchContentFilters(pages[2], searchContentInput{Type: "posts"}) {
		t.Fatal("matchContentFilters() should reject non-post for posts filter")
	}

	sorted := append([]site.Page(nil), pages...)
	sortContentPages(sorted, searchContentInput{Sort: "title", Order: "asc"})
	if sorted[0].Slug != "/about/" || sorted[2].Slug != "/posts/b/" {
		t.Fatalf("sortContentPages(title asc) = %#v", sorted)
	}
	sorted = append([]site.Page(nil), pages...)
	sortContentPages(sorted, searchContentInput{Query: "go", Order: "desc"})
	if sorted[0].Slug != "/posts/a/" {
		t.Fatalf("sortContentPages(relevance) = %#v", sorted)
	}

	if got := sliceContentPages(pages, 1, 1); len(got) != 1 || got[0].Slug != "/posts/b/" {
		t.Fatalf("sliceContentPages() = %#v", got)
	}
	if got := sliceContentPages(pages, 10, 1); len(got) != 0 {
		t.Fatalf("sliceContentPages(offset overflow) = %#v", got)
	}

	dto := toPageDTO(pages[0])
	if dto.Slug != pages[0].Slug || dto.Title != "Alpha" {
		t.Fatalf("toPageDTO() = %#v", dto)
	}
	if got := toPageDTOs(pages); len(got) != 3 || got[1].Slug != "/posts/b/" {
		t.Fatalf("toPageDTOs() = %#v", got)
	}
	if got := countSections(pages); len(got) == 0 || got[0].Name == "" {
		t.Fatalf("countSections() = %#v", got)
	}
	if got := topSection("/posts/hello/"); got != "posts" {
		t.Fatalf("topSection(posts) = %q", got)
	}
	if got := topSection("/about/"); got != "about" {
		t.Fatalf("topSection(about) = %q", got)
	}
	if got := uniqueLanguages(pages); len(got) != 2 {
		t.Fatalf("uniqueLanguages() = %#v", got)
	}
}

func TestValidationHelpers(t *testing.T) {
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
	write("posts/a/index.md", "---\ntitle: Alpha\ndate: 2026-07-03\n---\nBody A\n")
	write("posts/b/index.md", "---\ndraft: true\n---\nBody B\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	if got := sourcePagesForValidation(src, "posts/a"); len(got) != 1 {
		t.Fatalf("sourcePagesForValidation(slug) = %#v", got)
	}
	if got := sourcePagesForValidation(src, ""); len(got) != 2 {
		t.Fatalf("sourcePagesForValidation(all) = %#v", got)
	}
	issues := validateFrontMatterPage(hugosite.SourcePage{Slug: "/broken/", FrontmatterRaw: map[string]any{}})
	if len(issues) < 2 {
		t.Fatalf("validateFrontMatterPage() = %#v", issues)
	}
	out := validatePagesWithIssues(src.ListPages(0, 0), 0, 1)
	if !out.Success || out.Data.Total != 2 || len(out.Data.Pages) != 1 {
		t.Fatalf("validatePagesWithIssues() = %#v", out)
	}
	health := buildSiteHealth(&site.Index{}, src)
	if health.SourcePages != 2 || health.DraftPages != 1 {
		t.Fatalf("buildSiteHealth() = %#v", health)
	}
}

func TestReadHelperBranches(t *testing.T) {
	if got := clampLimit(0, 10, 50); got != 10 {
		t.Fatalf("clampLimit(0) = %d", got)
	}
	if got := clampLimit(100, 10, 50); got != 50 {
		t.Fatalf("clampLimit(100) = %d", got)
	}
	if got := clampLimit(25, 10, 50); got != 25 {
		t.Fatalf("clampLimit(25) = %d", got)
	}
	if got := nullsafeStrings(nil); len(got) != 0 {
		t.Fatalf("nullsafeStrings(nil) = %#v", got)
	}
	if !sliceContains([]string{"go", "mcp"}, "go") || sliceContains([]string{"go"}, "rust") {
		t.Fatal("sliceContains() failed expected cases")
	}
	if got := readingTimeMinutes(""); got != 1 {
		t.Fatalf("readingTimeMinutes(empty) = %d", got)
	}
	if got := readingTimeMinutes(strings.Repeat("word ", 201)); got != 2 {
		t.Fatalf("readingTimeMinutes(201 words) = %d", got)
	}

	idx := &site.Index{}
	related := computeRelated(idx, site.Page{Slug: "/posts/a/", Tags: []string{"go"}, Categories: []string{"docs"}}, 5)
	if len(related) != 0 {
		t.Fatalf("computeRelated() = %#v", related)
	}
}

func TestDiffHelperBranches(t *testing.T) {
	if got := diffStatus(true, []byte("same"), []byte("same")); got != "unchanged" {
		t.Fatalf("diffStatus(unchanged) = %q", got)
	}
	if got := diffStatus(true, []byte("new"), []byte("old")); got != "modified" {
		t.Fatalf("diffStatus(modified) = %q", got)
	}
	if got := diffStatus(false, []byte{}, nil); got != "deleted" {
		t.Fatalf("diffStatus(deleted) = %q", got)
	}
	if got := diffStatus(false, []byte("new"), nil); got != "added" {
		t.Fatalf("diffStatus(added) = %q", got)
	}
	if !isGitPathMissing(errors.New("fatal: path 'content/posts/hello/index.md' exists on disk, but not in 'HEAD'")) {
		t.Fatal("isGitPathMissing() should detect git missing-path error text")
	}

	root := t.TempDir()

	if diff, err := unifiedDiff("posts/hello/index.md", []byte("one\n"), []byte("two\n")); err != nil || !strings.Contains(diff, "two") {
		t.Fatalf("unifiedDiff() = %q, %v", diff, err)
	}

	if out, err := gitBytes(context.Background(), root, "--version"); err != nil || !strings.Contains(string(out), "git version") {
		t.Fatalf("gitBytes() = %q, %v", out, err)
	}
}
