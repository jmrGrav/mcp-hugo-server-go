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
	cfg.OAuth.Enabled = true
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
	checkStringField(agentAuth, "claim_uri", "https://mcp.arleo.eu/agent/identity/claim")
	checkStringField(agentAuth, "events_endpoint", "https://mcp.arleo.eu/agent/event/notify")
	checkStringField(agentAuth, "skill", "https://mcp.arleo.eu/auth.md")

	var identityTypes []string
	if err := json.Unmarshal(agentAuth["identity_types_supported"], &identityTypes); err != nil {
		t.Fatalf("identity_types_supported: %v", err)
	}
	for _, want := range []string{"anonymous", "identity_assertion"} {
		found := false
		for _, got := range identityTypes {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("identity_types_supported missing %q", want)
		}
	}

	var anonymous struct {
		CredentialTypesSupported []string `json:"credential_types_supported"`
		ClaimURI                 string   `json:"claim_uri"`
	}
	if err := json.Unmarshal(agentAuth["anonymous"], &anonymous); err != nil {
		t.Fatalf("anonymous: %v", err)
	}
	if anonymous.ClaimURI != "https://mcp.arleo.eu/agent/identity/claim" {
		t.Errorf("anonymous.claim_uri = %q", anonymous.ClaimURI)
	}
	if len(anonymous.CredentialTypesSupported) == 0 {
		t.Fatal("anonymous.credential_types_supported is empty")
	}
	var identityAssertion struct {
		AssertionTypesSupported  []string `json:"assertion_types_supported"`
		CredentialTypesSupported []string `json:"credential_types_supported"`
	}
	if err := json.Unmarshal(agentAuth["identity_assertion"], &identityAssertion); err != nil {
		t.Fatalf("identity_assertion: %v", err)
	}
	if len(identityAssertion.AssertionTypesSupported) == 0 {
		t.Fatal("identity_assertion.assertion_types_supported is empty")
	}
	if len(identityAssertion.CredentialTypesSupported) == 0 {
		t.Fatal("identity_assertion.credential_types_supported is empty")
	}

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

func TestWellKnownOAuthServerWithClientRegistry(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - client_id: claude-admin
    client_secret: admin-secret-value
    redirect_uris:
      - https://client.test/callback
    scope: site.admin
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	cfg := config.Default()
	cfg.SiteRoot = dir
	cfg.SiteURL = "https://www.arleo.eu"
	cfg.SiteName = "arleo.eu"
	cfg.OAuth.Issuer = "https://mcp.arleo.eu"
	cfg.OAuth.Resource = "https://mcp.arleo.eu/mcp"
	cfg.OAuth.ClientRegistryPath = registryPath
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
		t.Fatalf("status = %d want 200", rec.Code)
	}
	var meta struct {
		TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, m := range meta.TokenEndpointAuthMethodsSupported {
		if m == "client_secret_basic" || m == "client_secret_post" {
			found = true
		}
	}
	if !found {
		t.Fatalf("token_endpoint_auth_methods_supported = %v, want client_secret_* support", meta.TokenEndpointAuthMethodsSupported)
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
	if serverInfo["version"] == "" {
		t.Fatalf("serverInfo.version is empty")
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

	var meta struct {
		Resource              string   `json:"resource"`
		AuthorizationServers  []string `json:"authorization_servers"`
		ScopesSupported       []string `json:"scopes_supported"`
		ResourceDocumentation string   `json:"resource_documentation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if meta.Resource != "https://mcp.arleo.eu/mcp" {
		t.Errorf("resource = %q want https://mcp.arleo.eu/mcp", meta.Resource)
	}
	if len(meta.AuthorizationServers) != 1 || meta.AuthorizationServers[0] != "https://mcp.arleo.eu" {
		t.Errorf("authorization_servers = %v want [https://mcp.arleo.eu]", meta.AuthorizationServers)
	}
	// resource_documentation must point to auth.md, NOT the MCP endpoint.
	// Scanners follow this URL to find the registration flow; the /mcp endpoint
	// requires auth and returns 401, breaking agent-readiness checks.
	if meta.ResourceDocumentation != "https://mcp.arleo.eu/auth.md" {
		t.Errorf("resource_documentation = %q want https://mcp.arleo.eu/auth.md (must not be the /mcp endpoint)", meta.ResourceDocumentation)
	}
	for _, bad := range []string{"mcp"} {
		for _, scope := range meta.ScopesSupported {
			if scope == bad {
				t.Fatalf("scopes_supported must not contain legacy scope %q", bad)
			}
		}
	}
	wantScopes := []string{"content.read", "content.write", "site.admin", "system.admin"}
	for _, want := range wantScopes {
		found := false
		for _, s := range meta.ScopesSupported {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scopes_supported missing %q, got %v", want, meta.ScopesSupported)
		}
	}
}

func TestWellKnownProtectedResourceAliasMCP(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q want application/json", ct)
	}

	var meta struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if meta.Resource != "https://mcp.arleo.eu/mcp" {
		t.Fatalf("resource = %q want https://mcp.arleo.eu/mcp", meta.Resource)
	}
	if len(meta.AuthorizationServers) != 1 || meta.AuthorizationServers[0] != "https://mcp.arleo.eu" {
		t.Fatalf("authorization_servers = %v want [https://mcp.arleo.eu]", meta.AuthorizationServers)
	}
}

func TestAuthServerMetaRegistrationEndpoint(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var meta struct {
		RegistrationEndpoint string `json:"registration_endpoint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// registration_endpoint must always be present and point to /register (#117).
	// Removing or omitting it breaks agent-readiness scanners.
	if meta.RegistrationEndpoint == "" {
		t.Fatal("registration_endpoint must be present in OAuth authorization server metadata (#117)")
	}
	if !strings.HasSuffix(meta.RegistrationEndpoint, "/register") {
		t.Errorf("registration_endpoint = %q, must end with /register", meta.RegistrationEndpoint)
	}
}

func TestAuthServerMetaRegisterURI(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var meta struct {
		AgentAuth struct {
			RegisterURI string `json:"register_uri"`
			Skill       string `json:"skill"`
		} `json:"agent_auth"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// agent_auth.register_uri must be present so agent-readiness scanners can
	// find the registration endpoint via the auth server discovery metadata.
	if meta.AgentAuth.RegisterURI == "" {
		t.Fatal("agent_auth.register_uri must be present in OAuth auth server metadata")
	}
	// agent_auth.skill must point to auth.md, not to any endpoint requiring auth.
	if !strings.HasSuffix(meta.AgentAuth.Skill, "/auth.md") {
		t.Errorf("agent_auth.skill = %q, must end with /auth.md", meta.AgentAuth.Skill)
	}
}

func TestAuthMdContainsRegistrationFlow(t *testing.T) {
	dir := t.TempDir()
	// auth.md must contain a machine-readable registration_flow block so
	// agent-readiness scanners can autonomously complete registration.
	// This test prevents future code-cleanup from stripping required content.
	const authMd = `# Auth.md

## Agent registration

### Standalone registration flow

` + "```json\n" + `{
  "registration_flow": {
    "step_1_register": {
      "method": "POST",
      "url": "https://mcp.arleo.eu/register"
    }
  },
  "endpoints": {
    "registration_endpoint": "https://mcp.arleo.eu/register"
  }
}
` + "```\n"
	if err := os.WriteFile(filepath.Join(dir, "auth.md"), []byte(authMd), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := mustDiscoveryServer(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/auth.md", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "registration_flow") {
		t.Error("auth.md must contain 'registration_flow' for agent-readiness scanners")
	}
	if !strings.Contains(body, "agent_auth_metadata") {
		t.Error("auth.md must contain 'agent_auth_metadata' for agent-readiness scanners")
	}
	if !strings.Contains(body, "credential_types_supported") {
		t.Error("auth.md must document anonymous.credential_types_supported")
	}
	if !strings.Contains(body, "claim_uri") {
		t.Error("auth.md must document claim_uri")
	}
	if !strings.Contains(body, `"identity_types_supported": ["anonymous", "identity_assertion"]`) {
		t.Error("auth.md must document ID-JAG identity_assertion support")
	}
	if !strings.Contains(body, "/register") {
		t.Error("auth.md must reference the /register endpoint")
	}
	// Scanners extract URLs from auth.md by scanning for https:// prefixes.
	// A URL followed immediately by a markdown backtick (end-of-line) causes the
	// scanner to include the backtick in the URL, turning a valid path into a
	// 404. Ensure no well-known URLs are backtick-wrapped at end-of-line.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, ".well-known/") && strings.HasSuffix(strings.TrimRight(line, "\r"), "`") {
			t.Errorf("auth.md line ends with backtick after a .well-known/ URL — scanner will include the backtick in the URL: %q", line)
		}
	}
}

func TestAuthMdAppendsCanonicalRegistrationBlockWhenMissing(t *testing.T) {
	dir := t.TempDir()
	const content = "# auth.md\n\nAgent authentication instructions.\n"
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
	body := rec.Body.String()
	if !strings.Contains(body, "registration_flow") {
		t.Fatal("auth.md response must append a machine-readable registration_flow block")
	}
	if !strings.Contains(body, "agent_auth_metadata") {
		t.Fatal("auth.md response must append agent_auth_metadata")
	}
	if !strings.Contains(body, "registration_endpoint") {
		t.Fatal("auth.md response must mention registration_endpoint")
	}
	if !strings.Contains(body, "https://mcp.arleo.eu/register") {
		t.Fatal("auth.md response must reference the canonical /register endpoint")
	}
}

func TestAuthMdAppendsAgentAuthMetadataWhenRegistrationFlowAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	const content = `# Auth.md

` + "```json\n" + `{
  "registration_flow": {
    "registration_endpoint": "https://mcp.arleo.eu/register"
  }
}
` + "```\n"
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
	body := rec.Body.String()
	for _, want := range []string{
		"registration_flow",
		"agent_auth_metadata",
		"credential_types_supported",
		"claim_uri",
		`"identity_types_supported": ["anonymous", "identity_assertion"]`,
		"urn:ietf:params:oauth:token-type:id-jag",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("auth.md missing %q", want)
		}
	}
	if strings.Count(body, "registration_flow") != 1 {
		t.Fatalf("registration_flow should not be duplicated, body:\n%s", body)
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

func TestSecurityTxtServed(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://www.arleo.eu"
	cfg.SecurityContact = "mailto:security@example.com"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Contact: mailto:security@example.com") {
		t.Errorf("security.txt missing Contact, got: %q", body)
	}
	if !strings.Contains(body, "Expires:") {
		t.Errorf("security.txt missing Expires, got: %q", body)
	}
	if !strings.Contains(body, "Canonical:") {
		t.Errorf("security.txt missing Canonical, got: %q", body)
	}
}

func TestSecurityTxtNotFoundWhenUnconfigured(t *testing.T) {
	srv := mustDiscoveryServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d want 404 when SecurityContact not configured", rec.Code)
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

func TestDiscoveryHeadRequests(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.md"), []byte("# auth\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := mustDiscoveryServer(t, dir)
	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/mcp/server-card.json",
		"/robots.txt",
		"/llms.txt",
		"/auth.md",
	} {
		req := httptest.NewRequest(http.MethodHead, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HEAD %s status = %d want 200", path, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD %s should not include a body, got %q", path, rec.Body.String())
		}
	}
}
