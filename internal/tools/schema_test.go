package tools_test

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

type schemaFixture struct {
	Mode  string `json:"mode,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func TestMustSchemaInfersObjectType(t *testing.T) {
	raw := tools.MustSchema[schemaFixture]()
	schema, ok := raw.(*jsonschema.Schema)
	if !ok {
		t.Fatalf("MustSchema() type = %T, want *jsonschema.Schema", raw)
	}
	if schema.Type != "object" {
		t.Fatalf("MustSchema().Type = %q, want object", schema.Type)
	}
	if _, ok := schema.Properties["mode"]; !ok {
		t.Fatalf("MustSchema().Properties missing mode: %#v", schema.Properties)
	}
	if _, ok := schema.Properties["limit"]; !ok {
		t.Fatalf("MustSchema().Properties missing limit: %#v", schema.Properties)
	}
}

func TestWithEnumAndWithMaxLimitMutatePublishedSchema(t *testing.T) {
	schema := tools.MustSchema[schemaFixture]()
	schema = tools.WithEnum(schema, "mode", "standard", "compact")
	schema = tools.WithMaxLimit(schema, "limit", 50)

	got := schema.(*jsonschema.Schema)
	mode, ok := got.Properties["mode"]
	if !ok || len(mode.Enum) != 2 || mode.Enum[0] != "standard" || mode.Enum[1] != "compact" {
		t.Fatalf("mode enum = %#v, want standard/compact", mode.Enum)
	}
	limit, ok := got.Properties["limit"]
	if !ok || limit.Maximum == nil || *limit.Maximum != 50 {
		t.Fatalf("limit maximum = %#v, want 50", limit.Maximum)
	}
}

func TestWithEnumAndWithMaxLimitPanicOnUnknownField(t *testing.T) {
	t.Run("enum", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("WithEnum() should panic on unknown field")
			}
		}()
		tools.WithEnum(tools.MustSchema[schemaFixture](), "missing", "x")
	})

	t.Run("maximum", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("WithMaxLimit() should panic on unknown field")
			}
		}()
		tools.WithMaxLimit(tools.MustSchema[schemaFixture](), "missing", 10)
	})
}
