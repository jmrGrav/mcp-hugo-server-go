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

func TestLoadClientRegistryAndMintAdminScope(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - client_id: claude-admin
    client_secret: super-secret-value
    redirect_uris:
      - https://client.test/callback
    scope: site.admin
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

	meta := svc.AuthorizationServerMetadata()
	methods, _ := meta["token_endpoint_auth_methods_supported"].([]string)
	if len(methods) == 0 {
		t.Fatal("metadata missing token auth methods")
	}
	foundSecretMethod := false
	for _, m := range methods {
		if m == "client_secret_basic" || m == "client_secret_post" {
			foundSecretMethod = true
		}
	}
	if !foundSecretMethod {
		t.Fatalf("metadata methods = %v, want client_secret_* support", methods)
	}

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"claude-admin"},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"admin-state"},
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
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("missing auth code")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"claude-admin"},
		"client_secret": {"super-secret-value"},
		"redirect_uri":  {"https://client.test/callback"},
		"code":          {code},
		"code_verifier": {"verifier-verifier-verifier-verifier"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var resp struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if resp.Scope != "site.admin" {
		t.Fatalf("scope = %q want site.admin", resp.Scope)
	}
}
