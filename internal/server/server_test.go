package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
