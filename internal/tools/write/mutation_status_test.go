package write_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGetMutationStatusSucceededAfterCreatePage is a regression test for
// #586: after a create_page call with idempotency_key succeeds,
// get_mutation_status for the same tool+key must report status "succeeded"
// with the exact original result — without resending the create_page
// payload.
func TestGetMutationStatusSucceededAfterCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":            "mutation-status-create",
		"title":           "Original",
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "mutation-status-key-1",
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}
	createData := decodeWriteData(t, create)

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "create_page",
		"idempotency_key": "mutation-status-key-1",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "succeeded" {
		t.Fatalf("get_mutation_status data.status = %v, want succeeded", statusData["status"])
	}
	if statusData["tool"] != "create_page" {
		t.Fatalf("get_mutation_status data.tool = %v, want create_page", statusData["tool"])
	}
	if statusData["idempotency_key"] != "mutation-status-key-1" {
		t.Fatalf("get_mutation_status data.idempotency_key = %v, want mutation-status-key-1", statusData["idempotency_key"])
	}
	result, ok := statusData["result"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result = %#v, want object", statusData["result"])
	}
	// result is the entire original create_page response envelope (success/
	// data/errors/warnings/meta), not just its inner data.* — the same shape
	// idem.replay() would restore on a same-key retry of create_page itself.
	resultData, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result.data = %#v, want object", result["data"])
	}
	if resultData["slug"] != createData["slug"] || resultData["source_key"] != createData["source_key"] {
		t.Fatalf("get_mutation_status data.result.data = %#v, want to match original create_page result %#v", resultData, createData)
	}
	if result["success"] != true {
		t.Fatalf("get_mutation_status data.result.success = %v, want true", result["success"])
	}
}

// TestGetMutationStatusUnknownForUnusedKey covers the "no record" case:
// a key that was never used with a mutation must report "unknown", not an
// error and not a false "succeeded".
func TestGetMutationStatusUnknownForUnusedKey(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "create_page",
		"idempotency_key": "never-used-key",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "unknown" {
		t.Fatalf("get_mutation_status data.status = %v, want unknown", statusData["status"])
	}
	if _, present := statusData["result"]; present {
		t.Fatalf("get_mutation_status data.result = %#v, want absent for an unknown key", statusData["result"])
	}
}

// TestGetMutationStatusUnknownForFailedAttempt confirms only successful
// mutations are ever recorded: a create_page call that used an
// idempotency_key but failed (e.g. missing title) must not report
// "succeeded" for that key afterward.
func TestGetMutationStatusUnknownForFailedAttempt(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":            "mutation-status-fail",
		"title":           "", // invalid: title required
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "mutation-status-key-fail",
	})
	if !create.IsError {
		t.Fatal("create_page with empty title should fail")
	}

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "create_page",
		"idempotency_key": "mutation-status-key-fail",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "unknown" {
		t.Fatalf("get_mutation_status data.status = %v, want unknown (failed attempts are never recorded)", statusData["status"])
	}
}

// TestGetMutationStatusScopesLookupPerTool confirms the same
// idempotency_key value used against a different tool name doesn't
// spuriously report "succeeded" — the store is scoped by tool+key, not key
// alone.
func TestGetMutationStatusScopesLookupPerTool(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":            "mutation-status-scoped",
		"title":           "Original",
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "shared-key",
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "update_page",
		"idempotency_key": "shared-key",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "unknown" {
		t.Fatalf("get_mutation_status(update_page, shared-key) data.status = %v, want unknown (this key was used with create_page, not update_page)", statusData["status"])
	}
}

func TestGetMutationStatusRejectsUnknownTool(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "delete_everything",
		"idempotency_key": "some-key",
	})
	if !res.IsError {
		t.Fatal("get_mutation_status with an unrecognized tool name should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("get_mutation_status unknown-tool error = %s, want invalid_params", raw)
	}
}

func TestGetMutationStatusRejectsEmptyIdempotencyKey(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "create_page",
		"idempotency_key": "",
	})
	if !res.IsError {
		t.Fatal("get_mutation_status with an empty idempotency_key should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") {
		t.Fatalf("get_mutation_status empty-key error = %s, want invalid_params", raw)
	}
}

func TestGetMutationStatusSucceededAfterApplyContentPlan(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "mutation-status-apply-plan",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "mutation-status-apply-plan",
		"operations": []any{
			map[string]any{"op": "update_body", "body": "Updated via plan"},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID, _ := decodeWriteData(t, planRes)["plan_id"].(string)
	if planID == "" {
		t.Fatal("plan_content_change returned empty plan_id")
	}

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{
		"plan_id":         planID,
		"idempotency_key": "mutation-status-apply-plan-1",
	})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	applyData := decodeWriteData(t, applyRes)

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "apply_content_plan",
		"idempotency_key": "mutation-status-apply-plan-1",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "succeeded" {
		t.Fatalf("get_mutation_status data.status = %v, want succeeded", statusData["status"])
	}
	result, ok := statusData["result"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result = %#v, want object", statusData["result"])
	}
	resultData, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result.data = %#v, want object", result["data"])
	}
	if resultData["after_revision"] != applyData["after_revision"] {
		t.Fatalf("get_mutation_status apply_content_plan after_revision = %v, want %v", resultData["after_revision"], applyData["after_revision"])
	}
}

func TestGetMutationStatusSucceededAfterRollbackChange(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "mutation-status-rollback",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}

	pagePath := filepath.Join(contentRoot, "mutation-status-rollback", "index.md")
	beforeRevision := currentRevision(t, pagePath)

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "mutation-status-rollback",
		"operations": []any{
			map[string]any{"op": "update_body", "body": "Updated for rollback"},
		},
	})
	if planRes.IsError {
		t.Fatalf("plan_content_change failed: %s", marshalContent(t, planRes))
	}
	planID, _ := decodeWriteData(t, planRes)["plan_id"].(string)
	if planID == "" {
		t.Fatal("plan_content_change returned empty plan_id")
	}

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "mutation-status-rollback",
		"expected_revision": currentRevision(t, pagePath),
		"to_revision":       beforeRevision,
		"idempotency_key":   "mutation-status-rollback-1",
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool":            "rollback_change",
		"idempotency_key": "mutation-status-rollback-1",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status returned error: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "succeeded" {
		t.Fatalf("get_mutation_status data.status = %v, want succeeded", statusData["status"])
	}
	result, ok := statusData["result"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result = %#v, want object", statusData["result"])
	}
	resultData, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("get_mutation_status data.result.data = %#v, want object", result["data"])
	}
	if resultData["after_revision"] != rollbackData["after_revision"] {
		t.Fatalf("get_mutation_status rollback_change after_revision = %v, want %v", resultData["after_revision"], rollbackData["after_revision"])
	}
}
