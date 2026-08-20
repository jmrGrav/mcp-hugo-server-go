package db_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// TestReconcileRenderedIssuesAgainstIndexClearsStaleVerificationFileFail is
// #1186's actual reported symptom: a domain-ownership-verification file
// (e.g. static/abuseipdb-verification.html) has a cached rendered_issues_count
// FAIL from before the operator declared it in
// config.TechnicalVerificationSlugs. Its own content never changes, so
// syncPublicPage's hash-gate ("if existing == hash { return nil }") never
// revisits it on its own — a config-only change would otherwise leave the
// stale FAIL count in rendered_seo_summary forever. This must be cleared
// once idx's classifier (which #1186 wired up to be config-aware) reports
// the page as no longer content.
func TestReconcileRenderedIssuesAgainstIndexClearsStaleVerificationFileFail(t *testing.T) {
	d := openTestDB(t)

	// Simulate the pre-declaration state: the verification file synced
	// while still classified as ordinary content, with a cached FAIL.
	d.SetRenderedCheckFn(func(p site.Page) (int, bool) { return 1, true })
	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/abuseipdb-verification/", Title: "", URL: "https://example.test/abuseipdb-verification/", Lang: "en"})
	siteIdx.UpsertPage(site.Page{Slug: "/posts/hello/", Title: "Hello", URL: "https://example.test/posts/hello/", Lang: "en"})
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync: %v", err)
	}

	checked, withIssues, err := d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (pre-declaration): %v", err)
	}
	if checked != 2 || withIssues != 2 {
		t.Fatalf("expected both pages checked with issues before declaration, got checked=%d withIssues=%d", checked, withIssues)
	}

	// The operator now declares the slug. A fresh index reflects the new
	// config; ReconcileRenderedIssuesAgainstIndex must clear the stale FAIL
	// without needing the page's own content to change.
	configuredIdx, err := site.NewIndex(config.Config{TechnicalVerificationSlugs: []string{"abuseipdb-verification"}})
	if err != nil {
		t.Fatalf("NewIndex (configured): %v", err)
	}
	configuredIdx.UpsertPage(site.Page{Slug: "/abuseipdb-verification/", Title: "", URL: "https://example.test/abuseipdb-verification/", Lang: "en"})
	configuredIdx.UpsertPage(site.Page{Slug: "/posts/hello/", Title: "Hello", URL: "https://example.test/posts/hello/", Lang: "en"})

	if err := d.ReconcileRenderedIssuesAgainstIndex(configuredIdx); err != nil {
		t.Fatalf("ReconcileRenderedIssuesAgainstIndex: %v", err)
	}

	checked, withIssues, err = d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (after reconciliation): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected only the content page counted as checked after reconciliation, got %d", checked)
	}
	if withIssues != 1 {
		t.Fatalf("expected the remaining content page's FAIL to survive reconciliation, got %d", withIssues)
	}
}

func TestReconcileRenderedIssuesAgainstIndexNilIndexIsNoop(t *testing.T) {
	d := openTestDB(t)
	if err := d.ReconcileRenderedIssuesAgainstIndex(nil); err != nil {
		t.Fatalf("ReconcileRenderedIssuesAgainstIndex(nil) = %v, want nil", err)
	}
}

func TestReconcileRenderedIssuesAgainstIndexNoStaleRowsIsNoop(t *testing.T) {
	d := openTestDB(t)
	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	if err := d.ReconcileRenderedIssuesAgainstIndex(siteIdx); err != nil {
		t.Fatalf("ReconcileRenderedIssuesAgainstIndex (empty pages table): %v", err)
	}
}
