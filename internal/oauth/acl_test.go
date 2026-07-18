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

// TestACLAllowsReadToolForAnonymous documents the #450 scope collapse:
// get_page_markdown (formerly gated behind "content.read", rank 1) is now
// RequiredScope: "" (fully public, ungated) — the same rank as anonymous —
// so it must be allowed even with no caller scope at all. This test used to
// be TestACLBlocksReadToolForAnonymous, asserting the opposite; that
// boundary no longer exists by design.
func TestACLAllowsReadToolForAnonymous(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("get_page_markdown")
	if !p.AllowRequest(body, "") {
		t.Fatal("expected get_page_markdown to be allowed for anonymous (ungated per #450)")
	}
}

func TestACLAllowsReadToolForRead(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("get_page_markdown")
	if !p.AllowRequest(body, "read") {
		t.Fatal("expected get_page_markdown to be allowed for read")
	}
}

func TestACLBlocksWriteToolForRead(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("create_page")
	if p.AllowRequest(body, "read") {
		t.Fatal("expected create_page to be blocked for read")
	}
	if reason := p.DenyReason(body, "read"); reason != "forbidden_tool" {
		t.Fatalf("expected forbidden_tool, got %q", reason)
	}
}

func TestACLAllowsWriteToolForWrite(t *testing.T) {
	p := buildTestPolicy()
	body := toolsCallBody("create_page")
	if !p.AllowRequest(body, "write") {
		t.Fatal("expected create_page to be allowed for write")
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
	// get_page_markdown is ungated (RequiredScope: "") per #450, so it can no
	// longer stand in for "forbidden for anonymous" — use a write tool
	// instead, which still requires a "write" scope anonymous callers lack.
	body := mustMarshal([]any{
		map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"method": "tools/call",
			"params": map[string]any{"name": "list_pages"},
		},
		map[string]any{
			"jsonrpc": "2.0", "id": 2,
			"method": "tools/call",
			"params": map[string]any{"name": "create_page"},
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
	if p.AllowRequest(body, "read") {
		t.Fatal("read must not call delete_page (write scope required)")
	}
}

// TestACLFormerSiteAdminToolNowRequiresWrite documents the #450 fold: tools
// that used to require a separate "site.admin" scope (rank 3 in the old
// 4-tier model) are now RequiredScope: "write" with no exceptions, and
// "write" (rank 1, now the top rank) is the only scope that can call them.
func TestACLFormerSiteAdminToolNowRequiresWrite(t *testing.T) {
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
	reg.Register(tools.ToolDef{Name: "build_site", RequiredScope: "write"})

	p2 := oauth.NewScopePolicy(reg)
	body := toolsCallBody("build_site")
	if p2.AllowRequest(body, "read") {
		t.Fatal("read must not call build_site (write scope required)")
	}
	if !p2.AllowRequest(body, "write") {
		t.Fatal("write must be able to call build_site")
	}
}
