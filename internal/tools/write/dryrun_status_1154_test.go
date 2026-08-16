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
