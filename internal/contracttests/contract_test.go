package contracttests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type identity struct {
	Slug               string
	Lang               string
	URL                string
	ResolvedLang       string
	ResolvedSourcePath string
	TagSlugs           []string
	CategorySlugs      []string
}

func TestContractPageIdentityConsistentAcrossReadTools(t *testing.T) {
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	const slug = "/posts/hello/"
	wantSourceSuffix := filepath.ToSlash("testdata/fixtures/content/posts/hello.md")
	want := identity{
		Slug:          "/posts/hello/",
		Lang:          "en",
		URL:           "https://example.test/posts/hello/",
		ResolvedLang:  "",
		TagSlugs:      []string{"go", "hugo"},
		CategorySlugs: []string{"tutorials"},
	}

	got := map[string]identity{
		"get_page":               identityFromGetPage(t, callTool(t, anonSession, "get_page", map[string]any{"slug": slug})),
		"get_page_frontmatter":   identityFromFrontmatter(t, callTool(t, readSession, "get_page_frontmatter", map[string]any{"slug": slug})),
		"get_full_page_markdown": identityFromMarkdownPage(t, callTool(t, readSession, "get_full_page_markdown", map[string]any{"slug": slug})),
		"build_agent_context":    identityFromAgentContext(t, callTool(t, readSession, "build_agent_context", map[string]any{"slug": slug})),
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
		if actual.ResolvedSourcePath == "" || !strings.HasSuffix(filepath.ToSlash(actual.ResolvedSourcePath), wantSourceSuffix) {
			t.Fatalf("%s resolved_source_path = %q, want suffix %q", tool, actual.ResolvedSourcePath, wantSourceSuffix)
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
	}
}

func TestContractMultilingualResolutionConsistentAcrossReadAndWriteTools(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	publicRoot := filepath.Join(root, "public")

	frSource := filepath.Join(contentRoot, "posts", "bonjour", "index.fr.md")
	writeFile(t, frSource, "---\ntitle: Bonjour\ndate: 2026-07-13\ncategories:\n  - Infra\n---\nBonjour monde.\n")
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
		"get_page_frontmatter":   identityFromFrontmatter(t, callTool(t, readSession, "get_page_frontmatter", map[string]any{"slug": slug})),
		"get_full_page_markdown": identityFromMarkdownPage(t, callTool(t, readSession, "get_full_page_markdown", map[string]any{"slug": slug})),
		"build_agent_context":    identityFromAgentContext(t, callTool(t, readSession, "build_agent_context", map[string]any{"slug": slug})),
		"diff_page":              identityFromDiffPage(t, callTool(t, readSession, "diff_page", map[string]any{"slug": slug})),
	}

	for tool, actual := range readIDs {
		if actual.ResolvedLang != "fr" {
			t.Fatalf("%s resolved_lang = %q, want fr", tool, actual.ResolvedLang)
		}
		if actual.ResolvedSourcePath != frSource {
			t.Fatalf("%s resolved_source_path = %q, want %q", tool, actual.ResolvedSourcePath, frSource)
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
	if got := asString(m["resolved_lang"]); got != "fr" {
		t.Fatalf("update_page resolved_lang = %q, want fr", got)
	}
	if got := asString(m["resolved_source_path"]); got != frSource {
		t.Fatalf("update_page resolved_source_path = %q, want %q", got, frSource)
	}
}

func TestContractDryRunMutationsDoNotWriteToDisk(t *testing.T) {
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

	res := callTool(t, readSession, "get_full_page_markdown", map[string]any{"slug": "/posts/existing/"})
	if res.IsError {
		t.Fatalf("get_full_page_markdown after dry_run returned error: %s", marshalAny(t, res.Content))
	}
	page := mapAt(t, decodeContent(t, res), "page")
	if got := asString(page["markdown"]); got != "Original body." {
		t.Fatalf("markdown after dry_run = %q, want original body", got)
	}
}

func TestContractPaginationWalksToEndWithoutGaps(t *testing.T) {
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
	page := mapAt(t, decodeContent(t, res), "page")
	return identity{
		Slug:               asString(page["slug"]),
		Lang:               asString(page["lang"]),
		URL:                asString(page["url"]),
		ResolvedLang:       asString(page["resolved_lang"]),
		ResolvedSourcePath: asString(page["resolved_source_path"]),
		TagSlugs:           termSlugs(page["tag_terms"]),
		CategorySlugs:      termSlugs(page["category_terms"]),
	}
}

func identityFromFrontmatter(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	fm := mapAt(t, decodeContent(t, res), "frontmatter")
	return identity{
		Slug:               asString(fm["slug"]),
		Lang:               asString(fm["lang"]),
		URL:                asString(fm["url"]),
		ResolvedLang:       asString(fm["resolved_lang"]),
		ResolvedSourcePath: asString(fm["resolved_source_path"]),
		TagSlugs:           termSlugs(fm["tag_terms"]),
		CategorySlugs:      termSlugs(fm["category_terms"]),
	}
}

func identityFromMarkdownPage(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	page := mapAt(t, decodeContent(t, res), "page")
	return identity{
		Slug:               asString(page["slug"]),
		Lang:               asString(page["lang"]),
		URL:                asString(page["url"]),
		ResolvedLang:       asString(page["resolved_lang"]),
		ResolvedSourcePath: asString(page["resolved_source_path"]),
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
		Lang:               asString(fm["lang"]),
		URL:                asString(fm["url"]),
		ResolvedLang:       asString(fm["resolved_lang"]),
		ResolvedSourcePath: asString(fm["resolved_source_path"]),
		TagSlugs:           termSlugs(fm["tag_terms"]),
		CategorySlugs:      termSlugs(fm["category_terms"]),
	}
}

func identityFromDiffPage(t *testing.T, res *mcp.CallToolResult) identity {
	t.Helper()
	data := mapAt(t, decodeContent(t, res), "data")
	return identity{
		ResolvedLang:       asString(data["resolved_lang"]),
		ResolvedSourcePath: asString(data["resolved_source_path"]),
	}
}

func extractTopLevelPages(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	return extractCollection(t, m, "pages")
}

func extractTopLevelItems(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	return extractCollection(t, m, "items")
}

func extractTopLevelEntries(t *testing.T, m map[string]any) ([]string, int, int, bool, *int) {
	t.Helper()
	return extractCollection(t, m, "entries")
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

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
