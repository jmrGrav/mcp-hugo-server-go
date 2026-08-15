package tools

import (
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCloneToolWithInputEnumPublishesIndependentSchema(t *testing.T) {
	original := &mcp.Tool{
		Name: "generate_hero_image",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"style": map[string]any{"type": "string"},
			},
		},
	}

	clone, ok := cloneToolWithInputEnum(original, "style", []string{"tech", "geo"})
	if !ok {
		t.Fatal("cloneToolWithInputEnum() rejected a valid object schema")
	}
	if clone == original {
		t.Fatal("cloneToolWithInputEnum() returned the original tool")
	}
	properties := clone.InputSchema.(map[string]any)["properties"].(map[string]any)
	got := properties["style"].(map[string]any)["enum"]
	if !reflect.DeepEqual(got, []any{"tech", "geo"}) {
		t.Fatalf("advertised enum = %#v, want [tech geo]", got)
	}
	originalProperties := original.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, mutated := originalProperties["style"].(map[string]any)["enum"]; mutated {
		t.Fatal("advertising enum mutated the call-time validation schema")
	}
}

func TestCloneToolWithInputEnumRejectsSchemasWithoutTargetProperty(t *testing.T) {
	tests := []struct {
		name   string
		schema any
	}{
		{name: "no properties", schema: map[string]any{"type": "object"}},
		{name: "property missing", schema: map[string]any{"properties": map[string]any{}}},
		{name: "property not object", schema: map[string]any{"properties": map[string]any{"style": "string"}}},
		{name: "unencodable", schema: map[string]any{"bad": make(chan int)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if clone, ok := cloneToolWithInputEnum(&mcp.Tool{InputSchema: tt.schema}, "style", []string{"tech"}); ok || clone != nil {
				t.Fatalf("cloneToolWithInputEnum() = (%#v, %v), want (nil, false)", clone, ok)
			}
		})
	}
}

func TestAdvertiseInputEnumIgnoresInvalidRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	AdvertiseInputEnum(nil, "tool", "field", []string{"value"})
	AdvertiseInputEnum(server, "", "field", []string{"value"})
	AdvertiseInputEnum(server, "tool", "", []string{"value"})
	AdvertiseInputEnum(server, "tool", "field", nil)
}

func TestIsAdminScopeUsesPrivilegeHierarchy(t *testing.T) {
	if IsAdminScope("") || IsAdminScope("read") || IsAdminScope("write") || IsAdminScope("unknown") {
		t.Fatal("non-admin scope reported Hugo administration privileges")
	}
	if !IsAdminScope("admin") {
		t.Fatal("admin scope did not report Hugo administration privileges")
	}
}
