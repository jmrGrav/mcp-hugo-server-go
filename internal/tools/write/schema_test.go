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

	createTool := got["create_page"]
	assertSchemaHasProperties(t, createTool, "outputSchema.data", "status", "slug", "resolved_source_path", "rate_limit_remaining")

	updateTool := got["update_page"]
	assertSchemaHasProperties(t, updateTool, "outputSchema.data", "status", "slug", "resolved_source_path", "rate_limit_remaining")

	deleteTool := got["delete_page"]
	assertSchemaHasProperties(t, deleteTool, "outputSchema.data", "status", "slug", "resolved_source_path", "rate_limit_remaining")
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

func assertSchemaHasProperties(t *testing.T, tool *mcp.Tool, field string, want ...string) {
	t.Helper()
	schema := schemaAt(t, tool, field)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q: %s.properties type = %T, want map[string]any", tool.Name, field, schema["properties"])
	}
	for _, key := range want {
		if _, ok := props[key]; !ok {
			t.Fatalf("tool %q: %s.properties missing %q", tool.Name, field, key)
		}
	}
}

func schemaAt(t *testing.T, tool *mcp.Tool, field string) map[string]any {
	t.Helper()
	parts := strings.Split(field, ".")
	var cur any = tool.OutputSchema
	if parts[0] == "inputSchema" {
		cur = tool.InputSchema
	}
	for _, part := range parts[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("tool %q: %s segment %q type = %T, want map[string]any", tool.Name, field, part, cur)
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q: %s missing properties map", tool.Name, field)
		}
		cur = props[part]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: %s type = %T, want map[string]any", tool.Name, field, cur)
	}
	return m
}
