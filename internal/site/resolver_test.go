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

func TestPageResolverResolveWithLangOverridesImplicitSlugLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "posts/hello/index.fr.md", "---\ntitle: Bonjour\n---\nBonjour FR\n")
	writeSourcePage(t, contentRoot, "posts/hello/index.en.md", "---\ntitle: Hello\n---\nHello EN\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &Index{
		entries: []entry{
			{page: Page{Slug: "/posts/hello/", Lang: "fr", Title: "Rendered FR", RawHTML: "<article>Rendered FR</article>"}},
			{page: Page{Slug: "/en/posts/hello/", Lang: "en", Title: "Rendered EN", RawHTML: "<article>Rendered EN</article>"}},
		},
		bySlug: map[string]int{"/posts/hello/": 0, "/en/posts/hello/": 1},
		info:   map[string]string{},
	}
	resolver := NewPageResolver(idx, srcIdx, config.Config{ContentRoot: contentRoot, DefaultLanguage: "fr"})

	got, ok := resolver.ResolveWithLang("/posts/hello/", "en")
	if !ok {
		t.Fatal("ResolveWithLang(/posts/hello/, en) not found")
	}
	if got.Source == nil || got.Source.Lang != "en" {
		t.Fatalf("ResolveWithLang(/posts/hello/, en).Source = %#v, want english source", got.Source)
	}
	if got.Public == nil || got.Public.Lang != "en" || got.Public.Slug != "/en/posts/hello/" {
		t.Fatalf("ResolveWithLang(/posts/hello/, en).Public = %#v, want english public page", got.Public)
	}
}

// TestPageResolverResolveWithLangRejectsContradictoryPublicOnlyLang guards
// against a cross-language leak in ResolveWithLang's resolvePublicForSourceLang
// branch: unlike its sibling branch (idx.GetBySlug(publicSlug) + explicit
// pageMatchesExplicitLang gate), that first branch used to accept whatever
// resolvePublicForSourceLang returned with no language check at all. For a
// public-only page (no source translation), canonicalPublicSlugForSourceLang
// doesn't strip a language prefix already present in the slug, so a
// self-contradictory request — an /en/ URL paired with lang="fr" — resolved
// straight through to the English page instead of correctly reporting
// not-found. Asking for a translation that doesn't exist must never silently
// return a different language's content.
func TestPageResolverResolveWithLangRejectsContradictoryPublicOnlyLang(t *testing.T) {
	contentRoot := t.TempDir()
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	idx := &Index{
		entries: []entry{{page: Page{Slug: "/en/posts/hello/", Lang: "en", Title: "Rendered EN", RawHTML: "<article>Rendered EN</article>"}}},
		bySlug:  map[string]int{"/en/posts/hello/": 0},
		info:    map[string]string{},
	}
	resolver := NewPageResolver(idx, srcIdx, config.Config{ContentRoot: contentRoot, DefaultLanguage: "fr"})

	got, ok := resolver.ResolveWithLang("/en/posts/hello/", "fr")
	if ok || got.Public != nil {
		t.Fatalf("ResolveWithLang(/en/posts/hello/, fr) = (%#v, %v), want not found — no fr translation exists, must not leak the en page", got, ok)
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

func TestPageResolverPrefersDefaultLanguageForSourceOnlyMultilingualPage(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourcePage(t, contentRoot, "posts/hello/index.fr.md", "---\ntitle: Bonjour\n---\nBonjour FR\n")
	writeSourcePage(t, contentRoot, "posts/hello/index.en.md", "---\ntitle: Hello\n---\nHello EN\n")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	resolver := NewPageResolver(&Index{bySlug: map[string]int{}}, srcIdx, config.Config{
		ContentRoot:      contentRoot,
		DefaultLanguage:  "fr",
		RejectSymlinks:   true,
		RejectHiddenPath: true,
	})

	got, ok := resolver.Resolve("/posts/hello/")
	if !ok {
		t.Fatal("Resolve(source-only bilingual) not found")
	}
	if got.Public != nil {
		t.Fatalf("Resolve(source-only bilingual).Public = %#v want nil", got.Public)
	}
	if got.Source == nil {
		t.Fatal("Resolve(source-only bilingual).Source = nil, want source page")
	}
	if got.Source.Lang != "fr" {
		t.Fatalf("Resolve(source-only bilingual).Source.Lang = %q, want fr", got.Source.Lang)
	}
	if got.Source.Body != "Bonjour FR" {
		t.Fatalf("Resolve(source-only bilingual).Source.Body = %q, want Bonjour FR", got.Source.Body)
	}
	wantPath := filepath.Join(contentRoot, "posts", "hello", "index.fr.md")
	if got.SourcePath != wantPath {
		t.Fatalf("Resolve(source-only bilingual).SourcePath = %q want %q", got.SourcePath, wantPath)
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
