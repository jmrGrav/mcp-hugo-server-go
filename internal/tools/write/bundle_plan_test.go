package write_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

// writeBilingualBundle creates a content/<slug>/ page bundle with two
// translations (index.fr.md + index.en.md) — the editorial unit #854's
// bundle transactions operate on.
func writeBilingualBundle(t *testing.T, contentRoot, slug string) {
	t.Helper()
	dir := filepath.Join(contentRoot, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.fr.md"), []byte("---\ntitle: Article FR\n---\nCorps FR.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.en.md"), []byte("---\ntitle: Article EN\n---\nBody EN.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bodyOp(body string) map[string]any {
	return map[string]any{"op": "update_body", "body": body}
}

// TestBundleApplyFRENSuccess — AC5 case 1: FR+EN update as one bundle applies
// both translations, reporting per-translation outcomes plus one bundle status.
func TestBundleApplyFRENSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Nouveau corps FR.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("New body EN.")}},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_bundle_change failed: %s", marshalContent(t, planRes))
	}
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	if len(planData["translations"].([]any)) != 2 {
		t.Fatalf("plan translations = %v, want 2", planData["translations"])
	}

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_bundle_plan failed: %s", marshalContent(t, applyRes))
	}
	applyData := decodeWriteData(t, applyRes)
	if applyData["bundle_status"] != "applied" {
		t.Fatalf("bundle_status = %v, want applied", applyData["bundle_status"])
	}
	if got := len(applyData["translations"].([]any)); got != 2 {
		t.Fatalf("apply translations = %d, want 2", got)
	}
	if got := readFileString(t, contentRoot, "posts/example/index.fr.md"); !strings.Contains(got, "Nouveau corps FR.") {
		t.Fatalf("fr file not updated: %q", got)
	}
	if got := readFileString(t, contentRoot, "posts/example/index.en.md"); !strings.Contains(got, "New body EN.") {
		t.Fatalf("en file not updated: %q", got)
	}
}

// TestApplyBundlePlanDryRunProjectsOutcomesWithoutWriting covers the dry-run
// branch of apply_bundle_plan: it must report the same per-translation
// outcome shape a real apply would (via bundleDryRunOutcomes), including
// each source path, without touching the plan or the filesystem.
func TestApplyBundlePlanDryRunProjectsOutcomesWithoutWriting(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/dry-run-bundle")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/dry-run-bundle",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR previsualise.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("Previewed EN body.")}},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_bundle_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	dryRun := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID, "dry_run": true})
	if dryRun.IsError {
		t.Fatalf("dry-run apply_bundle_plan failed: %s", marshalContent(t, dryRun))
	}
	dryData := decodeWriteData(t, dryRun)
	if dryData["status"] != "unchanged" {
		t.Fatalf("dry-run status = %v, want unchanged", dryData["status"])
	}
	translations := dryData["translations"].([]any)
	if len(translations) != 2 {
		t.Fatalf("dry-run translations = %d, want 2", len(translations))
	}
	for _, raw := range translations {
		tr := raw.(map[string]any)
		if tr["status"] != "valid" {
			t.Fatalf("dry-run translation status = %v, want valid: %#v", tr["status"], tr)
		}
		if src, _ := tr["source_path"].(string); src == "" {
			t.Fatalf("dry-run translation missing source_path: %#v", tr)
		}
	}
	if got := readFileString(t, contentRoot, "posts/dry-run-bundle/index.fr.md"); strings.Contains(got, "previsualise") {
		t.Fatalf("dry-run must not write FR file: %q", got)
	}
	if got := readFileString(t, contentRoot, "posts/dry-run-bundle/index.en.md"); strings.Contains(got, "Previewed") {
		t.Fatalf("dry-run must not write EN file: %q", got)
	}

	// The plan must still be consumable for a real apply after a dry-run.
	apply := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if apply.IsError {
		t.Fatalf("apply after dry-run failed: %s", marshalContent(t, apply))
	}
	if got := readFileString(t, contentRoot, "posts/dry-run-bundle/index.fr.md"); !strings.Contains(got, "previsualise") {
		t.Fatalf("real apply after dry-run did not write FR file: %q", got)
	}
}

func TestRollbackBundleRestoresPersistedSnapshotAfterRestart(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/restart-bundle")
	beforeFR := readFileString(t, contentRoot, "posts/restart-bundle/index.fr.md")
	beforeEN := readFileString(t, contentRoot, "posts/restart-bundle/index.en.md")
	siteDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()
	first, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	plan := callTool(t, first, "plan_bundle_change", map[string]any{
		"slug": "posts/restart-bundle",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("FR after")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("EN after")}},
		},
	})
	if plan.IsError {
		t.Fatalf("plan failed: %s", marshalContent(t, plan))
	}
	apply := callTool(t, first, "apply_bundle_plan", map[string]any{"plan_id": decodeWriteData(t, plan)["plan_id"]})
	if apply.IsError {
		t.Fatalf("apply failed: %s", marshalContent(t, apply))
	}
	applyData := decodeWriteData(t, apply)
	beforeRevision := applyData["before_revision"].(string)
	afterRevision := applyData["after_revision"].(string)
	done()

	restarted, _, restartedDone := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer restartedDone()
	rollback := callTool(t, restarted, "rollback_bundle", map[string]any{
		"slug": "posts/restart-bundle", "to_bundle_revision": beforeRevision, "expected_bundle_revision": afterRevision,
	})
	if rollback.IsError {
		t.Fatalf("rollback after restart failed: %s", marshalContent(t, rollback))
	}
	if got := readFileString(t, contentRoot, "posts/restart-bundle/index.fr.md"); got != beforeFR {
		t.Fatalf("FR after restart rollback = %q, want %q", got, beforeFR)
	}
	if got := readFileString(t, contentRoot, "posts/restart-bundle/index.en.md"); got != beforeEN {
		t.Fatalf("EN after restart rollback = %q, want %q", got, beforeEN)
	}
	restartedDone()
	// The rollback itself captured B before restoring A. A second fresh
	// runtime must be able to use that durable pre-rollback snapshot to move
	// atomically back to B.
	again, _, againDone := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer againDone()
	backToB := callTool(t, again, "rollback_bundle", map[string]any{
		"slug": "posts/restart-bundle", "to_bundle_revision": afterRevision, "expected_bundle_revision": beforeRevision,
	})
	if backToB.IsError {
		t.Fatalf("reverse rollback after restart failed: %s", marshalContent(t, backToB))
	}
	if got := readFileString(t, contentRoot, "posts/restart-bundle/index.fr.md"); !strings.Contains(got, "FR after") {
		t.Fatalf("FR reverse rollback = %q, want B", got)
	}
	if got := readFileString(t, contentRoot, "posts/restart-bundle/index.en.md"); !strings.Contains(got, "EN after") {
		t.Fatalf("EN reverse rollback = %q, want B", got)
	}
}

// TestBundlePlanValidationFailureRejectsWholeBundle — AC5 case 2: one
// translation failing validation rejects the whole bundle; NO file changes.
func TestBundlePlanValidationFailureRejectsWholeBundle(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	beforeFR := readFileString(t, contentRoot, "posts/example/index.fr.md")
	beforeEN := readFileString(t, contentRoot, "posts/example/index.en.md")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// EN carries a blocked shortcode; validation must fail the whole plan.
	res := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR valide.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("Bad {{< script >}} body.")}},
		},
	})
	if !res.IsError {
		t.Fatalf("plan_bundle_change with a blocked shortcode should fail, got: %s", marshalContent(t, res))
	}
	if afterFR := readFileString(t, contentRoot, "posts/example/index.fr.md"); afterFR != beforeFR {
		t.Fatalf("fr file changed despite whole-bundle rejection:\nbefore=%q\nafter=%q", beforeFR, afterFR)
	}
	if afterEN := readFileString(t, contentRoot, "posts/example/index.en.md"); afterEN != beforeEN {
		t.Fatalf("en file changed despite whole-bundle rejection:\nbefore=%q\nafter=%q", beforeEN, afterEN)
	}
}

// TestPlanBundleChangeRejectsOverlongTag is a regression test for #904:
// plan_bundle_change shares resolvePlanOperations with plan_content_change,
// so the same fix (validateTaxonomyTerms on add_tag/add_category) covers
// both in one place. Confirms the whole bundle is rejected, not just the
// offending translation.
func TestPlanBundleChangeRejectsOverlongTag(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	over := strings.Repeat("a", 101)
	res := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{map[string]any{"op": "add_tag", "value": over}}},
		},
	})
	if !res.IsError {
		t.Fatal("plan_bundle_change: want error for overlong tag, got success")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("plan_bundle_change overlong-tag error = %s, want invalid_params", raw)
	}
}

// TestPlanBundleChangeCannotCreateReservedSlug mirrors
// TestPlanContentChangeCannotCreateReservedSlug (#904): plan_bundle_change
// rejects a nonexistent bundle slug with not_a_bundle/not_found before any
// operation runs, so — like plan_content_change — it has no path to create
// a brand-new page at a reserved slug the way create_page's #890 denylist
// guards against.
func TestPlanBundleChangeCannotCreateReservedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "404",
		"translations": []any{
			map[string]any{"lang": "en", "operations": []any{map[string]any{"op": "add_tag", "value": "x"}}},
		},
	})
	if !res.IsError {
		t.Fatal("plan_bundle_change: want error targeting nonexistent reserved-looking slug, got success")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "not_a_bundle") {
		t.Fatalf("plan_bundle_change on nonexistent slug %q = %s, want not_a_bundle (proving no slug-creation surface exists to bypass #890's denylist)", "404", raw)
	}
}

// TestBundleApplyInterruptionRollsBack — AC5 case 3: an interruption partway
// through the apply (simulated write failure at the second translation) leaves
// NO partial state — the first, already-written translation is rolled back.
func TestBundleApplyInterruptionRollsBack(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	beforeFR := readFileString(t, contentRoot, "posts/example/index.fr.md")
	beforeEN := readFileString(t, contentRoot, "posts/example/index.en.md")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR modifie.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("Modified body EN.")}},
		},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	// Fail the write of the SECOND translation (index 1) after the first has
	// already been written, exercising the in-process rollback path.
	restore := write.SetApplyBundleWriteHook(func(index int) error {
		if index == 1 {
			return fmt.Errorf("injected interruption")
		}
		return nil
	})
	defer restore()

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if !applyRes.IsError {
		t.Fatalf("apply_bundle_plan should fail on injected interruption, got: %s", marshalContent(t, applyRes))
	}
	if afterFR := readFileString(t, contentRoot, "posts/example/index.fr.md"); afterFR != beforeFR {
		t.Fatalf("fr file left partially applied after interruption:\nbefore=%q\nafter=%q", beforeFR, afterFR)
	}
	if afterEN := readFileString(t, contentRoot, "posts/example/index.en.md"); afterEN != beforeEN {
		t.Fatalf("en file changed after interruption:\nbefore=%q\nafter=%q", beforeEN, afterEN)
	}
}

// TestRollbackBundleRestoresAllTranslations — AC5 case 4: after a successful
// bundle apply, rollback_bundle restores every translation atomically.
func TestRollbackBundleRestoresAllTranslations(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	beforeFR := readFileString(t, contentRoot, "posts/example/index.fr.md")
	beforeEN := readFileString(t, contentRoot, "posts/example/index.en.md")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR v2.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("Body EN v2.")}},
		},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_bundle_plan failed: %s", marshalContent(t, applyRes))
	}
	applyData := decodeWriteData(t, applyRes)
	preApplyRev := applyData["before_revision"].(string)
	postApplyRev := applyData["after_revision"].(string)

	rbRes := callTool(t, session, "rollback_bundle", map[string]any{
		"slug":                     "posts/example",
		"to_bundle_revision":       preApplyRev,
		"expected_bundle_revision": postApplyRev,
	})
	if rbRes.IsError {
		t.Fatalf("rollback_bundle failed: %s", marshalContent(t, rbRes))
	}
	if decodeWriteData(t, rbRes)["bundle_status"] != "restored" {
		t.Fatalf("rollback bundle_status = %v, want restored", decodeWriteData(t, rbRes)["bundle_status"])
	}
	if afterFR := readFileString(t, contentRoot, "posts/example/index.fr.md"); afterFR != beforeFR {
		t.Fatalf("fr not restored:\nbefore=%q\nafter=%q", beforeFR, afterFR)
	}
	if afterEN := readFileString(t, contentRoot, "posts/example/index.en.md"); afterEN != beforeEN {
		t.Fatalf("en not restored:\nbefore=%q\nafter=%q", beforeEN, afterEN)
	}
}

// TestBundleApplyIdempotentReplay — AC3 (idempotency guarantee): replaying an
// apply_bundle_plan with the same idempotency_key returns the cached applied
// result rather than plan_not_found, even though the plan is consumed on the
// first apply. Confirms the replay check sits before plan consumption.
func TestBundleApplyIdempotentReplay(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR idem.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("Body EN idem.")}},
		},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	first := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID, "idempotency_key": "k1"})
	if first.IsError {
		t.Fatalf("first apply failed: %s", marshalContent(t, first))
	}
	firstAfter := decodeWriteData(t, first)["after_revision"]

	second := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID, "idempotency_key": "k1"})
	if second.IsError {
		t.Fatalf("idempotent replay should return cached result, not error: %s", marshalContent(t, second))
	}
	secondData := decodeWriteData(t, second)
	if secondData["bundle_status"] != "applied" {
		t.Fatalf("replay bundle_status = %v, want applied", secondData["bundle_status"])
	}
	if secondData["after_revision"] != firstAfter {
		t.Fatalf("replay after_revision = %v, want cached %v", secondData["after_revision"], firstAfter)
	}
}

// TestBundleApplyConflictRejectsWholeBundle — AC2/AC3: if the bundle changed
// since the plan (BundleRevision mismatch), the whole apply fails, unapplied.
func TestBundleApplyConflictRejectsWholeBundle(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/example",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Corps FR conflit.")}},
		},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	// Mutate a bundle file out-of-band after the plan, changing BundleRevision.
	if err := os.WriteFile(filepath.Join(contentRoot, "posts/example/index.en.md"),
		[]byte("---\ntitle: Article EN edited\n---\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeFR := readFileString(t, contentRoot, "posts/example/index.fr.md")

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if !applyRes.IsError {
		t.Fatalf("apply_bundle_plan should fail with bundle_conflict, got: %s", marshalContent(t, applyRes))
	}
	if !strings.Contains(marshalContent(t, applyRes), "bundle_conflict") {
		t.Fatalf("expected bundle_conflict, got: %s", marshalContent(t, applyRes))
	}
	if afterFR := readFileString(t, contentRoot, "posts/example/index.fr.md"); afterFR != beforeFR {
		t.Fatalf("fr file changed despite bundle_conflict rejection")
	}

	// request_context must carry the slug the plan resolved (#1001).
	m := decodeWriteContent(t, applyRes)
	reqCtx, ok := m["request_context"].(map[string]any)
	if !ok {
		t.Fatalf("request_context type = %T, want populated object", m["request_context"])
	}
	if got := reqCtx["slug"]; got != "posts/example" {
		t.Fatalf("request_context.slug = %v, want posts/example", got)
	}

	// A retryable bundle_conflict must leave the plan available for the
	// caller to inspect/retry or explicitly replace (#1001), mirroring
	// apply_content_plan's own fix.
	retry := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if !retry.IsError || !strings.Contains(marshalContent(t, retry), "bundle_conflict") {
		t.Fatalf("retryable bundle_conflict should preserve the plan, got: %s", marshalContent(t, retry))
	}
}

// TestPlanBundleChangeRejectsLeafPage documents the scope-down decision: leaf
// multilingual pages (no bundle directory) are rejected with not_a_bundle.
func TestPlanBundleChangeRejectsLeafPage(t *testing.T) {
	contentRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(contentRoot, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts/leaf.fr.md"), []byte("---\ntitle: Leaf\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug":         "posts/leaf",
		"translations": []any{map[string]any{"lang": "fr", "operations": []any{bodyOp("x")}}},
	})
	if !res.IsError {
		t.Fatalf("plan_bundle_change on a leaf page should fail")
	}
	if !strings.Contains(marshalContent(t, res), "not_a_bundle") {
		t.Fatalf("expected not_a_bundle, got: %s", marshalContent(t, res))
	}
}

// TestApplyBundlePlanSurvivesDerivedDBSyncFailureWithWarning exercises
// indexBundleTranslation's siteDB.SyncSourcePage soft-degrade branch: the
// bundle files and recovery journal already succeeded, so a subsequent
// per-translation failure to sync the derived DB must downgrade the bundle
// status to partial_success with a warning rather than fail the whole apply.
func TestApplyBundlePlanSurvivesDerivedDBSyncFailureWithWarning(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/bundle-derived-db-warning")
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	siteDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer siteDB.Close()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/bundle-derived-db-warning",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Nouveau corps FR.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("New body EN.")}},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_bundle_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	dropPagesTable(t, dbPath)

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_bundle_plan must survive a derived-DB sync failure, got error: %s", marshalContent(t, applyRes))
	}
	data := decodeWriteData(t, applyRes)
	if data["status"] != "partial_success" {
		t.Fatalf("apply_bundle_plan status = %v, want partial_success", data["status"])
	}
	warning, _ := data["warning"].(string)
	if !strings.Contains(warning, "derived DB could not be updated") {
		t.Fatalf("apply_bundle_plan warning = %q, want derived-DB warning", warning)
	}
}

// TestApplyBundlePlanSurvivesPostWriteConsumeFailureWithWarning is the
// apply_bundle_plan analogue of
// TestApplyContentPlanSurvivesPostWriteConsumeFailureWithWarning: the bundle
// write and recovery journal already succeeded by the time plans.consume()
// runs, so a failure to free the plan slot afterward must downgrade to
// partial_success with a warning, not fail the whole apply.
func TestApplyBundlePlanSurvivesPostWriteConsumeFailureWithWarning(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/bundle-consume-warning")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/bundle-consume-warning",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{bodyOp("Nouveau corps FR.")}},
			map[string]any{"lang": "en", "operations": []any{bodyOp("New body EN.")}},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_bundle_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	restore := write.SetPlanConsumeFailureHook(func(tool string) error {
		if tool == "apply_bundle_plan" {
			return errors.New("injected plan consumption failure")
		}
		return nil
	})
	defer restore()

	applyRes := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_bundle_plan must survive a post-write consume failure, got error: %s", marshalContent(t, applyRes))
	}
	data := decodeWriteData(t, applyRes)
	if data["status"] != "partial_success" {
		t.Fatalf("apply_bundle_plan status = %v, want partial_success", data["status"])
	}
	warning, _ := data["warning"].(string)
	if !strings.Contains(warning, "plan consumption could not be persisted") {
		t.Fatalf("apply_bundle_plan warning = %q, want plan-consumption warning", warning)
	}
	if got := readFileString(t, contentRoot, "posts/bundle-consume-warning/index.fr.md"); !strings.Contains(got, "Nouveau corps FR.") {
		t.Fatalf("apply_bundle_plan did not apply fr write despite consume failure: %q", got)
	}
}
