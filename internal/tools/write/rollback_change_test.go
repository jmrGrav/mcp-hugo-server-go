package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
