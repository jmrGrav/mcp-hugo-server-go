package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

func TestClientRegistrySupportsChatGPTAndClaudeRedirectPatterns(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: chatgpt-admin
    secret: super-secret-value
    scopes: ["read", "write", "admin"]
    redirect_uris:
      - https://chatgpt.com/connector/oauth/*
      - https://claude.ai/*
    enabled: true
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry() error = %v", err)
	}

	for _, redirectURI := range []string{
		"https://chatgpt.com/connector/oauth/callback-123",
		"https://claude.ai/oauth/callback",
	} {
		authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
			"response_type":         {"code"},
			"client_id":             {"chatgpt-admin"},
			"redirect_uri":          {redirectURI},
			"state":                 {"state-xyz"},
			"code_challenge":        {oauth.CodeChallengeS256("verifier-verifier-verifier-verifier")},
			"code_challenge_method": {"S256"},
		}.Encode(), nil)
		authReq.RemoteAddr = "127.0.0.1:1234"
		authRec := httptest.NewRecorder()
		svc.HandleAuthorize(authRec, authReq)
		if authRec.Code != http.StatusFound {
			t.Fatalf("authorize(%s) status = %d body = %q", redirectURI, authRec.Code, authRec.Body.String())
		}

		loc, err := url.Parse(authRec.Header().Get("Location"))
		if err != nil {
			t.Fatalf("authorize(%s) parse redirect: %v", redirectURI, err)
		}
		code := loc.Query().Get("code")
		if code == "" {
			t.Fatalf("authorize(%s) missing code", redirectURI)
		}

		tokenForm := url.Values{
			"grant_type":    {"authorization_code"},
			"client_id":     {"chatgpt-admin"},
			"client_secret": {"super-secret-value"},
			"redirect_uri":  {redirectURI},
			"code":          {code},
			"code_verifier": {"verifier-verifier-verifier-verifier"},
		}
		tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
		tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tokenRec := httptest.NewRecorder()
		svc.HandleToken(tokenRec, tokenReq)
		if tokenRec.Code != http.StatusOK {
			t.Fatalf("token(%s) status = %d body = %q", redirectURI, tokenRec.Code, tokenRec.Body.String())
		}

		var tokenResp struct {
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
			t.Fatalf("token(%s) decode: %v", redirectURI, err)
		}
		if tokenResp.Scope != "site.admin" {
			t.Fatalf("token(%s) scope = %q want site.admin", redirectURI, tokenResp.Scope)
		}
	}
}

func TestClientRegistryRejectsInvalidRedirectURIs(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: chatgpt-admin
    secret: super-secret-value
    scopes: ["read", "write", "admin"]
    redirect_uris:
      - https://chatgpt.com/connector/oauth/*
    enabled: true
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry() error = %v", err)
	}

	cases := []string{
		"https://evilchatgpt.com/connector/oauth/callback",
		"http://chatgpt.com/connector/oauth/callback",
		"https://chatgpt.com/connector/other/callback",
	}
	for _, redirectURI := range cases {
		authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
			"response_type":         {"code"},
			"client_id":             {"chatgpt-admin"},
			"redirect_uri":          {redirectURI},
			"state":                 {"state-xyz"},
			"code_challenge":        {oauth.CodeChallengeS256("verifier-verifier-verifier-verifier")},
			"code_challenge_method": {"S256"},
		}.Encode(), nil)
		authReq.RemoteAddr = "127.0.0.1:1234"
		authRec := httptest.NewRecorder()
		svc.HandleAuthorize(authRec, authReq)
		if authRec.Code != http.StatusBadRequest {
			t.Fatalf("authorize(%s) status = %d body = %q", redirectURI, authRec.Code, authRec.Body.String())
		}
		if !strings.Contains(authRec.Body.String(), "invalid_redirect_uri") {
			t.Fatalf("authorize(%s) body = %q want invalid_redirect_uri", redirectURI, authRec.Body.String())
		}
	}
}

func TestClientRegistryClampsRequestedScopeToClientMax(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: chatgpt-read
    secret: super-secret-value
    scopes: ["read"]
    redirect_uris:
      - https://chatgpt.com/connector/oauth/*
    enabled: true
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry() error = %v", err)
	}

	// Request a wider scope (site.admin) than the client is allowed (content.read).
	// Server must clamp to content.read and issue a code, not reject with invalid_scope.
	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"chatgpt-read"},
		"redirect_uri":          {"https://chatgpt.com/connector/oauth/callback"},
		"state":                 {"state-xyz"},
		"scope":                 {"content.read content.write site.admin"},
		"code_challenge":        {oauth.CodeChallengeS256("verifier-verifier-verifier-verifier")},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:1234"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if errParam := loc.Query().Get("error"); errParam != "" {
		t.Fatalf("authorize returned error=%q, want code (scope should be clamped)", errParam)
	}
	if loc.Query().Get("code") == "" {
		t.Fatalf("authorize redirect missing code param: %s", loc)
	}
}
