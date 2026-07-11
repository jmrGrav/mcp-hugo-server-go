package tools_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.ToolDef{Name: "a"})
	r.Register(tools.ToolDef{Name: "b", RequiredScope: "content.read"})

	all := r.All()
	if len(all) != 2 || all[0].Name != "a" || all[1].Name != "b" {
		t.Fatalf("All() = %#v", all)
	}
}

func TestIsAdminScope(t *testing.T) {
	if !tools.IsAdminScope("site.admin") {
		t.Fatal("IsAdminScope(site.admin) = false")
	}
	if tools.IsAdminScope("content.write") {
		t.Fatal("IsAdminScope(content.write) = true")
	}
	if tools.IsAdminScope("unknown") {
		t.Fatal("IsAdminScope(unknown) = true")
	}
}
