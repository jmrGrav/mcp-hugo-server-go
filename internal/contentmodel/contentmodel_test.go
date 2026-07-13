package contentmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePageSourceBundleDefault(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ResolvePageSource("/posts/hello/", "", root)
	if err != nil {
		t.Fatalf("ResolvePageSource() error = %v", err)
	}
	if got.Slug != "posts/hello" {
		t.Fatalf("ResolvePageSource().Slug = %q, want posts/hello", got.Slug)
	}
	if got.Lang != "" {
		t.Fatalf("ResolvePageSource().Lang = %q, want empty", got.Lang)
	}
	if got.SourcePath != full {
		t.Fatalf("ResolvePageSource().SourcePath = %q, want %q", got.SourcePath, full)
	}
}

func TestResolvePageSourceBundleExplicitLang(t *testing.T) {
	root := t.TempDir()
	defaultPath := filepath.Join(root, "posts", "hello", "index.md")
	full := filepath.Join(root, "posts", "hello", "index.fr.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(defaultPath, []byte("default"), 0o644); err != nil {
		t.Fatalf("WriteFile(default) error = %v", err)
	}
	if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ResolvePageSource("posts/hello", "fr", root)
	if err != nil {
		t.Fatalf("ResolvePageSource() error = %v", err)
	}
	if got.Lang != "fr" {
		t.Fatalf("ResolvePageSource().Lang = %q, want fr", got.Lang)
	}
	if got.SourcePath != full {
		t.Fatalf("ResolvePageSource().SourcePath = %q, want %q", got.SourcePath, full)
	}
}

func TestResolvePageSourceAmbiguousDefaultAndLocalized(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join("posts", "hello", "index.md"),
		filepath.Join("posts", "hello", "index.fr.md"),
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	_, err := ResolvePageSource("posts/hello", "", root)
	if err == nil || !strings.Contains(err.Error(), "ambiguous_language") {
		t.Fatalf("ResolvePageSource() error = %v, want ambiguous_language", err)
	}
}

func TestResolvePageSourceAmbiguousLanguage(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join("posts", "hello", "index.fr.md"),
		filepath.Join("posts", "hello", "index.en.md"),
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	_, err := ResolvePageSource("posts/hello", "", root)
	if err == nil || !strings.Contains(err.Error(), "ambiguous_language") {
		t.Fatalf("ResolvePageSource() error = %v, want ambiguous_language", err)
	}
}

func TestResolvePageSourceLeafMarkdown(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "posts", "hello.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ResolvePageSource("/posts/hello/", "", root)
	if err != nil {
		t.Fatalf("ResolvePageSource() error = %v", err)
	}
	if got.SourcePath != full {
		t.Fatalf("ResolvePageSource().SourcePath = %q, want %q", got.SourcePath, full)
	}
}

func TestResolvePageSourceRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "outside.md")
	if err := os.WriteFile(outside, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := ResolvePageSource("../outside", "", root)
	if err == nil || !strings.Contains(err.Error(), "invalid_slug") {
		t.Fatalf("ResolvePageSource() error = %v, want invalid_slug", err)
	}
}
