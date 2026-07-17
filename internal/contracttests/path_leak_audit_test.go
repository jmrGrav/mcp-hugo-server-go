package contracttests

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pathDisclosurePatterns catches absolute host filesystem paths that must
// never appear in a reader/content.read/site.admin-read tool response
// (issue #376). These mirror common deployment layouts (dev checkouts under
// /home, containers/CI runners, production installs under /var, /srv, /opt,
// /etc, and root-owned checkouts under /root).
var pathDisclosurePatterns = []*regexp.Regexp{
	regexp.MustCompile(`/home/[\w.-]+`),
	regexp.MustCompile(`/root/[\w.-]*`),
	regexp.MustCompile(`/var/(www|lib|opt)/[\w.-]+`),
	regexp.MustCompile(`/srv/[\w.-]+`),
	regexp.MustCompile(`/opt/[\w.-]+`),
	regexp.MustCompile(`/etc/[\w.-]+`),
	regexp.MustCompile(`/runner/[\w.-]+`),
}

// errorReporter is the subset of *testing.T that auditToolResponseForPathLeaks
// needs. It exists so TestPathLeakDetectionCatchesPlantedLeaks can capture
// detection failures into a plain struct instead of a real *testing.T —
// a failing subtest run via t.Run always marks its parent (and ultimately
// the whole package) as failed in Go's testing framework, so a real *testing.T
// can't be used to assert "detection correctly fires" without permanently
// failing the build.
type errorReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// recordingReporter implements errorReporter by collecting messages instead
// of failing anything, for testing the detector itself.
type recordingReporter struct {
	messages []string
}

func (r *recordingReporter) Helper() {}
func (r *recordingReporter) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// auditToolResponseForPathLeaks fails the test if the raw JSON body of a
// tool response contains an absolute host filesystem path, either via the
// generic disclosure patterns above or via the fixture's own known absolute
// roots (siteRoot/contentRoot), which is a stronger, deployment-specific
// check than the generic patterns alone.
func auditToolResponseForPathLeaks(t errorReporter, toolName string, raw []byte, knownRoots ...string) {
	t.Helper()
	text := string(raw)
	for _, pattern := range pathDisclosurePatterns {
		if m := pattern.FindAllString(text, -1); len(m) > 0 {
			t.Errorf("%s: response leaks absolute host path(s) matching %s: %v", toolName, pattern.String(), m)
		}
	}
	for _, root := range knownRoots {
		if root == "" {
			continue
		}
		if idx := strings.Index(text, root); idx >= 0 {
			t.Errorf("%s: response leaks configured absolute root %q at offset %d", toolName, root, idx)
		}
	}
}

// absoluteFixtureConfig is fixtureConfig() with SiteRoot/ContentRoot resolved
// to absolute paths, so a leak of either value is guaranteed to match the
// pathDisclosurePatterns above regardless of where the test runs from
// (matches /home on dev machines, /runner or similar on CI).
func absoluteFixtureConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := fixtureConfig()
	siteRoot, err := filepath.Abs(cfg.SiteRoot)
	if err != nil {
		t.Fatalf("filepath.Abs(SiteRoot): %v", err)
	}
	contentRoot, err := filepath.Abs(cfg.ContentRoot)
	if err != nil {
		t.Fatalf("filepath.Abs(ContentRoot): %v", err)
	}
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = siteRoot
	return cfg
}

// rawResponseBody returns everything the caller could possibly see: the
// structured content (success path) AND every text content block (error
// path — CallToolResult puts the error message in res.Content, not
// StructuredContent; see decodeErrorContent above). A path-leak scanner that
// only looked at StructuredContent would be blind to every error-path leak,
// which is exactly the vector #376 calls out (error messages exposing host
// configuration).
func rawResponseBody(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	var buf []byte
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		buf = append(buf, raw...)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			buf = append(buf, '\n')
			buf = append(buf, tc.Text...)
		}
	}
	return buf
}

// TestAuditAnonymousAndReadToolsNeverLeakAbsolutePaths is the automated
// regression test required by issue #376: every anonymous and content.read
// tool response (success or error) must never expose the configured
// absolute SiteRoot/ContentRoot, nor any host path matching common
// deployment-path patterns.
func TestAuditAnonymousAndReadToolsNeverLeakAbsolutePaths(t *testing.T) {
	cfg := absoluteFixtureConfig(t)
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(cfg.ContentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	const slug = "/posts/hello/"

	anonTests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "list_pages", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_page", args: map[string]any{"slug": slug}},
		{tool: "search_pages", args: map[string]any{"query": "hello", "limit": 2, "offset": 0}},
		{tool: "get_recent_posts", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "list_tags", args: map[string]any{}},
		{tool: "list_categories", args: map[string]any{}},
		{tool: "get_sitemap", args: map[string]any{"limit": 2, "offset": 0, "exclude_taxonomies": true}},
		{tool: "get_feed", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_site_information", args: map[string]any{}},
	}
	for _, tc := range anonTests {
		t.Run("anonymous/"+tc.tool, func(t *testing.T) {
			res := callTool(t, anonSession, tc.tool, tc.args)
			auditToolResponseForPathLeaks(t, tc.tool, rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
		})
	}
	// #376 explicitly calls out error messages as a path-leak vector. All
	// the success-path calls above never hit that code, so force a
	// content_not_found error here (via a slug that resolves to nothing)
	// and audit it too — this is also the only case that exercises the
	// res.Content branch of rawResponseBody end-to-end.
	t.Run("anonymous/get_page (not found error)", func(t *testing.T) {
		res := callTool(t, anonSession, "get_page", map[string]any{"slug": "/does/not/exist/"})
		if !res.IsError {
			t.Fatal("get_page for a nonexistent slug should return an error result")
		}
		auditToolResponseForPathLeaks(t, "get_page", rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
	})

	readTests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "get_page_markdown", args: map[string]any{"slug": slug}},
		{tool: "get_page_frontmatter", args: map[string]any{"slug": slug}},
		{tool: "get_related_content", args: map[string]any{"slug": slug, "limit": 2}},
		{tool: "build_agent_context", args: map[string]any{"slug": slug}},
		{tool: "export_agent_context", args: map[string]any{"limit": 1, "offset": 0}},
		{tool: "search_content", args: map[string]any{"type": "all", "limit": 2, "offset": 0}},
		{tool: "explain_structure", args: map[string]any{}},
		{tool: "get_site_health", args: map[string]any{}},
		{tool: "validate_frontmatter", args: map[string]any{"slug": slug}},
		{tool: "validate_site", args: map[string]any{}},
		{tool: "get_broken_links", args: map[string]any{"limit": 2, "offset": 0}},
		{tool: "get_backlinks", args: map[string]any{"slug": slug}},
		{tool: "suggest_links", args: map[string]any{"slug": slug, "limit": 2}},
		{tool: "list_content_types", args: map[string]any{}},
		// Against absoluteFixtureConfig, diff_page actually succeeds here
		// (this repo's own working tree is a real git repo, so it computes
		// a genuine diff rather than erroring); its error path is exercised
		// separately below via a nonexistent slug.
		{tool: "diff_page", args: map[string]any{"slug": slug}},
	}
	for _, tc := range readTests {
		t.Run("read/"+tc.tool, func(t *testing.T) {
			res := callTool(t, readSession, tc.tool, tc.args)
			auditToolResponseForPathLeaks(t, tc.tool, rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
		})
	}
	for _, tool := range []string{"get_page_markdown", "get_page_frontmatter", "diff_page"} {
		t.Run("read/"+tool+" (not found error)", func(t *testing.T) {
			res := callTool(t, readSession, tool, map[string]any{"slug": "/does/not/exist/"})
			if !res.IsError {
				t.Fatalf("%s for a nonexistent slug should return an error result", tool)
			}
			auditToolResponseForPathLeaks(t, tool, rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
		})
	}

	adminSession, adminDone := newSiteAdminReadSession(t, idx, srcIdx, cfg)
	defer adminDone()

	adminTests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "get_runtime_status", args: map[string]any{}},
		{tool: "check_sri_versions", args: map[string]any{}},
		{tool: "get_theme_status", args: map[string]any{}},
		// verify_publication makes an outbound HTTP request to site_url,
		// which fails against the fixture's non-routable example.test. This
		// is a *success* envelope with the probe failure folded into
		// data.http_error (see internal/tools/admin/verify_publication.go),
		// not an error result — audited anyway since it's still
		// operator-facing text derived from a failed network call.
		{tool: "verify_publication", args: map[string]any{"slug": slug}},
	}
	for _, tc := range adminTests {
		t.Run("site.admin/"+tc.tool, func(t *testing.T) {
			res := callTool(t, adminSession, tc.tool, tc.args)
			auditToolResponseForPathLeaks(t, tc.tool, rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
		})
	}
	t.Run("site.admin/verify_publication (not found error)", func(t *testing.T) {
		res := callTool(t, adminSession, "verify_publication", map[string]any{"slug": "/does/not/exist/"})
		if !res.IsError {
			t.Fatal("verify_publication for a nonexistent slug should return an error result")
		}
		auditToolResponseForPathLeaks(t, "verify_publication", rawResponseBody(t, res), cfg.SiteRoot, cfg.ContentRoot)
	})
}

// TestPathLeakDetectionCatchesPlantedLeaks proves auditToolResponseForPathLeaks
// actually detects a leak rather than passing vacuously. A regression test
// that can never fail is worse than no test: it advertises coverage that
// doesn't exist. This plants realistic leaks (an absolute host path in an
// error-style string, and a known configured root) and asserts detection
// fires for each; a final clean case proves it doesn't false-positive on an
// ordinary response.
func TestPathLeakDetectionCatchesPlantedLeaks(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		knownRoots []string
		wantLeak   bool
	}{
		{
			name:     "generic /home path in an error message",
			body:     `{"errors":[{"code":"git_metadata_unavailable","message":"source page /home/jm/Documents/mcp-hugo-server-go/testdata/fixtures/content/posts/hello.md is outside the repository root"}]}`,
			wantLeak: true,
		},
		{
			name:     "generic /etc path",
			body:     `{"warnings":["config not found at /etc/mcp-hugo-server-go/config.yaml"]}`,
			wantLeak: true,
		},
		{
			name:       "known configured root leaked without matching a generic pattern",
			body:       `{"warnings":["content root is /data/hugo-content"]}`,
			knownRoots: []string{"/data/hugo-content"},
			wantLeak:   true,
		},
		{
			name:     "clean response",
			body:     `{"success":true,"data":{"slug":"/posts/hello/","resolved_source_path":"content/posts/hello.md"}}`,
			wantLeak: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingReporter{}
			auditToolResponseForPathLeaks(rec, "fake_tool", []byte(tc.body), tc.knownRoots...)
			leaked := len(rec.messages) > 0
			if leaked != tc.wantLeak {
				t.Fatalf("body %q: detected leak = %v (messages=%v), want %v", tc.body, leaked, rec.messages, tc.wantLeak)
			}
		})
	}
}

// newSiteAdminReadSession wires only the read-only subset of site.admin
// tools relevant to #376 (get_runtime_status, check_sri_versions,
// get_theme_status, verify_publication). Mutating site.admin tools
// (build_site, generate_hero_image, run_post_build_hooks, create_preview,
// preview_build) are out of scope for this read-only-tools audit.
func newSiteAdminReadSession(t *testing.T, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolsadmin.RegisterRuntimeStatus(s, cfg)
	toolsadmin.RegisterSRI(s, cfg)
	toolsadmin.RegisterThemeStatus(s, cfg)
	toolsadmin.RegisterVerifyPublication(s, idx, srcIdx, cfg)
	return connectClient(t, s)
}
