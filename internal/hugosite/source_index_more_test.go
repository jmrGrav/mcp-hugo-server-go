package hugosite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceIndexTaxonomyAndLangHelpers(t *testing.T) {
	idx := &SourceIndex{
		pages: []SourcePage{
			{Slug: "posts/a", Tags: []string{"Go", "AI"}, Categories: []string{"Infra", "Docs"}},
			{Slug: "posts/b", Tags: []string{"go", "Ia"}, Categories: []string{"infra"}},
		},
		bySlug: map[string]int{"posts/a": 0, "posts/b": 1},
	}

	tags := idx.AllTags()
	if len(tags) == 0 {
		t.Fatal("AllTags() should not be empty")
	}
	categories := idx.AllCategories()
	if len(categories) == 0 {
		t.Fatal("AllCategories() should not be empty")
	}

	idx.Delete("posts/a")
	if _, ok := idx.GetBySlug("posts/a"); ok {
		t.Fatal("Delete(existing) should remove slug")
	}
	if got, ok := idx.GetBySlug("posts/b"); !ok || got.Slug != "posts/b" {
		t.Fatalf("remaining page = %#v ok=%v", got, ok)
	}

	cases := map[string]string{
		"posts/a/index.fr.md":    "fr",
		"posts/a/index.en-US.md": "en-US",
		"posts/a/index.md":       "",
		"posts/a/flat.en.md":     "",
		// Hugo section-index files (#457): must resolve the same as bundle
		// index.<lang>.md, at any depth including content root (homepage).
		"_index.en.md":       "en",
		"_index.fr.md":       "fr",
		"_index.md":          "",
		"posts/_index.en.md": "en",
		"posts/_index.md":    "",
	}
	for rel, want := range cases {
		if got := langFromRel(rel); got != want {
			t.Fatalf("langFromRel(%q) = %q want %q", rel, got, want)
		}
	}

	if got := stringSlice([]string(nil)); len(got) != 0 {
		t.Fatalf("stringSlice([]string(nil)) = %#v", got)
	}
	if got := stringSlice([]string{"a", "b"}); len(got) != 2 || got[1] != "b" {
		t.Fatalf("stringSlice([]string) = %#v", got)
	}
}

func TestSourceIndexDefaultAndDeleteLangHelpers(t *testing.T) {
	idx := &SourceIndex{
		pages: []SourcePage{
			{Slug: "posts/demo", Lang: "", FilePath: "index.md"},
			{Slug: "posts/demo", Lang: "fr", FilePath: "index.fr.md"},
			{Slug: "posts/demo", Lang: "en", FilePath: "index.en.md"},
		},
	}
	idx.rebuildMaps()

	page, ok := idx.GetDefaultBySlug("posts/demo")
	if !ok || page.FilePath != "index.md" {
		t.Fatalf("GetDefaultBySlug() = %#v, %v want default-language entry", page, ok)
	}

	idx.DeleteLang("posts/demo", "fr")
	if _, ok := idx.GetBySlugLang("posts/demo", "fr"); ok {
		t.Fatal("DeleteLang(fr) should remove only the requested language")
	}
	if _, ok := idx.GetBySlugLang("posts/demo", "en"); !ok {
		t.Fatal("DeleteLang(fr) should preserve en entry")
	}
	if _, ok := idx.GetDefaultBySlug("posts/demo"); !ok {
		t.Fatal("DeleteLang(fr) should preserve default entry")
	}
}

func TestParseFrontmatterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archetypes", "posts.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: Demo\ndraft: true\ntags:\n  - Hugo\n---\nBody ignored\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fm, err := ParseFrontmatterFile(path)
	if err != nil {
		t.Fatalf("ParseFrontmatterFile() error = %v", err)
	}
	if got := fm["title"]; got != "Demo" {
		t.Fatalf("frontmatter title = %v, want Demo", got)
	}
	if got := fm["draft"]; got != true {
		t.Fatalf("frontmatter draft = %v, want true", got)
	}
	tags, ok := fm["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "Hugo" {
		t.Fatalf("frontmatter tags = %#v, want one Hugo tag", fm["tags"])
	}
}
