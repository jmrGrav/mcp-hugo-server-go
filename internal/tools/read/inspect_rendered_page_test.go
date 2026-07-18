package read_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeRenderedHTML(t *testing.T, siteRoot, rel, body string) {
	t.Helper()
	full := filepath.Join(siteRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func inspectRenderedPageConfig(siteRoot string) config.Config {
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	return cfg
}

func inspectRenderedPageIndex(t *testing.T, siteRoot string) *site.Index {
	t.Helper()
	idx, err := site.NewIndex(inspectRenderedPageConfig(siteRoot))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	return idx
}

// newInspectRenderedPageClient wires the tool with a cfg.SiteRoot matching
// the index's own siteRoot — RegisterInspectRenderedPage reads the rendered
// HTML file straight off disk via cfg.SiteRoot, so the two must agree
// (unlike most other read tools here, which only ever read from idx).
func newInspectRenderedPageClient(t *testing.T, siteRoot string, idx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	return newTestClientWithCfg(t, idx, inspectRenderedPageConfig(siteRoot), nil)
}

func findChecks(t *testing.T, data map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := data["checks"].([]any)
	if !ok {
		t.Fatalf("checks field type = %T", data["checks"])
	}
	out := make(map[string]map[string]any, len(raw))
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("check entry type = %T", c)
		}
		name, _ := m["check"].(string)
		out[name] = m
	}
	return out
}

func TestInspectRenderedPageCleanPagePassesAllChecks(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Hello World</title>
<meta name="description" content="A short, valid description of this page.">
<link rel="canonical" href="https://example.test/posts/hello/">
</head>
<body>
<p>Hello. <a href="/posts/other/">other post</a></p>
<img src="/images/hello.png">
</body>
</html>`)
	writeRenderedHTML(t, siteRoot, "posts/other/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Other</title><meta name="description" content="Other page description text here."><link rel="canonical" href="https://example.test/posts/other/"></head>
<body>Other.</body>
</html>`)
	if err := os.MkdirAll(filepath.Join(siteRoot, "images"), 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "images", "hello.png"), []byte("fake-png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	if got := data["status"]; got != "ok" {
		t.Fatalf("status = %v, want ok; checks = %v", got, data["checks"])
	}
	checks := findChecks(t, data)
	for _, name := range []string{"title", "meta_description", "canonical", "hreflang", "internal_links", "missing_images", "render_errors"} {
		c, ok := checks[name]
		if !ok {
			t.Fatalf("missing check %q", name)
		}
		if c["status"] != "pass" {
			t.Fatalf("check %q status = %v, want pass (detail=%v)", name, c["status"], c["detail"])
		}
	}
}

func TestInspectRenderedPageFlagsMissingSEOFields(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/bare/index.html", `<!DOCTYPE html>
<html lang="en">
<head></head>
<body>No title, no description, no canonical.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/bare/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	if got := data["status"]; got != "issues_found" {
		t.Fatalf("status = %v, want issues_found", got)
	}
	checks := findChecks(t, data)
	for _, name := range []string{"title", "meta_description", "canonical"} {
		if checks[name]["status"] != "fail" {
			t.Fatalf("check %q status = %v, want fail", name, checks[name]["status"])
		}
	}
}

func TestInspectRenderedPageFlagsBrokenLinkAndMissingImage(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/broken/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Broken</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/broken/"></head>
<body>
<a href="/posts/does-not-exist/">missing target</a>
<img src="/images/missing.png">
</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/broken/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["internal_links"]["status"] != "fail" {
		t.Fatalf("internal_links status = %v, want fail", checks["internal_links"]["status"])
	}
	if checks["missing_images"]["status"] != "fail" {
		t.Fatalf("missing_images status = %v, want fail", checks["missing_images"]["status"])
	}
}

func TestInspectRenderedPageFlagsRenderErrorMarker(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/errpage/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Err Page</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/errpage/"></head>
<body>error calling "foo": something broke</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/errpage/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["render_errors"]["status"] != "fail" {
		t.Fatalf("render_errors status = %v, want fail", checks["render_errors"]["status"])
	}
}

func TestInspectRenderedPageMultilingualWarnsMissingHreflang(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "en/posts/hi/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hi</title><meta name="description" content="English description text."><link rel="canonical" href="https://example.test/en/posts/hi/"></head>
<body>Hi.</body>
</html>`)
	writeRenderedHTML(t, siteRoot, "fr/posts/salut/index.html", `<!DOCTYPE html>
<html lang="fr">
<head><title>Salut</title><meta name="description" content="Description en francais ici."><link rel="canonical" href="https://example.test/fr/posts/salut/"></head>
<body>Salut.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/en/posts/hi/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["hreflang"]["status"] != "warn" {
		t.Fatalf("hreflang status = %v, want warn (site is multilingual, no hreflang tags present)", checks["hreflang"]["status"])
	}
	if got := data["status"]; got != "warnings_found" {
		t.Fatalf("status = %v, want warnings_found", got)
	}
}

// TestInspectRenderedPageHreflangDetectionAttributeOrderCaseAndRelCombining
// covers #420: hreflang detection walks the parsed DOM, not raw HTML text,
// so a real <link rel="alternate" hreflang="fr" href="..."> tag must be
// found regardless of attribute order, attribute-name case, or being
// combined with other rel values.
func TestInspectRenderedPageHreflangDetectionAttributeOrderCaseAndRelCombining(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{"reordered attributes", `<link href="https://example.test/fr/posts/salut/" hreflang="fr" rel="alternate">`},
		{"uppercase attribute names", `<link REL="alternate" HREFLANG="fr" HREF="https://example.test/fr/posts/salut/">`},
		{"combined with another rel value", `<link rel="alternate stylesheet" hreflang="fr" href="https://example.test/fr/posts/salut/">`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			siteRoot := t.TempDir()
			writeRenderedHTML(t, siteRoot, "en/posts/hi/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hi</title><meta name="description" content="English description text."><link rel="canonical" href="https://example.test/en/posts/hi/">`+tc.link+`</head>
<body>Hi.</body>
</html>`)
			writeRenderedHTML(t, siteRoot, "fr/posts/salut/index.html", `<!DOCTYPE html>
<html lang="fr">
<head><title>Salut</title><meta name="description" content="Description en francais ici."><link rel="canonical" href="https://example.test/fr/posts/salut/"></head>
<body>Salut.</body>
</html>`)

			idx := inspectRenderedPageIndex(t, siteRoot)
			session, done := newInspectRenderedPageClient(t, siteRoot, idx)
			defer done()

			res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/en/posts/hi/"})
			if res.IsError {
				t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
			}
			m := decodeContent(t, res)
			data := m["data"].(map[string]any)
			checks := findChecks(t, data)
			if checks["hreflang"]["status"] != "pass" {
				t.Fatalf("hreflang status = %v, want pass (%s)", checks["hreflang"]["status"], tc.name)
			}
		})
	}
}

// TestInspectRenderedPageHreflangWithEmptyHrefIsIncomplete covers #420's
// acceptance criterion that a hreflang tag with an empty href must still be
// flagged, not silently accepted as a valid alternate.
func TestInspectRenderedPageHreflangWithEmptyHrefIsIncomplete(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "en/posts/hi/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hi</title><meta name="description" content="English description text."><link rel="canonical" href="https://example.test/en/posts/hi/"><link rel="alternate" hreflang="fr" href=""></head>
<body>Hi.</body>
</html>`)
	writeRenderedHTML(t, siteRoot, "fr/posts/salut/index.html", `<!DOCTYPE html>
<html lang="fr">
<head><title>Salut</title><meta name="description" content="Description en francais ici."><link rel="canonical" href="https://example.test/fr/posts/salut/"></head>
<body>Salut.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/en/posts/hi/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["hreflang"]["status"] != "warn" {
		t.Fatalf("hreflang status = %v, want warn (href is empty, must not be accepted as a valid alternate)", checks["hreflang"]["status"])
	}
}

// TestInspectRenderedPageHreflangMultipleTranslationsAllFound covers #420's
// acceptance criterion of multiple translations: any one valid alternate is
// enough to pass, regardless of how many other <link> tags are present.
func TestInspectRenderedPageHreflangMultipleTranslationsAllFound(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "en/posts/hi/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hi</title><meta name="description" content="English description text."><link rel="canonical" href="https://example.test/en/posts/hi/">
<link rel="alternate" hreflang="fr" href="https://example.test/fr/posts/salut/">
<link rel="alternate" hreflang="de" href="https://example.test/de/posts/hallo/">
</head>
<body>Hi.</body>
</html>`)
	writeRenderedHTML(t, siteRoot, "fr/posts/salut/index.html", `<!DOCTYPE html>
<html lang="fr">
<head><title>Salut</title><meta name="description" content="Description en francais ici."><link rel="canonical" href="https://example.test/fr/posts/salut/"></head>
<body>Salut.</body>
</html>`)
	writeRenderedHTML(t, siteRoot, "de/posts/hallo/index.html", `<!DOCTYPE html>
<html lang="de">
<head><title>Hallo</title><meta name="description" content="Eine ausreichend lange Beschreibung."><link rel="canonical" href="https://example.test/de/posts/hallo/"></head>
<body>Hallo.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/en/posts/hi/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["hreflang"]["status"] != "pass" {
		t.Fatalf("hreflang status = %v, want pass", checks["hreflang"]["status"])
	}
}

// TestInspectRenderedPageHreflangMonolingualSiteDoesNotFalsePositive covers
// #420's acceptance criterion of a monolingual site: with only one language
// across the whole public index, hreflang is not applicable at all, and the
// check must pass without requiring any <link rel="alternate"> tag —
// treating this as a false positive would incorrectly flag every
// single-language site as missing translations.
func TestInspectRenderedPageHreflangMonolingualSiteDoesNotFalsePositive(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/hi/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hi</title><meta name="description" content="English description text."><link rel="canonical" href="https://example.test/posts/hi/"></head>
<body>Hi.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hi/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["hreflang"]["status"] != "pass" {
		t.Fatalf("hreflang status = %v, want pass (single-language site, hreflang not applicable)", checks["hreflang"]["status"])
	}
}

// TestInspectRenderedPageFlagsCanonicalMismatch proves the canonical check
// compares against an independently-derived expected URL (cfg.SiteURL +
// slug), not against page.URL — which the index copies straight out of the
// same <link rel="canonical"> tag during indexing (comparing against that
// would be comparing the tag to itself and could never catch a real
// mismatch). The realistic failure this check exists for: the site was
// rendered with a different baseURL than the one currently configured in
// cfg.SiteURL (e.g. a stray staging build, or a config drift), so the
// canonical's host disagrees with the configured site URL even though the
// path is correct.
func TestInspectRenderedPageFlagsCanonicalMismatch(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/drifted/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Drifted</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://staging.example.test/posts/drifted/"></head>
<body>Body.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/drifted/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	checks := findChecks(t, data)
	if checks["canonical"]["status"] != "warn" {
		t.Fatalf("canonical status = %v, want warn (rendered canonical host %q differs from configured cfg.SiteURL)", checks["canonical"]["status"], "staging.example.test")
	}
}

func TestInspectRenderedPageUnknownSlugReturnsNotFound(t *testing.T) {
	siteRoot := t.TempDir()
	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/does/not/exist/"})
	if !res.IsError {
		t.Fatalf("inspect_rendered on unknown slug: want error, got success")
	}
}

func TestInspectRenderedPageEmptySlugIsInvalidParams(t *testing.T) {
	siteRoot := t.TempDir()
	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "   "})
	if !res.IsError {
		t.Fatalf("inspect_rendered with blank slug: want error, got success")
	}
}

// newInspectRenderedPagePreviewClient wires inspect_rendered with both a
// built site.Index (rendered HTML on disk, for the checks) and a real
// hugosite.SourceIndex backed by a git repository (for #435's
// include_preview facet, which needs diff_page's git logic and
// validate_frontmatter's per-page checks to have something real to read).
func newInspectRenderedPagePreviewClient(t *testing.T, siteRoot, contentRoot string) (*mcp.ClientSession, func()) {
	t.Helper()
	idx := inspectRenderedPageIndex(t, siteRoot)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	cfg := inspectRenderedPageConfig(siteRoot)
	cfg.ContentRoot = contentRoot
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
}

// TestInspectRenderedPageIncludePreviewSurfacesRisks is a regression test
// for #435: include_preview=true composes diff_page's git-diff status,
// get_broken_links' per-page scan, and validate_frontmatter's checks into
// one risks list, on a page that has all three at once (uncommitted
// change, a broken internal link, and a missing title in front matter).
func TestInspectRenderedPageIncludePreviewSurfacesRisks(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Hello World</title>
<meta name="description" content="A short, valid description of this page.">
<link rel="canonical" href="https://example.test/posts/hello/">
</head>
<body>
<p>Hello. <a href="/posts/missing/">a broken link</a></p>
</body>
</html>`)

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No title in front matter (missing_title issue for validate_frontmatter).
	if err := os.WriteFile(pagePath, []byte("---\ndate: 2026-07-03\n---\nHello world.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.test")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(pagePath, []byte("---\ndate: 2026-07-03\n---\nHello brave new world.\n"), 0o644); err != nil {
		t.Fatalf("rewrite page: %v", err)
	}

	session, done := newInspectRenderedPagePreviewClient(t, siteRoot, contentRoot)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hello/", "include_preview": true})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	preview, ok := data["preview"].(map[string]any)
	if !ok {
		t.Fatalf("preview type = %T, want map[string]any", data["preview"])
	}
	if got := preview["diff_status"]; got != "modified" {
		t.Fatalf("preview.diff_status = %v, want modified", got)
	}
	if summary, _ := preview["diff_summary"].(string); !strings.Contains(summary, "added") {
		t.Fatalf("preview.diff_summary = %q, want a line-count summary", summary)
	}
	if got := preview["broken_links_count"]; got != float64(1) {
		t.Fatalf("preview.broken_links_count = %v, want 1", got)
	}
	if got := preview["frontmatter_valid"]; got != false {
		t.Fatalf("preview.frontmatter_valid = %v, want false (missing title)", got)
	}
	issues, ok := preview["frontmatter_issues"].([]any)
	if !ok || len(issues) == 0 {
		t.Fatalf("preview.frontmatter_issues = %#v, want at least one issue", preview["frontmatter_issues"])
	}
	risks, ok := preview["risks"].([]any)
	if !ok || len(risks) != 3 {
		t.Fatalf("preview.risks = %#v, want exactly 3 (diff, broken link, frontmatter)", preview["risks"])
	}
}

// TestInspectRenderedPageOmitsPreviewByDefault is a regression test for
// #435: omitting include_preview must not add the preview field at all,
// preserving every existing caller's response shape and cost.
func TestInspectRenderedPageOmitsPreviewByDefault(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hello World</title><meta name="description" content="A short, valid description of this page."><link rel="canonical" href="https://example.test/posts/hello/"></head>
<body><p>Hello.</p></body>
</html>`)
	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", m["data"])
	}
	if _, ok := data["preview"]; ok {
		t.Fatalf("preview = %#v, want omitted when include_preview is not requested", data["preview"])
	}
}
