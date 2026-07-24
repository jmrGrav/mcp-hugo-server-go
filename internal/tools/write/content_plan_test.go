package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFileString(t *testing.T, contentRoot, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(contentRoot, relPath))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", relPath, err)
	}
	return string(data)
}

func TestPlanContentChangeAndApplyRoundTrip(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "add_tag", "value": "hugo"},
			map[string]any{"op": "update_body", "body": "New body."},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planData := decodeWriteData(t, planRes)
	planID, _ := planData["plan_id"].(string)
	if planID == "" {
		t.Fatalf("plan_content_change did not return plan_id: %v", planData)
	}
	applied, _ := planData["operations_applied"].([]any)
	if len(applied) != 2 {
		t.Fatalf("plan_content_change operations_applied = %v, want 2 entries", planData["operations_applied"])
	}
	if diff, _ := planData["diff"].(string); diff == "" {
		t.Fatal("plan_content_change did not return a diff")
	}

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	applyData := decodeWriteData(t, applyRes)
	if applyData["status"] != "ok" {
		t.Fatalf("apply_content_plan status = %v, want ok", applyData["status"])
	}
	if applyData["after_revision"] == "" || applyData["after_revision"] == nil {
		t.Fatal("apply_content_plan did not return after_revision")
	}

	written := readFileString(t, contentRoot, "posts/article/index.md")
	if !strings.Contains(written, "New body.") {
		t.Fatalf("apply_content_plan did not write the planned body: %q", written)
	}
	if !strings.Contains(written, "hugo") {
		t.Fatalf("apply_content_plan did not write the planned tag: %q", written)
	}
}

// TestApplyContentPlanUnknownPlanID is a regression test for #338/#340's
// design: a missing/expired/already-applied plan_id must fail with
// plan_not_found, distinguishing it from other error classes.
func TestApplyContentPlanUnknownPlanID(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": "plan_does_not_exist"})
	if !res.IsError {
		t.Fatal("apply_content_plan with unknown plan_id should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "plan_not_found") {
		t.Fatalf("apply_content_plan unknown plan_id error = %s", raw)
	}
}

// TestApplyContentPlanIsSingleUse is a regression test for the design doc's
// single-use invariant: applying a plan (successfully or not) removes it, so
// it can never be replayed against a page that has since moved on without a
// fresh plan_content_change call.
func TestApplyContentPlanIsSingleUse(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "add_tag", "value": "hugo"}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)

	first := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if first.IsError {
		t.Fatalf("first apply_content_plan failed: %s", marshalContent(t, first))
	}

	second := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if !second.IsError {
		t.Fatal("second apply_content_plan with the same plan_id should fail (single-use)")
	}
	raw := marshalContent(t, second)
	if !strings.Contains(raw, "plan_not_found") {
		t.Fatalf("second apply_content_plan error = %s", raw)
	}
}

// TestApplyContentPlanRevisionConflict is a regression test for the design
// doc's core invariant (§3 step 3): a plan is a promise conditioned on a
// specific starting revision, and apply must re-verify that promise still
// holds even if the plan itself is otherwise valid and unexpired.
func TestApplyContentPlanRevisionConflict(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "add_tag", "value": "hugo"}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)

	// Mutate the page out from under the plan via a normal update_page call.
	getPlanTarget := planData["target"].(map[string]any)
	revision := getPlanTarget["revision"].(string)
	mutate := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/article",
		"title":             "Changed Elsewhere",
		"expected_revision": revision,
	})
	if mutate.IsError {
		t.Fatalf("update_page setup mutation failed: %s", marshalContent(t, mutate))
	}

	res := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if !res.IsError {
		t.Fatal("apply_content_plan against a stale plan should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("apply_content_plan stale plan error = %s", raw)
	}

	// The plan should have been consumed even though the apply failed.
	retry := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if !retry.IsError || !strings.Contains(marshalContent(t, retry), "plan_not_found") {
		t.Fatalf("plan should be consumed after a failed apply attempt, got: %s", marshalContent(t, retry))
	}
}

// TestApplyContentPlanDryRunDoesNotConsumePlan verifies dry_run re-verifies
// without writing or consuming the plan, unlike a real apply attempt.
func TestApplyContentPlanDryRunDoesNotConsumePlan(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "add_tag", "value": "hugo"}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	dryRun := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID, "dry_run": true})
	if dryRun.IsError {
		t.Fatalf("apply_content_plan dry_run failed: %s", marshalContent(t, dryRun))
	}
	dryData := decodeWriteData(t, dryRun)
	if dryData["dry_run"] != true {
		t.Fatalf("apply_content_plan dry_run response data.dry_run = %v, want true", dryData["dry_run"])
	}

	real := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if real.IsError {
		t.Fatalf("apply_content_plan after dry_run failed (plan should still exist): %s", marshalContent(t, real))
	}
}

// TestPlanContentChangeReportsRejectedOperations is a regression test for
// the design doc's operations_rejected contract: an operation that doesn't
// apply cleanly (removing a tag the page doesn't have) is reported without
// failing the whole plan.
func TestPlanContentChangeReportsRejectedOperations(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "remove_tag", "value": "does-not-exist"},
			map[string]any{"op": "set_title", "value": "New Title"},
		},
	})
	if res.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	rejected, _ := data["operations_rejected"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("operations_rejected = %v, want 1 entry", data["operations_rejected"])
	}
	applied, _ := data["operations_applied"].([]any)
	if len(applied) != 1 {
		t.Fatalf("operations_applied = %v, want 1 entry (set_title)", data["operations_applied"])
	}
}

// TestPlanContentChangeUnknownOperation is a regression test ensuring the
// operation vocabulary stays deliberately small (docs/transactional-edit-
// design.md's non-goals: no general JSON-patch/arbitrary-field operation).
func TestPlanContentChangeUnknownOperation(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "set_arbitrary_field", "field": "layout", "value": "x"}},
	})
	if !res.IsError {
		t.Fatal("plan_content_change with an unknown operation should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("plan_content_change unknown op error = %s", raw)
	}
}

// TestPlanContentChangeBilingualDeltaIsPerLanguage is a regression test:
// add_tag/remove_tag must compute their delta against the *resolved
// language's* current tags, never a different language's file sharing the
// same slug. Before this fix, the delta was read via idx.GetBySlug (not
// language-aware), so planning against the fr file could compute a delta
// from the en file's tags and then overwrite the fr file's tags with an
// en-derived list.
func TestPlanContentChangeBilingualDeltaIsPerLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	enFile := filepath.Join(pageDir, "index.en.md")
	if err := os.WriteFile(frFile, []byte("---\ntitle: Titre\ntags: [\"francais\"]\n---\nContenu.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enFile, []byte("---\ntitle: Title\ntags: [\"english\"]\n---\nContent.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/bilingual",
		"lang":       "fr",
		"operations": []any{map[string]any{"op": "add_tag", "value": "nouveau"}},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	frContent := readFileString(t, contentRoot, "posts/bilingual/index.fr.md")
	if !strings.Contains(frContent, "francais") || !strings.Contains(frContent, "nouveau") {
		t.Fatalf("fr file should keep its original tag and gain the new one, got: %s", frContent)
	}

	enContent := readFileString(t, contentRoot, "posts/bilingual/index.en.md")
	if !strings.Contains(enContent, "english") {
		t.Fatalf("en file's tags must be untouched, got: %s", enContent)
	}
	if strings.Contains(enContent, "francais") || strings.Contains(enContent, "nouveau") {
		t.Fatalf("en file must not gain fr-side tags, got: %s", enContent)
	}
}

// TestPlanContentChangeDoesNotWrite verifies plan_content_change never
// touches disk, however many operations it's given.
func TestPlanContentChangeDoesNotWrite(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	before := readFileString(t, contentRoot, "posts/article/index.md")

	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "update_body", "body": "Should never be written."},
			map[string]any{"op": "set_title", "value": "Should never be written either"},
		},
	})
	if res.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, res))
	}

	after := readFileString(t, contentRoot, "posts/article/index.md")
	if before != after {
		t.Fatalf("plan_content_change wrote to disk: before=%q after=%q", before, after)
	}
}
