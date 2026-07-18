package oauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

func TestRedirectURIValidationHelpers(t *testing.T) {
	cases := []struct {
		uri string
		ok  bool
	}{
		{"https://client.test/callback", true},
		{"https://chatgpt.com/connector/oauth/*", true},
		{"https://claude.ai/*", true},
		{"http://localhost/callback", true},
		{"http://127.0.0.1/callback", true},
		{"ftp://client.test/callback", false},
		{"https://chatgpt.com/connector/*/callback", false},
		{"http://example.com/callback", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := validateRegisteredRedirectURI(tc.uri); (got == nil) != tc.ok {
			t.Fatalf("validateRegisteredRedirectURI(%q) = %v, want ok=%v", tc.uri, got, tc.ok)
		}
	}
}

func TestRedirectURIMatching(t *testing.T) {
	cases := []struct {
		registered string
		actual     string
		ok         bool
	}{
		{"https://chatgpt.com/connector/oauth/*", "https://chatgpt.com/connector/oauth/callback", true},
		{"https://chatgpt.com/connector/oauth/*", "https://evil.chatgpt.com/connector/oauth/callback", false},
		{"https://chatgpt.com/connector/oauth/*", "http://chatgpt.com/connector/oauth/callback", false},
		{"https://chatgpt.com/connector/oauth/*", "https://chatgpt.com/connector/other/callback", false},
		{"https://claude.ai/*", "https://claude.ai/oauth/callback", true},
		{"https://client.test/callback", "https://client.test/callback", true},
		{"https://client.test/callback", "https://client.test/other", false},
	}
	for _, tc := range cases {
		if got := matchRedirectURI(tc.registered, tc.actual); got != tc.ok {
			t.Fatalf("matchRedirectURI(%q, %q) = %v, want %v", tc.registered, tc.actual, got, tc.ok)
		}
	}
}

func TestRequestSourceIP(t *testing.T) {
	// requestSourceIP uses RemoteAddr only (not proxy headers) so that
	// CF-Connecting-IP/X-Forwarded-For injection cannot bypass CIDR checks (#54).
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	if got := requestSourceIP(req); got != "203.0.113.9" {
		t.Fatalf("requestSourceIP(remote) = %q, want 203.0.113.9", got)
	}
	// Proxy headers must NOT change the result.
	req.Header.Set("CF-Connecting-IP", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.2")
	req.Header.Set("X-Forwarded-For", "198.51.100.3, 10.0.0.1")
	if got := requestSourceIP(req); got != "203.0.113.9" {
		t.Fatalf("requestSourceIP with proxy headers = %q, want 203.0.113.9 (headers must not override)", got)
	}
}

func TestOAuthErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		code   string
		status int
	}{
		{errors.New("unsupported_response_type"), "unsupported_response_type", http.StatusBadRequest},
		{errors.New("access_denied: blocked"), "access_denied", http.StatusForbidden},
		{errors.New("unauthorized_client"), "unauthorized_client", http.StatusUnauthorized},
		{errors.New("unsupported_grant_type"), "unsupported_grant_type", http.StatusBadRequest},
		{errors.New("invalid_client"), "invalid_client", http.StatusUnauthorized},
		{errors.New("invalid_grant: expired"), "invalid_grant", http.StatusBadRequest},
		{errors.New("registration_disabled"), "invalid_request", http.StatusBadRequest},
		{errors.New("invalid redirect_uri"), "invalid_redirect_uri", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if got := oauthAuthorizeErrorCode(tc.err); got == "" && tc.code != "" {
			t.Fatalf("oauthAuthorizeErrorCode(%v) empty", tc.err)
		}
		if tc.code == "unsupported_response_type" || tc.code == "access_denied" || tc.code == "unauthorized_client" {
			if got := oauthAuthorizeErrorCode(tc.err); got != tc.code {
				t.Fatalf("oauthAuthorizeErrorCode(%v) = %q, want %q", tc.err, got, tc.code)
			}
		}
		if tc.code == "unsupported_grant_type" || tc.code == "invalid_client" || tc.code == "invalid_grant" {
			if got := oauthTokenErrorCode(tc.err); got != tc.code {
				t.Fatalf("oauthTokenErrorCode(%v) = %q, want %q", tc.err, got, tc.code)
			}
		}
		if tc.code == "invalid_request" || tc.code == "invalid_redirect_uri" {
			if got := oauthRegisterErrorCode(tc.err); got != tc.code {
				t.Fatalf("oauthRegisterErrorCode(%v) = %q, want %q", tc.err, got, tc.code)
			}
		}
	}
	if got := oauthAuthorizeErrorStatus(errors.New("access_denied: blocked")); got != http.StatusForbidden {
		t.Fatalf("oauthAuthorizeErrorStatus() = %d", got)
	}
	if got := oauthAuthorizeErrorStatus(errors.New("unauthorized_client")); got != http.StatusUnauthorized {
		t.Fatalf("oauthAuthorizeErrorStatus() = %d", got)
	}
	if got := oauthTokenErrorStatus(errors.New("invalid_client")); got != http.StatusUnauthorized {
		t.Fatalf("oauthTokenErrorStatus() = %d", got)
	}
}

func TestPKCEAndHashHelpers(t *testing.T) {
	verifier := "test-verifier-test-verifier-test-verifier-test"
	challenge := CodeChallengeS256(verifier)
	if challenge == "" || !ValidatePKCE(challenge, verifier) {
		t.Fatal("PKCE helper failed valid pair")
	}
	if ValidatePKCE(challenge, "wrong") {
		t.Fatal("ValidatePKCE should reject wrong verifier")
	}
	if HashToken("token") == HashToken("other") {
		t.Fatal("HashToken should be deterministic but distinct for different inputs")
	}
}

func TestSourceAllowed(t *testing.T) {
	svc := NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
	}, storage.NewMemory())
	if !svc.sourceAllowed("127.0.0.1") {
		t.Fatal("sourceAllowed should accept trusted IP")
	}
	if svc.sourceAllowed("203.0.113.10") {
		t.Fatal("sourceAllowed should reject untrusted IP")
	}
}

func TestTokenClientCredentials(t *testing.T) {
	form := url.Values{
		"client_id":     {"form-client"},
		"client_secret": {"form-secret"},
	}
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if gotID, gotSecret := tokenClientCredentials(req); gotID != "form-client" || gotSecret != "form-secret" {
		t.Fatalf("tokenClientCredentials(form) = %q/%q", gotID, gotSecret)
	}

	req = httptest.NewRequest(http.MethodPost, "/token", nil)
	req.SetBasicAuth("basic-client", "basic-secret")
	if gotID, gotSecret := tokenClientCredentials(req); gotID != "basic-client" || gotSecret != "basic-secret" {
		t.Fatalf("tokenClientCredentials(basic) = %q/%q", gotID, gotSecret)
	}
}

func TestPurgeExpired(t *testing.T) {
	store := storage.NewMemory()
	svc := NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
	}, store)
	token := HashToken("token")
	if err := store.AddAccessToken(token, "content.read", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
	svc.PurgeExpired()
	if _, ok := store.ValidateAccessToken(token); ok {
		t.Fatal("expired token should have been purged")
	}
}

func TestScopeConfigurationHelpers(t *testing.T) {
	// Per #450, all legacy 4-tier scope strings (including "system.admin",
	// an alias for the old "site.admin") canonicalize down to just "read"
	// and "write"; dedup now produces exactly 2 unique scopes.
	scopes, err := normalizeConfiguredScopes([]string{"system.admin", "content.write", "read"}, "site.admin")
	if err != nil {
		t.Fatalf("normalizeConfiguredScopes() error = %v", err)
	}
	if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "write" {
		t.Fatalf("normalizeConfiguredScopes() = %#v", scopes)
	}
	if got := highestConfiguredScope(scopes); got != "write" {
		t.Fatalf("highestConfiguredScope() = %q", got)
	}
	if got, err := requestedScope("content.read content.write"); err != nil || got != "write" {
		t.Fatalf("requestedScope() = %q, %v", got, err)
	}
	if got, err := requestedScope("unknown"); err == nil || got != "" {
		t.Fatalf("requestedScope(unknown) = %q, %v", got, err)
	}
	// Reproduces a real production outage (2026-07-18): Claude.ai's
	// /authorize request echoed the full advertised scopes_supported list
	// back verbatim as its scope parameter — and "reader" (part of
	// tools.KnownScopes at the time, published in scopes_supported) was not
	// accepted by requestedScope/normalizeConfiguredScope, so the whole
	// authorize call failed with invalid_scope. "reader" must still be
	// accepted here (now as a legacy alias resolved to "read" by
	// CanonicalScope), and a higher-ranked scope in the same list must
	// still win.
	if got, err := requestedScope("reader content.read content.write site.admin"); err != nil || got != "write" {
		t.Fatalf(`requestedScope("reader content.read content.write site.admin") = %q, %v, want "write", nil`, got, err)
	}
	// Per #450, "reader" is now a plain legacy alias for "read" (the
	// dedicated reader-safe profile it used to key into no longer exists as
	// a distinct scope — site.IsReaderProfile/ReaderSafeResolvedPage remain
	// in the codebase but are never triggered by any live scope string).
	if got, err := requestedScope("reader"); err != nil || got != "read" {
		t.Fatalf(`requestedScope("reader") = %q, %v, want "read", nil`, got, err)
	}
	// #449 follow-up: a request mixing a not-yet-recognized token with valid
	// ones must resolve using the valid tokens, not fail the whole request —
	// the same outage class as "reader" (#448) but for a future scope value.
	if got, err := requestedScope("future.scope content.write"); err != nil || got != "write" {
		t.Fatalf(`requestedScope("future.scope content.write") = %q, %v, want "write", nil`, got, err)
	}
	if got, err := requestedScope("reader future.scope"); err != nil || got != "read" {
		t.Fatalf(`requestedScope("reader future.scope") = %q, %v, want "read", nil`, got, err)
	}
	// Every token unrecognized must still error — there is no valid scope to
	// resolve to.
	if got, err := requestedScope("future.scope another.unknown"); err == nil || got != "" {
		t.Fatalf(`requestedScope("future.scope another.unknown") = %q, %v, want error`, got, err)
	}
	if !allowedScope("read", "write") {
		t.Fatal("allowedScope() should allow lower rank")
	}
	if allowedScope("write", "read") {
		t.Fatal("allowedScope() should reject higher rank")
	}
	if CanonicalScope("mcp") != "read" || !IsLegacyScope("mcp") {
		t.Fatal("legacy scope alias not normalized correctly")
	}
}
