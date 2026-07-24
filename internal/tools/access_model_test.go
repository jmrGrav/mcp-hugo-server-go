package tools_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	adminpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	anonpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	readpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	writepkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

func TestVerifiedToolScopeMatrix(t *testing.T) {
	want := map[string]string{
		"list_pages":           "",
		"get_page":             "",
		"search_pages":         "",
		"get_recent_posts":     "",
		"list_tags":            "",
		"list_categories":      "",
		"get_sitemap":          "",
		"get_feed":             "",
		"get_site_information": "",
		"get_page_markdown":    "",
		"get_page_frontmatter": "",
		"get_related_content":  "",
		"build_agent_context":  "",
		"export_agent_context": "",
		"get_page_for_edit":    "",
		"list_content_types":   "",
		"list_page_assets":     "",
		"search_content":       "",
		"check_ai_readiness":   "",
		"explain_structure":    "",
		"get_site_health":      "",
		"get_broken_links":     "",
		"get_backlinks":        "",
		"suggest_links":        "",
		"diff_page":            "",
		"inspect_rendered":     "",
		"validate_frontmatter": "",
		"validate_site":        "",
		"create_page":          "write",
		"update_page":          "write",
		"delete_page":          "write",
		"upload_page_asset":    "write",
		"delete_page_asset":    "write",
		"get_mutation_status":  "write",
		"plan_content_change":  "",
		"apply_content_plan":   "write",
		"build_site":           "write",
		"preview_build":        "write",
		"run_post_build_hooks": "write",
		"generate_hero_image":  "write",
		"check_sri_versions":   "write",
		"get_runtime_status":   "write",
		"get_theme_status":     "write",
		"verify_publication":   "write",
		"create_preview":       "write",
	}

	got := make(map[string]string, len(want))
	for _, def := range append(append(append(anonpkg.Defs(), readpkg.Defs()...), writepkg.Defs()...), adminpkg.Defs()...) {
		if prev, exists := got[def.Name]; exists {
			t.Fatalf("duplicate tool definition for %q: %q and %q", def.Name, prev, def.RequiredScope)
		}
		got[def.Name] = def.RequiredScope
	}

	if len(got) != len(want) {
		t.Fatalf("tool matrix size = %d, want %d", len(got), len(want))
	}
	for name, wantScope := range want {
		if got[name] != wantScope {
			t.Fatalf("tool %q scope = %q, want %q", name, got[name], wantScope)
		}
	}
}

func TestCurrentAccessHierarchyStillMatchesDesignAnchor(t *testing.T) {
	if got := len(anonpkg.Defs()); got != 9 {
		t.Fatalf("anonymous tool count = %d, want 9", got)
	}
	if got := len(readpkg.Defs()); got != 19 {
		t.Fatalf("read tool count = %d, want 19", got)
	}
	if got := len(writepkg.Defs()); got != 8 {
		t.Fatalf("write tool count = %d, want 8", got)
	}
	if got := len(adminpkg.Defs()); got != 9 {
		t.Fatalf("admin (folded into write) tool count = %d, want 9", got)
	}

	if got := tools.ScopeRank(""); got != 0 {
		t.Fatalf("ScopeRank(anonymous) = %d, want 0", got)
	}
	// Per #450, "read" is capability-identical to anonymous: both rank 0.
	if got := tools.ScopeRank("read"); got != 0 {
		t.Fatalf("ScopeRank(read) = %d, want 0", got)
	}
	if got := tools.ScopeRank("write"); got != 1 {
		t.Fatalf("ScopeRank(write) = %d, want 1", got)
	}
}
