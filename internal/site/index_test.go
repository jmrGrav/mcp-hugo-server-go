package site

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func minimalCfg(root string) config.Config {
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	return cfg
}

func mustNewIndex(t *testing.T) *Index {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	return idx
}

func TestNewIndexEmpty(t *testing.T) {
	root := t.TempDir()
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	if len(idx.Sitemap()) != 0 {
		t.Fatalf("expected 0 pages, got %d", len(idx.Sitemap()))
	}
}

// TestWarnIfSiteRootLooksLikeProjectRoot is a regression test for a real,
// reproduced bug: pointing site_root at a Hugo project root (rather than the
// build-output directory) causes vendored theme .html layout templates under
// themes/ to be walked and misparsed as content pages, silently corrupting
// published-page counts. NewIndex should now log a warning in that case, and
// stay silent for a genuine build-output directory.
func TestWarnIfSiteRootLooksLikeProjectRoot(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevLogger)

	t.Run("project root with themes/ triggers warning", func(t *testing.T) {
		buf.Reset()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "themes"), 0o755); err != nil {
			t.Fatalf("Mkdir themes: %v", err)
		}
		if _, err := NewIndex(minimalCfg(root)); err != nil {
			t.Fatalf("NewIndex() error = %v", err)
		}
		if !strings.Contains(buf.String(), "site_root looks like a Hugo project root") {
			t.Fatalf("expected project-root warning, got log: %s", buf.String())
		}
	})

	t.Run("project root with hugo.toml triggers warning", func(t *testing.T) {
		buf.Reset()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "hugo.toml"), []byte("baseURL = \"https://example.test\"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile hugo.toml: %v", err)
		}
		if _, err := NewIndex(minimalCfg(root)); err != nil {
			t.Fatalf("NewIndex() error = %v", err)
		}
		if !strings.Contains(buf.String(), "site_root looks like a Hugo project root") {
			t.Fatalf("expected project-root warning, got log: %s", buf.String())
		}
	})

	t.Run("genuine build-output directory stays silent", func(t *testing.T) {
		buf.Reset()
		root := filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
		if _, err := NewIndex(minimalCfg(root)); err != nil {
			t.Fatalf("NewIndex() error = %v", err)
		}
		if strings.Contains(buf.String(), "site_root looks like a Hugo project root") {
			t.Fatalf("unexpected project-root warning for a real build-output dir: %s", buf.String())
		}
	})
}

func TestSearchPages(t *testing.T) {
	idx := mustNewIndex(t)

	got := idx.Search("security", 10)
	if len(got) == 0 {
		t.Fatal("Search('security') returned no results")
	}
	if got[0].Slug != "/posts/bonjour/" {
		t.Fatalf("Search('security') top slug = %q want /posts/bonjour/", got[0].Slug)
	}

	got2 := idx.Search("english", 10)
	if len(got2) == 0 {
		t.Fatal("Search('english') returned no results")
	}
	found := false
	for _, p := range got2 {
		if strings.Contains(p.Slug, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Search('english') did not find hello page")
	}
}

func TestGetBySlug(t *testing.T) {
	idx := mustNewIndex(t)

	p, ok := idx.GetBySlug("/posts/hello")
	if !ok {
		t.Fatal("GetBySlug('/posts/hello') not found")
	}
	if p.Lang != "en" {
		t.Fatalf("GetBySlug() lang = %q want en", p.Lang)
	}
	if p.URL != "https://example.test/posts/hello/" {
		t.Fatalf("GetBySlug() URL = %q", p.URL)
	}

	_, ok2 := idx.GetBySlug("/posts/does-not-exist")
	if ok2 {
		t.Fatal("GetBySlug() should not find missing slug")
	}
}

func TestRecentPosts(t *testing.T) {
	idx := mustNewIndex(t)

	posts := idx.RecentPosts(5)
	if len(posts) < 1 {
		t.Fatal("RecentPosts() returned no posts")
	}
	if posts[0].Slug != "/posts/hello/" {
		t.Fatalf("RecentPosts() first = %q want /posts/hello/", posts[0].Slug)
	}
	for i := 1; i < len(posts); i++ {
		if posts[i-1].Date < posts[i].Date {
			t.Fatalf("RecentPosts() not sorted by date desc")
		}
	}
}

func TestAllTags(t *testing.T) {
	idx := mustNewIndex(t)
	tags := idx.AllTags()
	if len(tags) == 0 {
		t.Fatal("AllTags() returned empty slice")
	}
	if !sort.StringsAreSorted(tags) {
		t.Fatalf("AllTags() not sorted: %v", tags)
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if seen[tag] {
			t.Fatalf("AllTags() duplicate: %q", tag)
		}
		seen[tag] = true
	}
}

func TestGetBySlugEmptySlug(t *testing.T) {
	idx := mustNewIndex(t)
	_, ok := idx.GetBySlug("")
	if ok {
		t.Fatal("GetBySlug('') should return not found")
	}
}

func writeCanonicalHTMLPage(t *testing.T, root, relPath, title, canonicalHref string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	canonicalTag := ""
	if canonicalHref != "" {
		canonicalTag = `<link rel="canonical" href="` + canonicalHref + `">`
	}
	html := "<!doctype html><html><head><title>" + title + "</title>" + canonicalTag + "</head><body><p>" + title + "</p></body></html>"
	if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// TestResolveAliasRecognizesCanonicalCollapsedAliasOwnURL is #1112's fix:
// a Grav-legacy alias page (grav-csp-nonce/index.html) whose own
// <link rel=canonical> points at a different, real page (csp-nonce/) is a
// genuine, walkable file — its own URL must not read as "missing" to a
// link-target lookup, only as "not itself canonical". "grav-csp-nonce"
// sorts after "csp-nonce" lexically, so filepath.WalkDir visits the real
// canonical page first — the ordinary case #184 originally covered.
func TestResolveAliasRecognizesCanonicalCollapsedAliasOwnURL(t *testing.T) {
	root := t.TempDir()
	writeCanonicalHTMLPage(t, root, "csp-nonce/index.html", "CSP Nonce", "")
	writeCanonicalHTMLPage(t, root, "grav-csp-nonce/index.html", "CSP Nonce (legacy)", "https://example.test/csp-nonce/")

	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	if _, ok := idx.GetBySlug("/grav-csp-nonce/"); ok {
		t.Fatal("GetBySlug(alias) unexpectedly found an entry — #184's dedup should still collapse it out of bySlug")
	}
	target, ok := idx.ResolveAlias("/grav-csp-nonce/")
	if !ok {
		t.Fatal("ResolveAlias(/grav-csp-nonce/) = not found, want it recognized as a known alias (#1112)")
	}
	if target != "/csp-nonce/" {
		t.Fatalf("ResolveAlias(/grav-csp-nonce/) target = %q, want /csp-nonce/", target)
	}
	if _, ok := idx.ResolveAlias("/csp-nonce/"); ok {
		t.Fatal("ResolveAlias(/csp-nonce/) must not itself be reported as an alias — it's the real canonical owner")
	}
	pg, ok := idx.GetBySlug("/csp-nonce/")
	if !ok {
		t.Fatal("GetBySlug(/csp-nonce/) not found")
	}
	if pg.Title != "CSP Nonce" {
		t.Fatalf("GetBySlug(/csp-nonce/).Title = %q, want the real page's own title, not the alias's", pg.Title)
	}
}

// TestNewIndexAliasWalkedBeforeCanonicalTargetDoesNotSquatTheSlug is the
// latent correctness bug found while fixing #1112: filepath.WalkDir order
// is lexical, not canonical-aware. If an alias's own path happens to sort
// before its canonical target's, the pre-fix code let the alias's
// "duplicate slug detected, skipping" branch never fire for the *target*
// (it fires for the target's *own* insert attempt instead, since the alias
// got there first) — so the alias would permanently occupy the slot and
// the real page would be silently dropped, serving the wrong content under
// its own canonical URL. "aaa-alias" sorts before "real-page" lexically.
func TestNewIndexAliasWalkedBeforeCanonicalTargetDoesNotSquatTheSlug(t *testing.T) {
	root := t.TempDir()
	writeCanonicalHTMLPage(t, root, "aaa-alias/index.html", "Alias (walked first)", "https://example.test/real-page/")
	writeCanonicalHTMLPage(t, root, "real-page/index.html", "The Real Page", "")

	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	pg, ok := idx.GetBySlug("/real-page/")
	if !ok {
		t.Fatal("GetBySlug(/real-page/) not found — the real page was dropped in favor of the alias that squatted its slug first")
	}
	if pg.Title != "The Real Page" {
		t.Fatalf("GetBySlug(/real-page/).Title = %q, want %q — the alias's content leaked into the canonical slot", pg.Title, "The Real Page")
	}
	target, ok := idx.ResolveAlias("/aaa-alias/")
	if !ok {
		t.Fatal("ResolveAlias(/aaa-alias/) = not found, want the demoted alias still recorded")
	}
	if target != "/real-page/" {
		t.Fatalf("ResolveAlias(/aaa-alias/) target = %q, want /real-page/", target)
	}
	if len(idx.Sitemap()) != 1 {
		t.Fatalf("Sitemap() = %d entries, want exactly 1 (the alias must never get its own bySlug/entries slot)", len(idx.Sitemap()))
	}
}
