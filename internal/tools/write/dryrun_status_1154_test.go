package write_test

import (
	"path/filepath"
	"testing"
)

// These are regression tests for #1154: several mutation tools' dry_run
// responses hardcoded data.status:"unchanged" regardless of what the diff/
// per-item outcomes in the same payload showed would actually happen. Each
// test below drives a dry_run call where a real, non-trivial change is
// unambiguously predicted and asserts the top-level status reflects that,
// instead of silently claiming nothing would change.

func TestCreateBundleDryRunReportsWouldCreateNotUnchanged(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	res := callTool(t, session, "create_bundle", map[string]any{
		"slug":    "posts/dry-run-bundle-1154",
		"dry_run": true,
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
	})
	if res.IsError {
		t.Fatalf("create_bundle dry_run failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if got := data["status"]; got != "would_create" {
		t.Fatalf("create_bundle dry_run data.status = %v, want would_create", got)
	}
}

func TestDeleteBundleDryRunReportsWouldDeleteNotUnchanged(t *testing.T) {
	root := t.TempDir()
	writeBilingualBundle(t, root, "posts/dry-run-delete-1154")
	session, _, done := newTestServer(t, root)
	defer done()

	res := callTool(t, session, "delete_bundle", map[string]any{
		"slug": "posts/dry-run-delete-1154", "languages": []any{"fr", "en"},
		"expected_revisions": map[string]any{
			"fr": currentRevision(t, filepath.Join(root, "posts/dry-run-delete-1154/index.fr.md")),
			"en": currentRevision(t, filepath.Join(root, "posts/dry-run-delete-1154/index.en.md")),
		},
		"dry_run": true,
	})
	if res.IsError {
		t.Fatalf("delete_bundle dry_run failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if got := data["status"]; got != "would_delete" {
		t.Fatalf("delete_bundle dry_run data.status = %v, want would_delete", got)
	}
}

func TestRollbackBundleDryRunReportsWouldRestoreNotUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/dry-run-rollback-1154")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/dry-run-rollback-1154",
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
		"slug":                     "posts/dry-run-rollback-1154",
		"to_bundle_revision":       preApplyRev,
		"expected_bundle_revision": postApplyRev,
		"dry_run":                  true,
	})
	if rbRes.IsError {
		t.Fatalf("rollback_bundle dry_run failed: %s", marshalContent(t, rbRes))
	}
	data := decodeWriteData(t, rbRes)
	if got := data["status"]; got != "would_restore" {
		t.Fatalf("rollback_bundle dry_run data.status = %v, want would_restore", got)
	}
}

// TestApplyContentPlanDryRunNoOpReportsUnchanged is the flip side of #1154:
// a plan that resolves to a genuine no-op (setting the title to what it
// already is) must not claim "would_update" just because a plan exists.
func TestApplyContentPlanDryRunNoOpReportsUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/noop-plan-1154")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/noop-plan-1154",
		"operations": []any{map[string]any{"op": "set_title", "value": "Article"}},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	dryRun := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID, "dry_run": true})
	if dryRun.IsError {
		t.Fatalf("apply_content_plan dry_run failed: %s", marshalContent(t, dryRun))
	}
	data := decodeWriteData(t, dryRun)
	if got := data["status"]; got != "unchanged" {
		t.Fatalf("apply_content_plan dry_run (no-op plan) data.status = %v, want unchanged", got)
	}
}

// TestApplyBundlePlanDryRunNoOpReportsUnchanged is the flip side of #1154
// for the bundle path: a plan whose every translation resolves to a no-op
// must not claim "would_apply".
func TestApplyBundlePlanDryRunNoOpReportsUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/noop-bundle-plan-1154")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	setTitleOp := func(title string) map[string]any {
		return map[string]any{"op": "set_title", "value": title}
	}
	planRes := callTool(t, session, "plan_bundle_change", map[string]any{
		"slug": "posts/noop-bundle-plan-1154",
		"translations": []any{
			map[string]any{"lang": "fr", "operations": []any{setTitleOp("Article FR")}},
			map[string]any{"lang": "en", "operations": []any{setTitleOp("Article EN")}},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_bundle_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	dryRun := callTool(t, session, "apply_bundle_plan", map[string]any{"plan_id": planID, "dry_run": true})
	if dryRun.IsError {
		t.Fatalf("apply_bundle_plan dry_run failed: %s", marshalContent(t, dryRun))
	}
	data := decodeWriteData(t, dryRun)
	if got := data["status"]; got != "unchanged" {
		t.Fatalf("apply_bundle_plan dry_run (no-op plan) data.status = %v, want unchanged", got)
	}
}
