package server_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolsListSecuritySchemesMatchRequiredScope exercises the OpenAI Apps
// SDK compatibility metadata added to investigate the ChatGPT connector
// issue documented in the wiki's Client-Compatibility page: every tool's
// `_meta["securitySchemes"]` must describe the same scope ScopePolicy
// actually enforces on tools/call, over the real wire path (in-memory MCP
// client/server), not just the Go struct literal.
func TestToolsListSecuritySchemesMatchRequiredScope(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.HugoRoot = t.TempDir()
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	cfg.OAuth.Enabled = false

	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := server.NewStdio(cfg, idx)
	if err != nil {
		t.Fatalf("NewStdio() error = %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect() error = %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "security-schemes-audit", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect() error = %v", err)
	}
	defer clientSession.Close()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("ListTools() returned no tools at all — test fixture problem, not a pass")
	}

	// The stdio server is admin-scoped (NewStdio grants the full privileged
	// catalog), so every tool this fixture registers has a known required
	// scope and must carry a securityScheme — nothing should be silently
	// unannotated.
	adminOnly := map[string]bool{
		"stage_hugo_upgrade": true,
		"activate_hugo":      true,
		"rollback_hugo":      true,
		"bootstrap_hugo":     true,
	}

	var checked int
	for _, tool := range res.Tools {
		meta := map[string]any(tool.Meta)
		raw, ok := meta["securitySchemes"]
		if !ok {
			t.Errorf("tool %q: missing _meta[\"securitySchemes\"]", tool.Name)
			continue
		}
		// The client decodes _meta generically (map[string]any), so
		// securitySchemes arrives as []any of map[string]any, not the
		// concrete Go struct the server built it from. Round-trip through
		// JSON into the wire-shape-equivalent local type instead of
		// asserting on a Go type the client never actually produces.
		rawJSON, err := json.Marshal(raw)
		if err != nil {
			t.Errorf("tool %q: re-marshal _meta[\"securitySchemes\"]: %v", tool.Name, err)
			continue
		}
		var schemes []toolSecurityScheme
		if err := json.Unmarshal(rawJSON, &schemes); err != nil {
			t.Errorf("tool %q: decode _meta[\"securitySchemes\"] = %s: %v", tool.Name, rawJSON, err)
			continue
		}
		if len(schemes) != 1 {
			t.Errorf("tool %q: _meta[\"securitySchemes\"] = %s, want exactly one scheme", tool.Name, rawJSON)
			continue
		}
		checked++
		scheme := schemes[0]
		switch {
		case adminOnly[tool.Name]:
			if scheme.Type != "oauth2" || len(scheme.Scopes) != 1 || scheme.Scopes[0] != "admin" {
				t.Errorf("tool %q: securityScheme = %+v, want oauth2 scopes=[admin]", tool.Name, scheme)
			}
		case scheme.Type == "noauth":
			// A read-tier (anonymous-callable) tool — no scopes expected.
			if len(scheme.Scopes) != 0 {
				t.Errorf("tool %q: noauth scheme carries scopes %v, want none", tool.Name, scheme.Scopes)
			}
		case scheme.Type == "oauth2":
			if len(scheme.Scopes) != 1 || (scheme.Scopes[0] != "write" && scheme.Scopes[0] != "admin") {
				t.Errorf("tool %q: unexpected oauth2 scopes %v", tool.Name, scheme.Scopes)
			}
		default:
			t.Errorf("tool %q: unexpected securityScheme type %q", tool.Name, scheme.Type)
		}
	}
	if checked != len(res.Tools) {
		t.Fatalf("checked %d of %d tools — some were skipped by the type assertion above", checked, len(res.Tools))
	}
}

// toolSecurityScheme mirrors the unexported type in tool_security_schemes.go
// so this external test package (server_test) can decode the _meta value
// without exporting internal server plumbing just for a test.
type toolSecurityScheme struct {
	Type   string   `json:"type"`
	Scopes []string `json:"scopes,omitempty"`
}
