package write_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUpdatePageResyncsTypedSourceFieldsImmediately is the regression test for
// #921: update_page must refresh the typed Date/Draft/PublishDate/ExpiryDate
// fields on the in-memory SourcePage it upserts, not just FrontmatterRaw.
// search_content's sort/filter paths consume these typed fields directly.
func TestUpdatePageResyncsTypedSourceFieldsImmediately(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "typed-update")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const before = `---
title: Avant FR
date: 2026-01-02T03:04:05Z
publishDate: 2026-01-03T04:05:06Z
expiryDate: 2026-01-04T05:06:07Z
draft: true
---
Bonjour`
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	fr, ok := idx.GetBySlugLang("posts/typed-update", "fr")
	if !ok {
		t.Fatal("fr translation missing from initial SourceIndex")
	}
	corrupted := *fr
	corrupted.Date = "1999-01-01T00:00:00Z"
	corrupted.Draft = false
	corrupted.PublishDate = time.Time{}
	corrupted.ExpiryDate = time.Date(1999, 1, 2, 3, 4, 5, 0, time.UTC)
	idx.Upsert(corrupted)

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/typed-update",
		"lang":              "fr",
		"description":       "Description FR",
		"expected_revision": currentRevision(t, filepath.Join(pageDir, "index.fr.md")),
	})
	if res.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, res))
	}

	fr, ok = idx.GetBySlugLang("posts/typed-update", "fr")
	if !ok {
		t.Fatal("fr translation missing from in-memory SourceIndex after update")
	}
	if !fr.Draft {
		t.Fatalf("fr Draft = false, want true from disk frontmatter after update_page resync")
	}
	if fr.Date != "2026-01-02T03:04:05Z" {
		t.Fatalf("fr Date = %q, want on-disk frontmatter date", fr.Date)
	}
	wantPublish := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	if !fr.PublishDate.Equal(wantPublish) {
		t.Fatalf("fr PublishDate = %s, want %s", fr.PublishDate.Format(time.RFC3339), wantPublish.Format(time.RFC3339))
	}
	wantExpiry := time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC)
	if !fr.ExpiryDate.Equal(wantExpiry) {
		t.Fatalf("fr ExpiryDate = %s, want %s", fr.ExpiryDate.Format(time.RFC3339), wantExpiry.Format(time.RFC3339))
	}
	if got, _ := fr.FrontmatterRaw["description"].(string); got != "Description FR" {
		t.Fatalf("fr frontmatter description = %q, want updated value", got)
	}
}
