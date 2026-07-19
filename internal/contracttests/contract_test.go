package contracttests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type identity struct {
	Slug               string
	SourceKey          string
	Lang               string
	URL                string
	ResolvedLang       string
	ResolvedSourcePath string
	Revision           string
	TagSlugs           []string
	CategorySlugs      []string
}

func TestContractAnonymousReadToolsUseToolResponseEnvelope(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newAnonymousSession(t, idx, cfg, srcIdx)
	defer done()

	tests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "list_pages", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_page", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "search_pages", args: map[string]any{"query": "hello", "limit": 2, "offset": 0}},
		{tool: "get_recent_posts", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "list_tags", args: map[string]any{}},
		{tool: "list_categories", args: map[string]any{}},
		{tool: "get_sitemap", args: map[string]any{"limit": 2, "offset": 0, "exclude_taxonomies": true}},
		{tool: "get_feed", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_site_information", args: map[string]any{}},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			res := callTool(t, session, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s returned error: %s", tc.tool, marshalAny(t, res.Content))
			}
			assertToolResponseEnvelope(t, tc.tool, decodeContent(t, res))
		})
	}
}

func TestContractContentReadToolsUseToolResponseEnvelope(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newReadSession(t, idx, cfg, srcIdx)
	defer done()

	tests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "get_page_markdown", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "get_page_frontmatter", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "get_related_content", args: map[string]any{"slug": "/posts/hello/", "limit": 2}},
		{tool: "build_agent_context", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "export_agent_context", args: map[string]any{"limit": 1, "offset": 0}},
		{tool: "search_content", args: map[string]any{"type": "all", "limit": 2, "offset": 0}},
		{tool: "explain_structure", args: map[string]any{}},
		{tool: "get_site_health", args: map[string]any{}},
		{tool: "validate_frontmatter", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "validate_site", args: map[string]any{}},
		{tool: "get_broken_links", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_backlinks", args: map[string]any{"slug": "/posts/hello/"}},
		{tool: "suggest_links", args: map[string]any{"slug": "/posts/hello/", "limit": 2}},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			res := callTool(t, session, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s returned error: %s", tc.tool, marshalAny(t, res.Content))
			}
			assertToolResponseEnvelope(t, tc.tool, decodeContent(t, res))
		})
	}
}

// TestContractWriteToolsUseToolResponseEnvelope covers #426: a shared
// write/admin success-envelope helper (writeSuccessEnvelope,
// imageSuccessEnvelope) previously passed the response schema version into
// meta.server_version instead of the actual server build version, on every
// successful create_page/update_page/delete_page/upload_page_asset call.
func TestContractWriteToolsUseToolResponseEnvelope(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	contentRoot := t.TempDir()
	writeFile(t, filepath.Join(contentRoot, "posts", "existing", "index.md"), "---\ntitle: Existing\n---\nBody.\n")

	session, done := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer done()
	readSession, readDone := newReadSession(t, nil, fixtureConfig(), mustSourceIndexFromRoot(t, contentRoot))
	defer readDone()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/envelope-check",
		"title":      "Envelope Check",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		t.Fatalf("create_page returned error: %s", marshalAny(t, res.Content))
	}
	m := decodeContent(t, res)
	assertToolResponseEnvelope(t, "create_page", m)
	if got := asString(mapAt(t, m, "data")["slug"]); got != "/posts/envelope-check/" {
		t.Fatalf("create_page data.slug = %q, want /posts/envelope-check/ (canonical public form, #554)", got)
	}
	if got := asString(mapAt(t, m, "data")["source_key"]); got != "posts/envelope-check" {
		t.Fatalf("create_page data.source_key = %q, want posts/envelope-check", got)
	}

	revision := func() string {
		t.Helper()
		res := callTool(t, readSession, "get_page_markdown", map[string]any{"slug": "/posts/existing/"})
		if res.IsError {
			t.Fatalf("get_page_markdown returned error: %s", marshalAny(t, res.Content))
		}
		page := mapAt(t, decodeContent(t, res), "page")
		return asString(page["revision"])
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/existing",
		"title":             "Updated",
		"expected_revision": revision(),
	})
	if res.IsError {
		t.Fatalf("update_page returned error: %s", marshalAny(t, res.Content))
	}
	m = decodeContent(t, res)
	assertToolResponseEnvelope(t, "update_page", m)
	if got := asString(mapAt(t, m, "data")["slug"]); got != "/posts/existing/" {
		t.Fatalf("update_page data.slug = %q, want /posts/existing/ (canonical public form, #554)", got)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/existing",
		"expected_revision": revision(),
	})
	if res.IsError {
		t.Fatalf("delete_page returned error: %s", marshalAny(t, res.Content))
	}
	m = decodeContent(t, res)
	assertToolResponseEnvelope(t, "delete_page", m)
	if got := asString(mapAt(t, m, "data")["slug"]); got != "/posts/existing/" {
		t.Fatalf("delete_page data.slug = %q, want /posts/existing/ (canonical public form, #554)", got)
	}
}

func TestContractDiffPageErrorUsesStructuredEnvelope(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newReadSession(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "diff_page", map[string]any{"slug": "/posts/hello/"})
	if !res.IsError {
		t.Fatal("diff_page without a usable git root should return an error result in the fixture environment")
	}
	m := decodeErrorContent(t, res)
	assertToolErrorEnvelope(t, "diff_page", m, "git_metadata_unavailable")
}

func TestContractPageIdentityConsistentAcrossReadTools(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	const slug = "/posts/hello/"
	wantSourcePath := "content/posts/hello.md"
	want := identity{
		Slug:          "/posts/hello/",
		SourceKey:     "posts/hello",
		Lang:          "en",
		URL:           "https://example.test/posts/hello/",
		ResolvedLang:  "",
		TagSlugs:      []string{"go", "hugo"},
		CategorySlugs: []string{"tutorials"},
	}

	got := map[string]identity{
		"get_page":             identityFromGetPage(t, callTool(t, anonSession, "get_page", map[string]any{"slug": slug})),
		"get_page_frontmatter": identityFromFrontmatter(t, callTool(t, readSession, "get_page_frontmatter", map[string]any{"slug": slug})),
		"get_page_markdown":    identityFromMarkdownPage(t, callTool(t, readSession, "get_page_markdown", map[string]any{"slug": slug})),
		"build_agent_context":  identityFromAgentContext(t, callTool(t, readSession, "build_agent_context", map[string]any{"slug": slug})),
	}

	ref := got["get_page"]
	for tool, actual := range got {
		if actual.Slug != want.Slug {
			t.Fatalf("%s slug = %q, want %q", tool, actual.Slug, want.Slug)
		}
		if actual.Lang != want.Lang {
			t.Fatalf("%s lang = %q, want %q", tool, actual.Lang, want.Lang)
		}
		if actual.URL != want.URL {
			t.Fatalf("%s url = %q, want %q", tool, actual.URL, want.URL)
		}
		if actual.ResolvedLang != want.ResolvedLang {
			t.Fatalf("%s resolved_lang = %q, want %q", tool, actual.ResolvedLang, want.ResolvedLang)
		}
		if actual.ResolvedSourcePath != wantSourcePath {
			t.Fatalf("%s resolved_source_path = %q, want %q", tool, actual.ResolvedSourcePath, wantSourcePath)
		}
		if actual.SourceKey != want.SourceKey {
			t.Fatalf("%s source_key = %q, want %q", tool, actual.SourceKey, want.SourceKey)
		}
		if actual.Revision == "" {
			t.Fatalf("%s revision = empty, want stable source revision", tool)
		}
		if !slices.Equal(actual.TagSlugs, want.TagSlugs) {
			t.Fatalf("%s tag slugs = %v, want %v", tool, actual.TagSlugs, want.TagSlugs)
		}
		if !slices.Equal(actual.CategorySlugs, want.CategorySlugs) {
			t.Fatalf("%s category slugs = %v, want %v", tool, actual.CategorySlugs, want.CategorySlugs)
		}
		if tool != "get_page" && actual.ResolvedSourcePath != ref.ResolvedSourcePath {
			t.Fatalf("%s resolved_source_path = %q, want same path as get_page %q", tool, actual.ResolvedSourcePath, ref.ResolvedSourcePath)
		}
		if tool != "get_page" && actual.Revision != ref.Revision {
			t.Fatalf("%s revision = %q, want same revision as get_page %q", tool, actual.Revision, ref.Revision)
		}
	}
}

func TestContractRichReadToolsExposeLifecycleState(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	const slug = "/posts/hello/"
	tests := []struct {
		name string
		tool string
		args map[string]any
		read func(*testing.T, map[string]any) map[string]any
	}{
		{
			name: "get_page_markdown",
			tool: "get_page_markdown",
			args: map[string]any{"slug": slug},
			read: func(t *testing.T, m map[string]any) map[string]any {
				t.Helper()
				return mapAt(t, m, "page")
			},
		},
		{
			name: "get_page_frontmatter",
			tool: "get_page_frontmatter",
			args: map[string]any{"slug": slug},
			read: func(t *testing.T, m map[string]any) map[string]any {
				t.Helper()
				return mapAt(t, m, "frontmatter")
			},
		},
		{
			name: "build_agent_context",
			tool: "build_agent_context",
			args: map[string]any{"slug": slug},
			read: func(t *testing.T, m map[string]any) map[string]any {
				t.Helper()
				return mapAt(t, m, "context")
			},
		},
		{
			name: "search_content",
			tool: "search_content",
			args: map[string]any{"query": "hello", "limit": 1, "offset": 0},
			read: func(t *testing.T, m map[string]any) map[string]any {
				t.Helper()
				data := mapAt(t, m, "data")
				pages, ok := data["pages"].([]any)
				if !ok || len(pages) == 0 {
					t.Fatalf("search_content pages = %#v, want one page", data["pages"])
				}
				page, ok := pages[0].(map[string]any)
				if !ok {
					t.Fatalf("search_content page type = %T", pages[0])
				}
				return page
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, readSession, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s returned error: %s", tc.tool, marshalAny(t, res.Content))
			}
			state := mapAt(t, tc.read(t, decodeContent(t, res)), "state")
			assertLifecycleState(t, state, "present", "built", "available", "fresh")
		})
	}
}

func TestContractMultilingualResolutionConsistentAcrossReadAndWriteTools(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")

	frSourcePath := filepath.Join(contentRoot, "posts", "bonjour", "index.fr.md")
	frSource := "content/posts/bonjour/index.fr.md"
	writeFile(t, frSourcePath, "---\ntitle: Bonjour\ndate: 2026-07-13\ncategories:\n  - Infra\n---\nBonjour monde.\n")
	writeFile(t, filepath.Join(publicRoot, "posts", "bonjour", "index.fr.html"), "<html><body><article><h1>Bonjour</h1><p>Bonjour monde.</p></article></body></html>")

	cfg := fixtureConfig()
	cfg.SiteRoot = publicRoot
	cfg.ContentRoot = contentRoot
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}

	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()
	writeSession, writeDone := newWriteSession(t, contentRoot, cfg, idx)
	defer writeDone()

	const slug = "/posts/bonjour/"

	readIDs := map[string]identity{
		"get_page_frontmatter": identityFromFrontmatter(t, callTool(t, readSession, "get_page_frontmatter", map[string]any{"slug": slug})),
		"get_page_markdown":    identityFromMarkdownPage(t, callTool(t, readSession, "get_page_markdown", map[string]any{"slug": slug})),
		"build_agent_context":  identityFromAgentContext(t, callTool(t, readSession, "build_agent_context", map[string]any{"slug": slug})),
		"diff_page":            identityFromDiffPage(t, callTool(t, readSession, "diff_page", map[string]any{"slug": slug})),
	}

	for tool, actual := range readIDs {
		if actual.ResolvedLang != "fr" {
			t.Fatalf("%s resolved_lang = %q, want fr", tool, actual.ResolvedLang)
		}
		if actual.ResolvedSourcePath != frSource {
			t.Fatalf("%s resolved_source_path = %q, want %q", tool, actual.ResolvedSourcePath, frSource)
		}
		if actual.SourceKey != "posts/bonjour" {
			t.Fatalf("%s source_key = %q, want posts/bonjour", tool, actual.SourceKey)
		}
	}

	res := callTool(t, writeSession, "update_page", map[string]any{
		"slug":    "posts/bonjour",
		"title":   "Bonjour mis a jour",
		"dry_run": true,
	})
	if res.IsError {
		t.Fatalf("update_page dry_run returned error: %s", marshalAny(t, res.Content))
	}
	m := decodeContent(t, res)
	data := mapAt(t, m, "data")
	if got := asString(data["resolved_lang"]); got != "fr" {
		t.Fatalf("update_page data.resolved_lang = %q, want fr", got)
	}
	if got := asString(data["resolved_source_path"]); got != frSource {
		t.Fatalf("update_page data.resolved_source_path = %q, want %q", got, frSource)
	}
}

func TestContractDryRunMutationsDoNotWriteToDisk(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	contentRoot := t.TempDir()
	originalPath := filepath.Join(contentRoot, "posts", "existing", "index.md")
	original := "---\ntitle: Existing\n---\nOriginal body.\n"
	writeFile(t, originalPath, original)

	writeSession, writeDone := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer writeDone()
	readSession, readDone := newReadSession(t, nil, fixtureConfig(), mustSourceIndexFromRoot(t, contentRoot))
	defer readDone()

	callMustSucceed(t, writeSession, "create_page", map[string]any{
		"slug":       "posts/dry-created",
		"title":      "Dry Created",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
		"dry_run":    true,
	})
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "dry-created", "index.md")); !os.IsNotExist(err) {
		t.Fatal("create_page dry_run wrote a file to disk")
	}

	callMustSucceed(t, writeSession, "update_page", map[string]any{
		"slug":    "posts/existing",
		"title":   "Changed",
		"dry_run": true,
	})
	afterUpdate, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("ReadFile(existing after update dry_run) error = %v", err)
	}
	if string(afterUpdate) != original {
		t.Fatalf("update_page dry_run modified file contents:\nwant:\n%s\ngot:\n%s", original, string(afterUpdate))
	}

	callMustSucceed(t, writeSession, "delete_page", map[string]any{
		"slug":    "posts/existing",
		"dry_run": true,
	})
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("delete_page dry_run removed or altered file: %v", err)
	}

	res := callTool(t, readSession, "get_page_markdown", map[string]any{"slug": "/posts/existing/"})
	if res.IsError {
		t.Fatalf("get_page_markdown after dry_run returned error: %s", marshalAny(t, res.Content))
	}
	page := mapAt(t, decodeContent(t, res), "page")
	if got := asString(page["markdown"]); got != "Original body." {
		t.Fatalf("markdown after dry_run = %q, want original body", got)
	}
}

func TestContractPaginationWalksToEndWithoutGaps(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	tests := []struct {
		name    string
		group   string
		session *mcp.ClientSession
		tool    string
		args    map[string]any
		extract func(*testing.T, map[string]any) ([]string, int, int, bool, *int)
	}{
		{
			name:    "list_pages",
			group:   "all-content",
			session: anonSession,
			tool:    "list_pages",
			args:    map[string]any{"limit": 1, "offset": 0},
			extract: extractTopLevelPages,
		},
		{
			name:    "get_recent_posts",
			group:   "recent-posts",
			session: anonSession,
			tool:    "get_recent_posts",
			args:    map[string]any{"limit": 1, "offset": 0},
			extract: extractTopLevelPages,
		},
		{
			name:    "get_feed",
			group:   "recent-posts",
			session: anonSession,
			tool:    "get_feed",
			args:    map[string]any{"limit": 1, "offset": 0},
			extract: extractTopLevelItems,
		},
		{
			name:    "get_sitemap",
			group:   "all-content",
			session: anonSession,
			tool:    "get_sitemap",
			args:    map[string]any{"limit": 1, "offset": 0, "exclude_taxonomies": true},
			extract: extractTopLevelEntries,
		},
		{
			name:    "search_content",
			group:   "all-content",
			session: readSession,
			tool:    "search_content",
			args:    map[string]any{"type": "all", "limit": 1, "offset": 0},
			extract: extractSearchContentPages,
		},
	}

	setsByName := make(map[string][]string, len(tests))
	groupRef := map[string]string{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			offset := 0
			seen := map[string]struct{}{}
			var expectedTotal int
			var pages int
			for {
				args := cloneMap(tc.args)
				args["offset"] = offset
				res := callTool(t, tc.session, tc.tool, args)
				if res.IsError {
					t.Fatalf("%s returned error: %s", tc.tool, marshalAny(t, res.Content))
				}
				m := decodeContent(t, res)
				slugs, total, returnedCount, hasMore, nextOffset := tc.extract(t, m)
				if pages == 0 {
					expectedTotal = total
				} else if total != expectedTotal {
					t.Fatalf("%s total changed across pages: got %d, want %d", tc.tool, total, expectedTotal)
				}
				if returnedCount != len(slugs) {
					t.Fatalf("%s returned_count = %d, want len(slugs)=%d", tc.tool, returnedCount, len(slugs))
				}
				for _, slug := range slugs {
					if _, exists := seen[slug]; exists {
						t.Fatalf("%s repeated slug across pagination: %s", tc.tool, slug)
					}
					seen[slug] = struct{}{}
				}
				pages++
				if !hasMore {
					if nextOffset != nil {
						t.Fatalf("%s next_offset = %v when has_more=false", tc.tool, *nextOffset)
					}
					break
				}
				if nextOffset == nil {
					t.Fatalf("%s has_more=true but next_offset is nil", tc.tool)
				}
				if *nextOffset <= offset {
					t.Fatalf("%s next_offset = %d, want > current offset %d", tc.tool, *nextOffset, offset)
				}
				offset = *nextOffset
				if pages > expectedTotal+2 {
					t.Fatalf("%s pagination loop did not terminate", tc.tool)
				}
			}
			if len(seen) != expectedTotal {
				t.Fatalf("%s walked %d unique items, want total=%d", tc.tool, len(seen), expectedTotal)
			}
			slugs := make([]string, 0, len(seen))
			for slug := range seen {
				slugs = append(slugs, slug)
			}
			slices.Sort(slugs)
			setsByName[tc.name] = slugs
			if refName, ok := groupRef[tc.group]; ok {
				if !slices.Equal(slugs, setsByName[refName]) {
					t.Fatalf("%s slug set = %v, want same set as %s = %v", tc.tool, slugs, refName, setsByName[refName])
				}
			} else {
				groupRef[tc.group] = tc.name
			}
		})
	}
}

func TestContractGetPageMatchesGolden(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newAnonymousSession(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "get_page", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("get_page returned error: %s", marshalAny(t, res.Content))
	}
	assertGoldenJSON(t, "get_page_hello", decodeContent(t, res))
}

func TestContractListPagesMatchesGolden(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newAnonymousSession(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "list_pages", map[string]any{"limit": 2, "offset": 0})
	if res.IsError {
		t.Fatalf("list_pages returned error: %s", marshalAny(t, res.Content))
	}
	assertGoldenJSON(t, "list_pages_page1", decodeContent(t, res))
}

func TestContractGetRelatedContentMatchesGolden(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	session, done := newReadSession(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/", "limit": 2})
	if res.IsError {
		t.Fatalf("get_related_content returned error: %s", marshalAny(t, res.Content))
	}
	assertGoldenJSON(t, "get_related_content_hello", decodeContent(t, res))
}

func fixtureConfig() config.Config {
	cfg := config.Default()
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	return cfg
}

func mustFixtureIndex(t *testing.T) *site.Index {
	t.Helper()
	idx, err := site.NewIndex(fixtureConfig())
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	return idx
}

func mustFixtureSourceIndex(t *testing.T) *hugosite.SourceIndex {
	t.Helper()
	return mustSourceIndexFromRoot(t, filepath.Join("..", "..", "testdata", "fixtures", "content"))
}

func mustSourceIndexFromRoot(t *testing.T, root string) *hugosite.SourceIndex {
	t.Helper()
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex(%q) error = %v", root, err)
	}
	return idx
}

func newAnonymousSession(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolsanon.Register(s, idx, cfg, srcIdx)
	return connectClient(t, s)
}

func newReadSession(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolsread.Register(s, idx, cfg, srcIdx)
	toolsread.RegisterWithSourceIndex(s, idx, srcIdx, cfg)
	return connectClient(t, s)
}

func newWriteSession(t *testing.T, contentRoot string, cfg config.Config, siteIdx *site.Index) (*mcp.ClientSession, func()) {
	t.Helper()
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}
	srcIdx := mustSourceIndexFromRoot(t, contentRoot)
	cfg.ContentRoot = contentRoot
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolswrite.Register(s, pg, srcIdx, cfg, nil, siteIdx)
	return connectClient(t, s)
}

func connectClient(t *testing.T, s *mcp.Server) (*mcp.ClientSession, func()) {
	t.Helper()
	ctx := context.Background()
	buildinfo.Version = "test-server-version"
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func callMustSucceed(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := callTool(t, session, name, args)
	if res.IsError {
		t.Fatalf("%s returned error: %s", name, marshalAny(t, res.Content))
	}
	return decodeContent(t, res)
}

func decodeContent(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return m
}

func identityFromGetPage(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	data := mapAt(t, decodeContent(t, res), "data")
	page := mapAt(t, data, "page")
	return identity{
		Slug:               asString(page["slug"]),
		SourceKey:          asString(page["source_key"]),
		Lang:               asString(page["lang"]),
		URL:                asString(page["url"]),
		ResolvedLang:       asString(page["resolved_lang"]),
		ResolvedSourcePath: asString(page["resolved_source_path"]),
		Revision:           asString(page["revision"]),
		TagSlugs:           termSlugs(page["tag_terms"]),
		CategorySlugs:      termSlugs(page["category_terms"]),
	}
}

func identityFromFrontmatter(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	fm := mapAt(t, decodeContent(t, res), "frontmatter")
	return identity{
		Slug:               asString(fm["slug"]),
		SourceKey:          asString(fm["source_key"]),
		Lang:               asString(fm["lang"]),
		URL:                asString(fm["url"]),
		ResolvedLang:       asString(fm["resolved_lang"]),
		ResolvedSourcePath: asString(fm["resolved_source_path"]),
		Revision:           asString(fm["revision"]),
		TagSlugs:           termSlugs(fm["tag_terms"]),
		CategorySlugs:      termSlugs(fm["category_terms"]),
	}
}

func identityFromMarkdownPage(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	page := mapAt(t, decodeContent(t, res), "page")
	return identity{
		Slug:               asString(page["slug"]),
		SourceKey:          asString(page["source_key"]),
		Lang:               asString(page["lang"]),
		URL:                asString(page["url"]),
		ResolvedLang:       asString(page["resolved_lang"]),
		ResolvedSourcePath: asString(page["resolved_source_path"]),
		Revision:           asString(page["revision"]),
		TagSlugs:           termSlugs(page["tag_terms"]),
		CategorySlugs:      termSlugs(page["category_terms"]),
	}
}

func identityFromAgentContext(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	ctx := mapAt(t, decodeContent(t, res), "context")
	fm := mapAt(t, ctx, "frontmatter")
	return identity{
		Slug:               asString(fm["slug"]),
		SourceKey:          asString(fm["source_key"]),
		Lang:               asString(fm["lang"]),
		URL:                asString(fm["url"]),
		ResolvedLang:       asString(fm["resolved_lang"]),
		ResolvedSourcePath: asString(fm["resolved_source_path"]),
		Revision:           asString(fm["revision"]),
		TagSlugs:           termSlugs(fm["tag_terms"]),
		CategorySlugs:      termSlugs(fm["category_terms"]),
	}
}

func identityFromDiffPage(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	data := mapAt(t, decodeContent(t, res), "data")
	return identity{
		SourceKey:          asString(data["source_key"]),
		ResolvedLang:       asString(data["resolved_lang"]),
		ResolvedSourcePath: asString(data["resolved_source_path"]),
		Revision:           asString(data["revision"]),
	}
}

func extractTopLevelPages(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	data := mapAt(t, m, "data")
	return extractCollection(t, data, "pages")
}

func assertLifecycleState(t *testing.T, state map[string]any, source, build, public, index string) {
	t.Helper()
	if got := asString(state["source_state"]); got != source {
		t.Fatalf("source_state = %q, want %q", got, source)
	}
	if got := asString(state["build_state"]); got != build {
		t.Fatalf("build_state = %q, want %q", got, build)
	}
	if got := asString(state["public_state"]); got != public {
		t.Fatalf("public_state = %q, want %q", got, public)
	}
	if got := asString(state["index_state"]); got != index {
		t.Fatalf("index_state = %q, want %q", got, index)
	}
}

func extractTopLevelItems(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	data := mapAt(t, m, "data")
	return extractCollection(t, data, "items")
}

func extractTopLevelEntries(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	data := mapAt(t, m, "data")
	return extractCollection(t, data, "entries")
}

func extractSearchContentPages(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	data := mapAt(t, m, "data")
	return extractCollection(t, data, "pages")
}

func extractCollection(t *testing.T, m map[string]any, key string) ([]string, int, int, bool, *int) {
	t.Helper()
	rawItems, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s type = %T, want []any", key, m[key])
	}
	slugs := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		pm, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s item type = %T, want map[string]any", key, item)
		}
		slugs = append(slugs, asString(pm["slug"]))
	}
	total := asInt(t, m["total"])
	returnedCount := asInt(t, m["returned_count"])
	hasMore, _ := m["has_more"].(bool)
	var nextOffset *int
	if raw, ok := m["next_offset"]; ok && raw != nil {
		v := asInt(t, raw)
		nextOffset = &v
	}
	return slugs, total, returnedCount, hasMore, nextOffset
}

func termSlugs(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		slug, _ := m["slug"].(string)
		if slug != "" {
			out = append(out, slug)
		}
	}
	return out
}

func mapAt(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		if data, okData := m["data"].(map[string]any); okData {
			raw, ok = data[key]
		}
	}
	if !ok {
		t.Fatalf("missing key %q", key)
	}
	got, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s type = %T, want map[string]any", key, raw)
	}
	return got
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(t *testing.T, v any) int {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	default:
		t.Fatalf("value type = %T, want numeric", v)
		return 0
	}
}

func marshalAny(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error content: %v", err)
	}
	return string(raw)
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func assertToolResponseEnvelope(t *testing.T, tool string, m map[string]any) {
	t.Helper()
	if got, ok := m["success"].(bool); !ok || !got {
		t.Fatalf("%s success = %v, want true", tool, m["success"])
	}
	if _, ok := m["data"].(map[string]any); !ok {
		t.Fatalf("%s data type = %T, want map[string]any", tool, m["data"])
	}
	if _, ok := m["errors"].([]any); !ok {
		t.Fatalf("%s errors type = %T, want []any", tool, m["errors"])
	}
	assertToolResponseEnvelopeMeta(t, tool, m)
}

func decodeErrorContent(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured error content: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal structured error content: %v\nraw: %s", err, raw)
		}
		return m
	}
	if len(res.Content) == 0 {
		t.Fatal("error result content is empty")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("error result content[0] type = %T, want *mcp.TextContent", res.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text.Text), &m); err != nil {
		t.Fatalf("unmarshal error content: %v\nraw: %s", err, text.Text)
	}
	return m
}

func assertToolErrorEnvelope(t *testing.T, tool string, m map[string]any, wantCode string) {
	t.Helper()
	if got, ok := m["success"].(bool); !ok || got {
		t.Fatalf("%s success = %v, want false", tool, m["success"])
	}
	if _, ok := m["data"].(map[string]any); !ok {
		t.Fatalf("%s data type = %T, want map[string]any", tool, m["data"])
	}
	errors, ok := m["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Fatalf("%s errors = %#v, want non-empty []any", tool, m["errors"])
	}
	first, ok := errors[0].(map[string]any)
	if !ok {
		t.Fatalf("%s errors[0] type = %T, want map[string]any", tool, errors[0])
	}
	if got := asString(first["code"]); got != wantCode {
		t.Fatalf("%s errors[0].code = %q, want %q", tool, got, wantCode)
	}
	assertToolResponseEnvelopeMeta(t, tool, m)
}

func assertToolResponseEnvelopeMeta(t *testing.T, tool string, m map[string]any) {
	t.Helper()
	if _, ok := m["warnings"].([]any); !ok {
		t.Fatalf("%s warnings type = %T, want []any", tool, m["warnings"])
	}
	meta, ok := m["meta"].(map[string]any)
	if !ok {
		t.Fatalf("%s meta type = %T, want map[string]any", tool, m["meta"])
	}
	metaVersion, ok := meta["server_version"].(string)
	if !ok || metaVersion == "" {
		t.Fatalf("%s meta.server_version = %v, want non-empty string", tool, meta["server_version"])
	}
	// #426: meta.server_version must carry the build version
	// (buildinfo.Version, "test-server-version" in this harness — see
	// connectClient), never the response schema version. A prior bug had a
	// shared write/admin success-envelope helper pass the schema version
	// into this field by mistake.
	if metaVersion == toolcontract.ToolResultVersion {
		t.Fatalf("%s meta.server_version = %q, want the build version, not the schema version (regression of #426)", tool, metaVersion)
	}
	metaGeneratedAt, ok := meta["generated_at"].(string)
	if !ok || metaGeneratedAt == "" {
		t.Fatalf("%s meta.generated_at = %v, want non-empty string", tool, meta["generated_at"])
	}
	// #454: the ambiguous root-level `version` field (schema version, easily
	// mistaken for the server version) was removed; the schema-version
	// signal now lives unambiguously at meta.schema_version instead.
	if got := asString(meta["schema_version"]); got != toolcontract.ToolResultVersion {
		t.Fatalf("%s meta.schema_version = %q, want schema version %q", tool, got, toolcontract.ToolResultVersion)
	}
	if got := asString(meta["commit"]); got == "" {
		t.Fatalf("%s meta.commit = %q, want non-empty string", tool, got)
	}
	if got := asString(meta["build_channel"]); got == "" {
		t.Fatalf("%s meta.build_channel = %q, want non-empty string", tool, got)
	}
	if _, ok := m["version"]; ok {
		t.Fatalf("%s root-level version should be removed (#454), got %v", tool, m["version"])
	}
	if got := asString(m["generated_at"]); got != metaGeneratedAt {
		t.Fatalf("%s generated_at = %q, want meta.generated_at %q", tool, got, metaGeneratedAt)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func assertGoldenJSON(t *testing.T, name string, got map[string]any) {
	t.Helper()
	normalized := normalizeGoldenMap(cloneMap(got))
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(%s) error = %v", name, err)
	}
	goldenPath := filepath.Join("testdata", "golden", name+".golden.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", goldenPath, err)
	}
	if diff := strings.TrimSpace(string(raw)); diff != strings.TrimSpace(string(want)) {
		t.Fatalf("%s golden mismatch\n--- got ---\n%s\n--- want ---\n%s", name, raw, want)
	}
}

func normalizeGoldenMap(m map[string]any) map[string]any {
	delete(m, "generated_at")
	if meta, ok := m["meta"].(map[string]any); ok {
		delete(meta, "generated_at")
	}
	normalizeSourcePaths(m)
	return m
}

func normalizeSourcePaths(v any) {
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			if key == "resolved_source_path" {
				if path, ok := child.(string); ok {
					path = filepath.ToSlash(path)
					if idx := strings.Index(path, "testdata/fixtures/"); idx >= 0 {
						path = path[idx:]
					}
					x[key] = path
				}
				continue
			}
			normalizeSourcePaths(child)
		}
	case []any:
		for _, item := range x {
			normalizeSourcePaths(item)
		}
	}
}

func setContractBuildInfo(t *testing.T) func() {
	t.Helper()
	origVersion := buildinfo.Version
	origRelease := buildinfo.ReleaseVersion
	origCommit := buildinfo.Commit
	origChannel := buildinfo.BuildChannel

	buildinfo.Version = "main-testbuild"
	buildinfo.ReleaseVersion = "vtest"
	buildinfo.Commit = "0123456789ab"
	buildinfo.BuildChannel = "main"

	return func() {
		buildinfo.Version = origVersion
		buildinfo.ReleaseVersion = origRelease
		buildinfo.Commit = origCommit
		buildinfo.BuildChannel = origChannel
	}
}

var contractGoldenNames = []string{
	"get_page_hello",
	"list_pages_page1",
	"get_related_content_hello",
}

func TestContractGoldenFixturesExist(t *testing.T) {
	t.Helper()
	for _, name := range contractGoldenNames {
		path := filepath.Join("testdata", "golden", name+".golden.json")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing golden fixture %s: %v", path, err)
		}
		if info.IsDir() {
			t.Fatalf("golden fixture %s is a directory", path)
		}
	}
}

func TestContractGoldenFilesAreValidJSON(t *testing.T) {
	t.Helper()
	for _, name := range contractGoldenNames {
		path := filepath.Join("testdata", "golden", name+".golden.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s is not valid JSON: %v", path, err)
		}
	}
}
