package oauth_test

import (
	"database/sql"
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

	// The live HTTP handler for /.well-known/oauth-authorization-server is in the
	// server package and uses buildAuthServerMeta(cfg). Here we verify that after
	// loading a static client registry the service accepts client_secret_basic
	// credentials via a token request — the concrete proof that secret auth works.

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

func TestLoadClientRegistryUpsertsSQLiteClients(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.sqlite")
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
	}, mustSQLiteStore(t, storePath))
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry() error = %v", err)
	}

	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var clientID string
	var effectiveScope string
	var enabled int
	if err := db.QueryRow(`SELECT client_id, effective_scope, enabled FROM oauth_clients WHERE client_id = ?`, "chatgpt-admin").Scan(&clientID, &effectiveScope, &enabled); err != nil {
		t.Fatalf("query oauth_clients: %v", err)
	}
	if clientID != "chatgpt-admin" {
		t.Fatalf("client_id = %q want chatgpt-admin", clientID)
	}
	if effectiveScope != "system.admin" {
		t.Fatalf("effective_scope = %q want system.admin", effectiveScope)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d want 1", enabled)
	}
}

func TestLoadClientRegistryErrors(t *testing.T) {
	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())

	if err := svc.LoadClientRegistry(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected missing registry file to fail")
	}

	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte("clients: ["), 0o600); err != nil {
		t.Fatalf("write malformed registry: %v", err)
	}
	if err := svc.LoadClientRegistry(registryPath); err == nil {
		t.Fatal("expected malformed registry to fail")
	}

	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: duplicate
    secret: one
    redirect_uris:
      - https://client.test/callback
  - client_id: duplicate
    secret: two
    redirect_uris:
      - https://client.test/callback
`), 0o600); err != nil {
		t.Fatalf("write duplicate registry: %v", err)
	}
	if err := svc.LoadClientRegistry(registryPath); err == nil {
		t.Fatal("expected duplicate client_id to fail")
	}
}

func mustSQLiteStore(t *testing.T, path string) storage.Store {
	t.Helper()
	store, err := storage.NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	return store
}
