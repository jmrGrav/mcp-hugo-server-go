package tools_test

import (
	"testing"

	adminpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	anon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	readpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	writepkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

// expectedToolCount is the total number of tools registered by all packages.
// Update this constant whenever a tool is added or removed.
// Current breakdown:
//
//	anonymous (no auth):  9  — list_pages, get_page, search_pages, get_recent_posts,
//	                            list_tags, list_categories, get_sitemap, get_feed, get_site_information
//	content.read:        19  — get_page_markdown, get_page_frontmatter, get_related_content,
//	                            build_agent_context, export_agent_context, get_page_for_edit,
//	                            list_content_types, list_page_assets, search_content, check_ai_readiness,
//	                            explain_structure, get_site_health, get_broken_links, get_backlinks,
//	                            suggest_links, diff_page, inspect_rendered, validate_frontmatter,
//	                            validate_site
//	content.write:        8  — create_page, update_page, delete_page, upload_page_asset,
//	                            delete_page_asset, get_mutation_status, plan_content_change (no scope
//	                            gate — see #450), apply_content_plan
//	site.admin:          10  — build_site, preview_build, run_post_build_hooks,
//	                            generate_hero_image, check_sri_versions, get_runtime_status,
//	                            get_theme_status, verify_publication, create_preview,
//	                            publish_changes
const expectedToolCount = 46

func TestTotalToolCount(t *testing.T) {
	total := len(anon.Defs()) + len(readpkg.Defs()) + len(writepkg.Defs()) + len(adminpkg.Defs())
	if total != expectedToolCount {
		t.Fatalf("total tool count = %d, want %d\n"+
			"  anonymous=%d  read=%d  write=%d  admin=%d\n"+
			"Update expectedToolCount in toolcount_test.go when adding or removing tools.",
			total, expectedToolCount,
			len(anon.Defs()), len(readpkg.Defs()), len(writepkg.Defs()), len(adminpkg.Defs()))
	}
}

// maxToolNameLen is a defensive ceiling on canonical tool name length
// (#329). At least one MCP client connector was observed silently
// truncating and hash-suffixing names of 21+ characters (e.g.
// "get_full_page_markdown" -> "get_ful_7c6ab376aa24"), which destroys
// legibility for tool selection. 20 is a length no observed truncation
// case fell at or under; it is an inferred-safe ceiling from that
// evidence, not independently reconfirmed against a live connector.
const maxToolNameLen = 20

func TestToolNamesWithinConnectorTruncationBudget(t *testing.T) {
	for _, def := range anon.Defs() {
		if len(def.Name) > maxToolNameLen {
			t.Errorf("anonymous tool name %q is %d chars, want <= %d", def.Name, len(def.Name), maxToolNameLen)
		}
	}
	for _, def := range readpkg.Defs() {
		if len(def.Name) > maxToolNameLen {
			t.Errorf("content.read tool name %q is %d chars, want <= %d", def.Name, len(def.Name), maxToolNameLen)
		}
	}
	for _, def := range writepkg.Defs() {
		if len(def.Name) > maxToolNameLen {
			t.Errorf("content.write tool name %q is %d chars, want <= %d", def.Name, len(def.Name), maxToolNameLen)
		}
	}
	for _, def := range adminpkg.Defs() {
		if len(def.Name) > maxToolNameLen {
			t.Errorf("site.admin tool name %q is %d chars, want <= %d", def.Name, len(def.Name), maxToolNameLen)
		}
	}
}
