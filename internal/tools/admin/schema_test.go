package admin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAdminToolSchemasPresent(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]*mcp.Tool{}
	for i := range result.Tools {
		got[result.Tools[i].Name] = result.Tools[i]
	}
	for _, name := range []string{"build_site", "preview_build", "run_post_build_hooks", "generate_hero_image", "check_sri_versions", "list_previews", "revoke_preview", "revoke_all_previews", "inspect_preview"} {
		tool, ok := got[name]
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		assertObjectSchema(t, tool, "inputSchema")
		assertObjectSchema(t, tool, "outputSchema")
	}
	assertSchemaHasProperties(t, got["generate_hero_image"], "outputSchema.data", "path", "public_path", "source_key", "delete_slug", "delete_scope", "delete_filename")
	assertSchemaHasProperties(t, got["run_post_build_hooks"], "inputSchema", "dry_run")
	assertSchemaHasProperties(t, got["run_post_build_hooks"], "outputSchema.data", "results", "configured_count")
	assertSchemaHasProperties(t, got["create_preview"], "outputSchema.data", "preview_id", "url", "expires_at", "build", "effective_ttl_seconds")
	assertSchemaHasProperties(t, got["list_previews"], "outputSchema.data", "configured_count", "previews")
	assertSchemaHasProperties(t, got["revoke_preview"], "inputSchema", "preview_id")
	assertSchemaHasProperties(t, got["revoke_preview"], "outputSchema.data", "preview_id", "status")
	assertSchemaHasProperties(t, got["revoke_all_previews"], "outputSchema.data", "status", "revoked_count")
	assertSchemaHasProperties(t, got["inspect_preview"], "inputSchema", "slug", "preview_id")
	assertSchemaHasProperties(t, got["inspect_preview"], "outputSchema.data", "inspection_scope", "preview_id", "preview_build", "preview_expires_at", "slug", "url", "lang", "output_path", "state", "status", "checks")
}

func TestRunPostBuildHooksSchemaPublishesDryRunBooleanContract(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var tool *mcp.Tool
	for i := range result.Tools {
		if result.Tools[i].Name == "run_post_build_hooks" {
			tool = result.Tools[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("run_post_build_hooks missing from tools/list")
	}

	dryRun := schemaAt(t, tool, "inputSchema.dry_run")
	if got := dryRun["type"]; got != "boolean" {
		t.Fatalf("run_post_build_hooks inputSchema.dry_run.type = %v, want boolean", got)
	}
	if strings.Contains(strings.ToLower(tool.Description), "no arguments") {
		t.Fatalf("run_post_build_hooks description still implies no-arg contract: %q", tool.Description)
	}
	for _, needle := range []string{"dry_run:true", "configured_count", "without contacting them"} {
		if !strings.Contains(tool.Description, needle) {
			t.Fatalf("run_post_build_hooks description = %q, want substring %q", tool.Description, needle)
		}
	}
}

// TestGenerateHeroImageInvalidStyleReturnsStructuredError covers #892:
// generate_hero_image.style is validated in the handler (invalid_params:
// "style must be 'tech' or 'geo'"), not published as a JSON-Schema enum. A
// published enum would be enforced by the SDK's argument validation *before*
// the handler runs, so an out-of-enum value would return a bare text error
// with no StructuredContent/code, bypassing the toolcontract pipeline. This
// asserts (a) the field is not re-published as an enum, and (b) an invalid
// value produces a structured error envelope with a recognized code.
func TestGenerateHeroImageInvalidStyleReturnsStructuredError(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	result, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var tool *mcp.Tool
	for i := range result.Tools {
		if result.Tools[i].Name == "generate_hero_image" {
			tool = result.Tools[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("generate_hero_image not found in tools list")
	}
	// Regression guard: re-publishing the enum would reintroduce the
	// schema-layer bypass this test exists to prevent.
	style := schemaAt(t, tool, "inputSchema.style")
	if _, hasEnum := style["enum"]; hasEnum {
		t.Fatalf("generate_hero_image.style re-published as a JSON-Schema enum; out-of-enum "+
			"values would be rejected before the handler and lose StructuredContent (#892): %v", style["enum"])
	}

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "generate_hero_image",
		Arguments: map[string]any{
			"slug":  "posts/example",
			"title": "Example",
			"style": "watercolor",
		},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("generate_hero_image with invalid style did not return an error result")
	}
	if res.StructuredContent == nil {
		t.Fatal("generate_hero_image invalid style produced IsError but nil StructuredContent — " +
			"error bypassed the toolcontract pipeline (#892)")
	}
	out := decodeStructuredResult(t, res)
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("errors = %#v, want non-empty []any", out["errors"])
	}
	first, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("errors[0] type = %T, want map[string]any", errs[0])
	}
	if got, _ := first["code"].(string); got != "invalid_params" {
		t.Fatalf("errors[0].code = %q, want invalid_params", got)
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
