package oauth_test

import (
	"encoding/json"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

var publicTools = []string{
	"list_pages", "get_page", "search_pages", "get_recent_posts",
	"list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information",
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func toolsCallBody(name string) []byte {
	return mustMarshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name},
	})
}

func TestACLAllowsPublicTool(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := toolsCallBody("list_pages")
	if !p.AllowRequest(body) {
		t.Fatal("expected list_pages to be allowed")
	}
}

func TestACLBlocksProtectedTool(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := toolsCallBody("get_full_page_markdown")
	if p.AllowRequest(body) {
		t.Fatal("expected get_full_page_markdown to be blocked")
	}
	if reason := p.DenyReason(body); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLUnknownTool(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := toolsCallBody("nonexistent_tool")
	if p.AllowRequest(body) {
		t.Fatal("expected nonexistent_tool to be blocked")
	}
	if reason := p.DenyReason(body); reason != "unknown_tool" {
		t.Fatalf("expected unknown_tool, got %q", reason)
	}
}

func TestACLBatchWithForbidden(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := mustMarshal([]any{
		map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"method": "tools/call",
			"params": map[string]any{"name": "list_pages"},
		},
		map[string]any{
			"jsonrpc": "2.0", "id": 2,
			"method": "tools/call",
			"params": map[string]any{"name": "get_full_page_markdown"},
		},
	})
	if p.AllowRequest(body) {
		t.Fatal("expected batch with forbidden tool to be blocked")
	}
	if reason := p.DenyReason(body); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLToolsList(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := mustMarshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "tools/list",
	})
	if !p.AllowRequest(body) {
		t.Fatal("expected tools/list to be allowed")
	}
}

func TestACLInitialize(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if !p.AllowRequest(body) {
		t.Fatal("expected initialize to be allowed")
	}
}

func TestACLPing(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	if !p.AllowRequest(body) {
		t.Fatal("expected ping to be allowed")
	}
}

func TestACLNotification(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if !p.AllowRequest(body) {
		t.Fatal("expected notifications/* to be allowed")
	}
}

func TestACLMalformedBodyDenied(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	if p.AllowRequest([]byte("not-json")) {
		t.Fatal("expected malformed JSON to be denied")
	}
}

func TestACLAllPublicToolsAllowed(t *testing.T) {
	p := oauth.NewACLPolicy(publicTools)
	for _, name := range publicTools {
		body := toolsCallBody(name)
		if !p.AllowRequest(body) {
			t.Errorf("expected public tool %q to be allowed", name)
		}
	}
}
