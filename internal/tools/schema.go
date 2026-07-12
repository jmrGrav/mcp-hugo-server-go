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
	return s
}
