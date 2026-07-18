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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
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
	// Bake in a read client for http://localhost:9999/cb. Since #497, DCR
	// always grants "read" regardless of match, so this registry entry is no
	// longer strictly required for that outcome — kept for clarity so
	// TestReadCannotCallWriteTool/TestReadCannotCallSiteAdminTool document the
	// read→write boundary explicitly.
	registryPath := filepath.Join(t.TempDir(), "clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`clients:
- client_id: test-read-client
  client_secret: test-read-secret
  redirect_uris:
    - http://localhost:9999/cb
  scope: read
`), 0o600); err != nil {
		t.Fatalf("write test registry: %v", err)
	}
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.HugoRoot = t.TempDir()
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

func mustOAuthServerWithRegistry(t *testing.T, registryPath string) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.HugoRoot = t.TempDir()
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
	cfg.HugoRoot = t.TempDir()
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

func mustOAuthSQLiteServerWithConfig(t *testing.T, cfg config.Config, storePath string) *server.Server {
	t.Helper()
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

func addBearerToken(t *testing.T, storePath, rawToken, scope string) {
	t.Helper()
	store, err := storage.NewSQLite(storePath)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer store.Close()
	if err := store.AddAccessToken(oauthHashForTest(rawToken), scope, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
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

// TestInitializeExposesRuntimeBuildVersion proves mcp.Implementation.Version
// (surfaced in the initialize response's serverInfo.version) tracks
// buildinfo.Version — the actual deployed build/release identifier injected
// via -ldflags — rather than silently reporting the "dev" placeholder on a
// release build (#327).
func TestInitializeExposesRuntimeBuildVersion(t *testing.T) {
	orig := buildinfo.Version
	buildinfo.Version = "v1.4.7-runtime-test"
	defer func() { buildinfo.Version = orig }()

	srv := mustTestServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d body = %q", rec.Code, rec.Body.String())
	}

	var result struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	found := false
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		data := strings.TrimPrefix(line, "data: ")
		if err := json.Unmarshal([]byte(data), &result); err == nil && result.Result.ServerInfo.Version != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("initialize response did not contain a parseable serverInfo.version: %q", rec.Body.String())
	}
	if result.Result.ServerInfo.Version != "v1.4.7-runtime-test" {
		t.Fatalf("serverInfo.version = %q, want buildinfo.Version %q", result.Result.ServerInfo.Version, "v1.4.7-runtime-test")
	}
	if result.Result.ServerInfo.Version == "dev" {
		t.Fatal("serverInfo.version must not fall back to the dev placeholder once buildinfo.Version is set")
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

// TestOAuthEndpointsSupportCORSPreflight covers a real bug found live
// (Mistral Le Chat, 2026-07-18): a browser-based OAuth client calling
// /register, /authorize, or /token directly (not just navigating to them)
// sends a CORS preflight OPTIONS request first. Before this fix, these
// endpoints had no CORS support at all — OPTIONS got a plain 405 with no
// Access-Control-Allow-Origin — so a browser would block the real request
// before it ever reached this server, showing the client a generic
// "can't connect" with nothing in this server's own logs to explain it.
// Confirmed live via curl against production before landing this fix.
func TestOAuthEndpointsSupportCORSPreflight(t *testing.T) {
	srv := mustOAuthServer(t)
	for _, path := range []string{"/register", "/authorize", "/token"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			req.Header.Set("Origin", "https://chat.mistral.ai")
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("%s OPTIONS status = %d, want 204", path, rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("%s OPTIONS Access-Control-Allow-Origin = %q, want \"*\"", path, got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
				t.Fatalf("%s OPTIONS Access-Control-Allow-Methods missing", path)
			}
			if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
				t.Fatalf("%s OPTIONS Access-Control-Allow-Headers missing", path)
			}
		})
	}
}

// TestOAuthEndpointsSetCORSOnRealResponseNotJustPreflight covers the other
// half of the same fix: a passing preflight isn't enough for a browser to
// accept the actual response — the real GET/POST response needs
// Access-Control-Allow-Origin too, or client-side JS still can't read it.
func TestOAuthEndpointsSetCORSOnRealResponseNotJustPreflight(t *testing.T) {
	srv := mustOAuthServer(t)
	regBody, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{"http://localhost:9999/cb"},
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://chat.mistral.ai")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("register real response Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
}

func TestACLBlocksProtectedToolAnonymous(t *testing.T) {
	// With OAuth enabled, unauthenticated requests get 401 before reaching
	// the ACL layer — the challenge is the guard, not a 403.
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page_markdown"}}`)
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

func toolsListPayload(t *testing.T, body string) string {
	t.Helper()
	var result struct {
		Result struct {
			Tools []any `json:"tools"`
		} `json:"result"`
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		data := line
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
		if err := json.Unmarshal([]byte(data), &result); err == nil && len(result.Result.Tools) > 0 {
			normalized, err := json.Marshal(result.Result.Tools)
			if err != nil {
				t.Fatalf("marshal normalized tools payload: %v", err)
			}
			return string(normalized)
		}
	}
	t.Fatalf("tools/list response did not contain a parseable tools payload: %q", body)
	return ""
}

// initMCPSession sends an initialize request and returns the session ID.
// Required in stateful mode: without initialize, the session is uninitialized
// and tools/list returns an empty list.
func initMCPSession(t *testing.T, srv *server.Server, bearer string) string {
	t.Helper()
	body := []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
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

func doMCPToolsList(t *testing.T, srv *server.Server, bearer string) []string {
	t.Helper()
	body := doMCPToolsListBody(t, srv, bearer, nil)
	return toolsListNames(t, body)
}

func doMCPToolsListBody(t *testing.T, srv *server.Server, bearer string, extraHeaders map[string]string) string {
	t.Helper()
	sessionID := initMCPSession(t, srv, bearer)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d body = %q", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func doMCPCall(t *testing.T, srv *server.Server, bearer string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	// ACL-blocked calls (403) never reach go-sdk, so they don't need a session.
	// We still initialize to be correct for calls that succeed.
	sessionID := initMCPSession(t, srv, bearer)
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

// withDefaultLogger replaces the global slog default for the duration of one
// test. Do NOT use in tests that call t.Parallel() — the global mutation is
// not goroutine-safe across parallel test cases.
func withDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return &buf
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
	if !strings.Contains(wwwAuth, `realm="`) {
		t.Fatalf("WWW-Authenticate missing realm parameter: %q", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate missing resource_metadata: %q", wwwAuth)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q want %q", got, "no-store")
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "unauthorized" {
		t.Fatalf("body = %q want %q", body, "unauthorized")
	}
}

func TestInSessionMissingBearerEmitsStructuredLog(t *testing.T) {
	srv := mustOAuthServer(t)
	logBuf := withDefaultLogger(t)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", "session-123")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	raw := logBuf.String()
	if !strings.Contains(raw, `"msg":"audit"`) {
		t.Fatalf("missing audit log: %s", raw)
	}
	if !strings.Contains(raw, `"event_type":"auth_rejected"`) {
		t.Fatalf("missing event_type=auth_rejected in log: %s", raw)
	}
	if !strings.Contains(raw, `"result":"missing_bearer"`) {
		t.Fatalf("missing rejection reason in log: %s", raw)
	}
	if !strings.Contains(raw, `"has_session":true`) {
		t.Fatalf("missing has_session=true in log: %s", raw)
	}
}

func TestInSessionInvalidBearerEmitsStructuredLog(t *testing.T) {
	srv := mustOAuthServer(t)
	logBuf := withDefaultLogger(t)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", "session-123")
	req.Header.Set("Authorization", "Bearer definitely-invalid")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="`) {
		t.Fatalf("WWW-Authenticate missing realm parameter: %q", wwwAuth)
	}
	if !strings.Contains(wwwAuth, `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate missing invalid_token marker: %q", wwwAuth)
	}
	raw := logBuf.String()
	if !strings.Contains(raw, `"msg":"audit"`) {
		t.Fatalf("missing audit log: %s", raw)
	}
	if !strings.Contains(raw, `"event_type":"auth_rejected"`) {
		t.Fatalf("missing event_type=auth_rejected in log: %s", raw)
	}
	if !strings.Contains(raw, `"result":"invalid_token"`) {
		t.Fatalf("missing invalid_token reason in log: %s", raw)
	}
	if !strings.Contains(raw, `"has_session":true`) {
		t.Fatalf("missing has_session=true in log: %s", raw)
	}
}

// TestScopeDeniedToolCallEmitsStructuredAuditLog proves the security audit
// trail (#371) acceptance criteria for insufficient-scope denials: a
// read token attempting a write tool must produce a
// distinguishable event_type=scope_denied audit line (not just a generic
// tool_error), and that line must never contain the caller's raw bearer
// token.
func TestScopeDeniedToolCallEmitsStructuredAuditLog(t *testing.T) {
	srv := mustOAuthServer(t)
	logBuf := withDefaultLogger(t)
	bearer := obtainBearerToken(t, srv)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page"}}`)
	rec := doMCPCall(t, srv, bearer, payload)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	raw := logBuf.String()
	if !strings.Contains(raw, `"msg":"audit"`) {
		t.Fatalf("missing audit log: %s", raw)
	}
	if !strings.Contains(raw, `"event_type":"scope_denied"`) {
		t.Fatalf("missing event_type=scope_denied in log: %s", raw)
	}
	if !strings.Contains(raw, `"scope":"read"`) {
		t.Fatalf("missing caller scope in log: %s", raw)
	}
	if strings.Contains(raw, bearer) {
		t.Fatalf("audit log must never contain the raw bearer token: %s", raw)
	}
}

func TestToolsListAuthenticatedReturnsTwentyOneTools(t *testing.T) {
	// mustOAuthServer includes a read client for http://localhost:9999/cb,
	// so obtainBearerToken (which DCR-registers with that redirect URI) gets a
	// read token via resolveRegistrationScope (#249).
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)
	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 27 {
		t.Fatalf("authenticated tools/list = %d tools, want 27; got %v", len(names), names)
	}
	for _, name := range []string{"get_page_markdown", "get_page_frontmatter", "get_related_content", "build_agent_context", "export_agent_context", "search_content", "explain_structure", "get_site_health", "diff_page", "validate_frontmatter", "validate_site", "suggest_links"} {
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

func TestReaderTokenToolsListMatchesReadOnlyCatalog(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 27 {
		t.Fatalf("reader tools/list = %d tools, want 27; got %v", len(names), names)
	}
	for _, name := range []string{
		"list_pages", "get_page", "search_pages", "get_recent_posts", "list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information",
		"get_page_markdown", "get_page_frontmatter", "get_related_content", "build_agent_context", "export_agent_context",
		"search_content", "explain_structure", "get_site_health", "diff_page", "validate_frontmatter", "validate_site",
		"get_broken_links", "get_backlinks", "suggest_links",
	} {
		found := false
		for _, got := range names {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("reader tools/list missing %q; got %v", name, names)
		}
	}
	for _, forbidden := range []string{"create_page", "update_page", "delete_page", "build_site", "preview_build", "run_post_build_hooks"} {
		for _, got := range names {
			if got == forbidden {
				t.Fatalf("reader tools/list must not include %q; got %v", forbidden, names)
			}
		}
	}
}

func TestReaderTokenGetPageRejectsSourceOnlyFallback(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "drafts", "fresh"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "drafts", "fresh", "index.md"), []byte("---\ntitle: Fresh\n---\nFresh body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(public) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page","arguments":{"slug":"/drafts/fresh/","allow_source_fallback":true}}}`)
	rec := doMCPCall(t, srv, bearer, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("read get_page status = %d body = %q", rec.Code, rec.Body.String())
	}
	// Per #450, "read" grants full visibility including drafts/source-only
	// content (an explicit operator risk-acceptance decision, not an
	// oversight) — the reader-safe content_not_public rejection no longer
	// applies to any live scope.
	if strings.Contains(rec.Body.String(), "content_not_public") {
		t.Fatalf("read get_page must allow source-only fallback (full visibility per #450); body = %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Fresh body") {
		t.Fatalf("read get_page missing draft body content; body = %q", rec.Body.String())
	}
}

func TestReaderTokenListPagesUsesPublicMetadataOnly(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "demo", "index.md"), []byte("---\ntitle: Demo\ntags:\n  - SourceTag\ncategories:\n  - SourceCat\n---\nSource body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(publicRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(public page) error = %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Demo</title>
  <meta name="description" content="Rendered summary">
  <link rel="canonical" href="https://example.test/posts/demo/">
</head>
<body><main><article><h1>Demo</h1><p>Rendered body.</p></article></main></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicRoot, "posts", "demo", "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile(public html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_pages","arguments":{"limit":10,"offset":0}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read list_pages status = %d body = %q", rec.Code, rec.Body.String())
	}
	// Per #450, "read" grants full visibility: source-derived taxonomy
	// (categories not present in the public rendered HTML) is now
	// expected to be visible, not stripped.
	if !strings.Contains(rec.Body.String(), "SourceCat") {
		t.Fatalf("read list_pages must expose source-derived taxonomy (full visibility per #450); body = %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"slug\":\"/posts/demo/\"") {
		t.Fatalf("read list_pages missing public page slug; body = %q", rec.Body.String())
	}
}

func TestReaderTokenGetFullPageMarkdownUsesPublicContentOnly(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "demo", "index.md"), []byte("---\ntitle: Demo\ncategories:\n  - SecretCat\n---\nSource-only body that should stay hidden.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(publicRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(public page) error = %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Demo</title>
  <meta name="description" content="Rendered summary">
  <link rel="canonical" href="https://example.test/posts/demo/">
</head>
<body><main><article><h1>Demo</h1><p>Rendered public body.</p></article></main></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicRoot, "posts", "demo", "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile(public html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page_markdown","arguments":{"slug":"/posts/demo/"}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read get_page_markdown status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Per #450, "read" grants full visibility (drafts/source-only content
	// included, an explicit operator risk-acceptance decision): the
	// source markdown/frontmatter is now expected to be visible, not
	// stripped down to the public rendered projection.
	if !strings.Contains(body, "Source-only body that should stay hidden.") || !strings.Contains(body, "SecretCat") {
		t.Fatalf("read get_page_markdown must expose full source content (full visibility per #450); body = %q", body)
	}
}

func TestReaderTokenGetPageForEditUsesPublicContentAndOmitsQuality(t *testing.T) {
	// #339: get_page_for_edit's quality section needs raw source access
	// (front matter validation via sourcePagesForValidation), the same
	// class of source-derived signal as #324's taxonomy details. Confirm
	// end-to-end that a reader gets public-only frontmatter/markdown and
	// no quality section, rather than trusting sourceIndexForProfile's
	// nil-for-readers behavior by inspection alone.
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "demo", "index.md"), []byte("---\ntitle: Demo\ncategories:\n  - SecretCat\n---\nSource-only body that should stay hidden.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(publicRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(public page) error = %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Demo</title>
  <meta name="description" content="Rendered summary">
  <link rel="canonical" href="https://example.test/posts/demo/">
</head>
<body><main><article><h1>Demo</h1><p>Rendered public body.</p></article></main></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicRoot, "posts", "demo", "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile(public html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page_for_edit","arguments":{"slug":"/posts/demo/"}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read get_page_for_edit status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Per #450, "read" grants full visibility (drafts/source-only content
	// included, an explicit operator risk-acceptance decision): source
	// content/frontmatter and the source-derived quality section are both
	// now expected to be visible, not stripped as they were for the old
	// reader-safe profile.
	if !strings.Contains(body, "Source-only body that should stay hidden.") || !strings.Contains(body, "SecretCat") {
		t.Fatalf("read get_page_for_edit must expose full source content (full visibility per #450); body = %q", body)
	}
	if !strings.Contains(body, `"quality"`) {
		t.Fatalf("read get_page_for_edit must include quality (source access granted per #450); body = %q", body)
	}
}

func TestReaderTokenListPageAssetsOmitsBundleDirectoryForPublicPage(t *testing.T) {
	// #348: list_page_assets' payload is entirely source-derived (it lists a
	// filesystem directory under content root). Even for a page that is
	// publicly available, site.ReaderSafeResolvedPage strips SourcePath for
	// readers, so there is no directory to list. Confirm end-to-end that
	// this degrades to an empty assets list rather than leaking a path or
	// erroring in a way that suggests the page doesn't exist.
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "demo", "index.md"), []byte("---\ntitle: Demo\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "demo", "cover.webp"), []byte("cover bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(asset) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(publicRoot, "posts", "demo"), 0o755); err != nil {
		t.Fatalf("MkdirAll(public page) error = %v", err)
	}
	publicHTML := `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Demo</title><link rel="canonical" href="https://example.test/posts/demo/"></head>
<body><main><article><h1>Demo</h1><p>Rendered public body.</p></article></main></body>
</html>`
	if err := os.WriteFile(filepath.Join(publicRoot, "posts", "demo", "index.html"), []byte(publicHTML), 0o644); err != nil {
		t.Fatalf("WriteFile(public html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_page_assets","arguments":{"slug":"/posts/demo/"}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read list_page_assets status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Per #450, "read" grants full visibility: the source bundle directory
	// is now expected to be listed, not stripped to an empty result.
	if !strings.Contains(body, "cover.webp") {
		t.Fatalf("read list_page_assets must expose the source bundle contents (full visibility per #450); body = %q", body)
	}
}

func TestReaderTokenListPageAssetsRejectsSourceOnlyPage(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "draft"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "draft", "index.md"), []byte("---\ntitle: Draft\ndraft: true\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(public) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_page_assets","arguments":{"slug":"/posts/draft/"}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read list_page_assets status = %d body = %q", rec.Code, rec.Body.String())
	}
	// Per #450, "read" grants full visibility: a source-only (draft) page
	// is no longer rejected with content_not_public.
	if strings.Contains(rec.Body.String(), "content_not_public") {
		t.Fatalf("read list_page_assets must allow a source-only page (full visibility per #450); body = %q", rec.Body.String())
	}
}

func TestReaderTokenListContentTypesOmitsPageCounts(t *testing.T) {
	// #347: page_count is derived from source pages (including drafts),
	// the same class of source-derived signal as #324/#339. Archetype
	// metadata (filesystem templates, not page content) should remain
	// visible; only the observed/counted side must be reader-safe.
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(root, "archetypes"), 0o755); err != nil {
		t.Fatalf("MkdirAll(archetypes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "archetypes", "posts.md"), []byte("---\ntitle: \"\"\n---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(archetype) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "secret-draft"), 0o755); err != nil {
		t.Fatalf("MkdirAll(content) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "secret-draft", "index.md"), []byte("---\ntitle: SecretDraftTitle\ndraft: true\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(public) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_content_types","arguments":{}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read list_content_types status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Per #450, "read" grants full visibility: source-derived signals like
	// page_count and draft-derived entries are now expected to be visible.
	if !strings.Contains(body, `"page_count"`) {
		t.Fatalf("read list_content_types must include page_count (full visibility per #450); body = %q", body)
	}
	if !strings.Contains(body, `"posts"`) || !strings.Contains(body, "archetype") {
		t.Fatalf("read list_content_types should still see the archetype-derived 'posts' entry; body = %q", body)
	}
}

func TestReaderTokenGetSiteHealthOmitsTaxonomyInconsistencyDetails(t *testing.T) {
	// #324: taxonomy_inconsistency_details carries source-page slugs
	// (including draft/source-only pages). get_site_health has no
	// content_not_public reader guard the way validate_* tools do —
	// it silently delegates source-safety to sourceIndexForProfile,
	// which returns nil for the reader profile. Confirm that holds
	// end-to-end rather than trusting it by inspection alone.
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "a"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "a", "index.md"), []byte("---\ntitle: A\ncategories:\n  - security\n---\nBody A.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(contentRoot, "posts", "b"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "posts", "b", "index.md"), []byte("---\ntitle: B\ncategories:\n  - securite\n---\nBody B.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(public) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_site_health","arguments":{}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read get_site_health status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Per #450, "read" grants full visibility: taxonomy_inconsistencies
	// (source page slugs) is now expected to be visible, not stripped.
	if !strings.Contains(body, "taxonomy_inconsistencies") {
		t.Fatalf("read get_site_health must expose taxonomy_inconsistencies (full visibility per #450); body = %q", body)
	}
}

func TestReaderTokenGetFullPageMarkdownRejectsSourceOnlyPage(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")
	storePath := filepath.Join(root, "tokens.db")

	if err := os.MkdirAll(filepath.Join(contentRoot, "drafts", "fresh"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "drafts", "fresh", "index.md"), []byte("---\ntitle: Fresh\n---\nFresh body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(public) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><main>home</main></body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.HugoRoot = root
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"

	srv := mustOAuthSQLiteServerWithConfig(t, cfg, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page_markdown","arguments":{"slug":"/drafts/fresh/"}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read get_page_markdown status = %d body = %q", rec.Code, rec.Body.String())
	}
	// Per #450, "read" grants full visibility: a source-only (draft) page
	// is no longer rejected with content_not_public.
	if strings.Contains(rec.Body.String(), "content_not_public") {
		t.Fatalf("read get_page_markdown must allow a source-only page (full visibility per #450); body = %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Fresh body") {
		t.Fatalf("read get_page_markdown missing draft body content; body = %q", rec.Body.String())
	}
}

func TestReaderTokenValidateSiteRejectsSourceDiagnostics(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)

	const bearer = "reader-token"
	addBearerToken(t, storePath, bearer, "read")

	rec := doMCPCall(t, srv, bearer, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"validate_site","arguments":{}}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("read validate_site status = %d body = %q", rec.Code, rec.Body.String())
	}
	// Per #450, "read" grants full visibility: source diagnostics are no
	// longer rejected with content_not_public.
	if strings.Contains(rec.Body.String(), "content_not_public") {
		t.Fatalf("read validate_site must allow source diagnostics (full visibility per #450); body = %q", rec.Body.String())
	}
}

func TestLegacyMCPBearerBehavesLikeContentReadOverHTTP(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tokens.db")
	srv := mustOAuthSQLiteServer(t, storePath)
	bearer := obtainBearerToken(t, srv)
	rewriteTokenScopeToLegacyMCP(t, storePath, bearer)

	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 27 {
		t.Fatalf("legacy mcp tools/list = %d tools, want 27; got %v", len(names), names)
	}
	for _, bad := range []string{"create_page", "update_page", "delete_page", "build_site"} {
		for _, n := range names {
			if n == bad {
				t.Fatalf("legacy mcp tools/list must not include %q", bad)
			}
		}
	}

	readPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_page_markdown","arguments":{"slug":"/posts/hello"}}}`)
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
	if !strings.Contains(metricsRec.Body.String(), `mcp_legacy_scope_requests_total{scope="mcp"} 6`) {
		t.Fatalf("metrics missing legacy scope counter: %q", metricsRec.Body.String())
	}
}

// TestReadCannotCallWriteTool proves that a read bearer cannot invoke a
// write tool (issue #25 acceptance criterion 1; scope model collapsed to
// read/write by #450).
func TestReadCannotCallWriteTool(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_page"}}`)
	rec := doMCPCall(t, srv, bearer, body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("read must not call create_page: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "forbidden_tool") {
		t.Fatalf("expected forbidden_tool in response, got: %q", rec.Body.String())
	}
}

// TestReadCannotCallSiteAdminTool proves that a read bearer cannot invoke a
// tool that used to require site.admin (issue #25 acceptance criterion 2;
// site.admin folded into write by #450).
func TestReadCannotCallSiteAdminTool(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_site"}}`)
	rec := doMCPCall(t, srv, bearer, body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("read must not call build_site: status = %d body = %q", rec.Code, rec.Body.String())
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
	wantScopes := []string{"read", "write"}
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
	if tokenResp.Scope != "write" {
		t.Fatalf("token scope = %q want write", tokenResp.Scope)
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
		t.Fatalf("write token missing build_site; got %v", names)
	}
	if !writeFound {
		t.Fatalf("write token missing create_page; got %v", names)
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
	if tokenResp.Scope != "write" {
		t.Fatalf("token scope = %q want write", tokenResp.Scope)
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
		t.Fatalf("write token missing build_site; got %v", names)
	}
	if !writeFound {
		t.Fatalf("write token missing create_page; got %v", names)
	}
}

// TestAnonymousServerExposesReadTools documents the #450 scope collapse:
// "read" is capability-identical to anonymous (both ScopeRank 0), so tools
// that used to require the "content.read" tier (rank 1, gated) are now
// ungated and appear even when the server runs without OAuth (anonymous
// mode). This test used to be TestAnonymousServerDoesNotExposeAuthenticatedTools,
// asserting the opposite; that boundary no longer exists by design — read
// requires no secret and is auto-registrable.
func TestAnonymousServerExposesReadTools(t *testing.T) {
	// Use a config with ContentRoot set (no OAuth) so source-index-backed
	// read tools (validate_site, search_content, ...) are registered at
	// all; mustTestServer's bare config.Default() has no ContentRoot, so
	// those tools would be absent regardless of scope, and this test would
	// not actually exercise the #450 scope boundary.
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
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
	names := doMCPToolsList(t, srv, "")

	// These tools used to require content.read; per #450 they are now
	// ungated (same rank as anonymous) and must appear here.
	formerlyAuthOnlyTools := []string{
		"validate_site",
		"validate_frontmatter",
		"build_agent_context",
		"export_agent_context",
		"get_broken_links",
		"get_site_health",
		"diff_page",
		"search_content",
	}
	for _, want := range formerlyAuthOnlyTools {
		found := false
		for _, got := range names {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("public server must expose read tool %q (ungated per #450); got %v", want, names)
		}
	}

	// Public tools must still be present.
	publicTools := []string{"list_pages", "get_page", "list_tags"}
	for _, want := range publicTools {
		found := false
		for _, got := range names {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("anonymous server missing expected public tool %q; got %v", want, names)
		}
	}
}

// --- Regression: Claude.ai OAuth flow ---
//
// Claude.ai always sends scope=content.read+content.write+site.admin+system.admin
// regardless of what the server advertises. Before v1.2.10 this caused an
// invalid_scope redirect (disguised as HTTP 302) and the token endpoint was
// never called. This test locks in the correct behaviour: scope is clamped to
// the client ceiling (write), a code is issued, and the token exchange
// returns write with admin (now write-gated) tools visible.
func TestRegressionClaudeAiScopeClamping(t *testing.T) {
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
    client_secret: admin-secret
    redirect_uris:
      - https://claude.ai/api/mcp/auth_callback
    scope: site.admin
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	srv := mustOAuthServerWithRegistry(t, registryPath)

	verifier := "verifier-regression-claude-ai-00000000000"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Simulate the exact scope string Claude.ai sends (includes system.admin).
	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"claude-admin"},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"regression"},
		"scope":                 {"content.read content.write site.admin system.admin"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d (want 302); body = %q", authRec.Code, authRec.Body.String())
	}
	loc, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	// Must have a code, not an error — a 302 with ?error=invalid_scope looks
	// identical in nginx logs but breaks the token exchange silently (issue #121).
	if errParam := loc.Query().Get("error"); errParam != "" {
		t.Fatalf("authorize returned error=%q in Location; scope clamping must prevent this", errParam)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("authorize Location missing code: %s", loc)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"claude-admin"},
		"client_secret": {"admin-secret"},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
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
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Scope != "write" {
		t.Fatalf("token scope = %q want write (should be clamped from system.admin)", tokenResp.Scope)
	}
}

// --- Regression: ChatGPT OAuth flow ---
//
// ChatGPT uses a wildcard redirect URI and requests the legacy
// scope=content.read+content.write string (still accepted via CanonicalScope,
// #450). It must receive a canonical "write" token that exposes write tools
// (create_page). Per #450, site.admin tools folded into write with no
// exceptions, so build_site/check_sri_versions/preview_build are now also
// expected to be visible — this is the intended behavior, not a boundary
// violation.
func TestRegressionChatGPTWriteScopeToolBoundary(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - client_id: chatgpt-write
    client_secret: chatgpt-secret
    redirect_uris:
      - https://chatgpt.com/connector/oauth/*
    scope: content.write
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	srv := mustOAuthServerWithRegistry(t, registryPath)

	verifier := "verifier-regression-chatgpt-00000000000"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"chatgpt-write"},
		"redirect_uri":          {"https://chatgpt.com/connector/oauth/callback"},
		"state":                 {"regression"},
		"scope":                 {"content.read content.write"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, _ := url.Parse(authRec.Header().Get("Location"))
	if errParam := loc.Query().Get("error"); errParam != "" {
		t.Fatalf("authorize returned error=%q", errParam)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("authorize missing code")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"chatgpt-write"},
		"client_secret": {"chatgpt-secret"},
		"redirect_uri":  {"https://chatgpt.com/connector/oauth/callback"},
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
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Scope != "write" {
		t.Fatalf("token scope = %q want write", tokenResp.Scope)
	}

	names := doMCPToolsList(t, srv, tokenResp.AccessToken)
	// Must see write tools.
	if !containsToolName(names, "create_page") {
		t.Errorf("chatgpt write token missing create_page; got %v", names)
	}
	// Per #450, site.admin folded into write with no exceptions: a write
	// token now also sees the tools that used to require site.admin.
	for _, adminTool := range []string{"build_site", "check_sri_versions", "preview_build"} {
		if !containsToolName(names, adminTool) {
			t.Errorf("chatgpt write token must expose formerly-admin tool %q (folded into write per #450)", adminTool)
		}
	}
}

// --- Regression: IsItAgentReady 7/7 discovery endpoints ---
//
// These seven endpoints make up the API, Auth, MCP & Skill Discovery score.
// A score drop from 7/7 breaks AgentReady certification.
func TestRegressionIsItAgentReadyDiscoveryEndpoints(t *testing.T) {
	// auth.md must exist in SiteRoot for the /auth.md endpoint to return 200.
	siteRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(siteRoot, "auth.md"), []byte("# auth\n"), 0o644); err != nil {
		t.Fatalf("write auth.md: %v", err)
	}
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://www.arleo.eu"
	cfg.SiteName = "arleo.eu"
	cfg.OAuth = config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.arleo.eu",
		Resource:              "https://mcp.arleo.eu/mcp",
		DynamicClientEnabled:  true,
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
	}
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	endpoints := []struct {
		path        string
		description string
	}{
		{"/.well-known/oauth-authorization-server", "OAuth/OIDC discovery"},
		{"/.well-known/oauth-protected-resource", "OAuth protected resource"},
		{"/.well-known/mcp/server-card.json", "MCP server card"},
		{"/.well-known/mcp/server-card/mcp", "MCP server card (Le Chat per-resource alias, #424)"},
		{"/.well-known/mcp.json", "API catalog (mcp.json alias)"},
		{"/.well-known/agent.json", "Agent skills index"},
		{"/auth.md", "Auth.md agent integration"},
		{"/mcp", "MCP endpoint reachable (401 = auth challenge, not a failure)"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		// /mcp returns 401 when OAuth is enabled — that IS the correct response
		// (it tells the client to authenticate). Any other endpoint must be 200.
		if ep.path == "/mcp" {
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s (%s): got %d want 401", ep.path, ep.description, rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("%s: missing WWW-Authenticate header", ep.path)
			}
		} else {
			if rec.Code != http.StatusOK {
				t.Errorf("%s (%s): got %d want 200", ep.path, ep.description, rec.Code)
			}
		}
	}
}

func containsToolName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestOperatorTokenToolCatalogIgnoresClientBranding(t *testing.T) {
	mockDir := t.TempDir()
	mockHugo := filepath.Join(mockDir, "hugo")
	if err := os.WriteFile(mockHugo, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	registryPath := filepath.Join(t.TempDir(), "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - client_id: parity-admin
    client_secret: parity-secret
    redirect_uris:
      - https://claude.ai/oauth/callback
    scope: site.admin
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	srv := mustOAuthServerWithRegistry(t, registryPath)

	verifier := "verifier-operator-parity-000000000000"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"parity-admin"},
		"redirect_uri":          {"https://claude.ai/oauth/callback"},
		"state":                 {"operator-parity"},
		"scope":                 {"content.read content.write site.admin"},
		"code_challenge":        {challenge},
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
		"client_id":     {"parity-admin"},
		"client_secret": {"parity-secret"},
		"redirect_uri":  {"https://claude.ai/oauth/callback"},
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
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Scope != "write" {
		t.Fatalf("token scope = %q want write", tokenResp.Scope)
	}

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "claude-like",
			headers: map[string]string{
				"User-Agent":    "Claude/1.0",
				"X-Client-Name": "claude.ai",
			},
		},
		{
			name: "chatgpt-like",
			headers: map[string]string{
				"User-Agent":    "ChatGPT/1.0",
				"X-Client-Name": "chatgpt",
			},
		},
		{
			name: "generic-mcp",
			headers: map[string]string{
				"User-Agent":    "GenericMCP/1.0",
				"X-Client-Name": "generic",
			},
		},
	}

	var want string
	for i, tc := range cases {
		got := toolsListPayload(t, doMCPToolsListBody(t, srv, tokenResp.AccessToken, tc.headers))
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("%s tools/list body differed for same operator token\nwant: %s\n\ngot: %s", tc.name, want, got)
		}
	}
}
