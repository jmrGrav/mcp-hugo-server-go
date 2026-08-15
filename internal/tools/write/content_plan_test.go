package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
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
	if applyData["status"] != "updated" {
		t.Fatalf("apply_content_plan status = %v, want updated", applyData["status"])
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

func TestContentPlanSurvivesRestartAndApplies(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/restart-content-plan")
	journal, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	first, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: journal})
	plan := callTool(t, first, "plan_content_change", map[string]any{
		"slug": "posts/restart-content-plan", "operations": []any{bodyOp("Applied after restart")},
	})
	if plan.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, plan))
	}
	planID := decodeWriteData(t, plan)["plan_id"].(string)
	done()

	restarted, _, restartedDone := newTestServer(t, contentRoot, testServerOpts{SiteDB: journal})
	defer restartedDone()
	applied := callTool(t, restarted, "apply_content_plan", map[string]any{"plan_id": planID})
	if applied.IsError {
		t.Fatalf("apply_content_plan after restart failed: %s", marshalContent(t, applied))
	}
	if got := readFileString(t, contentRoot, "posts/restart-content-plan/index.md"); !strings.Contains(got, "Applied after restart") {
		t.Fatalf("restarted content plan did not update page: %q", got)
	}
}

// TestPlanContentChangeRejectsOverlongTag is a regression test for #904:
// create_page/update_page reject a tag exceeding maxTaxonomyTermRunes
// (#886), but plan_content_change's add_tag operation bypassed that check
// entirely. Rejected at plan time (fail-fast), not deferred to apply.
func TestPlanContentChangeRejectsOverlongTag(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	over := strings.Repeat("a", 101)
	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "add_tag", "value": over},
		},
	})
	if !res.IsError {
		t.Fatal("plan_content_change: want error for overlong tag, got success")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("plan_content_change overlong-tag error = %s, want invalid_params", raw)
	}
}

// TestPlanContentChangeRejectsOverlongCategory mirrors
// TestPlanContentChangeRejectsOverlongTag for add_category (#904).
func TestPlanContentChangeRejectsOverlongCategory(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	over := strings.Repeat("a", 101)
	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "add_category", "value": over},
		},
	})
	if !res.IsError {
		t.Fatal("plan_content_change: want error for overlong category, got success")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("plan_content_change overlong-category error = %s, want invalid_params", raw)
	}
}

// TestPlanContentChangeCannotCreateReservedSlug is a regression/confirmation
// test for #904's second acceptance criterion: plan_content_change can only
// target an *existing* page (resolveExistingSource), so there is no path by
// which a plan could create a brand-new page at a reserved slug like `404`
// or `_index` the way create_page's reservedSlugConflict check (#890)
// guards against. This documents that the reserved-slug half of #904's
// original claim does not reproduce (confirmed during PR4's review) —
// plan_content_change simply has no slug-creation surface to bypass.
func TestPlanContentChangeCannotCreateReservedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "404",
		"operations": []any{
			map[string]any{"op": "add_tag", "value": "x"},
		},
	})
	if !res.IsError {
		t.Fatal("plan_content_change: want error targeting nonexistent reserved-looking slug, got success")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "not_found") {
		t.Fatalf("plan_content_change on nonexistent slug %q = %s, want not_found (proving no slug-creation surface exists to bypass #890's denylist)", "404", raw)
	}
}

// TestApplyContentPlanSetFieldDescriptionRefreshesInMemoryIndex (#810) is a
// regression test for the exact scenario that surfaced this bug in
// production: plan_content_change + apply_content_plan's set_field
// operation (currently the only way to set description) wrote the
// description to disk correctly, but the in-memory SourceIndex entry's
// FrontmatterRaw kept its old value until a full reindex — so
// check_ai_readiness / get_page_for_edit's readiness block reported
// description_present:false immediately after a successful apply.
func TestApplyContentPlanSetFieldDescriptionRefreshesInMemoryIndex(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	if existing, ok := idx.GetBySlug("posts/article"); !ok || existing.FrontmatterRaw["description"] != nil {
		t.Fatalf("expected no description in FrontmatterRaw before the plan, got %#v", existing)
	}

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/article",
		"operations": []any{
			map[string]any{"op": "set_field", "field": "description", "value": "A real description."},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID, _ := decodeWriteData(t, planRes)["plan_id"].(string)
	if planID == "" {
		t.Fatalf("plan_content_change did not return plan_id")
	}

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	written := readFileString(t, contentRoot, "posts/article/index.md")
	if !strings.Contains(written, "description: A real description.") &&
		!strings.Contains(written, `description: "A real description."`) {
		t.Fatalf("apply_content_plan did not write description to disk: %q", written)
	}

	updated, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatalf("page not found in in-memory index after apply_content_plan")
	}
	got, _ := updated.FrontmatterRaw["description"].(string)
	if got != "A real description." {
		t.Fatalf("in-memory FrontmatterRaw[\"description\"] = %q after apply_content_plan, want \"A real description.\" (#810)", got)
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

	// request_context must carry the slug/lang the plan resolved (#1001) —
	// apply_content_plan's only input is plan_id, so without this the
	// caller has no page identity on the error at all.
	m := decodeWriteContent(t, res)
	reqCtx, ok := m["request_context"].(map[string]any)
	if !ok {
		t.Fatalf("request_context type = %T, want populated object", m["request_context"])
	}
	if got := reqCtx["slug"]; got != "posts/article" {
		t.Fatalf("request_context.slug = %v, want posts/article", got)
	}

	// The rejected apply must not have written anything beyond the setup
	// mutation above (#1001's "absence of write" criterion).
	if body := readFileString(t, contentRoot, "posts/article/index.md"); !strings.Contains(body, "Changed Elsewhere") || strings.Contains(body, "hugo") {
		t.Fatalf("apply_content_plan wrote despite revision_conflict: %s", body)
	}

	// A retryable conflict must leave the plan available for the caller to
	// inspect/retry or explicitly replace (#1001).
	retry := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if !retry.IsError || !strings.Contains(marshalContent(t, retry), "revision_conflict") {
		t.Fatalf("retryable conflict should preserve the plan, got: %s", marshalContent(t, retry))
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

// TestPlanContentChangeRejectsDedraftingTestContent is a regression test
// for #728: a test_content page must not be able to transition to
// draft:false through the plan/apply workflow.
func TestPlanContentChangeRejectsDedraftingTestContent(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/audit-plan-guarded", "title": "Audit Plan Guarded", "body": "Body.",
		"tags": []any{}, "categories": []any{},
		"test_content": map[string]any{"ttl_hours": 2, "owner": "audit-session-42"},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/audit-plan-guarded",
		"operations": []any{
			map[string]any{"op": "set_draft", "draft_value": false},
		},
	})
	if !planRes.IsError {
		t.Fatal("plan_content_change should reject set_draft:false on a test_content page")
	}
	raw := marshalContent(t, planRes)
	if !strings.Contains(raw, "test_content") || !strings.Contains(raw, "draft") {
		t.Fatalf("plan_content_change error should explain the test_content/draft invariant, got: %s", raw)
	}

	content := readFileString(t, contentRoot, "posts/audit-plan-guarded/index.md")
	if !strings.Contains(content, "draft: true") {
		t.Fatalf("plan_content_change must not change the file on disk, got: %s", content)
	}
}

// TestApplyContentPlanUpdatesPublicIndexWhenPresent covers apply_content_plan's
// public-index sync branch: when the page already exists in the built
// (public) site.Index, applying a plan must also push the changed
// title/tags/categories into that public entry.
func TestApplyContentPlanUpdatesPublicIndexWhenPresent(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/public-plan")
	cfg := config.Default()
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:  "/posts/public-plan/",
		Title: "Stale Public Title",
		URL:   "https://example.test/posts/public-plan/",
	})

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteIdx: siteIdx})
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/public-plan",
		"operations": []any{
			map[string]any{"op": "set_title", "value": "New Public Title"},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID, _ := decodeWriteData(t, planRes)["plan_id"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	pub, ok := siteIdx.GetBySlug("posts/public-plan")
	if !ok {
		t.Fatal("public index entry missing after apply_content_plan")
	}
	if pub.Title != "New Public Title" {
		t.Fatalf("public index Title = %q, want %q", pub.Title, "New Public Title")
	}
}

// TestApplyContentPlanSurvivesDerivedDBSyncFailureWithWarning exercises
// apply_content_plan's siteDB.SyncSourcePage soft-degrade branch: the source
// write and recovery journal already succeeded, so a subsequent failure to
// sync the derived DB must downgrade to partial_success with a warning
// rather than fail the whole apply.
func TestApplyContentPlanSurvivesDerivedDBSyncFailureWithWarning(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/plan-derived-db-warning")
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	siteDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer siteDB.Close()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/plan-derived-db-warning",
		"operations": []any{
			map[string]any{"op": "set_title", "value": "Derived DB Warning Title"},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID, _ := decodeWriteData(t, planRes)["plan_id"].(string)

	dropPagesTable(t, dbPath)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan must survive a derived-DB sync failure, got error: %s", marshalContent(t, applyRes))
	}
	data := decodeWriteData(t, applyRes)
	if data["status"] != "partial_success" {
		t.Fatalf("apply_content_plan status = %v, want partial_success", data["status"])
	}
	warning, _ := data["warning"].(string)
	if !strings.Contains(warning, "derived DB could not be updated") {
		t.Fatalf("apply_content_plan warning = %q, want derived-DB warning", warning)
	}
}
