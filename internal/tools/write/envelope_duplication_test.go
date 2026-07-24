package write_test

import (
	"testing"
)

// envelopeWrapperFields are the response envelope's own structural keys
// (success/errors/warnings/meta/data), not payload fields — they're
// expected at the root and have no data-level counterpart to collide with.
// This is deliberately NOT the place documented root-only fields like
// rate_limit_remaining/request_context get excluded: those must be checked
// too, since #520/#605's actual bug was rate_limit_remaining appearing at
// root *and* under data simultaneously on success responses. A field being
// legitimately root-level doesn't mean it's allowed to also duplicate into
// data — quite the opposite, that's exactly the bug class this guards.
var envelopeWrapperFields = map[string]bool{
	"success":  true,
	"errors":   true,
	"warnings": true,
	"meta":     true,
	"data":     true,
}

// assertNoRootDataDuplication is the generic form of assertRootOnlyField
// (#605): rather than checking one named field on one tool, it walks every
// key actually present at the root of a success response and fails if any
// of them (beyond the documented exceptions above) is also present under
// data — regardless of which tool produced the response or what the field
// is called. This is what lets a *future* tool or field reintroduce this
// bug class without a human having to remember to add a new named
// assertion for it.
func assertNoRootDataDuplication(t *testing.T, toolName string, res map[string]any) {
	t.Helper()
	data, _ := res["data"].(map[string]any)
	for k := range res {
		if envelopeWrapperFields[k] {
			continue
		}
		if _, dup := data[k]; dup {
			t.Errorf("%s: field %q present at both root and data.%s — root/data payload duplication (#520, #605)", toolName, k, k)
		}
	}
}

// TestWriteToolSuccessResponsesDoNotDuplicateRootData is the CI regression
// test #605 asked for: a generic, repo-wide check across every write tool
// covered by #520's original fix plus the tools #605 found affected next
// (apply_content_plan, rollback_change), so a new tool or new field added
// later can't silently reintroduce the same duplication one tool at a time
// the way #640/the live audits kept rediscovering it.
func TestWriteToolSuccessResponsesDoNotDuplicateRootData(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// create_page
	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dup-check", "title": "Dup Check", "body": "Body.",
		"tags": []any{"go"}, "categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}
	assertNoRootDataDuplication(t, "create_page", decodeWriteContent(t, createRes))
	createRevision := decodeWriteData(t, createRes)["new_revision"].(string)

	// update_page
	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug": "posts/dup-check", "title": "Dup Check Updated", "expected_revision": createRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	assertNoRootDataDuplication(t, "update_page", decodeWriteContent(t, updateRes))

	// upload_page_asset
	uploadRes := callTool(t, session, "upload_page_asset", map[string]any{
		"slug": "posts/dup-check", "filename": "dup-check.png", "content_base64": b64(minimalPNG),
	})
	if uploadRes.IsError {
		t.Fatalf("upload_page_asset failed: %s", marshalContent(t, uploadRes))
	}
	assertNoRootDataDuplication(t, "upload_page_asset", decodeWriteContent(t, uploadRes))
	uploadSha256 := decodeWriteData(t, uploadRes)["sha256"].(string)

	// delete_page_asset
	deleteAssetRes := callTool(t, session, "delete_page_asset", map[string]any{
		"slug": "posts/dup-check", "filename": "dup-check.png", "expected_sha256": uploadSha256,
	})
	if deleteAssetRes.IsError {
		t.Fatalf("delete_page_asset failed: %s", marshalContent(t, deleteAssetRes))
	}
	assertNoRootDataDuplication(t, "delete_page_asset", decodeWriteContent(t, deleteAssetRes))

	// plan_content_change + apply_content_plan
	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/dup-check",
		"operations": []any{map[string]any{"op": "update_body", "body": "Planned body."}},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	assertNoRootDataDuplication(t, "plan_content_change", decodeWriteContent(t, planRes))
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	target := decodeWriteData(t, planRes)["target"].(map[string]any)
	beforeApplyRevision := target["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	assertNoRootDataDuplication(t, "apply_content_plan", decodeWriteContent(t, applyRes))
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	// rollback_change
	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/dup-check",
		"to_revision":       beforeApplyRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	assertNoRootDataDuplication(t, "rollback_change", decodeWriteContent(t, rollbackRes))
	afterRollbackRevision := decodeWriteData(t, rollbackRes)["after_revision"].(string)

	// delete_page
	deleteRes := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dup-check", "expected_revision": afterRollbackRevision,
	})
	if deleteRes.IsError {
		t.Fatalf("delete_page failed: %s", marshalContent(t, deleteRes))
	}
	assertNoRootDataDuplication(t, "delete_page", decodeWriteContent(t, deleteRes))
}

// TestWriteToolDryRunResponsesDoNotDuplicateRootData is
// TestWriteToolSuccessResponsesDoNotDuplicateRootData's dry_run counterpart:
// the non-dry-run test above wouldn't catch a future dev re-adding
// data.rate_limit_remaining to a dry_run response specifically, since the
// field still exists on each *Data struct (kept for the error-path
// contract, omitempty) — only actually populating it on a real write is
// compiler-checked via the new*Output signature change.
func TestWriteToolDryRunResponsesDoNotDuplicateRootData(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createDryRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dup-check-dry", "title": "Dup Check", "body": "Body.",
		"tags": []any{}, "categories": []any{}, "dry_run": true,
	})
	if createDryRes.IsError {
		t.Fatalf("create_page dry_run failed: %s", marshalContent(t, createDryRes))
	}
	assertNoRootDataDuplication(t, "create_page (dry_run)", decodeWriteContent(t, createDryRes))

	updateDryRes := callTool(t, session, "update_page", map[string]any{
		"slug": "posts/article", "title": "New Title", "dry_run": true,
	})
	if updateDryRes.IsError {
		t.Fatalf("update_page dry_run failed: %s", marshalContent(t, updateDryRes))
	}
	assertNoRootDataDuplication(t, "update_page (dry_run)", decodeWriteContent(t, updateDryRes))

	deleteDryRes := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/article", "dry_run": true,
	})
	if deleteDryRes.IsError {
		t.Fatalf("delete_page dry_run failed: %s", marshalContent(t, deleteDryRes))
	}
	assertNoRootDataDuplication(t, "delete_page (dry_run)", decodeWriteContent(t, deleteDryRes))

	uploadDryRes := callTool(t, session, "upload_page_asset", map[string]any{
		"slug": "posts/article", "filename": "dry.png", "content_base64": b64(minimalPNG), "dry_run": true,
	})
	if uploadDryRes.IsError {
		t.Fatalf("upload_page_asset dry_run failed: %s", marshalContent(t, uploadDryRes))
	}
	assertNoRootDataDuplication(t, "upload_page_asset (dry_run)", decodeWriteContent(t, uploadDryRes))

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Planned body."}},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID := decodeWriteData(t, planRes)["plan_id"].(string)

	applyDryRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID, "dry_run": true})
	if applyDryRes.IsError {
		t.Fatalf("apply_content_plan dry_run failed: %s", marshalContent(t, applyDryRes))
	}
	assertNoRootDataDuplication(t, "apply_content_plan (dry_run)", decodeWriteContent(t, applyDryRes))
}
