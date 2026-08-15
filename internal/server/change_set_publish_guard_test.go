package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// mockHugoOnPath registers a hugo binary stub that always succeeds on PATH
// for the duration of the test, so build_site/publish_changes can actually
// run without a real Hugo install.
func mockHugoOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hugo"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// toolCallErrorCode extracts the structured errors[0].code from a tools/call
// response, failing the test if the call did not come back as an error at
// all — mirrors TestRawHTTPToolErrorKeepsContentNotFoundInStructuredResult's
// own parsing of the JSON-RPC result envelope.
func toolCallErrorCode(t *testing.T, body string) string {
	t.Helper()
	payload := body
	if i := strings.Index(payload, "{"); i > 0 {
		payload = payload[i:]
	}
	var rpc struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &rpc); err != nil {
		t.Fatalf("unmarshal tools/call response: %v\nbody=%q", err, body)
	}
	if !rpc.Result.IsError {
		t.Fatalf("result.isError = false, want true; body=%q", body)
	}
	errors, ok := rpc.Result.StructuredContent["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("structured errors = %#v, want one entry", rpc.Result.StructuredContent["errors"])
	}
	entry, ok := errors[0].(map[string]any)
	if !ok {
		t.Fatalf("errors[0] = %#v, want an object", errors[0])
	}
	code, _ := entry["code"].(string)
	return code
}

// TestForeignChangeSetBlocksBuildAndPublish is #1140's mandated regression
// test: the exact shape of the 2026-08-14 incident (two agents sharing one
// OAuth principal/token, editing concurrently — here modeled as two
// explicit change-sets under one shared bearer token, since #1135 already
// established that two clients presenting the same credentials get
// distinguished only by explicitly adopting change_set_id). Claude's
// change-set must be able to build/publish its own pending page; ChatGPT's
// concurrently-pending page under a different change-set must not be
// swept into that build, and vice versa.
func TestForeignChangeSetBlocksBuildAndPublish(t *testing.T) {
	mockHugoOnPath(t)
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	contentRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = t.TempDir()
	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const sharedToken = "shared-credentials-principal"
	addBearerToken(t, storePath, sharedToken, "write")

	createChangeSet := func() string {
		t.Helper()
		rec := doMCPCall(t, srv, sharedToken, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("create_change_set status = %d body = %q", rec.Code, rec.Body.String())
		}
		data := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)
		id, _ := data["change_set_id"].(string)
		if id == "" {
			t.Fatalf("create_change_set returned empty change_set_id: %#v", data)
		}
		return id
	}
	csClaude := createChangeSet()
	csChatGPT := createChangeSet()
	if csClaude == csChatGPT {
		t.Fatalf("two create_change_set calls under the shared token returned the same id: %q", csClaude)
	}

	createPage := func(slug, changeSetID string) {
		t.Helper()
		payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page","arguments":{"slug":%q,"title":"T","body":"B","tags":[],"categories":[],"change_set_id":%q}}}`, slug, changeSetID))
		rec := doMCPCall(t, srv, sharedToken, payload)
		if rec.Code != http.StatusOK {
			t.Fatalf("create_page(%q) status = %d body = %q", slug, rec.Code, rec.Body.String())
		}
		data := toolCallResultData(t, rec.Body.String())
		if data["success"] != true {
			t.Fatalf("create_page(%q) did not succeed: %#v", slug, data)
		}
	}
	createPage("posts/from-claude", csClaude)

	buildSiteRec := func(changeSetID string) string {
		t.Helper()
		payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site","arguments":{"change_set_id":%q}}}`, changeSetID))
		rec := doMCPCall(t, srv, sharedToken, payload)
		if rec.Code != http.StatusOK {
			t.Fatalf("build_site transport status = %d body = %q", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	// ChatGPT's change-set does not account for Claude's pending page:
	// building under ChatGPT's change-set must be refused.
	if code := toolCallErrorCode(t, buildSiteRec(csChatGPT)); code != "foreign_change_set_present" {
		t.Fatalf("build_site under csChatGPT while csClaude's page is pending: error code = %q, want foreign_change_set_present", code)
	}

	// Claude's own change-set fully accounts for the only pending page:
	// building under it must succeed.
	claudeData := toolCallResultData(t, buildSiteRec(csClaude))
	if claudeData["success"] != true {
		t.Fatalf("build_site under csClaude (its own pending page) did not succeed: %#v", claudeData)
	}

	// Now ChatGPT makes its own edit — the roles reverse.
	createPage("posts/from-chatgpt", csChatGPT)

	if code := toolCallErrorCode(t, buildSiteRec(csClaude)); code != "foreign_change_set_present" {
		t.Fatalf("build_site under csClaude while csChatGPT's page is pending: error code = %q, want foreign_change_set_present", code)
	}
	chatgptData := toolCallResultData(t, buildSiteRec(csChatGPT))
	if chatgptData["success"] != true {
		t.Fatalf("build_site under csChatGPT (its own pending page) did not succeed: %#v", chatgptData)
	}
}

// TestForeignChangeSetBlocksPublishChanges is publish_changes' own half of
// #1140's guard — publish_changes drives the same build_site pipeline, so
// this proves the wiring on that entry point independently of build_site's
// own test above.
func TestForeignChangeSetBlocksPublishChanges(t *testing.T) {
	mockHugoOnPath(t)
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	contentRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = t.TempDir()
	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const sharedToken = "shared-credentials-principal-2"
	addBearerToken(t, storePath, sharedToken, "write")

	rec := doMCPCall(t, srv, sharedToken, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
	csOwner := toolCallResultData(t, rec.Body.String())["data"].(map[string]any)["change_set_id"].(string)
	rec2 := doMCPCall(t, srv, sharedToken, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_change_set","arguments":{}}}`))
	csOther := toolCallResultData(t, rec2.Body.String())["data"].(map[string]any)["change_set_id"].(string)

	createRec := doMCPCall(t, srv, sharedToken, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page","arguments":{"slug":"posts/owner-only","title":"T","body":"B","tags":[],"categories":[],"change_set_id":%q}}}`, csOwner)))
	if createRec.Code != http.StatusOK || toolCallResultData(t, createRec.Body.String())["success"] != true {
		t.Fatalf("create_page failed: %s", createRec.Body.String())
	}

	otherPublishRec := doMCPCall(t, srv, sharedToken, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"publish_changes","arguments":{"slug":"posts/owner-only","change_set_id":%q}}}`, csOther)))
	if code := toolCallErrorCode(t, otherPublishRec.Body.String()); code != "foreign_change_set_present" {
		t.Fatalf("publish_changes under csOther while csOwner's page is pending: error code = %q, want foreign_change_set_present", code)
	}

	ownerPublishRec := doMCPCall(t, srv, sharedToken, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"publish_changes","arguments":{"slug":"posts/owner-only","change_set_id":%q}}}`, csOwner)))
	if ownerPublishRec.Code != http.StatusOK {
		t.Fatalf("publish_changes under csOwner transport status = %d body = %q", ownerPublishRec.Code, ownerPublishRec.Body.String())
	}
	ownerData := toolCallResultData(t, ownerPublishRec.Body.String())
	if ownerData["success"] != true {
		t.Fatalf("publish_changes under csOwner (its own pending page) did not succeed: %#v", ownerData)
	}
}
