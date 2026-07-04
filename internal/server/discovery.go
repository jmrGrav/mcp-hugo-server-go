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
		TokenEndpointAuthMethodsSupported: []string{"none"},
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
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
