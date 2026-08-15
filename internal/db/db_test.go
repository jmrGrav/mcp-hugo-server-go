package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenAndClose(t *testing.T) {
	d := openTestDB(t)
	if d == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestSyncPublicPage(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{
		Slug:       "/hello/",
		Title:      "Hello World",
		Summary:    "An introductory post",
		Tags:       []string{"go", "test"},
		Categories: []string{"tech"},
		Date:       "2024-01-01T00:00:00Z",
		URL:        "https://example.com/hello/",
		Lang:       "en",
	}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}
	// Idempotent — second call with same data should succeed and be a no-op.
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage (2nd): %v", err)
	}
}

func TestSyncAndSearchFTS5(t *testing.T) {
	d := openTestDB(t)
	pages := []site.Page{
		{Slug: "/gopher/", Title: "Gopher Guide", Summary: "All about Go gophers", URL: "https://x.com/gopher/", Lang: "en"},
		{Slug: "/rust/", Title: "Rust Programming", Summary: "Systems language", URL: "https://x.com/rust/", Lang: "en"},
		{Slug: "/draft/", Title: "Draft Post", Summary: "Not published", URL: "", Lang: "en"},
	}
	for _, p := range pages {
		if err := d.SyncPublicPage(p, nil); err != nil {
			t.Fatalf("SyncPublicPage %q: %v", p.Slug, err)
		}
	}

	results, err := d.Search("gopher", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one FTS result for 'gopher'")
	}
	if results[0].Slug != "/gopher/" {
		t.Errorf("top result = %q, want /gopher/", results[0].Slug)
	}
	// The draft page (no URL, published=0 in the schema — actually published=1 since we called SyncPublicPage)
	// Actually /draft/ has URL="" — but it still gets published=1 via SyncPublicPage. That's fine.
}

func TestSearchEmpty(t *testing.T) {
	d := openTestDB(t)
	results, err := d.Search("", 10)
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %v", results)
	}
}

func TestDeletePage(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{Slug: "/del/", Title: "To Delete", URL: "https://x.com/del/", Lang: "en"}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	results, _ := d.Search("Delete", 10)
	if len(results) == 0 {
		t.Fatal("expected page in FTS before delete")
	}

	if err := d.DeletePage("/del/"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	results, _ = d.Search("Delete", 10)
	for _, r := range results {
		if r.Slug == "/del/" {
			t.Error("deleted page still appears in FTS results")
		}
	}
}

func TestGetBrokenLinks(t *testing.T) {
	d := openTestDB(t)

	// Page that links internally to a missing page.
	p := site.Page{
		Slug:    "/source/",
		Title:   "Source Page",
		URL:     "https://x.com/source/",
		Lang:    "en",
		RawHTML: `<a href="/missing/">Missing</a> <a href="/source/">Self</a>`,
	}
	// siteIdx is nil so all internal links are "broken".
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	broken, err := d.GetBrokenLinks()
	if err != nil {
		t.Fatalf("GetBrokenLinks: %v", err)
	}
	var found bool
	for _, r := range broken {
		if r.SourceSlug == "/source/" && strings.Contains(r.Target, "missing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected broken link from /source/ to /missing/, got %+v", broken)
	}
}

// TestGetBrokenLinksIgnoresRawMarkdownSourceLinks is a regression test for a
// real production false-positive: a theme's "view source" link (or similar)
// pointing at a page's raw .md path was flagged as broken by this SQL-backed
// link checker, even though internal/tools/read/extended.go's sibling
// in-memory implementation (resolveInternalLink) already excludes the exact
// same pattern. The two implementations had drifted; txSyncLinks must not
// treat a .md target as a page the rendered public index should resolve.
func TestGetBrokenLinksIgnoresRawMarkdownSourceLinks(t *testing.T) {
	d := openTestDB(t)

	p := site.Page{
		Slug:    "/source/",
		Title:   "Source Page",
		URL:     "https://x.com/source/",
		Lang:    "en",
		RawHTML: `<a href="/source/index.md">View source</a> <a href="/missing/">Missing</a>`,
	}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	broken, err := d.GetBrokenLinks()
	if err != nil {
		t.Fatalf("GetBrokenLinks: %v", err)
	}
	for _, r := range broken {
		if strings.HasSuffix(r.Target, ".md") {
			t.Errorf("raw .md source link reported as broken: %+v", r)
		}
	}
	var foundRealBroken bool
	for _, r := range broken {
		if strings.Contains(r.Target, "missing") {
			foundRealBroken = true
		}
	}
	if !foundRealBroken {
		t.Errorf("a genuinely missing non-.md target must still be reported: %+v", broken)
	}
}

// TestSyncPublicPageRecordsExternalMarkdownLinkAsExternalNotDropped checks
// the .md exclusion added for #1101 is ordered after the external-host
// check, matching internal/tools/read/extended.go's resolveInternalLink: an
// external link that happens to end in .md (e.g. a GitHub README, common on
// this site's posts) must still get an 'external' row, not be silently
// dropped by an .md check that fires first.
func TestSyncPublicPageRecordsExternalMarkdownLinkAsExternalNotDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	p := site.Page{
		Slug:    "/source/",
		Title:   "Source Page",
		URL:     "https://x.com/source/",
		Lang:    "en",
		RawHTML: `<a href="https://github.com/example/repo/blob/main/README.md">README</a>`,
	}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer raw.Close()
	var status string
	err = raw.QueryRow("SELECT status FROM links WHERE target LIKE '%README.md%'").Scan(&status)
	if err != nil {
		t.Fatalf("external .md link row missing entirely (silently dropped): %v", err)
	}
	if status != "external" {
		t.Fatalf("external .md link status = %q, want external", status)
	}
}

func TestSyncSourcePage(t *testing.T) {
	d := openTestDB(t)
	sp := hugosite.SourcePage{
		Slug:       "posts/draft-one",
		FilePath:   "/content/posts/draft-one/index.md",
		Lang:       "en",
		Title:      "Draft One",
		Date:       "2024-06-01",
		Draft:      true,
		Tags:       []string{"draft"},
		Categories: []string{"blog"},
		Body:       "This is a draft.",
	}
	if err := d.SyncSourcePage(sp); err != nil {
		t.Fatalf("SyncSourcePage: %v", err)
	}
	// Draft pages have published=0 so FTS search should NOT return them.
	results, err := d.Search("draft", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Slug == "posts/draft-one" {
			t.Error("draft source page should not appear in published FTS results")
		}
	}
}

func TestSnapshotHealth(t *testing.T) {
	d := openTestDB(t)
	payload := `{"broken_links":0,"total_pages":42}`
	if err := d.SnapshotHealth(payload); err != nil {
		t.Fatalf("SnapshotHealth: %v", err)
	}
}

func TestRecoveryJournalListsOnlyUncommittedOperations(t *testing.T) {
	d := openTestDB(t)
	if err := d.RecordRecovery(db.RecoveryEntry{OperationID: "write-1", Kind: "content_write", State: "file_written", Payload: []byte(`{"path":"opaque"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordRecovery(db.RecoveryEntry{OperationID: "build-1", Kind: "build", State: "committed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].OperationID != "write-1" || pending[0].State != "file_written" {
		t.Fatalf("PendingRecovery = %+v", pending)
	}
}

func TestRecoveryJournalSurvivesReopenAndAdvancesStateInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := d.RecordRecovery(db.RecoveryEntry{OperationID: "op-1", Kind: "content_write", State: "in_progress", Payload: []byte(`{"path":"opaque"}`)}); err != nil {
		t.Fatalf("RecordRecovery(in_progress): %v", err)
	}
	// Advancing state for the same operation_id must update the existing
	// row, not create a second pending entry — startup recovery needs one
	// current fact per operation, not a full history.
	if err := d.RecordRecovery(db.RecoveryEntry{OperationID: "op-1", Kind: "content_write", State: "file_written", Payload: []byte(`{"path":"opaque"}`)}); err != nil {
		t.Fatalf("RecordRecovery(file_written): %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: the crash-recovery scenario this table exists for is a process
	// restart finding an operation that never reached "committed".
	d, err = db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	pending, err := d.PendingRecovery()
	if err != nil {
		t.Fatalf("PendingRecovery after reopen: %v", err)
	}
	if len(pending) != 1 || pending[0].OperationID != "op-1" || pending[0].State != "file_written" {
		t.Fatalf("PendingRecovery after reopen = %+v, want one op-1 entry in state file_written", pending)
	}

	if err := d.RecordRecovery(db.RecoveryEntry{OperationID: "op-1", Kind: "content_write", State: "committed", Payload: []byte(`{"path":"opaque"}`)}); err != nil {
		t.Fatalf("RecordRecovery(committed): %v", err)
	}
	pending, err = d.PendingRecovery()
	if err != nil {
		t.Fatalf("PendingRecovery after commit: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingRecovery after commit = %+v, want empty", pending)
	}
}

func TestPublicationManifestSurvivesReopenAndReplaysByBuildID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	observed := time.Date(2026, time.August, 14, 10, 11, 12, 0, time.UTC)
	manifest := db.PublicationManifest{
		BuildID:        "20260814-101112-abcd",
		SourceRevision: "source-v1",
		OutputRevision: "public-v1",
		HugoVersion:    "0.164.0+extended",
		Status:         "ok",
		ObservedAt:     observed,
	}
	if err := d.RecordPublicationManifest(manifest); err != nil {
		t.Fatalf("RecordPublicationManifest: %v", err)
	}
	// A retry after an uncertain transport result updates the same durable
	// fact instead of producing a second ambiguous build record.
	manifest.OutputRevision = "public-v1-reconciled"
	if err := d.RecordPublicationManifest(manifest); err != nil {
		t.Fatalf("RecordPublicationManifest replay: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d, err = db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	got, err := d.LatestPublicationManifest()
	if err != nil {
		t.Fatalf("LatestPublicationManifest: %v", err)
	}
	if got == nil {
		t.Fatal("LatestPublicationManifest = nil, want persisted record")
	}
	if got.BuildID != manifest.BuildID || got.SourceRevision != manifest.SourceRevision || got.OutputRevision != manifest.OutputRevision || got.HugoVersion != manifest.HugoVersion || got.Status != manifest.Status || !got.ObservedAt.Equal(observed) {
		t.Fatalf("persisted manifest = %+v, want %+v", got, manifest)
	}
}

func TestMutationJournalSurvivesReopenAndRespectsTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutation.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	createdAt := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	entry := db.MutationJournalEntry{
		CallerKey: "caller-a", Tool: "create_page", Key: "idem-1",
		RequestHash: "sha256:abc", ResultJSON: []byte(`{"status":"created"}`), CreatedAt: createdAt,
	}
	if err := d.RememberMutation(entry); err != nil {
		t.Fatalf("RememberMutation: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: the real restart this journal exists to survive.
	d, err = db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	got, err := d.LookupMutation("caller-a", "create_page", "idem-1", 0)
	if err != nil {
		t.Fatalf("LookupMutation after reopen: %v", err)
	}
	if got == nil || got.RequestHash != entry.RequestHash || string(got.ResultJSON) != string(entry.ResultJSON) {
		t.Fatalf("LookupMutation after reopen = %+v, want a match for %+v", got, entry)
	}

	// A different caller_key or tool must not replay someone else's mutation.
	if got, err := d.LookupMutation("caller-b", "create_page", "idem-1", 0); err != nil || got != nil {
		t.Fatalf("LookupMutation(caller-b) = %+v err=%v, want nil, no error", got, err)
	}
	if got, err := d.LookupMutation("caller-a", "update_page", "idem-1", 0); err != nil || got != nil {
		t.Fatalf("LookupMutation(different tool) = %+v err=%v, want nil, no error", got, err)
	}

	// A TTL in the past must expire the entry on read, mirroring the
	// ephemeral-record store's TTL semantics.
	if got, err := d.LookupMutation("caller-a", "create_page", "idem-1", time.Nanosecond); err != nil || got != nil {
		t.Fatalf("LookupMutation with elapsed TTL = %+v err=%v, want nil, no error", got, err)
	}
	if got, err := d.LookupMutation("caller-a", "create_page", "idem-1", 0); err != nil || got != nil {
		t.Fatalf("LookupMutation after TTL expiry deleted the row = %+v err=%v, want nil (TTL read also deletes)", got, err)
	}
}

func TestEphemeralRecordSurvivesReopenAndRespectsCallerAndTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	createdAt := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	if err := d.PutEphemeralRecord("content_plan", "plan-1", "caller-a", []byte(`{"field":"title"}`), createdAt); err != nil {
		t.Fatalf("PutEphemeralRecord: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: this is the actual restart this mechanism exists for — a fresh
	// DB handle standing in for a fresh process, no in-memory state carried
	// over except what's in the file.
	d, err = db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	payload, found, err := d.GetEphemeralRecord("content_plan", "plan-1", "caller-a", 0)
	if err != nil {
		t.Fatalf("GetEphemeralRecord after reopen: %v", err)
	}
	if !found || string(payload) != `{"field":"title"}` {
		t.Fatalf("GetEphemeralRecord after reopen = (%q, %v), want (%q, true)", payload, found, `{"field":"title"}`)
	}

	// A different caller_key must not read caller-a's plan back, even though
	// the (kind, record_id) pair matches exactly — this is the persisted
	// form of the same isolation the in-memory planStore already enforces.
	if _, found, err := d.GetEphemeralRecord("content_plan", "plan-1", "caller-b", 0); err != nil || found {
		t.Fatalf("GetEphemeralRecord(caller-b) = found=%v err=%v, want found=false, no error (#627 caller isolation)", found, err)
	}

	// A TTL in the past must expire the record on read, mirroring the
	// in-memory store's TTL semantics.
	if _, found, err := d.GetEphemeralRecord("content_plan", "plan-1", "caller-a", time.Nanosecond); err != nil || found {
		t.Fatalf("GetEphemeralRecord with elapsed TTL = found=%v err=%v, want found=false, no error", found, err)
	}
	if _, found, err := d.GetEphemeralRecord("content_plan", "plan-1", "caller-a", 0); err != nil || found {
		t.Fatalf("GetEphemeralRecord after TTL expiry deleted the row = found=%v err=%v, want found=false (TTL read also deletes)", found, err)
	}
}

func TestEphemeralRecordDeleteIsScopedToCaller(t *testing.T) {
	d := openTestDB(t)
	if err := d.PutEphemeralRecord("content_plan", "plan-2", "caller-a", []byte(`{}`), time.Time{}); err != nil {
		t.Fatalf("PutEphemeralRecord: %v", err)
	}
	// A delete from the wrong caller must not remove someone else's record.
	if err := d.DeleteEphemeralRecord("content_plan", "plan-2", "caller-b"); err != nil {
		t.Fatalf("DeleteEphemeralRecord(caller-b): %v", err)
	}
	if _, found, err := d.GetEphemeralRecord("content_plan", "plan-2", "caller-a", 0); err != nil || !found {
		t.Fatalf("record after mismatched-caller delete = found=%v err=%v, want found=true (delete must not cross caller boundary)", found, err)
	}
	if err := d.DeleteEphemeralRecord("content_plan", "plan-2", "caller-a"); err != nil {
		t.Fatalf("DeleteEphemeralRecord(caller-a): %v", err)
	}
	if _, found, err := d.GetEphemeralRecord("content_plan", "plan-2", "caller-a", 0); err != nil || found {
		t.Fatalf("record after correct-caller delete = found=%v err=%v, want found=false", found, err)
	}
}

func TestListEphemeralRecordsScopesByCallerAndExpiresByTTL(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()
	if err := d.PutEphemeralRecord("content_snapshot", "page-a\x00rev-1", "caller-a", []byte("first"), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("PutEphemeralRecord(rev-1): %v", err)
	}
	if err := d.PutEphemeralRecord("content_snapshot", "page-a\x00rev-2", "caller-a", []byte("second"), now); err != nil {
		t.Fatalf("PutEphemeralRecord(rev-2): %v", err)
	}
	if err := d.PutEphemeralRecord("content_snapshot", "page-a\x00rev-3", "caller-b", []byte("other caller"), now); err != nil {
		t.Fatalf("PutEphemeralRecord(rev-3): %v", err)
	}

	// TTL of one hour: the two-hour-old record for caller-a must be pruned
	// from the result and from the table, mirroring GetEphemeralRecord's
	// read-time TTL expiry.
	records, err := d.ListEphemeralRecords("content_snapshot", "caller-a", time.Hour)
	if err != nil {
		t.Fatalf("ListEphemeralRecords: %v", err)
	}
	if len(records) != 1 || records[0].ID != "page-a\x00rev-2" {
		t.Fatalf("ListEphemeralRecords(caller-a) = %+v, want only the unexpired rev-2 record", records)
	}
	if string(records[0].Payload) != "second" {
		t.Fatalf("ListEphemeralRecords payload = %q, want %q", records[0].Payload, "second")
	}

	if _, found, err := d.GetEphemeralRecord("content_snapshot", "page-a\x00rev-1", "caller-a", time.Hour); err != nil || found {
		t.Fatalf("expired rev-1 must have been deleted by the list's own TTL sweep: found=%v err=%v", found, err)
	}

	// caller-b's record must never appear in caller-a's listing.
	bRecords, err := d.ListEphemeralRecords("content_snapshot", "caller-b", time.Hour)
	if err != nil {
		t.Fatalf("ListEphemeralRecords(caller-b): %v", err)
	}
	if len(bRecords) != 1 || bRecords[0].ID != "page-a\x00rev-3" {
		t.Fatalf("ListEphemeralRecords(caller-b) = %+v, want only rev-3", bRecords)
	}
}

func TestPublicationManifestRejectsIncompleteFacts(t *testing.T) {
	d := openTestDB(t)
	if err := d.RecordPublicationManifest(db.PublicationManifest{BuildID: "build-1", Status: "ok"}); err == nil {
		t.Fatal("RecordPublicationManifest accepted missing revisions")
	}
	got, err := d.LatestPublicationManifest()
	if err != nil {
		t.Fatalf("LatestPublicationManifest: %v", err)
	}
	if got != nil {
		t.Fatalf("LatestPublicationManifest = %+v after rejected write, want nil", got)
	}
}

func TestStartupSync(t *testing.T) {
	d := openTestDB(t)

	// Minimal source index.
	tmp := t.TempDir()
	content := "---\ntitle: Hello\n---\nBody."
	mdPath := filepath.Join(tmp, "hello.md")
	if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(tmp)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := d.StartupSync(nil, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	// Second call should skip unchanged pages (hash-gated).
	if err := d.StartupSync(nil, srcIdx); err != nil {
		t.Fatalf("StartupSync (2nd): %v", err)
	}
}

// TestStartupSyncDoesNotDuplicateFTSRowForPageInBothIndexes is the
// public-API-level counterpart to the row-count assertions in
// internal_test.go's TestStartupSyncProducesOneRowPerLogicalPage (#475),
// matching the issue's own acceptance criterion verbatim: a page present in
// both the public and source index produces exactly one search hit. Note
// this alone would pass even with the pre-fix duplicate bug present — the
// source-keyed row is always published=0 and Search's own WHERE clause
// already excludes it — so it documents the expected external behavior
// rather than proving the fix; the internal test is what discriminates.
func TestStartupSyncDoesNotDuplicateFTSRowForPageInBothIndexes(t *testing.T) {
	d := openTestDB(t)

	contentRoot := t.TempDir()
	mdPath := filepath.Join(contentRoot, "hello.md")
	content := "---\ntitle: UniqueDuplicationTitle\n---\nBody.\n"
	if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
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
		Title: "UniqueDuplicationTitle",
		URL:   "https://example.test/hello/",
		Lang:  "en",
	})

	if err := d.StartupSync(siteIdx, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	results, err := d.Search("UniqueDuplicationTitle", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search(UniqueDuplicationTitle) = %d hits, want exactly 1 (one logical page, indexed once): %#v", len(results), results)
	}
	if results[0].Slug != "/hello/" {
		t.Fatalf("Search hit slug = %q, want the canonical public slug /hello/", results[0].Slug)
	}
}

func TestHashGatedSkip(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{Slug: "/stable/", Title: "Stable", URL: "https://x.com/stable/", Lang: "en"}

	// First sync writes to DB.
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("1st SyncPublicPage: %v", err)
	}
	// Second sync with identical data is a no-op (hash gate).
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("2nd SyncPublicPage: %v", err)
	}
	// Changed page invalidates cache.
	p.Title = "Stable (updated)"
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("3rd SyncPublicPage (updated): %v", err)
	}
	results, _ := d.Search("Stable updated", 10)
	if len(results) == 0 {
		t.Error("expected updated page to appear in FTS after title change")
	}
}

// TestPostBuildSyncPrunesStalePublishedPages guards against a reconciliation
// gap (#646): if a delete_page call's best-effort siteDB.DeletePage sync
// fails independently of the disk removal, the page vanishes from the
// sitemap but its row (and FTS entry) previously survived in the DB until
// the next process restart's StartupSync — PostBuildSync only ever upserted
// pages present in the sitemap and never pruned ones that disappeared.
// This test simulates that: sync a page while it's in the sitemap, then
// call PostBuildSync again with it removed, and assert the stale row (and
// its search hit) is gone without a restart.
func TestPostBuildSyncPrunesStalePublishedPages(t *testing.T) {
	d := openTestDB(t)

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:  "/going-away/",
		Title: "GoingAwayUniqueTitle",
		URL:   "https://example.test/going-away/",
		Lang:  "en",
	})

	if err := d.PostBuildSync(siteIdx); err != nil {
		t.Fatalf("PostBuildSync (1st): %v", err)
	}
	if results, _ := d.Search("GoingAwayUniqueTitle", 10); len(results) != 1 {
		t.Fatalf("expected page indexed after 1st PostBuildSync, got %d hits", len(results))
	}

	// Simulate delete_page's disk removal succeeding while its own
	// siteDB.DeletePage call failed: the page is gone from the sitemap, but
	// no DeletePage was ever issued for it directly.
	siteIdx.RemoveBySlug("/going-away/")

	if err := d.PostBuildSync(siteIdx); err != nil {
		t.Fatalf("PostBuildSync (2nd): %v", err)
	}

	results, err := d.Search("GoingAwayUniqueTitle", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected stale page pruned after next PostBuildSync, still got %d hits: %#v", len(results), results)
	}
}

func TestPruneMutationJournalIsTransactionalAndObservable(t *testing.T) {
	d := openTestDB(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for _, entry := range []db.MutationJournalEntry{
		{CallerKey: "caller", Tool: "create_page", Key: "expired", RequestHash: "a", ResultJSON: []byte(`{}`), CreatedAt: now.Add(-2 * time.Hour)},
		{CallerKey: "caller", Tool: "create_page", Key: "live", RequestHash: "b", ResultJSON: []byte(`{}`), CreatedAt: now.Add(-10 * time.Minute)},
	} {
		if err := d.RememberMutation(entry); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := d.PruneMutationJournal(time.Hour, now)
	if err != nil {
		t.Fatalf("PruneMutationJournal: %v", err)
	}
	if stats.ActiveEntries != 1 || stats.LastPrunedEntries != 1 || !stats.LastPrunedAt.Equal(now) {
		t.Fatalf("prune stats = %+v", stats)
	}
	if got, err := d.MutationJournalStats(); err != nil || got.ActiveEntries != 1 || got.LastPrunedEntries != 1 || !got.LastPrunedAt.Equal(now) {
		t.Fatalf("MutationJournalStats = %+v, %v", got, err)
	}
	if _, err := d.LookupMutation("caller", "create_page", "expired", 0); err != nil {
		t.Fatal(err)
	} else if entry, _ := d.LookupMutation("caller", "create_page", "live", 0); entry == nil {
		t.Fatal("live mutation was removed by prune")
	}
}

func TestPruneMutationJournalRollsBackWhenMaintenanceWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	if err := d.RememberMutation(db.MutationJournalEntry{CallerKey: "caller", Tool: "create_page", Key: "expired", RequestHash: "a", ResultJSON: []byte(`{}`), CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER reject_journal_maintenance BEFORE INSERT ON mutation_journal_maintenance BEGIN SELECT RAISE(ABORT, 'injected maintenance failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneMutationJournal(time.Hour, now); err == nil {
		t.Fatal("PruneMutationJournal succeeded despite injected maintenance failure")
	}
	entry, err := d.LookupMutation("caller", "create_page", "expired", 0)
	if err != nil || entry == nil {
		t.Fatalf("expired row was not rolled back after failed prune: entry=%+v err=%v", entry, err)
	}
}
