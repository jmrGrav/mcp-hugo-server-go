package hugosite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSourceIndexFrontmatterVariants(t *testing.T) {
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
	write("posts/time/index.md", "---\ntitle: 42\ndate: 2026-07-05T01:02:03Z\ndraft: true\ntags:\n  - go\n  - 7\ncategories:\n  - docs\n---\nBody\n")
	write("posts/plain.md", "No frontmatter\n")
	write("posts/toml/index.md", "+++\ntitle = \"42\"\ndate = \"2026-07-05T01:02:03Z\"\ndraft = true\ntags = [\"go\", \"7\"]\ncategories = [\"docs\"]\n+++\nBody\n")

	idx, err := NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	page, ok := idx.GetBySlug("posts/time")
	if !ok {
		t.Fatal("expected posts/time page")
	}
	if page.Title != "42" {
		t.Fatalf("Title = %q want 42", page.Title)
	}
	if page.Date != "2026-07-05T01:02:03Z" {
		t.Fatalf("Date = %q want RFC3339 string", page.Date)
	}
	if !page.Draft {
		t.Fatal("expected draft=true")
	}
	if len(page.Tags) != 2 || page.Tags[1] != "7" {
		t.Fatalf("Tags = %#v", page.Tags)
	}
	if len(page.Categories) != 1 || page.Categories[0] != "docs" {
		t.Fatalf("Categories = %#v", page.Categories)
	}
	if page2, ok := idx.GetBySlug("posts/plain"); !ok || page2.Title != "" {
		t.Fatalf("plain page = %#v, ok=%v", page2, ok)
	}

	tomlPage, ok := idx.GetBySlug("posts/toml")
	if !ok {
		t.Fatal("expected posts/toml page")
	}
	if tomlPage.Title != page.Title {
		t.Fatalf("TOML Title = %q, want same as YAML twin %q", tomlPage.Title, page.Title)
	}
	if tomlPage.Date != page.Date {
		t.Fatalf("TOML Date = %q, want same as YAML twin %q", tomlPage.Date, page.Date)
	}
	if !tomlPage.Draft {
		t.Fatal("expected TOML page draft=true")
	}
	if len(tomlPage.Tags) != 2 || tomlPage.Tags[1] != "7" {
		t.Fatalf("TOML Tags = %#v", tomlPage.Tags)
	}
	if len(tomlPage.Categories) != 1 || tomlPage.Categories[0] != "docs" {
		t.Fatalf("TOML Categories = %#v", tomlPage.Categories)
	}

	idx.Delete("missing")
	if _, ok := idx.GetBySlug("missing"); ok {
		t.Fatal("missing slug unexpectedly found")
	}
}

func TestSlugFromRelMultilingual(t *testing.T) {
	cases := []struct{ rel, want string }{
		{"posts/hello/index.md", "posts/hello"},
		{"posts/hello/index.en.md", "posts/hello"},
		{"posts/hello/index.fr.md", "posts/hello"},
		{"posts/hello/index.en-US.md", "posts/hello"},
		{"about/index.md", "about"},
		{"about/index.fr.md", "about"},
		{"posts/flat.md", "posts/flat"},
		{"posts/flat.en.md", "posts/flat.en"}, // flat files keep lang suffix
	}
	for _, c := range cases {
		got := SlugFromRel(c.rel)
		if got != c.want {
			t.Errorf("SlugFromRel(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestSplitFrontmatterFallbacks(t *testing.T) {
	fm, body := splitFrontmatter([]byte("plain body only"))
	if len(fm) != 0 || body != "plain body only" {
		t.Fatalf("splitFrontmatter(no fm) = %#v %q", fm, body)
	}
	fm, body = splitFrontmatter([]byte("---\ninvalid: [\n---\nbody\n"))
	if len(fm) != 0 || body != "body" {
		t.Fatalf("splitFrontmatter(invalid fm) = %#v %q", fm, body)
	}
	fm, body = splitFrontmatter([]byte("+++\ntitle = \"Hello\"\ndate = \"2026-01-01\"\n+++\nbody\n"))
	if fm["title"] != "Hello" || body != "body" {
		t.Fatalf("splitFrontmatter(toml fm) = %#v %q", fm, body)
	}
	fm, body = splitFrontmatter([]byte("+++\ninvalid = [\n+++\nbody\n"))
	if len(fm) != 0 || body != "body" {
		t.Fatalf("splitFrontmatter(invalid toml fm) = %#v %q", fm, body)
	}
	if got := stringVal(time.Date(2026, 7, 5, 1, 2, 3, 0, time.UTC)); got != "2026-07-05T01:02:03Z" {
		t.Fatalf("stringVal(time.Time) = %q", got)
	}
	if got := stringVal(123); got != "123" {
		t.Fatalf("stringVal(int) = %q", got)
	}
	if got := boolVal(true); !got {
		t.Fatal("boolVal(true) should be true")
	}
	if got := boolVal("nope"); got {
		t.Fatal("boolVal(non-bool) should be false")
	}
	if got := timeVal("2026-07-05T01:02:03Z"); got.Format(time.RFC3339) != "2026-07-05T01:02:03Z" {
		t.Fatalf("timeVal(RFC3339) = %v", got)
	}
	if got := timeVal("2026-07-05T01:02:03"); got.Format("2006-01-02T15:04:05") != "2026-07-05T01:02:03" {
		t.Fatalf("timeVal(no zone) = %v", got)
	}
	if got := timeVal("2026-07-05"); got.Format("2006-01-02") != "2026-07-05" {
		t.Fatalf("timeVal(date only) = %v", got)
	}
	if got := timeVal(""); !got.IsZero() {
		t.Fatalf("timeVal(empty) = %v, want zero", got)
	}
	if got := timeVal("not-a-date"); !got.IsZero() {
		t.Fatalf("timeVal(invalid) = %v, want zero", got)
	}
	if got := stringSlice([]any{"a", 1, true}); len(got) != 3 || got[1] != "1" {
		t.Fatalf("stringSlice([]any) = %#v", got)
	}
	if got := stringSlice(nil); len(got) != 0 {
		t.Fatalf("stringSlice(nil) = %#v", got)
	}
}
