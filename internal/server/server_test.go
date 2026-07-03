package server_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
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
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_full_page_markdown"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403 body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "forbidden_tool") {
		t.Fatalf("expected forbidden_tool in response body, got: %q", rec.Body.String())
	}
}

func TestACLAllowsPublicToolAnonymous(t *testing.T) {
	srv := mustOAuthServer(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_pages"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("expected list_pages to pass ACL, got 403: %q", rec.Body.String())
	}
}

func TestMCPMethodNotAllowed(t *testing.T) {
	srv := mustTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q want POST", got)
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

func TestToolsListAnonymousReturnsNineTools(t *testing.T) {
	srv := mustOAuthServer(t)
	names := doMCPToolsList(t, srv, "")
	if len(names) != 9 {
		t.Fatalf("anonymous tools/list = %d tools, want 9; got %v", len(names), names)
	}
	for _, name := range []string{"get_full_page_markdown", "get_page_frontmatter", "get_related_content", "build_agent_context", "export_agent_context"} {
		for _, n := range names {
			if n == name {
				t.Fatalf("anonymous tools/list must not include %q", name)
			}
		}
	}
}

func TestToolsListAuthenticatedReturnsFourteenTools(t *testing.T) {
	srv := mustOAuthServer(t)
	bearer := obtainBearerToken(t, srv)
	names := doMCPToolsList(t, srv, bearer)
	if len(names) != 14 {
		t.Fatalf("authenticated tools/list = %d tools, want 14; got %v", len(names), names)
	}
	for _, name := range []string{"get_full_page_markdown", "get_page_frontmatter", "get_related_content", "build_agent_context", "export_agent_context"} {
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
