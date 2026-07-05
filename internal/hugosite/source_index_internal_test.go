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

	idx.Delete("missing")
	if _, ok := idx.GetBySlug("missing"); ok {
		t.Fatal("missing slug unexpectedly found")
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
	if got := stringSlice([]any{"a", 1, true}); len(got) != 3 || got[1] != "1" {
		t.Fatalf("stringSlice([]any) = %#v", got)
	}
	if got := stringSlice(nil); len(got) != 0 {
		t.Fatalf("stringSlice(nil) = %#v", got)
	}
}
