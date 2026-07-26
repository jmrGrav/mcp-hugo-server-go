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
	writePlanPageFixtureHTML(t, htmlDir, filepath.Join("fr", "posts", "go-basics", "index.html"), `<!DOCTYPE html><html lang="fr"><head>
<link rel="canonical" href="https://example.test/fr/posts/go-basics/">
<meta property="og:title" content="Bases de Go">
<meta property="article:tag" content="go">
<meta property="article:tag" content="tutorial">
<meta property="article:section" content="programming">
</head><body><article>Bases de Go.</article></body></html>`)
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
	foundGoBasics := false
	for _, raw := range suggestedLinks {
		link, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if slug, _ := link["slug"].(string); slug == "/posts/go-basics/" || slug == "/fr/posts/go-basics/" {
			foundGoBasics = true
			break
		}
	}
	if !foundGoBasics {
		t.Errorf("plan_page: suggested_links = %#v, want one go-basics translation candidate", suggestedLinks)
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

func TestPlanPageLanguageFiltersSuggestions(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"tags":     []any{"go"},
		"language": "fr",
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	links, ok := decodeContent(t, res)["suggested_links"].([]any)
	if !ok || len(links) == 0 {
		t.Fatalf("plan_page suggested_links = %#v, want non-empty array", decodeContent(t, res)["suggested_links"])
	}
	first, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("plan_page first suggestion type = %T", links[0])
	}
	if got := first["slug"]; got != "/fr/posts/go-basics/" {
		t.Fatalf("plan_page language=fr first slug = %v, want /fr/posts/go-basics/", got)
	}
}

// TestPlanPageLanguageFilterSurvivesLowSuggestionLimit is a regression test:
// scoreLinkSuggestions truncates to its limit argument before plan_page ever
// applies the language filter. With suggestion_limit=1, the EN go-basics
// candidate ranks ahead of its FR sibling (same score, earlier in scan
// order) and would be the only one left once truncated — so a naive
// "score with limit, then filter by language" pipeline would return zero FR
// suggestions even though the FR sibling exists. plan_page must fetch the
// full candidate pool before filtering by language, then apply the caller's
// limit last.
func TestPlanPageLanguageFilterSurvivesLowSuggestionLimit(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"tags":             []any{"go"},
		"language":         "fr",
		"suggestion_limit": 1,
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	links, ok := decodeContent(t, res)["suggested_links"].([]any)
	if !ok || len(links) == 0 {
		t.Fatalf("plan_page suggested_links = %#v, want the FR go-basics candidate despite suggestion_limit=1", decodeContent(t, res)["suggested_links"])
	}
	first, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("plan_page first suggestion type = %T", links[0])
	}
	if got := first["slug"]; got != "/fr/posts/go-basics/" {
		t.Fatalf("plan_page language=fr, suggestion_limit=1 slug = %v, want /fr/posts/go-basics/", got)
	}
	if len(links) != 1 {
		t.Fatalf("plan_page suggestion_limit=1 returned %d links, want 1", len(links))
	}
}

func TestPlanPageOnePerSourceKeyCollapsesTranslations(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"tags":               []any{"go"},
		"one_per_source_key": true,
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	links, ok := decodeContent(t, res)["suggested_links"].([]any)
	if !ok || len(links) == 0 {
		t.Fatalf("plan_page suggested_links = %#v, want non-empty array", decodeContent(t, res)["suggested_links"])
	}
	count := 0
	for _, raw := range links {
		link, _ := raw.(map[string]any)
		if slug, _ := link["slug"].(string); slug == "/posts/go-basics/" || slug == "/fr/posts/go-basics/" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("plan_page one_per_source_key returned %d go-basics translation variants, want 1; links=%v", count, links)
	}
}

func TestPlanPageCompactOmitsHeavyContentTypeDetail(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"tags":          []any{"go"},
		"response_mode": "compact",
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	contentTypes, ok := decodeContent(t, res)["content_types"].([]any)
	if !ok || len(contentTypes) == 0 {
		t.Fatalf("plan_page content_types = %#v, want non-empty array", decodeContent(t, res)["content_types"])
	}
	first, ok := contentTypes[0].(map[string]any)
	if !ok {
		t.Fatalf("plan_page first content type = %T", contentTypes[0])
	}
	if _, present := first["expected_fields"]; present {
		t.Fatal("plan_page compact content_types[*].expected_fields present, want omitted")
	}
	if _, present := first["page_count"]; present {
		t.Fatal("plan_page compact content_types[*].page_count present, want omitted")
	}
	if _, present := first["archetype_path"]; present {
		t.Fatal("plan_page compact content_types[*].archetype_path present, want omitted")
	}
}

func TestPlanPageExplainsMissingRelevantCategories(t *testing.T) {
	idx := mustPlanPageIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "plan_page", map[string]any{
		"categories": []any{"no-match-anywhere"},
	})
	if res.IsError {
		t.Fatalf("plan_page returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if _, present := data["relevant_categories"]; present {
		t.Fatalf("plan_page relevant_categories present = %#v, want omitted when nothing matches", data["relevant_categories"])
	}
	if got := data["empty_categories_reason"]; got == nil || got == "" {
		t.Fatal("plan_page empty_categories_reason missing, want explicit explanation")
	}
}
