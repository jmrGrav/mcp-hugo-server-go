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
