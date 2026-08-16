package db_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestSyncTemplateFingerprintDetectsChange(t *testing.T) {
	d := openTestDB(t)

	// First-ever fingerprint: no prior row, so this must report changed —
	// the "nothing cached yet" case a brand-new DB is in, which the caller
	// (PostBuildSync's forceRenderedRecheck) needs to treat like a template
	// change so the first build actually populates the cache.
	changed, err := d.SyncTemplateFingerprint("fp-a")
	if err != nil {
		t.Fatalf("SyncTemplateFingerprint (1st): %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first-ever fingerprint sync, got false")
	}

	// Same fingerprint again: unchanged.
	changed, err = d.SyncTemplateFingerprint("fp-a")
	if err != nil {
		t.Fatalf("SyncTemplateFingerprint (2nd, same): %v", err)
	}
	if changed {
		t.Error("expected changed=false when fingerprint is identical to what's stored, got true")
	}

	// Different fingerprint: changed.
	changed, err = d.SyncTemplateFingerprint("fp-b")
	if err != nil {
		t.Fatalf("SyncTemplateFingerprint (3rd, different): %v", err)
	}
	if !changed {
		t.Error("expected changed=true when fingerprint differs from what's stored, got false")
	}

	// Confirm fp-b is now what's persisted (not still fp-a).
	changed, err = d.SyncTemplateFingerprint("fp-b")
	if err != nil {
		t.Fatalf("SyncTemplateFingerprint (4th, confirm persisted): %v", err)
	}
	if changed {
		t.Error("expected fp-b to have been persisted by the 3rd call, but SyncTemplateFingerprint(\"fp-b\") reported changed=true again")
	}
}

// TestPostBuildSyncForceRenderedRecheckRecomputesUnchangedPage is #1151's
// central load-bearing test: it proves forceRenderedRecheck is actually
// wired to something, not just accepted and ignored. A page whose content
// (and therefore content_hash) never changes must still get its
// rendered_issues_count recomputed when forceRenderedRecheck=true — the
// exact situation a template-only regression produces, since the page's own
// content_hash stays identical while its rendered <head> output does not.
//
// Verified in both directions: forceRenderedRecheck=false leaves the
// short-circuit in place (renderedCheckFn not called again, cached value
// unchanged) — proving this test would fail if the fingerprint plumbing
// were ripped out and forceRenderedRecheck silently stopped doing anything.
func TestPostBuildSyncForceRenderedRecheckRecomputesUnchangedPage(t *testing.T) {
	d := openTestDB(t)

	calls := 0
	d.SetRenderedCheckFn(func(p site.Page) (int, bool) {
		calls++
		return calls, true // a distinct, increasing value each call
	})

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	page := site.Page{
		Slug:  "/stable-content/",
		Title: "StableContentUniqueTitle",
		URL:   "https://example.test/stable-content/",
		Lang:  "en",
	}
	siteIdx.UpsertPage(page)

	// 1st build: content is new, renderedCheckFn must run once (calls=1).
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync (1st): %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected renderedCheckFn called once after 1st PostBuildSync, got %d", calls)
	}
	checked, _, err := d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (1st): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected 1 page checked after 1st PostBuildSync, got %d", checked)
	}

	// 2nd build: identical content, forceRenderedRecheck=false. The
	// content_hash short-circuit must still apply — renderedCheckFn must
	// NOT be called again. This is the control: if it fires here too, the
	// short-circuit itself is broken (unrelated to this issue, but this
	// test would catch that regression as a side effect).
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync (2nd, no force): %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected renderedCheckFn still called only once after an unforced no-content-change build, got %d calls", calls)
	}

	// 3rd build: identical content, forceRenderedRecheck=true — simulating
	// a template-only change (SyncTemplateFingerprint reported changed).
	// renderedCheckFn MUST run again even though content_hash is unchanged.
	if err := d.PostBuildSync(siteIdx, true); err != nil {
		t.Fatalf("PostBuildSync (3rd, forced): %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected renderedCheckFn called a 2nd time when forceRenderedRecheck=true despite unchanged content, got %d total calls", calls)
	}
	checked, _, err = d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (3rd): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected still exactly 1 page checked (same page, recomputed not duplicated) after forced recheck, got %d", checked)
	}
}

func TestRenderedIssuesSummaryCountsOnlyCheckedPages(t *testing.T) {
	d := openTestDB(t)

	// No renderedCheckFn wired: pages sync but rendered_issues_count stays
	// NULL — RenderedIssuesSummary must report 0 checked, not a misleading
	// "0 checked, 0 with issues" that could be confused with "checked and
	// clean".
	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/a/", Title: "A", URL: "https://example.test/a/", Lang: "en"})
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync: %v", err)
	}
	checked, withIssues, err := d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary: %v", err)
	}
	if checked != 0 || withIssues != 0 {
		t.Fatalf("expected 0 checked / 0 with issues with no renderedCheckFn wired, got checked=%d withIssues=%d", checked, withIssues)
	}

	// Now wire a fn that reports one page clean and one page with issues.
	d.SetRenderedCheckFn(func(p site.Page) (int, bool) {
		if p.Slug == "/b-broken/" {
			return 3, true
		}
		return 0, true
	})
	siteIdx.UpsertPage(site.Page{Slug: "/b-broken/", Title: "B", URL: "https://example.test/b-broken/", Lang: "en"})
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync (2nd, with renderedCheckFn): %v", err)
	}
	checked, withIssues, err = d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (2nd): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected 1 page checked (only /b-broken/ was synced after the fn was wired; /a/ already matched its content_hash so its NULL rendered_issues_count was never touched), got %d", checked)
	}
	if withIssues != 1 {
		t.Fatalf("expected 1 page with issues, got %d", withIssues)
	}
}
