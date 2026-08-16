package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// mustExposureProfileTestServer builds a no-OAuth server with a real
// ContentRoot/SiteRoot (unlike mustTestServer's bare config.Default(),
// which leaves ContentRoot unset and so never wires read.RegisterWithSourceIndex
// — most reader-tier tools, including search_content/get_broken_links/
// inspect_rendered/get_site_health, would never even register). Mirrors
// mustOAuthServer/mustOAuthSQLiteServer's own cfg setup minus the OAuth
// block, so the exposure-profile tests exercise the full tool catalog a
// production deployment actually has.
func mustExposureProfileTestServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.HugoRoot = t.TempDir()
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}
	return srv
}

// initMCPSessionAtPath mirrors initMCPSession but lets the caller control
// the request path/query (needed to pass ?profile=... on the initialize
// call, which is when the streaming handler's server-selector callback
// actually runs — later requests on the same Mcp-Session-Id reuse whatever
// *mcp.Server that selector returned, so the query param only matters
// here, not on the follow-up tools/list call).
func initMCPSessionAtPath(t *testing.T, srv *server.Server, path, bearer string) string {
	t.Helper()
	body := []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d body = %q", rec.Code, rec.Body.String())
	}
	sessionID := rec.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize response missing Mcp-Session-Id header")
	}
	return sessionID
}

func doMCPToolsListAtPath(t *testing.T, srv *server.Server, path, bearer string) []string {
	t.Helper()
	sessionID := initMCPSessionAtPath(t, srv, path, bearer)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d body = %q", rec.Code, rec.Body.String())
	}
	return toolsListNames(t, rec.Body.String())
}

func containsExposureTool(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestExposureProfileOmittedLeavesDefaultToolListUnchanged is the
// regression test #1137 needs most: every existing integration that never
// passes ?profile= must see exactly the same tool count/set as before this
// feature existed.
func TestExposureProfileOmittedLeavesDefaultToolListUnchanged(t *testing.T) {
	srv := mustExposureProfileTestServer(t)
	names := doMCPToolsListAtPath(t, srv, "/mcp", "")
	if len(names) == 0 {
		t.Fatal("default (no profile) tools/list returned zero tools")
	}
	// The full anonymous+read-open catalog is far larger than the reader
	// exposure tier (~15) — confirms no unintended narrowing snuck in.
	if len(names) < 30 {
		t.Fatalf("default (no profile) tools/list = %d tools, want the full unfiltered catalog (>=30); got %v", len(names), names)
	}
}

// TestExposureProfileReaderNarrowsToolsList is the primary end-to-end
// proof for #1137: ?profile=reader on an anonymous (no-OAuth) connection
// narrows tools/list down to the curated reader tier and excludes
// everything else, including tools that would normally be visible to an
// anonymous/public-scope caller today (e.g. get_capabilities).
func TestExposureProfileReaderNarrowsToolsList(t *testing.T) {
	srv := mustExposureProfileTestServer(t)
	names := doMCPToolsListAtPath(t, srv, "/mcp?profile=reader", "")

	for _, want := range []string{"get_page", "list_pages", "search_content", "get_broken_links", "inspect_rendered", "get_site_health"} {
		if !containsExposureTool(names, want) {
			t.Fatalf("profile=reader tools/list missing %q; got %v", want, names)
		}
	}
	for _, forbidden := range []string{"get_capabilities", "get_changelog", "build_agent_context", "validate_site"} {
		if containsExposureTool(names, forbidden) {
			t.Fatalf("profile=reader tools/list must not include %q (advanced tier); got %v", forbidden, names)
		}
	}
	if len(names) > 20 {
		t.Fatalf("profile=reader tools/list = %d tools, want close to the curated ~15 reader tier; got %v", len(names), names)
	}
}

// TestExposureProfileReaderHardBlocksCallNotJustListing proves the
// documented behavior in exposure_profile.go: a tool hidden by a profile
// is not merely undiscovered via tools/list, it genuinely cannot be
// called through that connection either (RemoveTools deletes it from the
// same map both tools/list and CallTool dispatch read from).
func TestExposureProfileReaderHardBlocksCallNotJustListing(t *testing.T) {
	srv := mustExposureProfileTestServer(t)
	sessionID := initMCPSessionAtPath(t, srv, "/mcp?profile=reader", "")

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d body = %q (JSON-RPC errors still return 200 with an error envelope)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("unknown tool")) {
		t.Fatalf("expected an \"unknown tool\" JSON-RPC error calling a profile-hidden tool, got body = %q", rec.Body.String())
	}
}

// TestExposureProfileUnknownValueRejected proves the reject-not-fall-through
// posture: a typo'd profile must not silently yield an unfiltered (or
// wrongly narrowed) tool list.
func TestExposureProfileUnknownValueRejected(t *testing.T) {
	srv := mustExposureProfileTestServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp?profile=superadmin", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown exposure profile; body = %q", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response body was not valid JSON: %v (%q)", err, rec.Body.String())
	}
	if parsed.Error.Message == "" {
		t.Fatalf("expected a JSON-RPC error message, got body = %q", rec.Body.String())
	}
}

// TestExposureProfileComposesWithWriteScope proves profile narrows within
// whatever the caller's OAuth scope already grants, rather than being a
// second independent axis: a write-scope token connecting with
// ?profile=reader must see only the reader tier, not "reader plus every
// write tool."
func TestExposureProfileComposesWithWriteScope(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)
	const bearer = "write-token-for-exposure-profile-test"
	addBearerToken(t, storePath, bearer, "write")

	names := doMCPToolsListAtPath(t, srv, "/mcp?profile=reader", bearer)
	for _, forbidden := range []string{"create_page", "update_page", "build_site", "get_runtime_status"} {
		if containsExposureTool(names, forbidden) {
			t.Fatalf("write-scope caller with profile=reader must not see %q; got %v", forbidden, names)
		}
	}
	if !containsExposureTool(names, "get_page") {
		t.Fatalf("write-scope caller with profile=reader must still see reader-tier %q; got %v", "get_page", names)
	}
}
