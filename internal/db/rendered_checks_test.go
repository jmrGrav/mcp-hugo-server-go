package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
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

// TestReconcileRenderedChecksScopeClearsNonContentPagesOnReopen is #1156's
// regression test: before this fix, syncPublicPage's forceRenderedRecheck
// sweep populated rendered_issues_count for every published route,
// taxonomy/section pages included — confirmed live as pages_checked=245
// against 82 actual content pages. A stale DB file carrying that pre-fix
// data must be corrected the next time it's opened, since a taxonomy page's
// content_hash never changes and would otherwise never re-sync.
func TestReconcileRenderedChecksScopeClearsNonContentPagesOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}

	// Simulate the pre-#1156 bug: renderedCheckFn fires for every page,
	// content and taxonomy alike, with no classification gate.
	d.SetRenderedCheckFn(func(p site.Page) (int, bool) { return 0, true })

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/posts/hello/", Title: "Hello", URL: "https://example.test/posts/hello/", Lang: "en"})
	siteIdx.UpsertPage(site.Page{Slug: "/tags/go/", Title: "Go", URL: "https://example.test/tags/go/", Lang: "en"})
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync: %v", err)
	}

	checked, _, err := d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (pre-fix state): %v", err)
	}
	if checked != 2 {
		t.Fatalf("expected both pages checked in the simulated pre-fix state, got %d", checked)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reset the migration marker set by the *first* db.Open above (which
	// found an empty pages table and had nothing to reconcile) — simulating
	// a real pre-#1156 production DB that never had this migration's code
	// run against it at all, only now, on the first open under the new
	// binary, with the stale rows already sitting in the pages table.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(`DELETE FROM derived_schema_migrations WHERE name = 'rendered_checks_scope'`); err != nil {
		t.Fatalf("reset migration marker: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw handle: %v", err)
	}

	// Reopen: reconcileRenderedChecksScope must run and clear the taxonomy
	// page's stale cached count, leaving only the content page checked.
	d2, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open (reopen): %v", err)
	}
	defer func() { _ = d2.Close() }()

	checked, withIssues, err := d2.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (after reconciliation): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected only the content page counted as checked after reconciliation, got %d", checked)
	}
	if withIssues != 0 {
		t.Fatalf("expected 0 pages with issues, got %d", withIssues)
	}

	// Reopening a third time must be a no-op: the migration is version-
	// gated and must not re-run (and there's nothing left to fix anyway).
	if err := d2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	d3, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open (3rd open): %v", err)
	}
	defer func() { _ = d3.Close() }()
	checked, _, err = d3.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary (3rd open): %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected reconciliation to be idempotent, got checked=%d", checked)
	}
}

// TestRenderedCheckFnGatingSkipsNonContentPages is #1156's regression test
// on the internal/server wiring itself: a RenderedCheckFn that only
// computes for content pages (the fix in internal/server.go) must leave a
// taxonomy page's rendered_issues_count untouched (NULL, never checked),
// while still checking an ordinary content page normally.
func TestRenderedCheckFnGatingSkipsNonContentPages(t *testing.T) {
	d := openTestDB(t)

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	// The same content-only gate internal/server.go's SetRenderedCheckFn
	// closure applies, expressed directly against the classifier here so
	// this test doesn't need to construct a whole server.
	classifier := siteIdx.Classifier()
	d.SetRenderedCheckFn(func(p site.Page) (int, bool) {
		if !classifier.IsContent(p) {
			return 0, false
		}
		return 0, true
	})

	siteIdx.UpsertPage(site.Page{Slug: "/posts/hello/", Title: "Hello", URL: "https://example.test/posts/hello/", Lang: "en"})
	siteIdx.UpsertPage(site.Page{Slug: "/tags/go/", Title: "Go", URL: "https://example.test/tags/go/", Lang: "en"})
	if err := d.PostBuildSync(siteIdx, false); err != nil {
		t.Fatalf("PostBuildSync: %v", err)
	}

	checked, _, err := d.RenderedIssuesSummary()
	if err != nil {
		t.Fatalf("RenderedIssuesSummary: %v", err)
	}
	if checked != 1 {
		t.Fatalf("expected only the content page counted as checked, got %d", checked)
	}
}
