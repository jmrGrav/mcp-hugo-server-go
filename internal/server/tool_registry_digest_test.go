package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
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

// doMCPCallAtPath mirrors doMCPCall but lets the caller control the
// initialize request's path/query — needed for ?profile=... (#1137), which
// only takes effect on the streaming handler's server-selector callback at
// initialize time.
func doMCPCallAtPath(t *testing.T, srv *server.Server, path, bearer string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	sessionID := initMCPSessionAtPath(t, srv, path, bearer)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestToolRegistryDigestPublishedThroughServerNew is #1225's end-to-end
// wiring proof: get_capabilities computes a non-empty
// tool_catalog.tool_registry_digest live, from this session's own
// *mcp.Server, the first time it's asked.
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
		t.Fatalf("tool_catalog.tool_registry_digest = %q, want a non-empty sha256:... value", digest)
	}
}

// TestToolRegistryDigestDiffersByScope is the #1225 redesign's core
// property (the original admin-superset design reported the SAME digest to
// every scope, hiding exactly the kind of drift it exists to catch — see
// docs/mcp-contract.md §6.28): a write-scope caller and an admin-scope
// caller against the SAME deployment must see DIFFERENT digests, because
// buildWriteScopedServer's RemoveTools genuinely removes the 4 managed
// Hugo lifecycle tools from write's own *mcp.Server before any session
// reaches it.
func TestToolRegistryDigestDiffersByScope(t *testing.T) {
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
	if writeDigest == "" || adminDigest == "" {
		t.Fatalf("tool_registry_digest is empty: write=%q admin=%q", writeDigest, adminDigest)
	}
	if writeDigest == adminDigest {
		t.Fatalf("tool_registry_digest must differ across scopes with different tool sets: both %q", writeDigest)
	}
}

// TestToolRegistryDigestDiffersByExposureProfile is the fix for the gap the
// #1225 PR review caught in the original design: a ?profile=-narrowed
// session (#1137) must get a DIFFERENT digest than an unnarrowed session of
// the same scope, because the digest is computed from this session's own
// *mcp.Server — the exact object buildExposureServer's RemoveTools already
// mutated before any handler ran.
//
// get_capabilities itself is advanced-tier only (toolExposureTier,
// internal/server/exposure_profile.go) — it cannot be called at all under
// reader/editorial, so this compares admin scope with no profile (every
// tool, including the 4 admin-tier managed Hugo lifecycle tools) against
// admin scope + ?profile=advanced (those 4 tools genuinely hidden), the
// narrowest pairing that keeps get_capabilities itself reachable on both
// sides while still changing the actual tool set.
func TestToolRegistryDigestDiffersByExposureProfile(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)
	addBearerToken(t, storePath, "digest-profile-token", "admin")

	digestAt := func(path string) string {
		rec := doMCPCallAtPath(t, srv, path, "digest-profile-token", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities"}}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("get_capabilities(%s) status = %d body = %q", path, rec.Code, rec.Body.String())
		}
		data := getCapabilitiesData(t, rec)
		toolCatalog, _ := data["tool_catalog"].(map[string]any)
		digest, _ := toolCatalog["tool_registry_digest"].(string)
		return digest
	}

	unnarrowed := digestAt("/mcp")
	narrowed := digestAt("/mcp?profile=advanced")
	if unnarrowed == "" || narrowed == "" {
		t.Fatalf("tool_registry_digest is empty: unnarrowed=%q advanced=%q", unnarrowed, narrowed)
	}
	if unnarrowed == narrowed {
		t.Fatalf("tool_registry_digest must differ between an unnarrowed admin session and an admin ?profile=advanced session (4 managed Hugo lifecycle tools hidden), want it to reflect this session's own tools/list: both %q", unnarrowed)
	}
}

// TestGetCapabilitiesDigestConcurrentCallsDoNotDeadlock proves
// toolRegistryDigestForServer's reentrancy assumption: it calls s.Connect
// (via internal/toolregistry.FromServer) from inside a tool handler already
// executing on a live session of s. If Server.callTool held a lock across
// handler execution, a second concurrent session's Connect could deadlock
// against it. Two goroutines hammer get_capabilities concurrently on
// sessions of the SAME public-tier *mcp.Server; run under `go test -race`
// this also catches any data race in the closure-local sync.Once/cache.
func TestGetCapabilitiesDigestConcurrentCallsDoNotDeadlock(t *testing.T) {
	srv := mustTestServer(t)
	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := doMCPCall(t, srv, "", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities"}}`))
			if rec.Code != http.StatusOK {
				errs <- rec.Body.String()
				return
			}
			data := getCapabilitiesData(t, rec)
			toolCatalog, _ := data["tool_catalog"].(map[string]any)
			digest, _ := toolCatalog["tool_registry_digest"].(string)
			if !strings.HasPrefix(digest, "sha256:") {
				errs <- "empty/malformed digest: " + digest
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent get_capabilities call failed: %s", e)
	}
}
