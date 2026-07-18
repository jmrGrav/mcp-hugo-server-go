package hugosite

import "testing"

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
