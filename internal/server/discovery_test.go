package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func mustDiscoveryServer(t *testing.T, siteRoot string) *server.Server {
	t.Helper()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://www.arleo.eu"
	cfg.SiteName = "arleo.eu"
	cfg.OAuth.Issuer = "https://mcp.arleo.eu"
	cfg.OAuth.Resource = "https://mcp.arleo.eu/mcp"
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

func TestWellKnownOAuthServer(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q want application/json", ct)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := got["agent_auth"]; !ok {
		t.Fatal("response missing agent_auth field")
	}
	if _, ok := got["issuer"]; !ok {
		t.Fatal("response missing issuer field")
	}

	var agentAuth map[string]json.RawMessage
	if err := json.Unmarshal(got["agent_auth"], &agentAuth); err != nil {
		t.Fatalf("agent_auth is not an object: %v", err)
	}

	checkStringField := func(obj map[string]json.RawMessage, key, want string) {
		t.Helper()
		raw, ok := obj[key]
		if !ok {
			t.Errorf("agent_auth missing %q", key)
			return
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("agent_auth[%q] not a string: %v", key, err)
			return
		}
		if got != want {
			t.Errorf("agent_auth[%q] = %q want %q", key, got, want)
		}
	}

	checkStringField(agentAuth, "identity_endpoint", "https://mcp.arleo.eu/agent/identity")
	checkStringField(agentAuth, "claim_endpoint", "https://mcp.arleo.eu/agent/identity/claim")
	checkStringField(agentAuth, "events_endpoint", "https://mcp.arleo.eu/agent/event/notify")
	checkStringField(agentAuth, "skill", "https://mcp.arleo.eu/auth.md")

	var grantTypes []string
	if err := json.Unmarshal(got["grant_types_supported"], &grantTypes); err != nil {
		t.Fatalf("grant_types_supported: %v", err)
	}
	wantGrants := []string{"authorization_code", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:workos:agent-auth:grant-type:claim"}
	for _, g := range wantGrants {
		found := false
		for _, ag := range grantTypes {
			if ag == g {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("grant_types_supported missing %q", g)
		}
	}
}

func TestWellKnownMCPServerCard(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp/server-card.json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q want application/json", ct)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET" {
		t.Fatalf("Access-Control-Allow-Methods = %q want GET", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers = %q want Content-Type", got)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["$schema"] != "https://static.modelcontextprotocol.io/schemas/mcp-server-card/v1.json" {
		t.Fatalf("$schema = %v", got["$schema"])
	}
	if got["version"] != "1.0" {
		t.Fatalf("version = %v", got["version"])
	}
	if got["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v", got["protocolVersion"])
	}

	serverInfo, ok := got["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo type = %T", got["serverInfo"])
	}
	if serverInfo["name"] != "mcp-hugo-server-go" {
		t.Fatalf("serverInfo.name = %v", serverInfo["name"])
	}
	if serverInfo["version"] != "v1.0.0" {
		t.Fatalf("serverInfo.version = %v", serverInfo["version"])
	}

	transport, ok := got["transport"].(map[string]any)
	if !ok {
		t.Fatalf("transport type = %T", got["transport"])
	}
	if transport["type"] != "streamable-http" {
		t.Fatalf("transport.type = %v", transport["type"])
	}
	if transport["endpoint"] != "/mcp" {
		t.Fatalf("transport.endpoint = %v", transport["endpoint"])
	}

	auth, ok := got["authentication"].(map[string]any)
	if !ok {
		t.Fatalf("authentication type = %T", got["authentication"])
	}
	if auth["required"] != true {
		t.Fatalf("authentication.required = %v", auth["required"])
	}
	schemes, ok := auth["schemes"].([]any)
	if !ok {
		t.Fatalf("authentication.schemes type = %T", auth["schemes"])
	}
	if len(schemes) != 2 || schemes[0] != "bearer" || schemes[1] != "oauth2" {
		t.Fatalf("authentication.schemes = %v", schemes)
	}
}

func TestLegacyMCPJSONAliasStillServed(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp.json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["version"] != "1.0" {
		t.Fatalf("legacy alias should serve same server card version, got %v", got["version"])
	}
}

func TestWellKnownOAuthServerMethodNotAllowed(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d want 405", rec.Code)
	}
}

func TestWellKnownProtectedResource(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q want application/json", ct)
	}

	var got struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Resource != "https://mcp.arleo.eu/mcp" {
		t.Errorf("resource = %q want https://mcp.arleo.eu/mcp", got.Resource)
	}
	if len(got.AuthorizationServers) != 1 || got.AuthorizationServers[0] != "https://mcp.arleo.eu" {
		t.Errorf("authorization_servers = %v want [https://mcp.arleo.eu]", got.AuthorizationServers)
	}

	var meta struct {
		ScopesSupported []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("invalid JSON for scopes_supported: %v", err)
	}
	for _, bad := range []string{"mcp"} {
		for _, scope := range meta.ScopesSupported {
			if scope == bad {
				t.Fatalf("oauth-protected-resource scopes_supported should not contain legacy scope %q", bad)
			}
		}
	}
}

func TestRobotsTxt(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "User-agent: *") {
		t.Errorf("robots.txt missing 'User-agent: *', got: %q", body)
	}
	if !strings.Contains(body, "Allow: /") {
		t.Errorf("robots.txt missing 'Allow: /', got: %q", body)
	}
	if !strings.Contains(body, "sitemap.xml") {
		t.Errorf("robots.txt missing sitemap.xml reference, got: %q", body)
	}
	if !strings.Contains(body, "www.arleo.eu") {
		t.Errorf("robots.txt missing site URL, got: %q", body)
	}
}

func TestLLMsTxt(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "arleo.eu") {
		t.Errorf("llms.txt missing site name, got: %q", body)
	}
	if !strings.Contains(body, "mcp") {
		t.Errorf("llms.txt missing MCP reference, got: %q", body)
	}
}

func TestAgentJSON(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent.json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["name"] == "" || got["url"] == "" {
		t.Fatalf("agent.json missing required fields: %#v", got)
	}
}

func TestAuthMdServed(t *testing.T) {
	dir := t.TempDir()
	const content = "# auth.md protocol\n\nAgent authentication instructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "auth.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := mustDiscoveryServer(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/auth.md", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q want text/markdown", ct)
	}
	if !strings.Contains(rec.Body.String(), "auth.md protocol") {
		t.Errorf("body missing expected content, got: %q", rec.Body.String())
	}
}

func TestAuthMdNotFound(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/auth.md", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d want 404", rec.Code)
	}
}

func TestOAuthServerServedWithOAuthDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.OAuth.Enabled = false
	cfg.OAuth.Issuer = "https://mcp.arleo.eu"
	cfg.OAuth.Resource = "https://mcp.arleo.eu/mcp"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("discovery must be served even when OAuth is disabled: status = %d", rec.Code)
	}
}
