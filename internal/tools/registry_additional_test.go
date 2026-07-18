package tools_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.ToolDef{Name: "a"})
	r.Register(tools.ToolDef{Name: "b", RequiredScope: "read"})

	all := r.All()
	if len(all) != 2 || all[0].Name != "a" || all[1].Name != "b" {
		t.Fatalf("All() = %#v", all)
	}
}

func TestIsWriteScope(t *testing.T) {
	if !tools.IsWriteScope("write") {
		t.Fatal("IsWriteScope(write) = false")
	}
	if tools.IsWriteScope("read") {
		t.Fatal("IsWriteScope(read) = true")
	}
	if tools.IsWriteScope("unknown") {
		t.Fatal("IsWriteScope(unknown) = true")
	}
}
