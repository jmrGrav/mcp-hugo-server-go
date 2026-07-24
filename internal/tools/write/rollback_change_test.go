package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

// TestRollbackChangeRestoresApplyContentPlanSnapshot is a regression test
// for the core #379-amended contract: rollback_change restores exactly the
// content apply_content_plan overwrote, guarded by expected_revision.
func TestRollbackChangeRestoresApplyContentPlanSnapshot(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	before := readFileString(t, contentRoot, "posts/article/index.md")

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	target := decodeWriteData(t, planRes)["target"].(map[string]any)
	beforeRevision := target["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)
	if rollbackData["status"] != "ok" {
		t.Fatalf("rollback_change status = %v, want ok", rollbackData["status"])
	}
	if rollbackData["after_revision"] != beforeRevision {
		t.Fatalf("rollback_change after_revision = %v, want %v", rollbackData["after_revision"], beforeRevision)
	}

	after := readFileString(t, contentRoot, "posts/article/index.md")
	if after != before {
		t.Fatalf("rollback_change did not restore original content:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestRollbackChangeUnknownRevisionIsSnapshotNotFound verifies a revision
// this server never snapshotted (arbitrary git history, or simply never
// produced by apply_content_plan) is rejected, not silently accepted.
func TestRollbackChangeUnknownRevisionIsSnapshotNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	currentRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       "sha256:never-existed",
		"expected_revision": currentRevision,
	})
	if !res.IsError {
		t.Fatal("rollback_change with an unknown revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "snapshot_not_found") {
		t.Fatalf("rollback_change unknown revision error = %s", raw)
	}
}

// TestRollbackChangeRevisionConflict verifies rollback_change refuses to
// undo a newer, unrelated change — the same optimistic-concurrency guard
// every other write tool uses.
func TestRollbackChangeRevisionConflict(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": "sha256:stale",
	})
	if !res.IsError {
		t.Fatal("rollback_change with a stale expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("rollback_change stale revision error = %s", raw)
	}
}

// TestRollbackChangeDryRunDoesNotWrite verifies dry_run previews the diff
// without touching disk or requiring expected_revision.
func TestRollbackChangeDryRunDoesNotWrite(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterContent := readFileString(t, contentRoot, "posts/article/index.md")

	dryRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":        "posts/article",
		"to_revision": beforeRevision,
		"dry_run":     true,
	})
	if dryRes.IsError {
		t.Fatalf("rollback_change dry_run failed: %s", marshalContent(t, dryRes))
	}
	dryData := decodeWriteData(t, dryRes)
	if dryData["dry_run"] != true {
		t.Fatalf("rollback_change dry_run response data.dry_run = %v, want true", dryData["dry_run"])
	}
	if diff, _ := dryData["diff"].(string); diff == "" {
		t.Fatal("rollback_change dry_run did not return a diff")
	}

	stillApplied := readFileString(t, contentRoot, "posts/article/index.md")
	if stillApplied != afterContent {
		t.Fatalf("rollback_change dry_run wrote to disk: before=%q after=%q", afterContent, stillApplied)
	}
}

// TestRollbackChangeIsRepeatable verifies rollback_change can roll back to
// the same snapshot more than once (IdempotentHint: true) — unlike a plan,
// a snapshot is not consumed on use.
func TestRollbackChangeIsRepeatable(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	firstRollback := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if firstRollback.IsError {
		t.Fatalf("first rollback_change failed: %s", marshalContent(t, firstRollback))
	}

	// Re-apply the same plan's change via a fresh plan, then roll back to
	// the same original beforeRevision a second time.
	planRes2 := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body again."}},
	})
	planID2 := decodeWriteData(t, planRes2)["plan_id"].(string)
	applyRes2 := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID2})
	if applyRes2.IsError {
		t.Fatalf("second apply_content_plan failed: %s", marshalContent(t, applyRes2))
	}
	afterApplyRevision2 := decodeWriteData(t, applyRes2)["after_revision"].(string)

	secondRollback := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision2,
	})
	if secondRollback.IsError {
		t.Fatalf("second rollback_change to the same snapshot failed: %s", marshalContent(t, secondRollback))
	}
}

// TestRollbackChangeBilingualIsPerLanguage is a regression test for the
// same bug class TestPlanContentChangeBilingualDeltaIsPerLanguage guards:
// rollback_change must never restore the wrong language's file. The
// snapshot store is keyed by the resolved file's own path (not a
// lang-blind slug lookup), so this is expected to pass — pinning it as a
// guard against a future change reintroducing that class of bug.
func TestRollbackChangeBilingualIsPerLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	enFile := filepath.Join(pageDir, "index.en.md")
	if err := os.WriteFile(frFile, []byte("---\ntitle: Titre\ntags: [\"francais\"]\n---\nContenu original.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enFile, []byte("---\ntitle: Title\ntags: [\"english\"]\n---\nOriginal content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/bilingual",
		"lang":       "fr",
		"operations": []any{map[string]any{"op": "update_body", "body": "Contenu modifie."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	enBeforeRollback := readFileString(t, contentRoot, "posts/bilingual/index.en.md")

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/bilingual",
		"lang":              "fr",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}

	frAfterRollback := readFileString(t, contentRoot, "posts/bilingual/index.fr.md")
	if !strings.Contains(frAfterRollback, "Contenu original") {
		t.Fatalf("fr file should be restored to its original body, got: %s", frAfterRollback)
	}
	if strings.Contains(frAfterRollback, "modifie") {
		t.Fatalf("fr file should no longer contain the rolled-back edit, got: %s", frAfterRollback)
	}

	enAfterRollback := readFileString(t, contentRoot, "posts/bilingual/index.en.md")
	if enAfterRollback != enBeforeRollback {
		t.Fatalf("en file must be untouched by a fr-scoped rollback:\nbefore=%q\nafter=%q", enBeforeRollback, enAfterRollback)
	}
}

// TestRollbackChangeRestoresUpdatePageSnapshot is a regression test for #629:
// before this fix, only apply_content_plan captured a snapshot, so a
// revision produced solely by update_page (with no plan_content_change /
// apply_content_plan ever involved) could never be rolled back to —
// rollback_change failed with snapshot_not_found, indistinguishable from
// "this revision never existed". create_page itself is deliberately not
// snapshotted (there's no meaningful pre-create state to restore to); this
// test exercises exactly the scenario the issue calls out: create a page,
// then update it once via update_page, then roll back to the revision
// update_page overwrote.
func TestRollbackChangeRestoresUpdatePageSnapshot(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/first",
		"title":      "First Post",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}
	createData := decodeWriteData(t, createRes)
	beforeUpdateRevision := createData["new_revision"].(string)
	before := readFileString(t, contentRoot, "posts/first/index.md")

	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/first",
		"body":              "Updated body.",
		"expected_revision": beforeUpdateRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	updateData := decodeWriteData(t, updateRes)
	afterUpdateRevision := updateData["new_revision"].(string)

	updated := readFileString(t, contentRoot, "posts/first/index.md")
	if !strings.Contains(updated, "Updated body.") {
		t.Fatalf("update_page did not apply, got: %s", updated)
	}

	// This is the case the issue's title names directly: to_revision here
	// (beforeUpdateRevision) was produced by create_page and only ever
	// overwritten by update_page — apply_content_plan was never called for
	// this page. Before the fix, this failed with snapshot_not_found.
	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/first",
		"to_revision":       beforeUpdateRevision,
		"expected_revision": afterUpdateRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)
	if rollbackData["status"] != "ok" {
		t.Fatalf("rollback_change status = %v, want ok", rollbackData["status"])
	}
	if rollbackData["after_revision"] != beforeUpdateRevision {
		t.Fatalf("rollback_change after_revision = %v, want %v", rollbackData["after_revision"], beforeUpdateRevision)
	}

	after := readFileString(t, contentRoot, "posts/first/index.md")
	if after != before {
		t.Fatalf("rollback_change did not restore pre-update content:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestRollbackChangeRejectsSnapshotWithBlockedShortcode is a regression
// test for a strix-security finding on PR #636: extending snapshot capture
// to update_page's primary write path expanded rollback_change's reach to
// legacy content that predates (or otherwise bypassed) #590's blocked-
// shortcode denylist. A snapshot is a verbatim copy of whatever content the
// page held before the write that produced it — create_page/update_page
// reject a body invoking a blocked shortcode outright, but restoring a
// snapshot of already-existing content must not be a side door around that
// same policy.
func TestRollbackChangeRejectsSnapshotWithBlockedShortcode(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/legacy",
		"title":      "Legacy Post",
		"body":       "Clean original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	// Simulate legacy content that already contains a shortcode this
	// server would now reject on a direct write — e.g. written before
	// #590's denylist existed, or via a path outside this server's own
	// validation. create_page/update_page can't produce this directly
	// (that's the whole point of the check being tested), so it's written
	// straight to disk. The revision is recomputed from the mutated bytes
	// so the following update_page's expected_revision check sees the
	// real on-disk state, not the pre-mutation revision create_page
	// returned.
	pagePath := filepath.Join(contentRoot, "posts/legacy/index.md")
	legacyRaw := readFileString(t, contentRoot, "posts/legacy/index.md")
	legacyRaw = strings.Replace(legacyRaw, "Clean original body.", "Clean original body.\n\n{{< script >}}alert(1){{< /script >}}", 1)
	if err := os.WriteFile(pagePath, []byte(legacyRaw), 0o644); err != nil {
		t.Fatalf("failed to write legacy content directly: %v", err)
	}
	beforeUpdateRevision := contentmodel.SourceRevisionBytes([]byte(legacyRaw))

	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/legacy",
		"body":              "New clean body.",
		"expected_revision": beforeUpdateRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	afterUpdateRevision := decodeWriteData(t, updateRes)["new_revision"].(string)

	// beforeUpdateRevision now points at a snapshot containing the blocked
	// shortcode. Restoring it must be rejected, not silently written.
	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/legacy",
		"to_revision":       beforeUpdateRevision,
		"expected_revision": afterUpdateRevision,
	})
	if !rollbackRes.IsError {
		t.Fatal("rollback_change restoring a snapshot with a blocked shortcode should fail")
	}
	raw := marshalContent(t, rollbackRes)
	if !strings.Contains(raw, "blocked shortcode") {
		t.Fatalf("rollback_change error = %s, want a blocked-shortcode rejection", raw)
	}

	// The rejected rollback must never have touched disk.
	stillNew := readFileString(t, contentRoot, "posts/legacy/index.md")
	if !strings.Contains(stillNew, "New clean body.") {
		t.Fatalf("rejected rollback_change modified the file on disk: %q", stillNew)
	}
}

// TestRollbackChangeUpdatesInMemorySourceIndexBody is a regression test for
// #643: rollback_change wrote the restored content to disk correctly, but
// never reassigned the in-memory SourceIndex entry's Body field — only
// Tags/Categories/Title/Revision were updated on the upserted entry. Every
// tool reading a page's body from the source index before the next full
// index rebuild (get_page_markdown in particular) kept serving the
// pre-rollback body as a result, with no staleness signal (index_state
// still reported "fresh"). This asserts the fix directly against the index
// entry rollback_change updates, the same one get_page_markdown reads from.
func TestRollbackChangeUpdatesInMemorySourceIndexBody(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	originalEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after writeBundle")
	}
	originalBody := originalEntry.Body

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body for #643 regression test."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	target := decodeWriteData(t, planRes)["target"].(map[string]any)
	beforeRevision := target["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	changedEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after apply_content_plan")
	}
	if !strings.Contains(changedEntry.Body, "Changed body for #643 regression test.") {
		t.Fatalf("source index Body not updated by apply_content_plan: %q", changedEntry.Body)
	}

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}

	restoredEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after rollback_change")
	}
	if restoredEntry.Body != originalBody {
		t.Fatalf("source index Body not restored by rollback_change:\nwant=%q\ngot=%q", originalBody, restoredEntry.Body)
	}
	if strings.Contains(restoredEntry.Body, "Changed body for #643 regression test.") {
		t.Fatal("source index Body still contains post-apply content after rollback_change — #643 regression")
	}
}
