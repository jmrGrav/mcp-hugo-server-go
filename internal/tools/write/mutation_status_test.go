package write_test

import (
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
