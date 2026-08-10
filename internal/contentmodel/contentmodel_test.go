package contentmodel

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestResolvePageSourceRejectsBlankSlug(t *testing.T) {
	root := t.TempDir()
	_, err := ResolvePageSource("   ", "", root)
	if err == nil || !strings.Contains(err.Error(), "slug_not_found") {
		t.Fatalf("ResolvePageSource(blank) error = %v, want slug_not_found", err)
	}
}

func TestResolvePageSourceLocalizedLeafExplicitLang(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "posts", "hello.fr.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("bonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ResolvePageSource("posts/hello", "fr", root)
	if err != nil {
		t.Fatalf("ResolvePageSource() error = %v", err)
	}
	if got.Lang != "fr" || got.SourcePath != full {
		t.Fatalf("ResolvePageSource() = %#v, want lang=fr path=%q", got, full)
	}
}

func TestResolvePageSourceExplicitLangNotFound(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "posts", "hello", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := ResolvePageSource("posts/hello", "fr", root)
	if err == nil || !strings.Contains(err.Error(), "source_file_not_found") {
		t.Fatalf("ResolvePageSource() error = %v, want source_file_not_found", err)
	}
}

func TestSourceRevisionBytesAndSourceRevision(t *testing.T) {
	raw := []byte("---\ntitle: demo\n---\nbody\n")
	sum := sha256.Sum256(raw)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got := SourceRevisionBytes(raw); got != want {
		t.Fatalf("SourceRevisionBytes() = %q, want %q", got, want)
	}

	path := filepath.Join(t.TempDir(), "index.md")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := SourceRevision(path)
	if err != nil {
		t.Fatalf("SourceRevision() error = %v", err)
	}
	if got != want {
		t.Fatalf("SourceRevision() = %q, want %q", got, want)
	}
}

func TestSourceRevisionReadFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")
	_, err := SourceRevision(missing)
	if err == nil || !strings.Contains(err.Error(), "read source for revision") {
		t.Fatalf("SourceRevision() error = %v, want wrapped read failure", err)
	}
}

func TestIsReservedTestSlug(t *testing.T) {
	tests := []struct {
		slug string
		want bool
	}{
		{slug: "posts/mcp-audit-20260725", want: true},
		{slug: "/posts/Test-Audit-0718", want: true},
		{slug: "documentation/CODEX-feature-probe", want: true},
		{slug: "posts/audit-securite-cloudflare", want: false},
		{slug: "posts/test-production-guidance", want: false},
	}

	for _, tt := range tests {
		if got := IsReservedTestSlug(tt.slug); got != tt.want {
			t.Fatalf("IsReservedTestSlug(%q) = %v, want %v", tt.slug, got, tt.want)
		}
	}
}

func TestExtractLang(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/tmp/posts/hello/index.fr.md", want: "fr"},
		{path: "/tmp/posts/hello/index.en-US.md", want: "en-US"},
		{path: "/tmp/posts/hello.md", want: ""},
		{path: "/tmp/posts/hello.fr.md", want: "fr"},
		{path: "/tmp/posts/hello", want: ""},
	}

	for _, tt := range tests {
		if got := extractLang(tt.path); got != tt.want {
			t.Fatalf("extractLang(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestBundleTranslations(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"index.md",
		"index.fr.md",
		"index.en.md",
		"cover.png",
		"notes.txt",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("Mkdir(assets) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "nested.md"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("WriteFile(nested) error = %v", err)
	}

	got, err := BundleTranslations(dir)
	if err != nil {
		t.Fatalf("BundleTranslations() error = %v", err)
	}
	want := map[string]string{
		"":   filepath.Join(dir, "index.md"),
		"fr": filepath.Join(dir, "index.fr.md"),
		"en": filepath.Join(dir, "index.en.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("BundleTranslations() len = %d, want %d; got=%#v", len(got), len(want), got)
	}
	for lang, path := range want {
		if got[lang] != path {
			t.Fatalf("BundleTranslations()[%q] = %q, want %q", lang, got[lang], path)
		}
	}
}

func TestBundleTranslationsReadFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := BundleTranslations(missing)
	if err == nil || !strings.Contains(err.Error(), "read bundle dir") {
		t.Fatalf("BundleTranslations() error = %v, want wrapped read bundle dir failure", err)
	}
}
