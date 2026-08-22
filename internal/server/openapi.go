package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// buildOpenAPISpec describes the server's fixed HTTP surface only: the OAuth
// endpoints, the JSON-RPC /mcp entry point, and the well-known discovery
// files. It deliberately does NOT enumerate the MCP tool catalog as REST
// paths — that catalog is dynamic (32 tools today, more tomorrow) and is
// already correctly declared as such by /.well-known/mcp/server-card.json
// ("tools":["dynamic"]). A caller wanting the live tool list must call
// tools/list over /mcp; an OpenAPI document that tried to enumerate tools
// would go stale on every release.
func buildOpenAPISpec(cfg config.Config) map[string]any {
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	if issuer == "" {
		issuer = strings.TrimRight(cfg.SiteURL, "/")
	}

	jsonRPCErrorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"jsonrpc": map[string]any{"type": "string", "enum": []string{"2.0"}},
			"id":      map[string]any{},
			"error": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code":    map[string]any{"type": "integer"},
					"message": map[string]any{"type": "string"},
				},
			},
		},
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "arleo.eu MCP server — fixed HTTP surface",
			"version":     buildinfo.Version,
			"description": "OAuth 2.0 DCR/PKCE endpoints and the JSON-RPC /mcp entry point. The MCP tool catalog itself is dynamic and NOT enumerated here — call tools/list over /mcp, or see /.well-known/mcp/server-card.json. Full narrative documentation: " + issuer + "/auth.md",
		},
		"servers": []map[string]any{
			{"url": issuer, "description": "Authorization server + MCP endpoint"},
		},
		"paths": map[string]any{
			"/register": map[string]any{
				"post": map[string]any{
					"summary":     "Dynamic client registration (RFC 7591)",
					"description": "Public-client DCR: token_endpoint_auth_method \"none\", PKCE required at /authorize. Never returns a client_secret.",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"redirect_uris": map[string]any{
											"type":  "array",
											"items": map[string]any{"type": "string", "format": "uri"},
										},
									},
									"required": []string{"redirect_uris"},
								},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Client registered",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"client_id":                        map[string]any{"type": "string"},
											"client_id_issued_at":              map[string]any{"type": "integer"},
											"redirect_uris":                    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
											"grant_types":                      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
											"response_types":                   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
											"token_endpoint_auth_method":       map[string]any{"type": "string", "enum": []string{"none"}},
											"code_challenge_methods_supported": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
											"scope":                            map[string]any{"type": "string"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "invalid_request / invalid_redirect_uri"},
					},
				},
			},
			"/authorize": map[string]any{
				"get": map[string]any{
					"summary":     "Authorization Code + PKCE authorize step",
					"description": "S256 code_challenge is required.",
					"parameters": []map[string]any{
						{"name": "response_type", "in": "query", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"code"}}},
						{"name": "client_id", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "redirect_uri", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "uri"}},
						{"name": "scope", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						{"name": "state", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						{"name": "code_challenge", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "code_challenge_method", "in": "query", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"S256"}}},
					},
					"responses": map[string]any{
						"302": map[string]any{"description": "Redirect to redirect_uri with ?code=...&state=..., or ?error=... on rejection"},
						"400": map[string]any{"description": "invalid_request (e.g. redirect_uri mismatch)"},
					},
				},
			},
			"/token": map[string]any{
				"post": map[string]any{
					"summary":     "Token endpoint",
					"description": "grant_type=authorization_code (with code_verifier) or grant_type=refresh_token.",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/x-www-form-urlencoded": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"grant_type":    map[string]any{"type": "string", "enum": []string{"authorization_code", "refresh_token"}},
										"code":          map[string]any{"type": "string"},
										"redirect_uri":  map[string]any{"type": "string"},
										"client_id":     map[string]any{"type": "string"},
										"code_verifier": map[string]any{"type": "string"},
										"refresh_token": map[string]any{"type": "string"},
									},
									"required": []string{"grant_type", "client_id"},
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Token issued",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"access_token":       map[string]any{"type": "string"},
											"token_type":         map[string]any{"type": "string", "enum": []string{"Bearer"}},
											"expires_in":         map[string]any{"type": "integer"},
											"refresh_token":      map[string]any{"type": "string"},
											"refresh_expires_in": map[string]any{"type": "integer"},
											"scope":              map[string]any{"type": "string"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "invalid_grant / invalid_request"},
					},
				},
			},
			"/mcp": map[string]any{
				"post": map[string]any{
					"summary":     "MCP JSON-RPC 2.0 endpoint",
					"description": "The actual tool catalog (tools/list, tools/call, ...) is negotiated over this single JSON-RPC endpoint per the Model Context Protocol spec, not as individual REST paths. Requires \"Authorization: Bearer <access_token>\" when OAuth is enabled — including for anonymous-tier tools (see " + issuer + "/auth.md).",
					"security":    []map[string]any{{"bearerAuth": []string{}}},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{"schema": map[string]any{"type": "object", "description": "A JSON-RPC 2.0 request object."}},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "JSON-RPC 2.0 response (result or error)"},
						"401": map[string]any{"description": "missing/invalid Bearer token", "content": map[string]any{"application/json": map[string]any{"schema": jsonRPCErrorSchema}}},
						"413": map[string]any{"description": "request body exceeds the reverse-proxy body-size limit", "content": map[string]any{"application/json": map[string]any{"schema": jsonRPCErrorSchema}}},
					},
				},
				"get":    map[string]any{"summary": "MCP streaming transport (Server-Sent Events)", "responses": map[string]any{"200": map[string]any{"description": "text/event-stream"}}},
				"delete": map[string]any{"summary": "Terminate an MCP session", "responses": map[string]any{"200": map[string]any{"description": "Session terminated"}}},
			},
			"/agent/identity": map[string]any{
				"post": map[string]any{
					"summary":     "Anonymous agent identity issuance",
					"description": "Body: {\"type\":\"anonymous\"}.",
					"responses":   map[string]any{"200": map[string]any{"description": "Identity issued", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}},
				},
			},
			"/agent/identity/verify": map[string]any{
				"get": map[string]any{
					"summary":    "Operator approval form for an agent identity claim",
					"parameters": []map[string]any{{"name": "claim_token", "in": "query", "required": false, "schema": map[string]any{"type": "string"}}},
					"responses":  map[string]any{"200": map[string]any{"description": "HTML approval form", "content": map[string]any{"text/html": map[string]any{}}}},
				},
				"post": map[string]any{
					"summary":     "Approve an agent identity claim (operator, write scope)",
					"description": "Requires a write-scope Bearer token, via \"Authorization: Bearer <token>\" or the admin_token form field (browser form submissions can't set custom headers). Form fields: admin_token, claim_token.",
					"security":    []map[string]any{{"bearerAuth": []string{}}},
					"responses": map[string]any{
						"200": map[string]any{"description": "Claim verified", "content": map[string]any{"text/html": map[string]any{}}},
						"401": map[string]any{"description": "missing Bearer token"},
						"403": map[string]any{"description": "write scope required"},
					},
				},
			},
			"/agent/identity/claim": map[string]any{
				"post": map[string]any{"summary": "Claim an anonymous agent identity", "responses": map[string]any{"200": map[string]any{"description": "Claim result"}}},
			},
			"/agent/event/notify": map[string]any{
				"post": map[string]any{"summary": "Agent identity event notification (e.g. assertion revocation)", "responses": map[string]any{"200": map[string]any{"description": "Accepted"}}},
			},
			"/.well-known/oauth-authorization-server": map[string]any{
				"get": map[string]any{"summary": "RFC 8414 authorization server metadata", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
			"/.well-known/oauth-protected-resource": map[string]any{
				"get": map[string]any{"summary": "RFC 9728 protected resource metadata", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
			"/.well-known/mcp/server-card.json": map[string]any{
				"get": map[string]any{"summary": "MCP server card (tool catalog declared as dynamic)", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
			"/.well-known/agent.json": map[string]any{
				"get": map[string]any{"summary": "A2A-style agent card", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
			"/auth.md": map[string]any{
				"get": map[string]any{"summary": "Human/agent-readable authentication policy and registration walkthrough", "responses": map[string]any{"200": map[string]any{"description": "text/markdown"}}},
			},
			"/health": map[string]any{
				"get": map[string]any{"summary": "Liveness probe", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "opaque",
					"description":  "OAuth 2.0 access token obtained via /register + /authorize + /token (Authorization Code + PKCE).",
				},
			},
		},
	}
}

// handleOpenAPI mirrors serveDiscoveryJSON (method guard, CORS, cache
// headers) but sends "application/openapi+json" rather than
// "application/json" — api-catalog's linkset declares that specific media
// type for this document, and this is the one discovery endpoint whose
// content actually is an OpenAPI document rather than ad hoc JSON.
func handleOpenAPI(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/openapi+json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(buildOpenAPISpec(cfg))
}
