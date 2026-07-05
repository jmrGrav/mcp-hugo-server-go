package read

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 12 {
		t.Fatalf("Defs() = %d, want 12", len(defs))
	}
	if defs[0].RequiredScope != "content.read" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, nil, config.Default())
	RegisterWithSourceIndex(nil, nil, nil, config.Default())
}
