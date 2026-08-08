package read_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// #894: validate_site/validate_frontmatter must expose test_content_owner (via
// the structured test_content field) and support an optional owner filter so a
// multi-agent caller can enumerate just its own disposable residue. These tests
// fail red against pre-fix code (no test_content field, no owner filter) and
// pass green once both are wired.

func setupOwnerFilterSite(t *testing.T) (*site.Index, *hugosite.SourceIndex) {
	t.Helper()
	contentRoot := t.TempDir()
	write := func(slug, frontmatter string) {
		dir := filepath.Join(contentRoot, "posts", slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(frontmatter), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Explicit test content owned by agent-a.
	write("owned-by-a", "---\ntitle: A residue\ndate: 2026-08-07\ntest_content: true\ntest_content_owner: agent-a\n---\nBody.\n")
	// Explicit test content owned by agent-b.
	write("owned-by-b", "---\ntitle: B residue\ndate: 2026-08-07\ntest_content: true\ntest_content_owner: agent-b\n---\nBody.\n")
	// Reserved-prefix legacy test content with no owner recorded.
	write("mcp-audit-legacy", "---\ntitle: Legacy audit\ndate: 2026-08-07\n---\nBody.\n")
	// An ordinary published page that is not test content at all.
	write("real-article", "---\ntitle: Real article\ndate: 2026-08-07\n---\nBody.\n")

	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}
	return idx, srcIdx
}

// ownerOf returns a map slug->owner from validate_site's structured
// test_content field.
func testContentOwnerMap(t *testing.T, data map[string]any) map[string]string {
	t.Helper()
	raw, ok := data["test_content"].([]any)
	if !ok {
		t.Fatalf("data.test_content = %#v, want an array of {slug, owner} objects", data["test_content"])
	}
	out := make(map[string]string, len(raw))
	for _, e := range raw {
		entry, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("test_content entry = %#v, want object", e)
		}
		slug, _ := entry["slug"].(string)
		owner, _ := entry["owner"].(string)
		out[slug] = owner
	}
	return out
}

func TestValidateSiteExposesTestContentOwner(t *testing.T) {
	idx, srcIdx := setupOwnerFilterSite(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"include_valid": true})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	owners := testContentOwnerMap(t, data)

	if got := owners["/posts/owned-by-a/"]; got != "agent-a" {
		t.Fatalf("owner for owned-by-a = %q, want agent-a; full map=%#v", got, owners)
	}
	if got := owners["/posts/owned-by-b/"]; got != "agent-b" {
		t.Fatalf("owner for owned-by-b = %q, want agent-b; full map=%#v", got, owners)
	}
	// Reserved-prefix legacy content is still surfaced (advisory) but has no owner.
	if _, present := owners["/posts/mcp-audit-legacy/"]; !present {
		t.Fatalf("reserved-prefix legacy test content missing from test_content; map=%#v", owners)
	}
	if got := owners["/posts/mcp-audit-legacy/"]; got != "" {
		t.Fatalf("reserved-prefix legacy owner = %q, want empty", got)
	}
	if _, present := owners["/posts/real-article/"]; present {
		t.Fatalf("real-article must not appear in test_content; map=%#v", owners)
	}
}

// TestValidateSiteOwnerFilterDoesNotMaskDetailRows guards against the owner
// filter leaking out of the advisory test-content lists into the validation
// detail rows / counts. A test page owned by another agent that ALSO has a
// validation issue must still be counted in `invalid` AND still appear in the
// paginated `pages` detail rows under an owner filter — the filter narrows only
// test_content/test_content_slugs, never the scan scope. Fails red against the
// pre-fix `continue`, which dropped non-matching test pages from allResults.
func TestValidateSiteOwnerFilterDoesNotMaskDetailRows(t *testing.T) {
	contentRoot := t.TempDir()
	write := func(slug, frontmatter string) {
		dir := filepath.Join(contentRoot, "posts", slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(frontmatter), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// agent-a's test residue: valid.
	write("owned-by-a", "---\ntitle: A residue\ndate: 2026-08-07\ntest_content: true\ntest_content_owner: agent-a\n---\nBody.\n")
	// agent-b's test residue: INVALID (missing title). Under owner=agent-a this
	// must NOT vanish from the invalid count or the detail rows.
	write("owned-by-b-invalid", "---\ndate: 2026-08-07\ntest_content: true\ntest_content_owner: agent-b\n---\nBody.\n")

	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}

	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"include_valid": true, "owner": "agent-a"})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	data := decodeContent(t, res)

	// Advisory list is correctly narrowed to agent-a only.
	owners := testContentOwnerMap(t, data)
	if len(owners) != 1 || owners["/posts/owned-by-a/"] != "agent-a" {
		t.Fatalf("test_content should list only agent-a, got %#v", owners)
	}

	// Counts describe the FULL scan scope regardless of the owner filter:
	// both pages are checked, agent-b's page is still invalid.
	if got := data["pages_checked"]; got != float64(2) {
		t.Fatalf("pages_checked = %v, want 2 (owner filter must not shrink scan scope)", got)
	}
	if got := data["invalid"]; got != float64(1) {
		t.Fatalf("invalid = %v, want 1 (agent-b's invalid page must still count under owner filter)", got)
	}

	// The invalid, non-matching-owner page must still be present in the detail
	// rows — an owner filter must never mask an invalid page from `pages`.
	pages, ok := data["pages"].([]any)
	if !ok {
		t.Fatalf("data.pages = %#v, want array", data["pages"])
	}
	var sawInvalidOther bool
	for _, p := range pages {
		row, _ := p.(map[string]any)
		if row["slug"] == "/posts/owned-by-b-invalid/" {
			sawInvalidOther = true
		}
	}
	if !sawInvalidOther {
		t.Fatalf("owner=agent-a filter masked agent-b's INVALID page from detail rows; pages=%#v", pages)
	}
}

func TestValidateSiteOwnerFilterNarrowsToCaller(t *testing.T) {
	idx, srcIdx := setupOwnerFilterSite(t)
	session, done := newTestClientWithSourceIndex(t, idx, srcIdx)
	defer done()

	res := callTool(t, session, "validate_site", map[string]any{"include_valid": true, "owner": "agent-a"})
	if res.IsError {
		t.Fatalf("validate_site returned error: %v", res.Content)
	}
	data := decodeContent(t, res)

	owners := testContentOwnerMap(t, data)
	if len(owners) != 1 {
		t.Fatalf("owner=agent-a should return exactly one test_content entry, got %#v", owners)
	}
	if got := owners["/posts/owned-by-a/"]; got != "agent-a" {
		t.Fatalf("filtered test_content = %#v, want only owned-by-a/agent-a", owners)
	}

	// test_content_slugs must be narrowed too, so an agent can act on just its own.
	slugs, ok := data["test_content_slugs"].([]any)
	if !ok || len(slugs) != 1 || slugs[0] != "/posts/owned-by-a/" {
		t.Fatalf("filtered test_content_slugs = %#v, want exactly [/posts/owned-by-a/]", data["test_content_slugs"])
	}
}
