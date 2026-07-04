package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

type discoveryIdentityAssertion struct {
	AssertionTypesSupported []string `json:"assertion_types_supported"`
}

type discoveryAgentAuth struct {
	Skill                  string                     `json:"skill"`
	IdentityEndpoint       string                     `json:"identity_endpoint"`
	ClaimEndpoint          string                     `json:"claim_endpoint"`
	EventsEndpoint         string                     `json:"events_endpoint"`
	IdentityTypesSupported []string                   `json:"identity_types_supported"`
	IdentityAssertion      discoveryIdentityAssertion `json:"identity_assertion"`
	EventsSupported        []string                   `json:"events_supported"`
}

type authServerMeta struct {
	Issuer                            string             `json:"issuer"`
	AuthorizationEndpoint             string             `json:"authorization_endpoint"`
	TokenEndpoint                     string             `json:"token_endpoint"`
	RegistrationEndpoint              string             `json:"registration_endpoint"`
	ResponseTypesSupported            []string           `json:"response_types_supported"`
	GrantTypesSupported               []string           `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string           `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string           `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string           `json:"scopes_supported"`
	ServiceDocumentation              string             `json:"service_documentation"`
	AgentAuth                         discoveryAgentAuth `json:"agent_auth"`
}

type protectedResourceMeta struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported"`
	ResourceDocumentation  string   `json:"resource_documentation"`
}

func buildAuthServerMeta(cfg config.Config) authServerMeta {
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	resource := strings.TrimSpace(cfg.OAuth.Resource)
	if resource == "" {
		resource = issuer + "/mcp"
	}
	return authServerMeta{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/authorize",
		TokenEndpoint:                     issuer + "/token",
		RegistrationEndpoint:              issuer + "/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:workos:agent-auth:grant-type:claim"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: tokenEndpointAuthMethods(cfg),
		ScopesSupported:                   tools.KnownScopes,
		ServiceDocumentation:              resource,
		AgentAuth: discoveryAgentAuth{
			Skill:                  issuer + "/auth.md",
			IdentityEndpoint:       issuer + "/agent/identity",
			ClaimEndpoint:          issuer + "/agent/identity/claim",
			EventsEndpoint:         issuer + "/agent/event/notify",
			IdentityTypesSupported: []string{"anonymous"},
			IdentityAssertion: discoveryIdentityAssertion{
				AssertionTypesSupported: []string{"urn:ietf:params:oauth:token-type:id-jag"},
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
		ResourceDocumentation:  resource,
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
			Version: Version,
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
			Required: true,
			Schemes:  []string{"bearer", "oauth2"},
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
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
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
