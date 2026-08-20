package server

import (
	"context"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolSecurityScheme mirrors OpenAI's Apps SDK tool-descriptor extension
// (developers.openai.com/plugins/reference#tool-descriptor-parameters):
// each tool declares the auth it needs via `securitySchemes`, either
// {"type": "noauth"} or {"type": "oauth2", "scopes": [...]}. Core MCP has
// no equivalent field, so this is additive metadata only — it does not
// change what ScopePolicy actually enforces on tools/call.
type toolSecurityScheme struct {
	Type   string   `json:"type"`
	Scopes []string `json:"scopes,omitempty"`
}

func securitySchemeForRequiredScope(requiredScope string) toolSecurityScheme {
	if requiredScope == "" {
		return toolSecurityScheme{Type: "noauth"}
	}
	return toolSecurityScheme{Type: "oauth2", Scopes: []string{requiredScope}}
}

// toolSecuritySchemesMiddleware annotates every tool returned by tools/list
// with its required-scope securityScheme under `_meta["securitySchemes"]`
// — the SDK's mcp.Tool has no top-level securitySchemes field (it isn't
// part of core MCP), so this uses the same `_meta` mirror OpenAI's own
// reference documents for clients that only read `_meta`. reg supplies
// the required scope for each tool name; a tool absent from reg (should
// not happen — reg is built from every tool package) is left unannotated
// rather than guessed at.
func toolSecuritySchemesMiddleware(reg *tools.Registry) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return result, err
			}
			listResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, err
			}
			for _, t := range listResult.Tools {
				if t == nil {
					continue
				}
				requiredScope, known := reg.RequiredScopeFor(t.Name)
				if !known {
					continue
				}
				scheme := securitySchemeForRequiredScope(requiredScope)
				if t.Meta == nil {
					t.Meta = mcp.Meta{}
				}
				t.Meta["securitySchemes"] = []toolSecurityScheme{scheme}
			}
			return listResult, err
		}
	}
}
