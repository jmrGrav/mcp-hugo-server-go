package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
	Description     string   `json:"description"`
	Acquisition     string   `json:"acquisition"`
	AcquisitionMode string   `json:"acquisition_mode,omitempty"`
	InternalScopes  []string `json:"internal_scopes"`
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

func readerAcquisitionProfile(cfg config.Config) (mode, description string) {
	switch {
	case !cfg.OAuth.Enabled:
		return "anonymous_mcp_access", "anonymous MCP access"
	case cfg.OAuth.DynamicClientEnabled && cfg.OAuth.AllowReaderSelfRegistration:
		return "self_serve_oauth_or_agent_identity_registration", "self-serve OAuth registration or anonymous agent identity registration"
	case cfg.OAuth.DynamicClientEnabled:
		return "self_serve_oauth_registration", "self-serve OAuth registration"
	case cfg.OAuth.AllowReaderSelfRegistration:
		return "self_serve_agent_identity_registration", "anonymous agent identity registration"
	default:
		return "operator_approved_claim_or_pre_registered_oauth_client", "operator-approved anonymous claim or pre-registered OAuth client"
	}
}

func discoveryAccessProfiles(cfg config.Config) map[string]discoveryAccessProfile {
	readerMode, readerAcquisition := readerAcquisitionProfile(cfg)
	return map[string]discoveryAccessProfile{
		"reader": {
			Description:     "Human-facing label for the canonical read scope used for discovery and content inspection, including source and drafts when the deployment allows it.",
			Acquisition:     readerAcquisition,
			AcquisitionMode: readerMode,
			InternalScopes:  []string{"read"},
		},
		"operator": {
			Description:     "Human-facing label for the canonical write scope used by approved operators for mutations and site operations.",
			Acquisition:     "approved token present in the server registry",
			AcquisitionMode: "approved_token",
			InternalScopes:  []string{"write"},
		},
		"administrator": {
			Description:     "Human-facing label for the separately approved scope required by managed Hugo binary lifecycle operations.",
			Acquisition:     "explicit administrator token present in the server registry",
			AcquisitionMode: "approved_admin_token",
			InternalScopes:  []string{"admin"},
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
		AccessProfiles:                    discoveryAccessProfiles(cfg),
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

// buildProtectedResourceMeta builds the RFC 9728 protected-resource-metadata
// document. requestHost (typically r.Host) makes the "resource" field
// reflect the origin the caller actually queried, but ONLY when requestHost
// is exactly cfg.SiteURL's hostname — the one legitimate alternate host
// this document is reverse-proxied to (www.arleo.eu; see the nginx vhost
// and deploy/config-production.yaml). requestHost is caller-controlled
// (the HTTP Host header); reflecting an arbitrary value into a document
// agents use to decide where to send OAuth tokens would let any request
// with a spoofed Host header assert an attacker-chosen "resource". Every
// other requestHost — including the issuer's own host — leaves the
// configured (or defaulted) resource untouched.
//
// A caller reaching this handler via the issuer's own host (mcp.arleo.eu)
// therefore always gets the configured/default resource unchanged; a
// caller reaching it via cfg.SiteURL's host gets that host's own origin
// instead. Without this, a validator that checks "resource" against the
// origin it queried (several do, per RFC 9728) sees a mismatch on every
// host except the issuer's — even though cfg.OAuth.Resource is deliberately
// set to "<issuer>/mcp" in production, not left empty, so a check on
// cfg.OAuth.Resource == "" alone would never fire here.
func buildProtectedResourceMeta(cfg config.Config, requestHost string) protectedResourceMeta {
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	resource := strings.TrimSpace(cfg.OAuth.Resource)
	if resource == "" {
		resource = issuer + "/mcp"
	}
	if requestHost != "" {
		reqHost := hostnameOnly(requestHost)
		siteHost := urlHost(cfg.SiteURL)
		isuHost := urlHost(issuer)
		if siteHost != "" && strings.EqualFold(reqHost, siteHost) && !strings.EqualFold(reqHost, isuHost) {
			resource = "https://" + requestHost
		}
	}
	return protectedResourceMeta{
		Resource:               resource,
		AuthorizationServers:   []string{issuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        tools.KnownScopes,
		AccessProfiles:         discoveryAccessProfiles(cfg),
		ResourceDocumentation:  issuer + "/auth.md",
	}
}

// urlHost extracts the hostname (no scheme, no port) from a URL (an
// OAuth issuer or a site URL). Returns "" on a malformed input, which never
// matches a real requestHost — buildProtectedResourceMeta then leaves the
// resource identifier untouched in that case, which is safe.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// hostnameOnly strips a trailing ":port" from an http.Request.Host-shaped
// value so it can be compared against urlHost's bare hostname.
func hostnameOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
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

// handleHealth is a minimal liveness probe: reaching this handler at all
// proves the process is up and its HTTP router is functioning. It is
// referenced by static/.well-known/api-catalog's "status" link — that
// linkset promised a health endpoint before one existed here (#openapi.json
// follow-up), which previously 404'd.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": buildinfo.Version})
}

func handleOAuthProtectedResource(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildProtectedResourceMeta(cfg, r.Host))
}

// handleOAuthProtectedResourceMCP serves the /mcp-suffixed alias
// (/.well-known/oauth-protected-resource/mcp), whose path mirrors the
// protected resource's own path per RFC 9728 §3.1. That resource is always
// "<issuer>/mcp" — it does not move when the document is reached through a
// reverse-proxied alternate host — so, unlike the base path, this alias
// never substitutes requestHost into "resource".
func handleOAuthProtectedResourceMCP(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	serveDiscoveryJSON(w, r, buildProtectedResourceMeta(cfg, ""))
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
	readerProfile := discoveryAccessProfiles(cfg)["reader"]
	if !hasRegistrationFlow {
		block.WriteString(fmt.Sprintf(
			"## Agent registration\n\n"+
				"External access profiles: `reader`, `operator`, and `administrator`.\n"+
				"These labels map to canonical OAuth scopes `read`, `write`, and `admin`.\n"+
				"`reader` maps to `read`; `operator` maps to `write`; `administrator` maps to `admin`.\n\n"+
				"Registration endpoint: `%s/register`\n"+
				"Authorization endpoint: `%s/authorize`\n"+
				"Token endpoint: `%s/token`\n"+
				"Protected resource metadata: %s/.well-known/oauth-protected-resource\n"+
				"MCP endpoint: `%s/mcp`\n"+
				"Scopes: `read`, `write`, `admin`\n\n"+
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
				"      \"write\",\n"+
				"      \"admin\"\n"+
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
		block.WriteString(fmt.Sprintf(
			"### Access profiles\n\n"+
				"These profiles are the public access story. The OAuth scopes remain the internal capability strings accepted by the server during v1.x.\n\n"+
				"```json\n"+
				"{\n"+
				"  \"access_profiles\": {\n"+
				"    \"reader\": {\n"+
				"      \"description\": %q,\n"+
				"      \"acquisition\": %q,\n"+
				"      \"acquisition_mode\": %q,\n"+
				"      \"internal_scopes\": [\"read\"]\n"+
				"    },\n"+
				"    \"operator\": {\n"+
				"      \"description\": \"Human-facing label for the canonical write scope used by approved operators for mutations and site operations.\",\n"+
				"      \"acquisition\": \"approved token present in the server registry\",\n"+
				"      \"acquisition_mode\": \"approved_token\",\n"+
				"      \"internal_scopes\": [\"write\"]\n"+
				"    }\n"+
				"    ,\"administrator\": {\n"+
				"      \"description\": \"Human-facing label for the separately approved managed Hugo administrator scope.\",\n"+
				"      \"acquisition\": \"explicit administrator token present in the server registry\",\n"+
				"      \"acquisition_mode\": \"approved_admin_token\",\n"+
				"      \"internal_scopes\": [\"admin\"]\n"+
				"    }\n"+
				"  }\n"+
				"}\n"+
				"```\n",
			readerProfile.Description, readerProfile.Acquisition, readerProfile.AcquisitionMode,
		))
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
