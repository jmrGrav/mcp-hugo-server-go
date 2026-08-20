package oauth

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

// redirectToRegisteredClient is the final open-redirect guard between an
// allowlisted client redirect_uri and the actual HTTP redirect — previously
// only exercised incidentally through higher-level authorize-flow tests.

func TestRedirectToRegisteredClientRejectsNilURI(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/authorize", nil)
	redirectToRegisteredClient(w, r, nil, nil)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRedirectToRegisteredClientRejectsMissingSchemeOrHost(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/authorize", nil)
	u, err := url.Parse("/relative/path")
	if err != nil {
		t.Fatal(err)
	}
	redirectToRegisteredClient(w, r, u, nil)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 for a relative URI with no scheme/host", w.Code)
	}
}

func TestRedirectToRegisteredClientRejectsNonHTTPSNonLoopback(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/authorize", nil)
	u, err := url.Parse("http://evil.example.com/callback")
	if err != nil {
		t.Fatal(err)
	}
	redirectToRegisteredClient(w, r, u, nil)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 for plain-http non-loopback redirect target", w.Code)
	}
}

func TestRedirectToRegisteredClientAllowsHTTPS(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/authorize", nil)
	u, err := url.Parse("https://claude.ai/api/mcp/auth_callback")
	if err != nil {
		t.Fatal(err)
	}
	redirectToRegisteredClient(w, r, u, url.Values{"code": {"abc"}, "state": {"xyz"}})
	if w.Code != 302 {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Fatal("Location header not set")
	}
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location %q did not parse: %v", loc, err)
	}
	if parsed.Query().Get("code") != "abc" || parsed.Query().Get("state") != "xyz" {
		t.Fatalf("Location %q missing expected query params", loc)
	}
}

func TestRedirectToRegisteredClientAllowsLoopbackHTTP(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/authorize", nil)
	u, err := url.Parse("http://127.0.0.1:51234/callback")
	if err != nil {
		t.Fatal(err)
	}
	redirectToRegisteredClient(w, r, u, url.Values{"code": {"abc"}})
	if w.Code != 302 {
		t.Fatalf("status = %d, want 302 for loopback http redirect target", w.Code)
	}
}
