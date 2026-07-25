package read_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func writePlanPageFixtureHTML(t *testing.T, root, rel, html string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func mustPlanPageIndex(t *testing.T) *site.Index {
	t.Helper()
	htmlDir := t.TempDir()
	writePlanPageFixtureHTML(t, htmlDir, filepath.Join("posts", "go-basics", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/go-basics/">
<meta property="og:title" content="Go Basics">
<meta property="article:tag" content="go">
<meta property="article:tag" content="tutorial">
<meta property="article:section" content="programming">
</head><body><article>Go basics body.</article></body></html>`)
	writePlanPageFixtureHTML(t, htmlDir, filepath.Join("posts", "debug-guide", "index.html"), `<!DOCTYPE html><html lang="en"><head>
<link rel="canonical" href="https://example.test/posts/debug-guide/">
<meta property="og:title" content="Debug Guide">
<meta property="article:tag" content="debug">
</head><body><article>Debug guide body.</article></body></html>`)

	cfg := config.Default()
	cfg.SiteRoot = htmlDir
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx
}

// TestPlanPageBundlesContentTypesTagsAndLinks is the core regression test
// for #622: plan_page must bundle content_types (identical to
// list_content_types), relevant_tags/relevant_categories (a substring
// match against topic/submitted tags/categories), and suggested_links
// (identical to suggest_links) into a single call.
func TestPlanPageBundlesContentTypesTagsAndLinks(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"topic": "getting started with go", "tags": []any{"go"},
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	data := decodeContent(t, res)

	if _, ok := data["content_types"].([]any); !ok {
		t.Fatalf("plan_page: content_types = %T, want []any", data["content_types"])
	}

	relevantTags, ok := data["relevant_tags"].([]any)
	if !ok {
		t.Fatalf("plan_page: relevant_tags = %T, want []any", data["relevant_tags"])
	}
	found := false
	for _, v := range relevantTags {
		if v == "go" {
			found = true
		}
	}
	if !found {
		t.Errorf("plan_page: relevant_tags = %v, want to include \"go\" (matches topic and submitted tag)", relevantTags)
	}

	suggestedLinks, ok := data["suggested_links"].([]any)
	if !ok || len(suggestedLinks) == 0 {
		t.Fatalf("plan_page: suggested_links = %#v, want at least one candidate sharing the \"go\" tag", data["suggested_links"])
	}
	first, ok := suggestedLinks[0].(map[string]any)
	if !ok || first["slug"] != "/posts/go-basics/" {
		t.Errorf("plan_page: suggested_links[0] = %#v, want the go-basics page", suggestedLinks[0])
	}
}

// TestPlanPageOmitsLinksWithoutTagsOrCategories confirms suggested_links
// (and its empty_links_reason) are omitted entirely when neither tags nor
// categories is provided — matching suggest_links' own requirement that at
// least one of slug/tags/categories be given to score anything.
func TestPlanPageOmitsLinksWithoutTagsOrCategories(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{"topic": "a topic with no tags"})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if _, present := data["suggested_links"]; present {
		t.Errorf("plan_page: suggested_links present without tags/categories: %#v", data["suggested_links"])
	}
	if _, present := data["empty_links_reason"]; present {
		t.Errorf("plan_page: empty_links_reason present without tags/categories: %#v", data["empty_links_reason"])
	}
}

// TestPlanPageRelevantTagsMatchesExistingCasingVariant confirms the
// substring heuristic surfaces an existing differently-cased spelling for
// a tag the caller is about to introduce (#622's "closest existing
// casing/spelling" use case).
func TestPlanPageRelevantTagsMatchesExistingCasingVariant(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{"tags": []any{"Debug"}})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	relevantTags, _ := data["relevant_tags"].([]any)
	found := false
	for _, v := range relevantTags {
		if v == "debug" {
			found = true
		}
	}
	if !found {
		t.Errorf("plan_page: relevant_tags = %v, want to surface existing \"debug\" for submitted \"Debug\"", relevantTags)
	}
}
