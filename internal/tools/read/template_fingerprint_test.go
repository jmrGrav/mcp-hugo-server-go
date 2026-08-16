package read_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
)

func templateFingerprintConfig(hugoRoot string) config.Config {
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	return cfg
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestComputeTemplateFingerprintChangesOnLocalLayoutEdit is #1151's central
// scope test: a change to a local layouts/ file (the primary source of a
// page's rendered <head> output) must change the fingerprint, since this
// is exactly the class of regression #1136 was filed over — every page's
// content_hash stays identical while a template edit breaks something
// site-wide.
func TestComputeTemplateFingerprintChangesOnLocalLayoutEdit(t *testing.T) {
	hugoRoot := t.TempDir()
	layoutFile := filepath.Join(hugoRoot, "layouts", "baseof.html")
	writeFile(t, layoutFile, "<html><head><title>{{ .Title }}</title></head></html>")
	cfg := templateFingerprintConfig(hugoRoot)

	fp1, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (1st): %v", err)
	}

	writeFile(t, layoutFile, "<html><head></head></html>") // canonical/title dropped, e.g. a theme regression
	fp2, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (2nd): %v", err)
	}

	if fp1 == fp2 {
		t.Error("expected fingerprint to change after editing layouts/baseof.html, got identical fingerprints")
	}
}

// TestComputeTemplateFingerprintChangesOnConfigFileEdit covers the "site
// config" input named in #1151's scope constraint: hugo.toml params can
// gate what a template emits (e.g. a canonical-URL toggle), so a config
// edit must move the fingerprint even though it touches no layouts/ file.
func TestComputeTemplateFingerprintChangesOnConfigFileEdit(t *testing.T) {
	hugoRoot := t.TempDir()
	configFile := filepath.Join(hugoRoot, "hugo.toml")
	writeFile(t, configFile, "title = \"Example\"\n")
	cfg := templateFingerprintConfig(hugoRoot)

	fp1, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (1st): %v", err)
	}

	writeFile(t, configFile, "title = \"Example\"\ncanonifyURLs = false\n")
	fp2, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (2nd): %v", err)
	}

	if fp1 == fp2 {
		t.Error("expected fingerprint to change after editing hugo.toml, got identical fingerprints")
	}
}

// TestComputeTemplateFingerprintStableAcrossUnrelatedRoots is the
// complementary control: editing something outside layouts/theme/config
// (e.g. content) must NOT move the fingerprint. A fingerprint that reacts
// to unrelated changes would force a full rendered-checks re-scan on every
// single content edit, reproducing the exact cost #1151 exists to avoid —
// the whole reason output_revision was rejected as the invalidation key.
func TestComputeTemplateFingerprintStableAcrossUnrelatedRoots(t *testing.T) {
	hugoRoot := t.TempDir()
	writeFile(t, filepath.Join(hugoRoot, "layouts", "baseof.html"), "<html></html>")
	cfg := templateFingerprintConfig(hugoRoot)

	fp1, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (1st): %v", err)
	}

	// Simulate an ordinary content edit: a file under content/, which
	// ComputeTemplateFingerprint never looks at.
	writeFile(t, filepath.Join(hugoRoot, "content", "posts", "hello.md"), "---\ntitle: Hello\n---\nBody.\n")
	fp2, err := read.ComputeTemplateFingerprint(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ComputeTemplateFingerprint (2nd): %v", err)
	}

	if fp1 != fp2 {
		t.Error("expected fingerprint to stay stable after an unrelated content/ edit, but it changed")
	}
}

// TestComputeTemplateFingerprintNoErrorWhenLayoutsDirMissing: a themeless,
// override-less site (no local layouts/ at all) is a valid configuration —
// hashDirInto must treat a missing directory as "contributes nothing," not
// an error.
func TestComputeTemplateFingerprintNoErrorWhenLayoutsDirMissing(t *testing.T) {
	hugoRoot := t.TempDir() // deliberately empty — no layouts/, no config file
	cfg := templateFingerprintConfig(hugoRoot)

	if _, err := read.ComputeTemplateFingerprint(context.Background(), cfg); err != nil {
		t.Fatalf("ComputeTemplateFingerprint on an empty HugoRoot: %v", err)
	}
}

// TestRenderedIssueCountCountsFailingChecksOnly verifies RenderedIssueCount
// reuses the real per-check functions inspect_rendered calls live (a clean
// page reports 0, a page missing title/canonical/meta description reports
// 3), and that ok=true when the rendered HTML resolves. It deliberately
// does not exercise every one of the nine checks individually — those are
// already covered by inspect_rendered_page_test.go's per-check tests, and
// RenderedIssueCount calls the exact same functions, so a per-check
// regression there already fails those tests.
func TestRenderedIssueCountCountsFailingChecksOnly(t *testing.T) {
	siteRoot := t.TempDir()
	writeFile(t, filepath.Join(siteRoot, "posts", "clean", "index.html"), `<!DOCTYPE html>
<html lang="en">
<head>
<title>Clean Page</title>
<meta name="description" content="A short, valid description of this page.">
<link rel="canonical" href="https://example.test/posts/clean/">
</head>
<body>Clean.</body>
</html>`)
	writeFile(t, filepath.Join(siteRoot, "posts", "bare", "index.html"), `<!DOCTYPE html>
<html lang="en">
<head></head>
<body>No title, no description, no canonical.</body>
</html>`)

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	clean := site.Page{Slug: "/posts/clean/", URL: "https://example.test/posts/clean/", Lang: "en", OutputPath: "posts/clean/index.html"}
	n, ok := read.RenderedIssueCount(cfg, idx, clean)
	if !ok {
		t.Fatal("RenderedIssueCount: ok = false for a resolvable rendered page")
	}
	if n != 0 {
		t.Errorf("clean page: issues = %d, want 0", n)
	}

	bare := site.Page{Slug: "/posts/bare/", URL: "https://example.test/posts/bare/", Lang: "en", OutputPath: "posts/bare/index.html"}
	n, ok = read.RenderedIssueCount(cfg, idx, bare)
	if !ok {
		t.Fatal("RenderedIssueCount: ok = false for a resolvable rendered page")
	}
	if n != 3 { // title + meta_description + canonical
		t.Errorf("bare page: issues = %d, want 3 (title, meta_description, canonical)", n)
	}
}

// TestRenderedIssueCountReportsNotOKWhenRenderedHTMLMissing: a page whose
// on-disk output is missing/unreadable must report ok=false, not a
// misleading 0 — the caller (syncPublicPage) relies on this to leave a
// previously cached count untouched rather than overwrite it with "clean."
func TestRenderedIssueCountReportsNotOKWhenRenderedHTMLMissing(t *testing.T) {
	siteRoot := t.TempDir() // no rendered HTML written at all
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	missing := site.Page{Slug: "/posts/missing/", URL: "https://example.test/posts/missing/", Lang: "en", OutputPath: "posts/missing/index.html"}
	if _, ok := read.RenderedIssueCount(cfg, idx, missing); ok {
		t.Error("expected ok=false for a page whose rendered HTML file does not exist on disk")
	}
}
