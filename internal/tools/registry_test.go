package tools_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

var anonymousToolNames = []string{
	"list_pages", "get_page", "search_pages", "get_recent_posts",
	"list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information",
}

var readToolNames = []string{
	"get_full_page_markdown", "get_page_frontmatter",
	"get_related_content", "build_agent_context", "export_agent_context",
	"search_content", "explain_site_structure", "get_site_health", "diff_page",
	"validate_front_matter", "validate_site",
}

func populateRegistry(r *tools.Registry) {
	for _, name := range anonymousToolNames {
		r.Register(tools.ToolDef{Name: name, RequiredScope: ""})
	}
	for _, name := range readToolNames {
		r.Register(tools.ToolDef{Name: name, RequiredScope: "content.read"})
	}
}

func toolNames(defs []tools.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

func containsName(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestAnonymousScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)

	got := r.ForScope("")
	names := toolNames(got)

	if len(got) != 9 {
		t.Fatalf("ForScope(\"\") = %d tools, want 9; names: %v", len(got), names)
	}
	for _, name := range anonymousToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(\"\") missing anonymous tool %q", name)
		}
	}
	for _, name := range readToolNames {
		if containsName(names, name) {
			t.Fatalf("ForScope(\"\") must not include content.read tool %q", name)
		}
	}
}

func TestContentReadScopeSeesReadTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)

	got := r.ForScope("content.read")
	names := toolNames(got)

	if len(got) != 20 {
		t.Fatalf("ForScope(\"content.read\") = %d tools, want 20; names: %v", len(got), names)
	}
	for _, name := range anonymousToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(\"content.read\") missing anonymous tool %q", name)
		}
	}
	for _, name := range readToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(\"content.read\") missing content.read tool %q", name)
		}
	}
}

func TestScopeInclusion(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.ToolDef{Name: "list_pages", RequiredScope: ""})
	r.Register(tools.ToolDef{Name: "create_page", RequiredScope: "content.write"})

	got := r.ForScope("site.admin")
	names := toolNames(got)

	if !containsName(names, "create_page") {
		t.Fatalf("ForScope(\"site.admin\") must include content.write tool \"create_page\"; got %v", names)
	}
	if !containsName(names, "list_pages") {
		t.Fatalf("ForScope(\"site.admin\") must include anonymous tool \"list_pages\"; got %v", names)
	}
}

func TestScopeRank(t *testing.T) {
	cases := []struct {
		scope string
		want  int
	}{
		{"", 0},
		{"content.read", 1},
		{"content.write", 2},
		{"site.admin", 3},
		{"system.admin", 4},
		{"mcp", 0},
		{"unknown", 0},
	}
	for _, tc := range cases {
		got := tools.ScopeRank(tc.scope)
		if got != tc.want {
			t.Errorf("ScopeRank(%q) = %d, want %d", tc.scope, got, tc.want)
		}
	}
}

func TestUnknownScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)

	got := r.ForScope("unknown")
	names := toolNames(got)

	if len(got) != 9 {
		t.Fatalf("ForScope(\"unknown\") = %d tools, want 9 (public only); names: %v", len(got), names)
	}
	for _, name := range anonymousToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(\"unknown\") missing anonymous tool %q", name)
		}
	}
}

func TestLegacyMCPScopeStaysUnknownInRegistry(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)

	got := r.ForScope("mcp")
	names := toolNames(got)

	if len(got) != 9 {
		t.Fatalf("ForScope(\"mcp\") = %d tools, want 9 (registry must not special-case legacy aliases); names: %v", len(got), names)
	}
	for _, name := range anonymousToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(\"mcp\") missing anonymous tool %q", name)
		}
	}
}
