package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// getCapabilitiesData extracts the tools/call structuredContent.data map
// from a get_capabilities response recorded by doMCPCall.
func getCapabilitiesData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	payload := rec.Body.String()
	if i := strings.Index(payload, "{"); i > 0 {
		payload = payload[i:]
	}
	var rpc struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &rpc); err != nil {
		t.Fatalf("unmarshal get_capabilities response: %v\nbody=%q", err, payload)
	}
	data, _ := rpc.Result.StructuredContent["data"].(map[string]any)
	return data
}

// TestToolRegistryDigestPublishedThroughServerNew is #1225's end-to-end
// wiring proof: server.New builds an admin-scope server, computes its
// tool-registry digest via internal/toolregistry, and publishes it so
// get_capabilities reports a non-empty tool_catalog.tool_registry_digest —
// unlike tool_names_revision (#1175), which differs per scope, this digest
// reflects the deployment's FULL admin-scope superset regardless of which
// scope answered the call.
func TestToolRegistryDigestPublishedThroughServerNew(t *testing.T) {
	srv := mustTestServer(t) // bearerless (OAuth disabled), public tier
	rec := doMCPCall(t, srv, "", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("get_capabilities status = %d body = %q", rec.Code, rec.Body.String())
	}
	data := getCapabilitiesData(t, rec)
	toolCatalog, _ := data["tool_catalog"].(map[string]any)
	digest, _ := toolCatalog["tool_registry_digest"].(string)
	if !strings.HasPrefix(digest, "sha256:") || len(digest) <= len("sha256:") {
		t.Fatalf("tool_catalog.tool_registry_digest = %q, want a non-empty sha256:... value published by server.New", digest)
	}
}

// TestToolRegistryDigestSameAcrossScopes confirms the digest is deployment-
// wide (from the admin superset), not scope-narrowed like tool_names_revision:
// a write-scope caller and an admin-scope caller against the SAME running
// deployment must see the identical digest.
func TestToolRegistryDigestSameAcrossScopes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)
	addBearerToken(t, storePath, "digest-write-token", "write")
	addBearerToken(t, storePath, "digest-admin-token", "admin")

	digestFor := func(bearer string) string {
		rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities"}}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("get_capabilities status = %d body = %q", rec.Code, rec.Body.String())
		}
		data := getCapabilitiesData(t, rec)
		toolCatalog, _ := data["tool_catalog"].(map[string]any)
		digest, _ := toolCatalog["tool_registry_digest"].(string)
		return digest
	}

	writeDigest := digestFor("digest-write-token")
	adminDigest := digestFor("digest-admin-token")
	if writeDigest == "" {
		t.Fatal("write-scope tool_registry_digest is empty")
	}
	if writeDigest != adminDigest {
		t.Fatalf("tool_registry_digest differs by scope: write=%q admin=%q, want identical (both must reflect the deployment's full admin-scope superset)", writeDigest, adminDigest)
	}
}
