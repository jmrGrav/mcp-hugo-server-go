package anonymous

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestAnonymousHelperDTOBranches(t *testing.T) {
	page := site.Page{
		Slug:    "/posts/hello/",
		Title:   "Hello",
		Summary: "Summary",
		Date:    "2026-07-11",
		URL:     "https://example.test/posts/hello/",
		Lang:    "en",
		RawHTML: "<p>Hello</p>",
	}

	dto := toPageDTO(page)
	if dto.Tags == nil || dto.Categories == nil {
		t.Fatal("toPageDTO() should normalize nil slices to empty slices")
	}

	detail := toPageDetailDTO(page)
	if detail.TagTerms == nil || detail.CategoryTerms == nil {
		t.Fatal("toPageDetailDTO() should normalize terms slices")
	}
	if detail.HTML != "<p>Hello</p>" {
		t.Fatalf("toPageDetailDTO().HTML = %q", detail.HTML)
	}

	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Hello\ntags: [Ia]\ncategories: [Infrastructure]\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	enriched := toPageDTOsEnriched([]site.Page{page}, srcIdx, map[string]string{"ia": "ai"})
	if len(enriched) != 1 {
		t.Fatalf("toPageDTOsEnriched() len = %d", len(enriched))
	}
	if len(enriched[0].Categories) != 1 || enriched[0].Categories[0] != "Infrastructure" {
		t.Fatalf("toPageDTOsEnriched() categories = %#v", enriched[0].Categories)
	}

	resolvedPublic := toResolvedPageDetailDTO(site.ResolvedPage{
		Public: &page,
		Source: &hugosite.SourcePage{
			Slug:       "posts/hello",
			Tags:       []string{"Ia"},
			Categories: []string{"Infra"},
		},
	})
	if len(resolvedPublic.Tags) != 1 || resolvedPublic.Tags[0] != "Ia" {
		t.Fatalf("toResolvedPageDetailDTO(public+source) tags = %#v", resolvedPublic.Tags)
	}

	resolvedSource := toResolvedPageDetailDTO(site.ResolvedPage{
		Source: &hugosite.SourcePage{
			Slug:       "drafts/fresh",
			Title:      "Fresh",
			Body:       "Fresh body",
			Date:       "2026-07-11",
			Tags:       []string{"draft"},
			Categories: []string{"notes"},
		},
	})
	if resolvedSource.Slug != "/drafts/fresh/" || resolvedSource.HTML != "Fresh body" {
		t.Fatalf("toResolvedPageDetailDTO(source-only) = %#v", resolvedSource)
	}

	empty := toResolvedPageDetailDTO(site.ResolvedPage{})
	if empty.Slug != "" || empty.Title != "" || empty.HTML != "" || len(empty.Tags) != 0 || len(empty.Categories) != 0 {
		t.Fatalf("toResolvedPageDetailDTO(empty) = %#v", empty)
	}
}

func TestAnonymousScalarHelpers(t *testing.T) {
	if ptr := boolPtr(true); ptr == nil || !*ptr {
		t.Fatal("boolPtr(true) should return true pointer")
	}
	if got := clampLimit(0, 10, 50); got != 10 {
		t.Fatalf("clampLimit(default) = %d", got)
	}
	if got := clampLimit(100, 10, 50); got != 50 {
		t.Fatalf("clampLimit(max) = %d", got)
	}
	if got := clampLimit(20, 10, 50); got != 20 {
		t.Fatalf("clampLimit(pass-through) = %d", got)
	}
}
