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
		"get_page_markdown":    "content.read",
		"get_page_frontmatter": "content.read",
		"get_related_content":  "content.read",
		"build_agent_context":  "content.read",
		"export_agent_context": "content.read",
		"get_page_for_edit":    "content.read",
		"list_content_types":   "content.read",
		"search_content":       "content.read",
		"explain_structure":    "content.read",
		"get_site_health":      "content.read",
		"get_broken_links":     "content.read",
		"get_backlinks":        "content.read",
		"suggest_links":        "content.read",
		"diff_page":            "content.read",
		"inspect_rendered":     "content.read",
		"validate_frontmatter": "content.read",
		"validate_site":        "content.read",
		"create_page":          "content.write",
		"update_page":          "content.write",
		"delete_page":          "content.write",
		"build_site":           "site.admin",
		"preview_build":        "site.admin",
		"run_post_build_hooks": "site.admin",
		"generate_hero_image":  "site.admin",
		"check_sri_versions":   "site.admin",
		"get_runtime_status":   "site.admin",
		"get_theme_status":     "site.admin",
		"verify_publication":   "site.admin",
		"create_preview":       "site.admin",
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
	if got := len(readpkg.Defs()); got != 17 {
		t.Fatalf("content.read tool count = %d, want 17", got)
	}
	if got := len(writepkg.Defs()); got != 3 {
		t.Fatalf("content.write tool count = %d, want 3", got)
	}
	if got := len(adminpkg.Defs()); got != 9 {
		t.Fatalf("site.admin tool count = %d, want 9", got)
	}

	if got := tools.ScopeRank(""); got != 0 {
		t.Fatalf("ScopeRank(anonymous) = %d, want 0", got)
	}
	if got := tools.ScopeRank("content.read"); got != 1 {
		t.Fatalf("ScopeRank(content.read) = %d, want 1", got)
	}
	if got := tools.ScopeRank("content.write"); got != 2 {
		t.Fatalf("ScopeRank(content.write) = %d, want 2", got)
	}
	if got := tools.ScopeRank("site.admin"); got != 3 {
		t.Fatalf("ScopeRank(site.admin) = %d, want 3", got)
	}
}
