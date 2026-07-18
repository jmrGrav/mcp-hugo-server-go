package tools_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

// Per #450, the four-tier scope model collapsed to two: "" (public/read,
// fully ungated — RequiredScope: "") and "write" (gated). What used to be
// two distinct groups (anonymousToolNames requiring no scope, readToolNames
// requiring "content.read") are now both registered with RequiredScope: "",
// so at the registry level they are indistinguishable: every tool in
// publicToolNames is visible to literally any caller, including garbage or
// unrecognized scope strings, exactly like the pre-#450 anonymous set.
var publicToolNames = []string{
	"list_pages", "get_page", "search_pages", "get_recent_posts",
	"list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information",
	"get_page_markdown", "get_page_frontmatter",
	"get_related_content", "build_agent_context", "export_agent_context",
	"search_content", "explain_structure", "get_site_health", "diff_page",
	"validate_frontmatter", "validate_site",
}

const writeToolName = "create_page"

func populateRegistry(r *tools.Registry) {
	for _, name := range publicToolNames {
		r.Register(tools.ToolDef{Name: name, RequiredScope: ""})
	}
	r.Register(tools.ToolDef{Name: writeToolName, RequiredScope: "write"})
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

// assertPublicToolsVisibleNotWrite is the shared assertion for any caller
// scope that must NOT unlock the write tool: every RequiredScope: "" tool
// is unconditionally visible (regardless of the caller's scope string —
// even a garbage/unrecognized one), and the write tool is not.
func assertPublicToolsVisibleNotWrite(t *testing.T, scope string, got []tools.ToolDef) {
	t.Helper()
	names := toolNames(got)
	if len(got) != len(publicToolNames) {
		t.Fatalf("ForScope(%q) = %d tools, want %d (public only); names: %v", scope, len(got), len(publicToolNames), names)
	}
	for _, name := range publicToolNames {
		if !containsName(names, name) {
			t.Fatalf("ForScope(%q) missing public tool %q", scope, name)
		}
	}
	if containsName(names, writeToolName) {
		t.Fatalf("ForScope(%q) must not include write tool %q", scope, writeToolName)
	}
}

func TestAnonymousScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)
	assertPublicToolsVisibleNotWrite(t, "", r.ForScope(""))
}

// TestReadScopeSeesOnlyPublicTools documents the #450 contract: "read"
// carries no additional visibility beyond the unconditionally-public set —
// every formerly "content.read"-gated tool is now RequiredScope: "" and
// visible to any caller, so a "read" token sees exactly the same tools as
// an anonymous one.
func TestReadScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)
	assertPublicToolsVisibleNotWrite(t, "read", r.ForScope("read"))
}

// TestLegacyReaderScopeSeesOnlyPublicTools documents that, post-#450, the
// literal string "reader" is no longer a scope the registry itself
// recognizes (tools.KnownScopes is now just {"read", "write"}). Alias
// resolution of deprecated scope strings like "reader" happens once, at the
// oauth-layer boundary (oauth.CanonicalScope), before a scope value ever
// reaches the registry. Because every public tool is RequiredScope: "" (and
// thus visible unconditionally), an unresolved "reader" string still sees
// the full public set — it just can never unlock the write tool.
func TestLegacyReaderScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)
	assertPublicToolsVisibleNotWrite(t, "reader", r.ForScope("reader"))
}

func TestScopeInclusion(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.ToolDef{Name: "list_pages", RequiredScope: ""})
	r.Register(tools.ToolDef{Name: "create_page", RequiredScope: "write"})

	got := r.ForScope("write")
	names := toolNames(got)

	if !containsName(names, "create_page") {
		t.Fatalf("ForScope(\"write\") must include write tool \"create_page\"; got %v", names)
	}
	if !containsName(names, "list_pages") {
		t.Fatalf("ForScope(\"write\") must include public tool \"list_pages\"; got %v", names)
	}
}

func TestScopeRank(t *testing.T) {
	cases := []struct {
		scope string
		want  int
	}{
		{"", 0},
		{"read", 0},
		{"write", 1},
		{"content.read", 0},
		{"reader", 0},
		{"content.write", 0},
		{"site.admin", 0},
		{"system.admin", 0},
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
	assertPublicToolsVisibleNotWrite(t, "unknown", r.ForScope("unknown"))
}

func TestLegacyMCPScopeSeesOnlyPublicTools(t *testing.T) {
	r := tools.NewRegistry()
	populateRegistry(r)
	assertPublicToolsVisibleNotWrite(t, "mcp", r.ForScope("mcp"))
}
