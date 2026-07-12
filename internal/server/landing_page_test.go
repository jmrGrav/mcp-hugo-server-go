package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLandingPageServedAtRoot(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"MCP Hugo Server",
		"https://mcp.arleo.eu/mcp",
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/.well-known/mcp/server-card.json",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("landing page missing %q", want)
		}
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html;") {
		t.Fatalf("Content-Type = %q", got)
	}

	headRec := httptest.NewRecorder()
	headReq := httptest.NewRequest(http.MethodHead, "/", nil)
	srv.Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 {
		t.Fatalf("HEAD / = status %d body %q", headRec.Code, headRec.Body.String())
	}

	postRec := httptest.NewRecorder()
	postReq := httptest.NewRequest(http.MethodPost, "/", nil)
	srv.Handler().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d want 405", postRec.Code)
	}
}
