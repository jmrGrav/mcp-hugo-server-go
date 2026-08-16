package server_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
)

// getRuntimeStatusPublicationSafety fetches get_runtime_status (optionally
// scoped to changeSetID, mirroring build_site's own change_set_id
// resolution) and returns data.publication_safety as a map, or nil if the
// field was omitted.
func getRuntimeStatusPublicationSafety(t *testing.T, srv *server.Server, token, changeSetID string) map[string]any {
	t.Helper()
	args := "{}"
	if changeSetID != "" {
		args = fmt.Sprintf(`{"change_set_id":%q}`, changeSetID)
	}
	payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_runtime_status","arguments":%s}}`, args))
	rec := doMCPCall(t, srv, token, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("get_runtime_status transport status = %d body = %q", rec.Code, rec.Body.String())
	}
	data, _ := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)
	safety, _ := data["publication_safety"].(map[string]any)
	return safety
}

// TestRuntimeStatusPublicationSafetyReflectsChangeSetOwnership is #1142's
// mandated regression test: it asserts data.publication_safety correctly
// separates one change-set's own pending work from a different change-set's
// pending work, mirroring the exact two-change-sets-under-one-shared-token
// scenario #1140's own guard test uses, and that the same answer this field
// gives lines up with what build_site itself would actually do.
func TestRuntimeStatusPublicationSafetyReflectsChangeSetOwnership(t *testing.T) {
	mockHugoOnPath(t)
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	contentRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = t.TempDir()
	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const sharedToken = "shared-credentials-runtime-status"
	addBearerToken(t, storePath, sharedToken, "write")

	createChangeSet := func() string {
		t.Helper()
		rec := doMCPCall(t, srv, sharedToken, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
		data := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)
		id, _ := data["change_set_id"].(string)
		if id == "" {
			t.Fatalf("create_change_set returned empty change_set_id: %#v", data)
		}
		return id
	}
	csMine := createChangeSet()
	csOther := createChangeSet()

	createPage := func(slug, changeSetID string) {
		t.Helper()
		payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page","arguments":{"slug":%q,"title":"T","body":"B","tags":[],"categories":[],"change_set_id":%q}}}`, slug, changeSetID))
		rec := doMCPCall(t, srv, sharedToken, payload)
		data := toolCallResultData(t, rec.Body.String())
		if data["success"] != true {
			t.Fatalf("create_page(%q) did not succeed: %#v", slug, data)
		}
	}

	// Before anything is created: nothing pending, trivially safe.
	safety := getRuntimeStatusPublicationSafety(t, srv, sharedToken, csMine)
	if safety == nil {
		t.Fatal("publication_safety is nil with a change-set registry wired")
	}
	if safety["safe_to_publish"] != true {
		t.Fatalf("safe_to_publish = %#v before any pending page, want true", safety["safe_to_publish"])
	}

	createPage("posts/mine", csMine)
	createPage("posts/theirs-1", csOther)
	createPage("posts/theirs-2", csOther)

	safety = getRuntimeStatusPublicationSafety(t, srv, sharedToken, csMine)
	current, _ := safety["current_change_set"].(map[string]any)
	if current == nil || current["id"] != csMine || current["changes"] != float64(1) {
		t.Fatalf("current_change_set = %#v, want {id: %q, changes: 1}", current, csMine)
	}
	other, _ := safety["other_change_sets"].(map[string]any)
	if other == nil || other["count"] != float64(1) || other["changes"] != float64(2) {
		t.Fatalf("other_change_sets = %#v, want {count: 1, changes: 2}", other)
	}
	if safety["active_change_sets"] != float64(2) {
		t.Fatalf("active_change_sets = %#v, want 2", safety["active_change_sets"])
	}
	if safety["external_unknown_changes"] != float64(0) {
		t.Fatalf("external_unknown_changes = %#v, want 0", safety["external_unknown_changes"])
	}
	if safety["unpublished_changes_count"] != float64(3) {
		t.Fatalf("unpublished_changes_count = %#v, want 3 (1 mine + 2 theirs)", safety["unpublished_changes_count"])
	}
	if safety["safe_to_publish"] != false {
		t.Fatalf("safe_to_publish for csMine = %#v with csOther's pending pages present, want false", safety["safe_to_publish"])
	}

	// The exact same pending state, viewed from csOther's own perspective:
	// csMine's page is now the "other" one, and csOther is unsafe for the
	// same reason in reverse.
	otherView := getRuntimeStatusPublicationSafety(t, srv, sharedToken, csOther)
	otherCurrent, _ := otherView["current_change_set"].(map[string]any)
	if otherCurrent == nil || otherCurrent["id"] != csOther || otherCurrent["changes"] != float64(2) {
		t.Fatalf("current_change_set from csOther's view = %#v, want {id: %q, changes: 2}", otherCurrent, csOther)
	}
	if otherOther, _ := otherView["other_change_sets"].(map[string]any); otherOther["count"] != float64(1) || otherOther["changes"] != float64(1) {
		t.Fatalf("other_change_sets from csOther's view = %#v, want {count: 1, changes: 1}", otherOther)
	}
	if otherView["safe_to_publish"] != false {
		t.Fatalf("safe_to_publish for csOther = %#v with csMine's pending page present, want false", otherView["safe_to_publish"])
	}

	// This must match what build_site itself actually does: refuse under
	// either single change-set while the other's page is still pending.
	refusedBuild := doMCPCall(t, srv, sharedToken, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site","arguments":{"change_set_id":%q}}}`, csMine)))
	if code := toolCallErrorCode(t, refusedBuild.Body.String()); code != "foreign_change_set_present" {
		t.Fatalf("build_site under csMine while csOther's pages are pending: error code = %q, want foreign_change_set_present (publication_safety said safe_to_publish=false for exactly this reason)", code)
	}

	// Building acknowledging both change-sets clears BuildPending on every
	// page (a single Hugo pass renders the whole tree — see
	// guardForeignChangeSet's own doc comment) — afterward nothing is
	// pending at all, and publication_safety must reflect that cleanly.
	buildRec := doMCPCall(t, srv, sharedToken, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site","arguments":{"change_set_ids":[%q,%q]}}}`, csMine, csOther)))
	if buildRec.Code != http.StatusOK || toolCallResultData(t, buildRec.Body.String())["success"] != true {
		t.Fatalf("build_site acknowledging both change-sets failed: %s", buildRec.Body.String())
	}

	safety = getRuntimeStatusPublicationSafety(t, srv, sharedToken, csMine)
	if current, _ := safety["current_change_set"].(map[string]any); current["changes"] != float64(0) {
		t.Fatalf("current_change_set after a full build = %#v, want changes: 0", current)
	}
	if other, _ := safety["other_change_sets"].(map[string]any); other["changes"] != float64(0) {
		t.Fatalf("other_change_sets after a full build = %#v, want changes: 0", other)
	}
	if safety["safe_to_publish"] != true {
		t.Fatalf("safe_to_publish after a full build cleared every pending page = %#v, want true", safety["safe_to_publish"])
	}
}

// TestRuntimeStatusPublicationSafetyRejectsForeignChangeSetID proves
// data.publication_safety's change_set_id input is resolved with the exact
// same ownership check every mutation tool uses — a change_set_id this
// caller doesn't own is invalid_params, not silently treated as "current".
func TestRuntimeStatusPublicationSafetyRejectsForeignChangeSetID(t *testing.T) {
	mockHugoOnPath(t)
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.ContentRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	addBearerToken(t, storePath, "principal-a", "write")
	addBearerToken(t, storePath, "principal-b", "write")

	rec := doMCPCall(t, srv, "principal-a", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
	csA := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)["change_set_id"].(string)

	payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_runtime_status","arguments":{"change_set_id":%q}}}`, csA))
	badRec := doMCPCall(t, srv, "principal-b", payload)
	if code := toolCallErrorCode(t, badRec.Body.String()); code != "invalid_params" {
		t.Fatalf("get_runtime_status(change_set_id=csA) as principal-b: error code = %q, want invalid_params", code)
	}
}

// TestRuntimeStatusPublicationSafetyTruePredictsSuccessfulBuild is the other
// half of #1142's contract: not just that safe_to_publish=false predicts a
// refusal (covered above), but that safe_to_publish=true actually predicts
// build_site succeeding under the same change_set_id — a single change-set
// with its own pending page and nothing foreign pending.
func TestRuntimeStatusPublicationSafetyTruePredictsSuccessfulBuild(t *testing.T) {
	mockHugoOnPath(t)
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.ContentRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const token = "solo-principal"
	addBearerToken(t, storePath, token, "write")

	rec := doMCPCall(t, srv, token, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
	cs := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)["change_set_id"].(string)

	createRec := doMCPCall(t, srv, token, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page","arguments":{"slug":"posts/solo","title":"T","body":"B","tags":[],"categories":[],"change_set_id":%q}}}`, cs)))
	if toolCallResultData(t, createRec.Body.String())["success"] != true {
		t.Fatalf("create_page failed: %s", createRec.Body.String())
	}

	safety := getRuntimeStatusPublicationSafety(t, srv, token, cs)
	if safety["safe_to_publish"] != true {
		t.Fatalf("safe_to_publish = %#v with only this change-set's own pending page, want true", safety["safe_to_publish"])
	}

	buildRec := doMCPCall(t, srv, token, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site","arguments":{"change_set_id":%q}}}`, cs)))
	if buildRec.Code != http.StatusOK || toolCallResultData(t, buildRec.Body.String())["success"] != true {
		t.Fatalf("build_site under %q failed despite safe_to_publish=true: %s", cs, buildRec.Body.String())
	}
}
