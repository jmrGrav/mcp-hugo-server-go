package write_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWriteToolSchemasPresent(t *testing.T) {
	session, _, done := newTestServer(t, t.TempDir())
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]*mcp.Tool{}
	for i := range result.Tools {
		got[result.Tools[i].Name] = result.Tools[i]
	}
	for _, name := range []string{"create_page", "update_page", "delete_page"} {
		tool, ok := got[name]
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		assertObjectSchema(t, tool, "inputSchema")
		assertObjectSchema(t, tool, "outputSchema")
	}
}

func TestWriteToolAnnotationsDescribeIdempotency(t *testing.T) {
	session, _, done := newTestServer(t, t.TempDir())
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]*mcp.Tool{}
	for i := range result.Tools {
		got[result.Tools[i].Name] = result.Tools[i]
	}

	createTool := got["create_page"]
	if createTool == nil || createTool.Annotations == nil {
		t.Fatalf("create_page annotations missing: %#v", createTool)
	}
	if createTool.Annotations.IdempotentHint {
		t.Fatalf("create_page IdempotentHint = true, want false because repeated calls rewrite date/frontmatter")
	}
	if !strings.Contains(createTool.Description, "already_exists") {
		t.Fatalf("create_page description = %q, want duplicate-create behavior explained", createTool.Description)
	}
	if !strings.Contains(createTool.Description, "idempotency_key") {
		t.Fatalf("create_page description = %q, want idempotency_key replay guidance", createTool.Description)
	}

	updateTool := got["update_page"]
	if updateTool == nil || updateTool.Annotations == nil {
		t.Fatalf("update_page annotations missing: %#v", updateTool)
	}
	if !updateTool.Annotations.IdempotentHint {
		t.Fatal("update_page IdempotentHint = false, want true for same-input convergent updates")
	}
}

func assertObjectSchema(t *testing.T, tool *mcp.Tool, field string) {
	t.Helper()
	var schema any
	switch field {
	case "inputSchema":
		schema = tool.InputSchema
	case "outputSchema":
		schema = tool.OutputSchema
	default:
		t.Fatalf("unknown schema field %q", field)
	}
	if schema == nil {
		t.Fatalf("tool %q: %s is nil", tool.Name, field)
	}
	m, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: %s type = %T, want map[string]any", tool.Name, field, schema)
	}
	if m["type"] != "object" {
		t.Fatalf("tool %q: %s.type = %v, want object", tool.Name, field, m["type"])
	}
}
