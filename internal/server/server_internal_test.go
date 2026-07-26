package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func TestOpenStoreBranches(t *testing.T) {
	if store, err := openStore(config.OAuthConfig{}); err != nil || store == nil {
		t.Fatalf("openStore(memory) = %v, %v", store, err)
	}

	if _, err := openStore(config.OAuthConfig{StorageBackend: "json"}); err == nil {
		t.Fatal("openStore(json) should require storage_path")
	}
	if _, err := openStore(config.OAuthConfig{StorageBackend: "sqlite"}); err == nil {
		t.Fatal("openStore(sqlite) should require storage_path")
	}

	jsonPath := filepath.Join(t.TempDir(), "tokens.json")
	jsonStore, err := openStore(config.OAuthConfig{StorageBackend: "json", StoragePath: jsonPath})
	if err != nil {
		t.Fatalf("openStore(json path) error = %v", err)
	}
	_ = jsonStore.Close()

	sqlitePath := filepath.Join(t.TempDir(), "tokens.sqlite")
	sqliteStore, err := openStore(config.OAuthConfig{StorageBackend: "sqlite", StoragePath: sqlitePath})
	if err != nil {
		t.Fatalf("openStore(sqlite path) error = %v", err)
	}
	_ = sqliteStore.Close()
}

func TestRegistryRequiredScopeFor(t *testing.T) {
	reg := tools.NewRegistry()
	for _, d := range toolsanon.Defs() {
		reg.Register(d)
	}
	for _, d := range toolsread.Defs() {
		reg.Register(d)
	}
	if got, ok := reg.RequiredScopeFor("list_pages"); !ok || got != "" {
		t.Fatalf("RequiredScopeFor(list_pages) = %q, %v", got, ok)
	}
	if got, ok := reg.RequiredScopeFor("validate_site"); !ok || got != "" {
		t.Fatalf("RequiredScopeFor(validate_site) = %q, %v", got, ok)
	}
	if got, ok := reg.RequiredScopeFor("missing"); ok || got != "" {
		t.Fatalf("RequiredScopeFor(missing) = %q, %v", got, ok)
	}
}

func TestServerRunShutsDown(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HTTPBindAddr = "127.0.0.1"
	cfg.HTTPBindPort = 0
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		done <- srv.Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestDiscoveryBuildersFallbacks(t *testing.T) {
	cfg := config.Default()
	cfg.SiteURL = ""
	cfg.SiteName = ""
	cfg.OAuth.Issuer = "https://mcp.test"
	cfg.OAuth.Resource = ""
	cfg.OAuth.DynamicClientEnabled = true

	authMeta := buildAuthServerMeta(cfg)
	if authMeta.Issuer != "https://mcp.test" {
		t.Fatalf("buildAuthServerMeta issuer = %q", authMeta.Issuer)
	}
	if authMeta.RegistrationEndpoint != "https://mcp.test/register" {
		t.Fatalf("buildAuthServerMeta registration endpoint = %q", authMeta.RegistrationEndpoint)
	}
	if authMeta.ServiceDocumentation != "https://mcp.test/mcp" {
		t.Fatalf("buildAuthServerMeta service documentation = %q", authMeta.ServiceDocumentation)
	}

	resourceMeta := buildProtectedResourceMeta(cfg)
	if resourceMeta.Resource != "https://mcp.test/mcp" {
		t.Fatalf("buildProtectedResourceMeta resource = %q", resourceMeta.Resource)
	}

	card := buildMCPServerCard(cfg)
	if card.ServerInfo.Title != "MCP Server" {
		t.Fatalf("buildMCPServerCard title = %q", card.ServerInfo.Title)
	}
	if card.DocumentationURL != "https://mcp.test/auth.md" {
		t.Fatalf("buildMCPServerCard documentation = %q", card.DocumentationURL)
	}

	llms := buildLLMsTxt(cfg)
	if !strings.Contains(llms, "MCP endpoint: https://mcp.test/mcp") {
		t.Fatalf("buildLLMsTxt() = %q", llms)
	}
	if got := buildAgentCard(cfg); got.Name != "MCP Hugo Server" || got.URL != "https://mcp.test" {
		t.Fatalf("buildAgentCard() = %#v", got)
	}
}

func TestReaderAcquisitionProfileMatrix(t *testing.T) {
	tests := []struct {
		name            string
		cfg             config.Config
		wantMode        string
		wantDescription string
	}{
		{
			name:            "oauth disabled",
			cfg:             config.Default(),
			wantMode:        "anonymous_mcp_access",
			wantDescription: "anonymous MCP access",
		},
		{
			name: "dynamic and self-registration",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.DynamicClientEnabled = true
				cfg.OAuth.AllowReaderSelfRegistration = true
				return cfg
			}(),
			wantMode:        "self_serve_oauth_or_agent_identity_registration",
			wantDescription: "self-serve OAuth registration or anonymous agent identity registration",
		},
		{
			name: "dynamic only",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.DynamicClientEnabled = true
				return cfg
			}(),
			wantMode:        "self_serve_oauth_registration",
			wantDescription: "self-serve OAuth registration",
		},
		{
			name: "self-registration only",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.AllowReaderSelfRegistration = true
				return cfg
			}(),
			wantMode:        "self_serve_agent_identity_registration",
			wantDescription: "anonymous agent identity registration",
		},
		{
			name: "operator approved",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				return cfg
			}(),
			wantMode:        "operator_approved_claim_or_pre_registered_oauth_client",
			wantDescription: "operator-approved anonymous claim or pre-registered OAuth client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotDescription := readerAcquisitionProfile(tt.cfg)
			if gotMode != tt.wantMode || gotDescription != tt.wantDescription {
				t.Fatalf("readerAcquisitionProfile() = (%q, %q), want (%q, %q)", gotMode, gotDescription, tt.wantMode, tt.wantDescription)
			}
		})
	}
}

func TestServeDiscoveryTextSupportsGetAndHeadOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("GET Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Fatalf("GET body = %q, want hello", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("HEAD body = %q, want empty", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
	}
}

func TestHandleSecurityTxtMethodAndMissingConfig(t *testing.T) {
	cfg := config.Default()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	handleSecurityTxt(rec, req, cfg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET without SecurityContactURL status = %d, want 404", rec.Code)
	}

	cfg.SecurityContact = "mailto:security@example.test"
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/.well-known/security.txt", nil)
	handleSecurityTxt(rec, req, cfg)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST with SecurityContact status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
	}
}

func TestHandleAuthMdMethodAndMissingFile(t *testing.T) {
	cfg := config.Default()
	cfg.OAuth.Issuer = "https://mcp.test"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth.md", nil)
	handleAuthMd(rec, req, cfg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing auth.md status = %d, want 404", rec.Code)
	}

	root := t.TempDir()
	cfg.SiteRoot = root
	if err := os.WriteFile(filepath.Join(root, "auth.md"), []byte("# auth\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(auth.md): %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth.md", nil)
	handleAuthMd(rec, req, cfg)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST auth.md status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
	}
}

// TestRegistryServerConsistency guards against drift between the Defs()
// declarations in each tool package and the global registry built by
// buildRegistry() in the server. If a tool is added to a package but
// not to its Defs(), or if a scope is changed in one place but not the
// other, this test will catch it (#70).
func TestRegistryServerConsistency(t *testing.T) {
	// Collect all Defs from every tool package (the authoritative declarations).
	allDefs := make(map[string]string) // name -> requiredScope
	for _, d := range toolsanon.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolsread.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolswrite.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolsadmin.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}

	// Build the registry the same way the server does.
	reg := buildRegistry()

	// Every tool in Defs() must be in the registry with the same scope.
	for name, wantScope := range allDefs {
		gotScope, ok := reg.RequiredScopeFor(name)
		if !ok {
			t.Errorf("tool %q is declared in Defs() but missing from buildRegistry()", name)
			continue
		}
		if gotScope != wantScope {
			t.Errorf("tool %q: Defs() scope=%q, registry scope=%q — drift detected", name, wantScope, gotScope)
		}
	}

	// Every tool in the registry must appear in at least one Defs().
	for _, d := range reg.All() {
		if _, ok := allDefs[d.Name]; !ok {
			t.Errorf("tool %q is in buildRegistry() but not declared in any package Defs()", d.Name)
		}
	}
}

func TestServerRunWithOAuthEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HTTPBindAddr = "127.0.0.1"
	cfg.HTTPBindPort = 0
	cfg.OAuth.Enabled = true
	cfg.OAuth.Issuer = "https://mcp.test"
	cfg.OAuth.Resource = "https://mcp.test/mcp"
	cfg.OAuth.DynamicClientEnabled = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		done <- srv.Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestMCPBearerAuthMiddlewareMissingBearerPreservesChallengeShape(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		t.Fatal("verifier should not run when bearer header is missing")
		return nil, nil
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on missing bearer")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "unauthorized" {
		t.Fatalf("body = %q want unauthorized", got)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="https://mcp.test"`) {
		t.Fatalf("WWW-Authenticate = %q, want realm", wwwAuth)
	}
	if !strings.Contains(wwwAuth, `resource_metadata="https://mcp.test/.well-known/oauth-protected-resource"`) {
		t.Fatalf("WWW-Authenticate = %q, want resource_metadata", wwwAuth)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q want no-store", got)
	}
}

func TestMCPBearerAuthMiddlewareInvalidBearerPreservesInvalidTokenMarker(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		return nil, errors.Join(sdkauth.ErrInvalidToken, errors.New("wrapped verifier detail"))
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer broken")
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on invalid bearer")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate = %q, want invalid_token marker", wwwAuth)
	}
}

func TestInterceptResponseWriterFlushBufferedToReal(t *testing.T) {
	rec := httptest.NewRecorder()
	iw := newInterceptResponseWriter(rec)
	iw.Header().Set("Content-Type", "application/json")
	iw.Header().Add("X-Test", "one")
	iw.WriteHeader(http.StatusAccepted)
	if _, err := iw.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	iw.flushBufferedToReal()

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("X-Test"); got != "one" {
		t.Fatalf("X-Test = %q, want one", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want JSON payload", got)
	}
}

func TestMCPBearerAuthMiddlewareInvalidFormatPreservesChallengeShape(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		t.Fatal("verifier should not run when bearer header format is invalid")
		return nil, nil
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Basic nope")
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on invalid bearer format")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="https://mcp.test"`) {
		t.Fatalf("WWW-Authenticate = %q, want realm", wwwAuth)
	}
	if strings.Contains(wwwAuth, `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate = %q, invalid format must not be marked invalid_token", wwwAuth)
	}
}

func TestMCPBearerAuthMiddlewareValidBearerReachesNextWithoutCorruption(t *testing.T) {
	store := storage.NewMemory()
	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
	}, store)
	if err := store.AddAccessToken(oauth.HashToken("token-valid"), "reader", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}

	var (
		called bool
		gotBR  mcpBearerResult
		gotOK  bool
	)
	mw := newMCPBearerAuthMiddleware(oauthTokenVerifier(svc), "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer token-valid")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotBR, gotOK = bearerResultFromContext(r.Context())
		w.Header().Set("X-Next", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called for valid bearer")
	}
	if !gotOK {
		t.Fatal("bearerResultFromContext() = not ok, want ok")
	}
	if gotBR.scope != "read" || !gotBR.legacy || gotBR.tokenHash != oauth.HashToken("token-valid") {
		t.Fatalf("bearerResultFromContext() = %#v, want canonical scope, legacy alias marker, and token hash", gotBR)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("X-Next"); got != "yes" {
		t.Fatalf("X-Next = %q, want yes", got)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
}
