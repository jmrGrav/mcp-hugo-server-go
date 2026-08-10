package tools

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// MustSchema infers a JSON Schema for T and panics on programmer error during
// tool registration. Tool schemas are static metadata, so failing fast here is
// preferable to exposing an underspecified MCP surface.
func MustSchema[T any]() any {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Errorf("infer schema for %T: %w", *new(T), err))
	}
	if s.Type == "" {
		s.Type = "object"
	}
	// response_mode is deliberately handler-validated (#892), so publishing a
	// JSON Schema enum would prevent the structured invalid_params response from
	// reaching callers. Keep its vocabulary discoverable in every inferred
	// schema instead. Doing this here makes newly added response_mode inputs
	// inherit the contract rather than relying on every registration site.
	WithResponseModeDescription(s)
	return s
}

const responseModeDescription = "Accepted values: standard (default) or compact. Other values, including reserved future values, are rejected with a structured invalid_params error."

// WithResponseModeDescription publishes the handler-validated response_mode
// vocabulary without adding a JSON Schema enum. It is a no-op for schemas
// without that optional field.
func WithResponseModeDescription(s any) any {
	schema := s.(*jsonschema.Schema)
	prop, ok := schema.Properties["response_mode"]
	if !ok {
		return schema
	}
	if prop.Description == "" {
		prop.Description = responseModeDescription
	} else {
		prop.Description += " " + responseModeDescription
	}
	return schema
}

// WithEnum sets a real published `enum` constraint on schema's field property
// (#418). jsonschema-go's struct-tag inference does not parse constraint
// sub-keys out of `jsonschema:"..."` tags — the tag becomes description text,
// not a schema constraint — so this post-processes the schema MustSchema[T]()
// already generated. Panics if field isn't a known property, since that means
// the caller and the struct have drifted and the schema would silently omit
// the constraint rather than fail loudly at registration time.
//
// Once a field carries an enum, the MCP SDK's own request validation (via
// jsonschema-go) rejects out-of-enum values *before* the tool handler runs,
// returning a plain-text validation error (only Content, no
// StructuredContent/code) rather than this server's structured error
// envelope.
//
// #892: that tradeoff turned out to be the wrong default. Every enum field
// this server shipped (response_mode, search_pages.match,
// generate_hero_image.style) already had an equivalent handler-level check
// that produced a proper structured invalid_params error — the published
// enum only served to shadow it, so an out-of-enum value got the bare text
// error instead of the structured one. Those fields were migrated to
// handler/WrapTool validation and no longer call WithEnum. Prefer that
// pattern for new fields. WithEnum remains available for a genuinely
// schema-only constraint that has no handler equivalent, but be aware
// violations of it will NOT carry a structured error code.
func WithEnum(s any, field string, values ...string) any {
	schema := s.(*jsonschema.Schema)
	prop, ok := schema.Properties[field]
	if !ok {
		panic(fmt.Errorf("tools.WithEnum: field %q not found in schema properties", field))
	}
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	prop.Enum = enum
	return schema
}

// WithMaxLimit sets a published `maximum` constraint on schema's field
// property (#418), for the same reason and with the same request-validation
// tradeoff documented on WithEnum. Intended for the pagination `limit`
// params across this server's read tools, all of which enforce an integer
// clampLimit(v, defaultVal, maxVal) range at runtime, so the published
// ceiling never falls behind the runtime clamp.
//
// Deliberately does NOT publish a `minimum`. clampLimit treats any v <= 0
// (including 0 itself, and negative values) as "use the default", not as an
// error — a real, currently-accepted request shape. Publishing `minimum: 1`
// would make the SDK reject `limit: 0` before the handler ever runs,
// breaking that existing behavior for any client that serializes an
// explicit zero. Only the ceiling is a genuine constraint worth publishing.
func WithMaxLimit(s any, field string, max int) any {
	schema := s.(*jsonschema.Schema)
	prop, ok := schema.Properties[field]
	if !ok {
		panic(fmt.Errorf("tools.WithMaxLimit: field %q not found in schema properties", field))
	}
	maxF := float64(max)
	prop.Maximum = &maxF
	return schema
}
