package oauth_test

import (
	"encoding/json"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

func buildTestPolicy() *oauth.ScopePolicy {
	reg := tools.NewRegistry()
	for _, d := range toolsanon.Defs() {
		reg.Register(d)
	}
	for _, d := range toolsread.Defs() {
		reg.Register(d)
	}
	for _, d := range toolswrite.Defs() {
		reg.Register(d)
	}
	return oauth.NewScopePolicy(reg)
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

func TestACLAllowsPublicToolAnonymous(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("list_pages")
	if !p.AllowRequest(body, "") {
		t.Fatal("expected list_pages to be allowed for anonymous")
	}
}

func TestACLBlocksReadToolForAnonymous(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("get_page_markdown")
	if p.AllowRequest(body, "") {
		t.Fatal("expected get_page_markdown to be blocked for anonymous")
	}
	if reason := p.DenyReason(body, ""); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLAllowsReadToolForContentRead(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("get_page_markdown")
	if !p.AllowRequest(body, "content.read") {
		t.Fatal("expected get_page_markdown to be allowed for content.read")
	}
}

func TestACLBlocksWriteToolForContentRead(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("create_page")
	if p.AllowRequest(body, "content.read") {
		t.Fatal("expected create_page to be blocked for content.read")
	}
	if reason := p.DenyReason(body, "content.read"); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLAllowsWriteToolForContentWrite(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("create_page")
	if !p.AllowRequest(body, "content.write") {
		t.Fatal("expected create_page to be allowed for content.write")
	}
}

func TestACLUnknownTool(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("nonexistent_tool")
	if p.AllowRequest(body, "") {
		t.Fatal("expected nonexistent_tool to be blocked")
	}
	if reason := p.DenyReason(body, ""); reason != "unknown_tool" {
		t.Fatalf("expected unknown_tool, got %q", reason)
	}
}

func TestACLBatchWithForbidden(t *testing.T) {
	p := buildTestPolicy()
	body := mustMarshal([]any{
		map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"method": "tools/call",
			"params": map[string]any{"name": "list_pages"},
		},
		map[string]any{
			"jsonrpc": "2.0", "id": 2,
			"method": "tools/call",
			"params": map[string]any{"name": "get_page_markdown"},
		},
	})
	if p.AllowRequest(body, "") {
		t.Fatal("expected batch with forbidden tool to be blocked for anonymous")
	}
	if reason := p.DenyReason(body, ""); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLToolsList(t *testing.T) {
	p := buildTestPolicy()
	body := mustMarshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "tools/list",
	})
	if !p.AllowRequest(body, "") {
		t.Fatal("expected tools/list to be allowed for anonymous")
	}
}

func TestACLInitialize(t *testing.T) {
	p := buildTestPolicy()
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if !p.AllowRequest(body, "") {
		t.Fatal("expected initialize to be allowed")
	}
}

func TestACLPing(t *testing.T) {
	p := buildTestPolicy()
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	if !p.AllowRequest(body, "") {
		t.Fatal("expected ping to be allowed")
	}
}

func TestACLNotification(t *testing.T) {
	p := buildTestPolicy()
	body := mustMarshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if !p.AllowRequest(body, "") {
		t.Fatal("expected notifications/* to be allowed")
	}
}

func TestACLBatchSizeCapRejected(t *testing.T) {
	p := buildTestPolicy()
	batch := make([]any, 51)
	for i := range batch {
		batch[i] = map[string]any{
			"jsonrpc": "2.0", "id": i + 1,
			"method": "tools/list",
		}
	}
	body := mustMarshal(batch)
	if p.AllowRequest(body, "") {
		t.Fatal("expected batch of 51 to be rejected")
	}
}

func TestACLBatchSizeCapAllowed(t *testing.T) {
	p := buildTestPolicy()
	batch := make([]any, 50)
	for i := range batch {
		batch[i] = map[string]any{
			"jsonrpc": "2.0", "id": i + 1,
			"method": "tools/list",
		}
	}
	body := mustMarshal(batch)
	if !p.AllowRequest(body, "") {
		t.Fatal("expected batch of 50 to be allowed")
	}
}

func TestACLMalformedBodyDenied(t *testing.T) {
	p := buildTestPolicy()
	if p.AllowRequest([]byte("not-json"), "") {
		t.Fatal("expected malformed JSON to be denied")
	}
}

func TestACLAllPublicToolsAllowedForAnonymous(t *testing.T) {
	p := buildTestPolicy()
	for _, d := range toolsanon.Defs() {
		body := toolsCallBody(d.Name)
		if !p.AllowRequest(body, "") {
			t.Errorf("expected public tool %q to be allowed for anonymous", d.Name)
		}
	}
}

func TestACLWriteScopeBlockedForRead(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("delete_page")
	if p.AllowRequest(body, "content.read") {
		t.Fatal("content.read must not call delete_page (content.write scope required)")
	}
}

func TestACLSiteAdminBlockedForWrite(t *testing.T) {
	reg := tools.NewRegistry()
	for _, d := range toolsanon.Defs() {
		reg.Register(d)
	}
	for _, d := range toolsread.Defs() {
		reg.Register(d)
	}
	for _, d := range toolswrite.Defs() {
		reg.Register(d)
	}
	reg.Register(tools.ToolDef{Name: "build_site", RequiredScope: "site.admin"})

	p2 := oauth.NewScopePolicy(reg)
	body := toolsCallBody("build_site")
	if p2.AllowRequest(body, "content.write") {
		t.Fatal("content.write must not call build_site (site.admin scope required)")
	}
	if !p2.AllowRequest(body, "site.admin") {
		t.Fatal("site.admin must be able to call build_site")
	}
}
