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
//	content.read:        14  — get_full_page_markdown, get_page_frontmatter, get_related_content,
//	                            build_agent_context, export_agent_context, search_content,
//	                            explain_site_structure, get_site_health, get_broken_links,
//	                            get_backlinks, suggest_internal_links, diff_page,
//	                            validate_front_matter, validate_site
//	content.write:        3  — create_page, update_page, delete_page
//	site.admin:           8  — build_site, preview_build, run_post_build_hooks,
//	                            generate_featured_image, check_sri_versions, get_runtime_status,
//	                            get_theme_status, verify_publication
const expectedToolCount = 34

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
