package read_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func responsiveSiteHealthConfig(siteRoot string) config.Config {
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	return cfg
}

func writeResponsiveTestPage(t *testing.T, siteRoot, rel, body string) {
	t.Helper()
	full := filepath.Join(siteRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestGetSiteHealthResponsiveSummaryOmittedByDefault is a regression test
// for #1138 Part 2: without include_responsive_summary=true, the full-site
// rendered-HTML scan must not run at all, and neither field it populates
// should appear — the existing default-call cost/shape must be unchanged.
func TestGetSiteHealthResponsiveSummaryOmittedByDefault(t *testing.T) {
	siteRoot := t.TempDir()
	writeResponsiveTestPage(t, siteRoot, "posts/wide/index.html", `<!DOCTYPE html>
<html lang="en"><head><title>Wide</title></head>
<body><table style="width:900px"><tr><td>`+strings.Repeat("a", 40)+`</td></tr></table></body></html>`)
	idx, err := site.NewIndex(responsiveSiteHealthConfig(siteRoot))
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, responsiveSiteHealthConfig(siteRoot), nil)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if _, ok := data["responsive_summary"]; ok {
		t.Fatalf("responsive_summary = %#v, want omitted when include_responsive_summary is not requested", data["responsive_summary"])
	}
	breakdown, _ := data["score_breakdown"].(map[string]any)
	if _, ok := breakdown["mobile_readability"]; ok {
		t.Fatalf("score_breakdown.mobile_readability = %#v, want omitted when include_responsive_summary is not requested", breakdown["mobile_readability"])
	}
}

// TestGetSiteHealthResponsiveSummaryFindsAtRiskPage is an end-to-end
// regression test for #1138 Part 2: with include_responsive_summary=true,
// a page with a fixed-width unwrapped table must be counted in
// pages_at_risk, and mobile_readability must NOT force status off healthy
// (the issue's own "weight 0 at first" starting posture, unlike
// title_shape/broken_links).
func TestGetSiteHealthResponsiveSummaryFindsAtRiskPage(t *testing.T) {
	siteRoot := t.TempDir()
	writeResponsiveTestPage(t, siteRoot, "posts/wide/index.html", `<!DOCTYPE html>
<html lang="en"><head><title>Wide</title></head>
<body><table style="width:900px"><tr><td>`+strings.Repeat("a", 40)+`</td></tr></table></body></html>`)
	writeResponsiveTestPage(t, siteRoot, "posts/clean/index.html", `<!DOCTYPE html>
<html lang="en"><head><title>Clean</title></head>
<body><p>Nothing risky here.</p></body></html>`)
	idx, err := site.NewIndex(responsiveSiteHealthConfig(siteRoot))
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, responsiveSiteHealthConfig(siteRoot), nil)
	defer done()

	res := callTool(t, session, "get_site_health", map[string]any{"include_responsive_summary": true})
	if res.IsError {
		t.Fatalf("get_site_health returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	summary, ok := data["responsive_summary"].(map[string]any)
	if !ok {
		t.Fatalf("responsive_summary field type = %T, want present map", data["responsive_summary"])
	}
	if summary["pages_at_risk"] != float64(1) {
		t.Fatalf("responsive_summary.pages_at_risk = %v, want 1", summary["pages_at_risk"])
	}
	if fixScope, _ := summary["fix_scope"].(string); fixScope == "" || fixScope == "none" {
		t.Fatalf("responsive_summary.fix_scope = %q, want a non-none scope while pages_at_risk > 0", fixScope)
	}

	breakdown := data["score_breakdown"].(map[string]any)
	mobileReadability, ok := breakdown["mobile_readability"].(map[string]any)
	if !ok {
		t.Fatal("score_breakdown.mobile_readability missing when include_responsive_summary=true")
	}
	if mobileReadability["weight"] != float64(0) {
		t.Fatalf("score_breakdown.mobile_readability.weight = %v, want 0", mobileReadability["weight"])
	}
	if mobileReadability["score"] != float64(0) {
		t.Fatalf("score_breakdown.mobile_readability.score = %v, want 0 (one page at risk)", mobileReadability["score"])
	}

	// Weight 0 + no status forcing is this issue's explicit starting
	// posture (unlike title_shape/broken_links) — status must stay
	// unaffected by mobile_readability alone.
	if status, _ := data["status"].(string); status != "healthy" {
		t.Fatalf("status = %q, want healthy (mobile_readability must not force status at weight 0)", status)
	}
}
