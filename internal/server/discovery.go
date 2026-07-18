package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

type discoveryIdentityAssertion struct {
	AssertionTypesSupported  []string `json:"assertion_types_supported"`
	CredentialTypesSupported []string `json:"credential_types_supported"`
}

type discoveryAnonymousAuth struct {
	CredentialTypesSupported []string `json:"credential_types_supported"`
	ClaimURI                 string   `json:"claim_uri"`
}

type discoveryAgentAuth struct {
	Skill                  string                     `json:"skill"`
	RegisterURI            string                     `json:"register_uri"`
	IdentityEndpoint       string                     `json:"identity_endpoint"`
	ClaimEndpoint          string                     `json:"claim_endpoint"`
	ClaimURI               string                     `json:"claim_uri"`
	EventsEndpoint         string                     `json:"events_endpoint"`
	IdentityTypesSupported []string                   `json:"identity_types_supported"`
	Anonymous              discoveryAnonymousAuth     `json:"anonymous"`
	IdentityAssertion      discoveryIdentityAssertion `json:"identity_assertion"`
	EventsSupported        []string                   `json:"events_supported"`
}

type discoveryAccessProfile struct {
	Description    string   `json:"description"`
	Acquisition    string   `json:"acquisition"`
	InternalScopes []string `json:"internal_scopes"`
}

type authServerMeta struct {
	Issuer                            string                            `json:"issuer"`
	AuthorizationEndpoint             string                            `json:"authorization_endpoint"`
	TokenEndpoint                     string                            `json:"token_endpoint"`
	RegistrationEndpoint              string                            `json:"registration_endpoint,omitempty"`
	ResponseTypesSupported            []string                          `json:"response_types_supported"`
	GrantTypesSupported               []string                          `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string                          `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string                          `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string                          `json:"scopes_supported"`
	AccessProfiles                    map[string]discoveryAccessProfile `json:"access_profiles"`
	ServiceDocumentation              string                            `json:"service_documentation"`
	AgentAuth                         discoveryAgentAuth                `json:"agent_auth"`
}

type protectedResourceMeta struct {
	Resource               string                            `json:"resource"`
	AuthorizationServers   []string                          `json:"authorization_servers"`
	BearerMethodsSupported []string                          `json:"bearer_methods_supported"`
	ScopesSupported        []string                          `json:"scopes_supported"`
	AccessProfiles         map[string]discoveryAccessProfile `json:"access_profiles"`
	ResourceDocumentation  string                            `json:"resource_documentation"`
}

func discoveryAccessProfiles() map[string]discoveryAccessProfile {
	return map[string]discoveryAccessProfile{
		"reader": {
			Description:    "Read-only access profile for discovery and content inspection (full visibility, drafts included).",
			Acquisition:    "anonymous or self-serve registration",
			InternalScopes: []string{"read"},
		},
		"operator": {
			Description:    "Approved operator profile that bundles read, write, and site operation capabilities.",
			Acquisition:    "approved token present in the server registry",
			InternalScopes: []string{"read", "write"},
		},
	}
}

func buildAuthServerMeta(cfg config.Config) authServerMeta {
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	resource := strings.TrimSpace(cfg.OAuth.Resource)
	if resource == "" {
		resource = issuer + "/mcp"
	}
	// /register is always live when OAuth is enabled (RFC 7591 DCR endpoint).
	// DynamicClientEnabled controls whether unauthenticated public registration
	// is accepted; the endpoint itself is always advertised so agent discovery
	// stays coherent with auth.md and the live /register surface (#117).
	registrationEndpoint := issuer + "/register"
	return authServerMeta{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/authorize",
		TokenEndpoint:                     issuer + "/token",
		RegistrationEndpoint:              registrationEndpoint,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:workos:agent-auth:grant-type:claim"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: tokenEndpointAuthMethods(cfg),
		ScopesSupported:                   tools.KnownScopes,
		AccessProfiles:                    discoveryAccessProfiles(),
		ServiceDocumentation:              resource,
		AgentAuth: discoveryAgentAuth{
			Skill:                  issuer + "/auth.md",
			RegisterURI:            issuer + "/register",
			IdentityEndpoint:       issuer + "/agent/identity",
			ClaimEndpoint:          issuer + "/agent/identity/claim",
			ClaimURI:               issuer + "/agent/identity/claim",
			EventsEndpoint:         issuer + "/agent/event/notify",
			IdentityTypesSupported: []string{"anonymous", "identity_assertion"},
			Anonymous: discoveryAnonymousAuth{
				CredentialTypesSupported: []string{"none"},
				ClaimURI:                 issuer + "/agent/identity/claim",
			},
			IdentityAssertion: discoveryIdentityAssertion{
				AssertionTypesSupported:  []string{"urn:ietf:params:oauth:token-type:id-jag"},
				CredentialTypesSupported: []string{"urn:ietf:params:oauth:token-type:id-jag"},
			},
			EventsSupported: []string{"https://schemas.workos.com/events/agent/auth/identity/assertion/revoked"},
		},
	}
}

func tokenEndpointAuthMethods(cfg config.Config) []string {
	methods := make([]string, 0, 3)
	if cfg.OAuth.DynamicClientEnabled {
		methods = append(methods, "none")
	}
	if strings.TrimSpace(cfg.OAuth.ClientRegistryPath) != "" {
		methods = append(methods, "client_secret_basic", "client_secret_post")
	}
	if len(methods) == 0 {
		methods = append(methods, "none")
	}
	return methods
}

func buildProtectedResourceMeta(cfg config.Config) protectedResourceMeta {
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	resource := strings.TrimSpace(cfg.OAuth.Resource)
	if resource == "" {
		resource = issuer + "/mcp"
	}
	return protectedResourceMeta{
		Resource:               resource,
		AuthorizationServers:   []string{issuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        tools.KnownScopes,
		AccessProfiles:         discoveryAccessProfiles(),
		ResourceDocumentation:  issuer + "/auth.md",
	}
}

type mcpServerCard struct {
	Schema           string            `json:"$schema"`
	Version          string            `json:"version"`
	ProtocolVersion  string            `json:"protocolVersion"`
	ServerInfo       mcpServerInfo     `json:"serverInfo"`
	Description      string            `json:"description"`
	Transport        mcpTransport      `json:"transport"`
	Capabilities     mcpCapabilities   `json:"capabilities"`
	Authentication   mcpAuthentication `json:"authentication"`
	DocumentationURL string            `json:"documentationUrl,omitempty"`
	Resources        []string          `json:"resources,omitempty"`
	Tools            []string          `json:"tools,omitempty"`
	Prompts          []string          `json:"prompts,omitempty"`
}

type agentCard struct {
	Schema       string   `json:"$schema"`
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	URL          string   `json:"url"`
	Capabilities []string `json:"capabilities"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type mcpTransport struct {
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
}

type mcpCapabilities struct {
	Tools     map[string]any `json:"tools"`
	Prompts   map[string]any `json:"prompts"`
	Resources map[string]any `json:"resources"`
}

type mcpAuthentication struct {
	Required bool     `json:"required"`
	Schemes  []string `json:"schemes"`
	// AuthorizationServers and ProtectedResourceMetadata let a client that
	// discovers OAuth via this server card (rather than following the
	// WWW-Authenticate resource_metadata pointer on a 401, as RFC 9728
	// intends) bootstrap the standard discovery chain without an extra
	// round trip. Same authorization_servers value as the RFC 9728
	// protected-resource-metadata document (#424).
	AuthorizationServers      []string `json:"authorization_servers,omitempty"`
	ProtectedResourceMetadata string   `json:"protected_resource_metadata,omitempty"`
}

func buildMCPServerCard(cfg config.Config) mcpServerCard {
	name := cfg.SiteName
	if name == "" {
		name = cfg.SiteURL
	}
	base := strings.TrimRight(cfg.OAuth.Issuer, "/")
	if base == "" {
		base = strings.TrimRight(cfg.SiteURL, "/")
	}
	title := name
	if title == "" {
		title = "MCP Server"
	}
	description := name
	if description == "" {
		description = title
	}
	return mcpServerCard{
		Schema:          "https://static.modelcontextprotocol.io/schemas/mcp-server-card/v1.json",
		Version:         "1.0",
		ProtocolVersion: "2025-06-18",
		ServerInfo: mcpServerInfo{
			Name:    "mcp-hugo-server-go",
			Title:   title,
			Version: buildinfo.Version,
		},
		Description: description + " — a Hugo-published site available via MCP.",
		Transport: mcpTransport{
			Type:     "streamable-http",
			Endpoint: "/mcp",
		},
		Capabilities: mcpCapabilities{
			Tools: map[string]any{
				"listChanged": true,
			},
			Prompts: map[string]any{
				"listChanged": true,
			},
			Resources: map[string]any{
				"subscribe":   true,
				"listChanged": true,
			},
		},
		Authentication: mcpAuthentication{
			Required:                  cfg.OAuth.Enabled,
			Schemes:                   []string{"bearer", "oauth2"},
			AuthorizationServers:      []string{base},
			ProtectedResourceMetadata: base + "/.well-known/oauth-protected-resource",
		},
		DocumentationURL: base + "/auth.md",
		Resources:        []string{"dynamic"},
		Tools:            []string{"dynamic"},
		Prompts:          []string{"dynamic"},
	}
}

func handleMCPServerCard(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildMCPServerCard(cfg))
}

func handleMCPJSON(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildMCPServerCard(cfg))
}

func buildLLMsTxt(cfg config.Config) string {
	name := cfg.SiteName
	if name == "" {
		name = cfg.SiteURL
	}
	siteURL := strings.TrimRight(cfg.SiteURL, "/")
	mcpBase := strings.TrimRight(cfg.OAuth.Issuer, "/")
	if mcpBase == "" {
		mcpBase = siteURL
	}
	return fmt.Sprintf("# %s\n\n> %s — a Hugo-published site available via MCP.\n\nMCP endpoint: %s/mcp\n", name, siteURL, mcpBase)
}

func buildAgentCard(cfg config.Config) agentCard {
	name := cfg.SiteName
	if name == "" {
		name = cfg.SiteURL
	}
	if name == "" {
		name = "MCP Hugo Server"
	}
	base := strings.TrimRight(cfg.SiteURL, "/")
	if base == "" {
		base = strings.TrimRight(cfg.OAuth.Issuer, "/")
	}
	return agentCard{
		Schema:       "https://a2a.google.com/schemas/agent-card/v1.json",
		Version:      "1.0",
		Name:         name,
		Description:  name + " exposed through MCP and OAuth-backed discovery.",
		URL:          base,
		Capabilities: []string{"chat", "tools"},
	}
}

func handleOAuthAuthServer(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildAuthServerMeta(cfg))
}

func handleOAuthProtectedResource(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildProtectedResourceMeta(cfg))
}

func handleSecurityTxt(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	if cfg.SecurityContact == "" {
		http.NotFound(w, r)
		return
	}
	canonical := strings.TrimRight(cfg.SiteURL, "/")
	if canonical == "" {
		canonical = strings.TrimRight(cfg.OAuth.Issuer, "/")
	}
	expires := time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339)
	var body string
	if canonical != "" {
		body = fmt.Sprintf("Contact: %s\nExpires: %s\nCanonical: %s/.well-known/security.txt\n",
			cfg.SecurityContact, expires, canonical)
	} else {
		body = fmt.Sprintf("Contact: %s\nExpires: %s\n",
			cfg.SecurityContact, expires)
	}
	serveDiscoveryText(w, r, "text/plain; charset=utf-8", body)
}

func handleRobotsTxt(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	siteURL := strings.TrimRight(cfg.SiteURL, "/")
	body := fmt.Sprintf("User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", siteURL)
	serveDiscoveryText(w, r, "text/plain; charset=utf-8", body)
}

func handleLLMsTxt(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryText(w, r, "text/plain; charset=utf-8", buildLLMsTxt(cfg))
}

func handleAgentJSON(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildAgentCard(cfg))
}

func handleAuthMd(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile(filepath.Join(cfg.SiteRoot, "auth.md"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data = appendCanonicalAuthMdRegistrationBlock(data, cfg)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

func appendCanonicalAuthMdRegistrationBlock(data []byte, cfg config.Config) []byte {
	lower := bytes.ToLower(data)
	hasRegistrationFlow := bytes.Contains(lower, []byte("registration_flow"))
	hasAgentAuthMetadata := bytes.Contains(lower, []byte("agent_auth_metadata"))
	hasAccessProfiles := bytes.Contains(lower, []byte("access_profiles"))
	if hasRegistrationFlow && hasAgentAuthMetadata && hasAccessProfiles {
		return data
	}

	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	if issuer == "" {
		issuer = strings.TrimRight(cfg.SiteURL, "/")
	}
	if issuer == "" {
		return data
	}

	var block strings.Builder
	if !hasRegistrationFlow {
		block.WriteString(fmt.Sprintf(
			"## Agent registration\n\n"+
				"External access profiles: `reader` and `operator`.\n"+
				"`reader` is the public-safe read-only profile; `operator` bundles read, write, and site operations.\n"+
				"The OAuth scopes below remain the internal capability strings accepted by the server during v1.x.\n\n"+
				"Registration endpoint: `%s/register`\n"+
				"Authorization endpoint: `%s/authorize`\n"+
				"Token endpoint: `%s/token`\n"+
				"Protected resource metadata: %s/.well-known/oauth-protected-resource\n"+
				"MCP endpoint: `%s/mcp`\n"+
				"Scopes: `read`, `write`\n\n"+
				"```json\n"+
				"{\n"+
				"  \"registration_flow\": {\n"+
				"    \"registration_endpoint\": \"%s/register\",\n"+
				"    \"authorization_endpoint\": \"%s/authorize\",\n"+
				"    \"token_endpoint\": \"%s/token\",\n"+
				"    \"protected_resource_metadata\": \"%s/.well-known/oauth-protected-resource\",\n"+
				"    \"mcp_endpoint\": \"%s/mcp\",\n"+
				"    \"scopes\": [\n"+
				"      \"read\",\n"+
				"      \"write\"\n"+
				"    ]\n"+
				"  }\n"+
				"}\n"+
				"```\n",
			issuer, issuer, issuer, issuer, issuer, issuer, issuer, issuer, issuer, issuer,
		))
	}
	if !hasAccessProfiles {
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(
			"### Access profiles\n\n" +
				"These profiles are the public access story. The OAuth scopes remain the internal capability strings accepted by the server during v1.x.\n\n" +
				"```json\n" +
				"{\n" +
				"  \"access_profiles\": {\n" +
				"    \"reader\": {\n" +
				"      \"description\": \"Read-only access profile for discovery and content inspection (full visibility, drafts included).\",\n" +
				"      \"acquisition\": \"anonymous or self-serve registration\",\n" +
				"      \"internal_scopes\": [\"read\"]\n" +
				"    },\n" +
				"    \"operator\": {\n" +
				"      \"description\": \"Approved operator profile that bundles read, write, and site operation capabilities.\",\n" +
				"      \"acquisition\": \"approved token present in the server registry\",\n" +
				"      \"internal_scopes\": [\"read\", \"write\"]\n" +
				"    }\n" +
				"  }\n" +
				"}\n" +
				"```\n",
		)
	}
	if !hasAgentAuthMetadata {
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(fmt.Sprintf(
			"### Agent auth metadata\n\n"+
				"Machine-readable metadata for agent registration checks:\n\n"+
				"```json\n"+
				"{\n"+
				"  \"agent_auth_metadata\": {\n"+
				"    \"skill\": \"%s/auth.md\",\n"+
				"    \"register_uri\": \"%s/register\",\n"+
				"    \"identity_endpoint\": \"%s/agent/identity\",\n"+
				"    \"claim_endpoint\": \"%s/agent/identity/claim\",\n"+
				"    \"claim_uri\": \"%s/agent/identity/claim\",\n"+
				"    \"events_endpoint\": \"%s/agent/event/notify\",\n"+
				"    \"identity_types_supported\": [\"anonymous\", \"identity_assertion\"],\n"+
				"    \"anonymous\": {\n"+
				"      \"credential_types_supported\": [\"none\"],\n"+
				"      \"claim_uri\": \"%s/agent/identity/claim\"\n"+
				"    },\n"+
				"    \"identity_assertion\": {\n"+
				"      \"assertion_types_supported\": [\"urn:ietf:params:oauth:token-type:id-jag\"],\n"+
				"      \"credential_types_supported\": [\"urn:ietf:params:oauth:token-type:id-jag\"]\n"+
				"    },\n"+
				"    \"events_supported\": [\"https://schemas.workos.com/events/agent/auth/identity/assertion/revoked\"]\n"+
				"  }\n"+
				"}\n"+
				"```\n",
			issuer, issuer, issuer, issuer, issuer, issuer, issuer,
		))
	}

	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, '\n')
	data = append(data, []byte(block.String())...)
	return data
}

func serveDiscoveryJSON(w http.ResponseWriter, r *http.Request, v interface{}) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func handleLandingPage(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	if issuer == "" {
		issuer = "https://localhost"
	}
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>MCP Server</title>
<style>body{font-family:monospace;max-width:600px;margin:3em auto;line-height:1.6}a{color:#0066cc}</style>
</head>
<body>
<h1>MCP Hugo Server</h1>
<p>This is an MCP (Model Context Protocol) server for Hugo sites.</p>
<table>
<tr><td><strong>MCP endpoint</strong></td><td><a href="%s/mcp">%s/mcp</a></td></tr>
<tr><td><strong>OAuth issuer</strong></td><td>%s</td></tr>
<tr><td><strong>Authorization metadata</strong></td><td><a href="%s/.well-known/oauth-authorization-server">/.well-known/oauth-authorization-server</a></td></tr>
<tr><td><strong>Protected resource</strong></td><td><a href="%s/.well-known/oauth-protected-resource">/.well-known/oauth-protected-resource</a></td></tr>
<tr><td><strong>Server card</strong></td><td><a href="%s/.well-known/mcp/server-card.json">/.well-known/mcp/server-card.json</a></td></tr>
</table>
</body>
</html>`, issuer, issuer, issuer, issuer, issuer, issuer)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprint(w, body)
}

func serveDiscoveryText(w http.ResponseWriter, r *http.Request, contentType, body string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprint(w, body)
}
