package site

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestPageResolverResolvesPublicAndSourceSlugs(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "posts/hello/index.fr.md", "---\ntitle: Bonjour\n---\n# Bonjour\n\nSource body\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &Index{
		entries: []entry{{page: Page{Slug: "/posts/hello/", Title: "Rendered", RawHTML: "<main>Rendered</main>"}}},
		bySlug:  map[string]int{"/posts/hello/": 0},
		info:    map[string]string{},
	}
	resolver := NewPageResolver(idx, srcIdx, config.Config{ContentRoot: contentRoot})

	for _, raw := range []string{"/posts/hello/", "/posts/hello", "posts/hello"} {
		t.Run(raw, func(t *testing.T) {
			got, ok := resolver.Resolve(raw)
			if !ok {
				t.Fatalf("Resolve(%q) not found", raw)
			}
			if got.Public == nil || got.Public.Title != "Rendered" {
				t.Fatalf("Resolve(%q).Public = %#v", raw, got.Public)
			}
			if got.Source == nil || got.Source.Body != "# Bonjour\n\nSource body" {
				t.Fatalf("Resolve(%q).Source = %#v", raw, got.Source)
			}
			wantPath := filepath.Join(contentRoot, "posts", "hello", "index.fr.md")
			if got.SourcePath != wantPath {
				t.Fatalf("Resolve(%q).SourcePath = %q want %q", raw, got.SourcePath, wantPath)
			}
		})
	}
}

func TestPageResolverResolvesLanguagePrefixedPublicSlugToSource(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "posts/hello/index.md", "---\ntitle: Hello\n---\nClean source body\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &Index{
		entries: []entry{{page: Page{Slug: "/en/posts/hello/", Title: "Rendered", RawHTML: "<nav>Share</nav><article>Rendered</article>"}}},
		bySlug:  map[string]int{"/en/posts/hello/": 0},
		info:    map[string]string{},
	}
	resolver := NewPageResolver(idx, srcIdx, config.Config{ContentRoot: contentRoot})

	got, ok := resolver.Resolve("/en/posts/hello/")
	if !ok {
		t.Fatal("Resolve(language-prefixed public slug) not found")
	}
	if got.Public == nil || got.Public.Title != "Rendered" {
		t.Fatalf("Resolve(language-prefixed).Public = %#v", got.Public)
	}
	if got.Source == nil || got.Source.Body != "Clean source body" {
		t.Fatalf("Resolve(language-prefixed).Source = %#v", got.Source)
	}
}

func TestPageResolverPrefersMatchingLanguageVariant(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "posts/hello/index.fr.md", "---\ntitle: Bonjour\n---\nBonjour FR\n")
	writeSourcePage(t, contentRoot, "posts/hello/index.en.md", "---\ntitle: Hello\n---\nHello EN\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &Index{
		entries: []entry{{page: Page{Slug: "/en/posts/hello/", Lang: "en", Title: "Rendered EN", RawHTML: "<article>Rendered EN</article>"}}},
		bySlug:  map[string]int{"/en/posts/hello/": 0},
		info:    map[string]string{},
	}
	resolver := NewPageResolver(idx, srcIdx, config.Config{ContentRoot: contentRoot})

	got, ok := resolver.Resolve("/en/posts/hello/")
	if !ok {
		t.Fatal("Resolve(english public slug) not found")
	}
	if got.Source == nil {
		t.Fatal("Resolve(english public slug).Source = nil, want source page")
	}
	if got.Source.Lang != "en" {
		t.Fatalf("Resolve(english public slug).Source.Lang = %q, want en", got.Source.Lang)
	}
	if got.Source.Body != "Hello EN" {
		t.Fatalf("Resolve(english public slug).Source.Body = %q, want Hello EN", got.Source.Body)
	}
	wantPath := filepath.Join(contentRoot, "posts", "hello", "index.en.md")
	if got.SourcePath != wantPath {
		t.Fatalf("Resolve(english public slug).SourcePath = %q want %q", got.SourcePath, wantPath)
	}
}

func TestPageResolverResolvesSourceOnlyPageAfterCreateWithoutBuild(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "drafts/fresh/index.md", "---\ntitle: Fresh\n---\nFresh source body\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	resolver := NewPageResolver(&Index{bySlug: map[string]int{}}, srcIdx, config.Config{ContentRoot: contentRoot})

	got, ok := resolver.Resolve("/drafts/fresh/")
	if !ok {
		t.Fatal("Resolve(source-only) not found")
	}
	if got.Public != nil {
		t.Fatalf("Resolve(source-only).Public = %#v want nil", got.Public)
	}
	if got.Source == nil || got.Source.Title != "Fresh" || got.Source.Body != "Fresh source body" {
		t.Fatalf("Resolve(source-only).Source = %#v", got.Source)
	}
}

func TestPageResolverResolvesPublicOnlyPageWithHTMLFallback(t *testing.T) {
	idx := &Index{
		entries: []entry{{page: Page{Slug: "/generated/", Title: "Generated", RawHTML: "<main>Generated only</main>"}}},
		bySlug:  map[string]int{"/generated/": 0},
		info:    map[string]string{},
	}
	resolver := NewPageResolver(idx, nil, config.Config{})

	got, ok := resolver.Resolve("generated")
	if !ok {
		t.Fatal("Resolve(public-only) not found")
	}
	if got.Public == nil || got.Public.Title != "Generated" {
		t.Fatalf("Resolve(public-only).Public = %#v", got.Public)
	}
	if got.Source != nil || got.SourcePath != "" {
		t.Fatalf("Resolve(public-only) source = %#v path=%q, want nil source", got.Source, got.SourcePath)
	}
}

func writeSourcePage(t *testing.T, root, rel, raw string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
