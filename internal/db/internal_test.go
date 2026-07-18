package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// countRows returns the number of rows in table whose slug column matches
// any of slugs — used to directly inspect pages/page_fts row counts, since
// db.Search's own published=1 filter would otherwise mask a duplicate
// source-keyed row regardless of whether the underlying bug is present
// (the source-only row is always published=0).
func countRows(t *testing.T, d *DB, table string, slugs ...string) int {
	t.Helper()
	placeholders := ""
	args := make([]any, len(slugs))
	for i, s := range slugs {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args[i] = s
	}
	var count int
	if err := d.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE slug IN ("+placeholders+")", args...).Scan(&count); err != nil {
		t.Fatalf("countRows(%s): %v", table, err)
	}
	return count
}

// TestStartupSyncProducesOneRowPerLogicalPage is a regression test for
// #475: a page present in both the public (built) index and the source
// index must produce exactly one row in `pages` and one in `page_fts`,
// keyed by its canonical public slug — not two rows (one under the public
// slug via SyncPublicPage, one under the bare source slug via
// SyncSourcePage) for the same logical page.
func TestStartupSyncProducesOneRowPerLogicalPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	contentRoot := t.TempDir()
	mdPath := filepath.Join(contentRoot, "hello.md")
	if err := os.WriteFile(mdPath, []byte("---\ntitle: Hello\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:  "/hello/",
		Title: "Hello",
		URL:   "https://example.test/hello/",
		Lang:  "en",
	})

	if err := d.StartupSync(siteIdx, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	// Both the public slug ("/hello/") and the bare source slug ("hello")
	// are checked — pre-fix, both would carry a row for this one logical
	// page; post-fix, only the canonical public slug should.
	if got := countRows(t, d, "pages", "/hello/", "hello"); got != 1 {
		t.Fatalf("pages rows for /hello/ + hello = %d, want 1 (one logical page, indexed once)", got)
	}
	if got := countRows(t, d, "page_fts", "/hello/", "hello"); got != 1 {
		t.Fatalf("page_fts rows for /hello/ + hello = %d, want 1 (one logical page, indexed once)", got)
	}

	// A second StartupSync (simulating a server restart) must not
	// resurrect a duplicate, and must clean up a pre-existing legacy one
	// if present (verified separately below by seeding one directly).
	if err := d.StartupSync(siteIdx, srcIdx); err != nil {
		t.Fatalf("StartupSync (2nd): %v", err)
	}
	if got := countRows(t, d, "pages", "/hello/", "hello"); got != 1 {
		t.Fatalf("pages rows for /hello/ + hello after 2nd StartupSync = %d, want 1", got)
	}
}

// TestStartupSyncCleansUpLegacyDuplicateRow is a regression test for #475:
// a duplicate bare-slug row left over from before this fix (or from a
// write-path SyncSourcePage call while the page was still source-only)
// must be cleaned up by the next StartupSync once the page also has a
// public/built counterpart, not left behind forever.
func TestStartupSyncCleansUpLegacyDuplicateRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	contentRoot := t.TempDir()
	mdPath := filepath.Join(contentRoot, "hello.md")
	if err := os.WriteFile(mdPath, []byte("---\ntitle: Hello\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	var sourcePage hugosite.SourcePage
	for _, p := range srcIdx.ListPages(0, 0) {
		sourcePage = p
	}
	if sourcePage.Slug == "" {
		t.Fatal("expected exactly one source page")
	}

	// Simulate the legacy duplicate: a bare-slug row written before the
	// page had a public counterpart (e.g. right after create_page).
	if err := d.SyncSourcePage(sourcePage); err != nil {
		t.Fatalf("SyncSourcePage (legacy duplicate seed): %v", err)
	}
	if got := countRows(t, d, "pages", sourcePage.Slug); got != 1 {
		t.Fatalf("pages rows for bare slug after seeding legacy duplicate = %d, want 1", got)
	}

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:  "/hello/",
		Title: "Hello",
		URL:   "https://example.test/hello/",
		Lang:  "en",
	})

	if err := d.StartupSync(siteIdx, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	if got := countRows(t, d, "pages", sourcePage.Slug); got != 0 {
		t.Fatalf("pages rows for legacy bare slug %q after StartupSync = %d, want 0 (cleaned up as an orphan)", sourcePage.Slug, got)
	}
	if got := countRows(t, d, "pages", "/hello/"); got != 1 {
		t.Fatalf("pages rows for /hello/ after StartupSync = %d, want 1", got)
	}
}

// TestStartupSyncDedupesMultilingualBundle is a regression test for #475
// covering a multilingual branch bundle (index.en.md + index.fr.md under the
// same directory). hugosite.SlugFromRel strips the language segment before
// the .md extension, so both language variants of a bundle share one bare
// source slug (e.g. "posts/x") — meaning both public variants ("/posts/x/"
// and "/fr/posts/x/") must resolve to that same bare slug via
// site.SourceSlugCandidates, and both SourcePage entries in
// srcIdx.ListPages() (one per language) must be skipped, not just one of
// them. Without this, the non-default-language source row would survive as
// a duplicate.
func TestStartupSyncDedupesMultilingualBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	contentRoot := t.TempDir()
	bundleDir := filepath.Join(contentRoot, "posts", "x")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "index.en.md"), []byte("---\ntitle: English\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "index.fr.md"), []byte("---\ntitle: French\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	var bareSlug string
	langCount := 0
	for _, p := range srcIdx.ListPages(0, 0) {
		langCount++
		bareSlug = p.Slug
	}
	if langCount != 2 {
		t.Fatalf("expected 2 source pages (en + fr) sharing one bare slug, got %d", langCount)
	}

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/posts/x/", Title: "English", URL: "https://example.test/posts/x/", Lang: "en"})
	siteIdx.UpsertPage(site.Page{Slug: "/fr/posts/x/", Title: "French", URL: "https://example.test/fr/posts/x/", Lang: "fr"})

	if err := d.StartupSync(siteIdx, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	if got := countRows(t, d, "pages", "/posts/x/", "/fr/posts/x/", bareSlug); got != 2 {
		t.Fatalf("pages rows for the bundle = %d, want 2 (one per public language, zero for the shared bare source slug %q)", got, bareSlug)
	}
	if got := countRows(t, d, "pages", bareSlug); got != 0 {
		t.Fatalf("pages rows for bare source slug %q = %d, want 0 (both language variants deduped against their public counterparts)", bareSlug, got)
	}
}
