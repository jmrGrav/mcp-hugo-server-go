package server_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	_ "modernc.org/sqlite"
)

func mustTestServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.Default()
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

func mustOAuthServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	cfg.OAuth = config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		DynamicClientEnabled:  true,
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32", "::1/128"},
	}
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

func mustOAuthServerWithRegistry(t *testing.T, registryPath string) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	cfg.OAuth = config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		DynamicClientEnabled:  true,
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32", "::1/128"},
		ClientRegistryPath:    registryPath,
	}
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

func mustOAuthSQLiteServer(t *testing.T, storePath string) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	cfg.OAuth = config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		DynamicClientEnabled:  true,
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32", "::1/128"},
		StorageBackend:        "sqlite",
		StoragePath:           storePath,
	}
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

func TestMCPEndpointResponds(t *testing.T) {
	srv := mustTestServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200 body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tools") {
		t.Fatalf("body missing tools: %q", rec.Body.String())
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	srv := mustTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d want 404", rec.Code)
	}
}

func TestACLBlocksProtectedToolAnonymous(t *testing.T) {
	// With OAuth enabled, unauthenticated requests get 401 before reaching
	// the ACL layer — the challenge is the guard, not a 403.
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_full_page_markdown"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401 body = %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("unauthenticated /mcp must include WWW-Authenticate header")
	}
}

func TestACLAllowsPublicToolAnonymous(t *testing.T) {
	// With OAuth enabled every /mcp request without a token gets 401 so that
	// OAuth clients discover the authorization server. Public tools are still
	// accessible once a token is acquired (any scope level).
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_pages"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d: %q", rec.Code, rec.Body.String())
	}
}

func TestMCPMethodNotAllowed(t *testing.T) {
	srv := mustTestServer(t)
	// PUT is not a valid MCP method (GET/POST/DELETE are all spec-valid)
	req := httptest.NewRequest(http.MethodPut, "/mcp", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d want 405", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if allow != "GET, POST, DELETE" {
		t.Fatalf("Allow = %q want \"GET, POST, DELETE\"", allow)
	}
}

func toolsListNames(t *testing.T, body string) []string {
	t.Helper()
	var result struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		data := line
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
		if err := json.Unmarshal([]byte(data), &result); err == nil && len(result.Result.Tools) > 0 {
			names := make([]string, len(result.Result.Tools))
			for i, tool := range result.Result.Tools {
				names[i] = tool.Name
			}
			return names
		}
	}
	return nil
}

func doMCPToolsList(t *testing.T, srv *server.Server, bearer string) []string {
	t.Helper()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
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

func doMCPCall(t *testing.T, srv *server.Server, bearer string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func obtainBearerToken(t *testing.T, srv *server.Server) string {
	t.Helper()

	regBody, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{"http://localhost:9999/cb"},
	})
	regReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d body = %q", regRec.Code, regRec.Body.String())
	}
	var regResp struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(regRec.Body.Bytes(), &regResp); err != nil {
		t.Fatalf("register decode: %v", err)
	}
	clientID := regResp.ClientID

	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authParams := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost:9999/cb"},
		"state":                 {"teststate"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := fmt.Sprintf("/authorize?%s", authParams.Encode())
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %q", authRec.Header().Get("Location"))
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.NopCloser(bytes.NewReader(tokenRec.Body.Bytes()))).Decode(&tokenResp); err != nil {
		t.Fatalf("token decode: %v", err)
	}
	if tokenResp.AccessToken == "" {
		t.Fatalf("empty access_token in: %q", tokenRec.Body.String())
	}
	return tokenResp.AccessToken
}

func rewriteTokenScopeToLegacyMCP(t *testing.T, storePath, token string) {
	t.Helper()
	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatalf("open token store: %v", err)
	}
	defer db.Close()
	key := oauthHashForTest(token)
	if _, err := db.Exec(`UPDATE access_tokens SET scope = ? WHERE token = ?`, "mcp", key); err != nil {
		t.Fatalf("update token scope: %v", err)
	}
}

func oauthHashForTest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func TestUnauthenticatedMCPReturns401WithWWWAuthenticate(t *testing.T) {
	// When OAuth is enabled every unauthenticated /mcp request must return 401
	// so that OAuth clients (Claude.ai, ChatGPT) discover the authorization
	// server and start the PKCE flow (RFC 6750 §3.1).
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Fatal("missing WWW-Authenticate header on unauthenticated /mcp")
	}
	if !strings.Contains(wwwAuth, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate missing resource_metadata: %q", wwwAuth)
	}
}

func TestToolsListAuthenticatedReturnsTwentyOneTools(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)
	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 21 {
		t.Fatalf("authenticated tools/list = %d tools, want 21; got %v", len(names), names)
	}
	for _, name := range []string{"get_full_page_markdown", "get_page_frontmatter", "get_related_content", "build_agent_context", "export_agent_context", "search_content", "explain_site_structure", "get_site_health", "diff_page", "validate_front_matter", "validate_site"} {
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("authenticated tools/list missing %q; got %v", name, names)
		}
	}
}

func TestLegacyMCPBearerBehavesLikeContentReadOverHTTP(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)
	bearer := obtainBearerToken(t, srv)
	rewriteTokenScopeToLegacyMCP(t, storePath, bearer)

	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 21 {
		t.Fatalf("legacy mcp tools/list = %d tools, want 21; got %v", len(names), names)
	}
	for _, bad := range []string{"create_page", "update_page", "delete_page", "build_site"} {
		for _, n := range names {
			if n == bad {
				t.Fatalf("legacy mcp tools/list must not include %q", bad)
			}
		}
	}

	readPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_full_page_markdown","arguments":{"slug":"/posts/hello"}}}`)
	readRec := doMCPCall(t, srv, bearer, readPayload)
	if readRec.Code != http.StatusOK {
		t.Fatalf("legacy mcp must allow read tool: status = %d body = %q", readRec.Code, readRec.Body.String())
	}
	if readRec.Body.Len() == 0 {
		t.Fatal("legacy mcp read tool returned empty body")
	}

	writePayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page"}}`)
	writeRec := doMCPCall(t, srv, bearer, writePayload)
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("legacy mcp must reject write tool: status = %d body = %q", writeRec.Code, writeRec.Body.String())
	}
	if !strings.Contains(writeRec.Body.String(), "forbidden_tool") {
		t.Fatalf("expected forbidden_tool for legacy mcp write attempt, got %q", writeRec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d body = %q", metricsRec.Code, metricsRec.Body.String())
	}
	if !strings.Contains(metricsRec.Body.String(), `mcp_legacy_scope_requests_total{scope="mcp"} 3`) {
		t.Fatalf("metrics missing legacy scope counter: %q", metricsRec.Body.String())
	}
}

// TestContentReadCannotCallWriteTool proves that a content.read bearer cannot
// invoke a content.write tool (issue #25 acceptance criterion 1).
func TestContentReadCannotCallWriteTool(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page"}}`)
	rec := doMCPCall(t, srv, bearer, body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("content.read must not call create_page: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "forbidden_tool") {
		t.Fatalf("expected forbidden_tool in response, got: %q", rec.Body.String())
	}
}

// TestContentReadCannotCallSiteAdminTool proves that a content.read bearer cannot
// invoke a site.admin tool (issue #25 acceptance criterion 2).
func TestContentReadCannotCallSiteAdminTool(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site"}}`)
	rec := doMCPCall(t, srv, bearer, body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("content.read must not call build_site: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "forbidden_tool") {
		t.Fatalf("expected forbidden_tool in response, got: %q", rec.Body.String())
	}
}

// TestUnauthenticatedCannotCallSystemAdminTool verifies unauthenticated callers
// are rejected before reaching the tool layer (RFC 6750: 401, not 403).
func TestUnauthenticatedCannotCallSystemAdminTool(t *testing.T) {
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_sri_versions"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated must get 401 before tool layer: status = %d body = %q", rec.Code, rec.Body.String())
	}
}

// TestScopesSupported verifies that the server discovery announces the actual granular
// scopes (issue #28 acceptance criterion).
func TestScopesSupported(t *testing.T) {
	srv := mustOAuthServer(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var meta struct {
		ScopesSupported []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantScopes := []string{"content.read", "content.write", "site.admin"}
	for _, want := range wantScopes {
		found := false
		for _, got := range meta.ScopesSupported {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scopes_supported missing %q; got %v", want, meta.ScopesSupported)
		}
	}
	for _, bad := range []string{"mcp", "system.admin"} {
		for _, got := range meta.ScopesSupported {
			if got == bad {
				t.Errorf("scopes_supported should not contain legacy scope %q", bad)
			}
		}
	}
}

func TestConfidentialClientCanAccessSiteAdminTools(t *testing.T) {
	mockDir := t.TempDir()
	mockHugo := filepath.Join(mockDir, "hugo")
	if err := os.WriteFile(mockHugo, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	registryPath := filepath.Join(t.TempDir(), "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - client_id: claude-admin
    client_secret: admin-secret-value
    redirect_uris:
      - https://client.test/callback
    scope: site.admin
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	srv := mustOAuthServerWithRegistry(t, registryPath)

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type": {"code"},
		"client_id":     {"claude-admin"},
		"redirect_uri":  {"https://client.test/callback"},
		"state":         {"site-admin"},
		"code_challenge": {func() string {
			sum := sha256.Sum256([]byte("verifier-verifier-verifier-verifier-verifier"))
			return base64.RawURLEncoding.EncodeToString(sum[:])
		}()},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("authorize missing code")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"claude-admin"},
		"client_secret": {"admin-secret-value"},
		"redirect_uri":  {"https://client.test/callback"},
		"code":          {code},
		"code_verifier": {"verifier-verifier-verifier-verifier-verifier"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Scope != "site.admin" {
		t.Fatalf("token scope = %q want site.admin", tokenResp.Scope)
	}

	names := doMCPToolsList(t, srv, tokenResp.AccessToken)
	found := false
	writeFound := false
	for _, name := range names {
		if name == "build_site" {
			found = true
		}
		if name == "create_page" {
			writeFound = true
		}
	}
	if !found {
		t.Fatalf("site.admin token missing build_site; got %v", names)
	}
	if !writeFound {
		t.Fatalf("site.admin token missing create_page; got %v", names)
	}

	callRec := doMCPCall(t, srv, tokenResp.AccessToken, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site","arguments":{}}}`))
	if callRec.Code != http.StatusOK {
		t.Fatalf("build_site status = %d body = %q", callRec.Code, callRec.Body.String())
	}
	if !strings.Contains(callRec.Body.String(), "status") {
		t.Fatalf("build_site response missing status: %q", callRec.Body.String())
	}
}

func TestSystemAdminClientSeesWriteAndAdminTools(t *testing.T) {
	mockDir := t.TempDir()
	mockHugo := filepath.Join(mockDir, "hugo")
	if err := os.WriteFile(mockHugo, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	registryPath := filepath.Join(t.TempDir(), "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: claude-admin
    secret: admin-secret-value
    scopes: ["read", "write", "admin"]
    redirect_uris:
      - https://claude.ai/*
    enabled: true
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	srv := mustOAuthServerWithRegistry(t, registryPath)

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type": {"code"},
		"client_id":     {"claude-admin"},
		"redirect_uri":  {"https://claude.ai/oauth/callback"},
		"state":         {"admin-state"},
		"code_challenge": {func() string {
			sum := sha256.Sum256([]byte("verifier-verifier-verifier-verifier-verifier"))
			return base64.RawURLEncoding.EncodeToString(sum[:])
		}()},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("authorize missing code")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"claude-admin"},
		"client_secret": {"admin-secret-value"},
		"redirect_uri":  {"https://claude.ai/oauth/callback"},
		"code":          {code},
		"code_verifier": {"verifier-verifier-verifier-verifier-verifier"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Scope != "site.admin" {
		t.Fatalf("token scope = %q want site.admin", tokenResp.Scope)
	}

	names := doMCPToolsList(t, srv, tokenResp.AccessToken)
	found := false
	writeFound := false
	for _, name := range names {
		if name == "build_site" {
			found = true
		}
		if name == "create_page" {
			writeFound = true
		}
	}
	if !found {
		t.Fatalf("site.admin token missing build_site; got %v", names)
	}
	if !writeFound {
		t.Fatalf("site.admin token missing create_page; got %v", names)
	}
}
