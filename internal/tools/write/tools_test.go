package write_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testServerOpts struct {
	SiteRoot string
	SiteDB   *db.DB
	SiteIdx  *site.Index
	// RateLimit overrides config.Default()'s RateLimit section when non-nil,
	// e.g. for tests that need a low CreateUpdatePerMin/DestructivePerMin to
	// exercise rate limiting without hundreds of calls.
	RateLimit *config.RateLimitConfig
	// HugoRoot, when non-empty, is set on cfg so delete_page's hero image
	// cleanup (#606) has somewhere to look for {slug}-featured.jpg.
	HugoRoot string
	// ForceDryRunAll, when true, sets cfg.ForceDryRunAll (#611) so every
	// mutation tool call in the test session behaves as if dry_run: true
	// were passed, regardless of what the test actually sends.
	ForceDryRunAll bool
}

// newTestServer builds a write-tool MCP server over an in-memory transport and
// returns the client session, the source index (for post-call inspection), and
// a cleanup function. Callers that don't need the source index can ignore it.
func newTestServer(t *testing.T, contentRoot string, opts ...testServerOpts) (*mcp.ClientSession, *hugosite.SourceIndex, func()) {
	t.Helper()
	var o testServerOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = o.SiteRoot
	cfg.HugoRoot = o.HugoRoot
	if o.RateLimit != nil {
		cfg.RateLimit = *o.RateLimit
	}
	cfg.ForceDryRunAll = o.ForceDryRunAll

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg, o.SiteDB, o.SiteIdx)

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
	return session, idx, func() { _ = session.Close() }
}

func waitForStartSignals(t *testing.T, started <-chan struct{}, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for goroutine start signal %d/%d", i+1, want)
		}
	}
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func decodeWriteContent(t *testing.T, res *mcp.CallToolResult) map[string]any {
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

func decodeWriteData(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	out := decodeWriteContent(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	return data
}

// assertRootOnlyField asserts field is a documented root-level-only
// compatibility field (#466, #510, #522): present at the root, and NOT
// duplicated under data. Prior to #520/#605, rate_limit_remaining was
// present in both places with the same value — an undeclared duplication
// that contradicted docs/mcp-contract.md's own "no other top-level payload
// duplication" claim for these tools; this now guards against it recurring.
func assertRootOnlyField(t *testing.T, root, data map[string]any, field string) {
	t.Helper()
	if _, present := root[field]; !present {
		t.Fatalf("%s missing from root", field)
	}
	if _, present := data[field]; present {
		t.Fatalf("%s unexpectedly duplicated under data (root/data payload duplication, #520/#605)", field)
	}
}

func decodeWriteErrorEnvelope(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent != nil {
		return decodeWriteContent(t, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	return m
}

func decodeWriteErrorData(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	env := decodeWriteErrorEnvelope(t, res)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("error data type = %T, want map[string]any", env["data"])
	}
	return data
}

func assertWritePageState(t *testing.T, raw any, source, build, public, index string) {
	t.Helper()
	state, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("state type = %T", raw)
	}
	if got := state["source_state"]; got != source {
		t.Fatalf("source_state = %v, want %q", got, source)
	}
	if got := state["build_state"]; got != build {
		t.Fatalf("build_state = %v, want %q", got, build)
	}
	if got := state["public_state"]; got != public {
		t.Fatalf("public_state = %v, want %q", got, public)
	}
	if got := state["index_state"]; got != index {
		t.Fatalf("index_state = %v, want %q", got, index)
	}
}

func currentRevision(t *testing.T, path string) string {
	t.Helper()
	rev, err := contentmodel.SourceRevision(path)
	if err != nil {
		t.Fatalf("SourceRevision(%s): %v", path, err)
	}
	return rev
}

func marshalContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	raw, err := json.Marshal(res.Content)
	if err != nil {
		t.Fatalf("json.Marshal(content): %v", err)
	}
	return string(raw)
}

func assertSingleStructuredWriteErrorField(t *testing.T, res *mcp.CallToolResult, code, field string) {
	t.Helper()
	env := decodeWriteErrorEnvelope(t, res)
	errors, ok := env["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("structured errors = %#v, want exactly one error", env["errors"])
	}
	err0, ok := errors[0].(map[string]any)
	if !ok {
		t.Fatalf("structured errors[0] type = %T, want map[string]any", errors[0])
	}
	if got := err0["code"]; got != code {
		t.Fatalf("structured error code = %v, want %q", got, code)
	}
	if got := err0["field"]; got != field {
		t.Fatalf("structured error field = %v, want %q", got, field)
	}
}

func TestCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "my-post",
		"title":      "My Post",
		"body":       "Hello world.",
		"tags":       []any{"go", "hugo"},
		"categories": []any{"tutorials"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	out := decodeWriteContent(t, res)
	dataEnvelope := decodeWriteData(t, res)
	assertRootOnlyField(t, out, dataEnvelope, "rate_limit_remaining")
	assertWritePageState(t, dataEnvelope["state"], "present", "pending", "not_yet_available", "source_only")
	if got := dataEnvelope["slug"]; got != "/my-post/" {
		t.Fatalf("create_page data.slug = %v, want /my-post/ (canonical public form, #554)", got)
	}
	if got := dataEnvelope["source_key"]; got != "my-post" {
		t.Fatalf("create_page data.source_key = %v, want my-post", got)
	}

	path := filepath.Join(contentRoot, "my-post", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not found at %s: %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, "My Post") {
		t.Errorf("frontmatter missing title: %s", content)
	}
	if !strings.Contains(content, "Hello world.") {
		t.Errorf("body missing: %s", content)
	}
	if !strings.Contains(content, "go") {
		t.Errorf("tags missing: %s", content)
	}
	if !strings.Contains(content, "draft") {
		t.Errorf("frontmatter missing draft field: %s", content)
	}
	if got := dataEnvelope["resolved_source_path"]; got != "content/my-post/index.md" {
		t.Fatalf("create_page data.resolved_source_path = %v, want content/my-post/index.md", got)
	}
	if got := dataEnvelope["resolved_lang"]; got != "" {
		t.Fatalf("create_page data.resolved_lang = %v, want empty default lang", got)
	}
}

// #467: create_page/update_page surface an advisory (never a failure) when
// the most recent build_site attempt in this process failed, so an agent
// notices a broken publish pipeline from the write call itself.
func TestCreatePageWarnsWhenLastBuildFailed(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	buildstatus.RecordFailure("permission_denied", time.Now())

	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "build-warn-post",
		"title":      "Build Warn Post",
		"body":       "Hello world.",
		"tags":       []any{"go"},
		"categories": []any{"tutorials"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	warning, _ := dataEnvelope["warning"].(string)
	if !strings.Contains(warning, "permission_denied") {
		t.Fatalf("create_page data.warning = %q, want it to mention the last failed build_site attempt", warning)
	}
}

func TestCreatePageOmitsBuildWarningWhenLastBuildSucceeded(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	buildstatus.RecordSuccess(time.Now())

	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "build-ok-post",
		"title":      "Build OK Post",
		"body":       "Hello world.",
		"tags":       []any{"go"},
		"categories": []any{"tutorials"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if warning, _ := dataEnvelope["warning"].(string); warning != "" {
		t.Fatalf("create_page data.warning = %q, want empty when the last build_site attempt succeeded", warning)
	}
}

func TestCreatePageRejectsDuplicateSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	first := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/duplicate",
		"title":      "Original",
		"body":       "Long original body",
		"tags":       []any{"first"},
		"categories": []any{"tests"},
	})
	if first.IsError {
		raw, _ := json.Marshal(first.Content)
		t.Fatalf("initial create_page failed: %s", raw)
	}

	second := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/duplicate",
		"title":      "Overwrite attempt",
		"body":       "This must not replace the original content.",
		"tags":       []any{"second"},
		"categories": []any{"tests"},
	})
	if !second.IsError {
		raw, _ := json.Marshal(second.Content)
		t.Fatalf("duplicate create_page should fail: %s", raw)
	}
	raw, _ := json.Marshal(second.Content)
	if !strings.Contains(string(raw), "already_exists") {
		t.Fatalf("duplicate create_page must return already_exists, got: %s", raw)
	}

	path := filepath.Join(contentRoot, "posts", "duplicate", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, "Original") || strings.Contains(content, "Overwrite attempt") {
		t.Fatalf("duplicate create_page must preserve original content, got:\n%s", content)
	}
}

func TestCreatePageDryRunRejectsDuplicateSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	first := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/dry-run-duplicate",
		"title":      "Original",
		"body":       "Long original body",
		"tags":       []any{"first"},
		"categories": []any{"tests"},
	})
	if first.IsError {
		raw, _ := json.Marshal(first.Content)
		t.Fatalf("initial create_page failed: %s", raw)
	}

	preview := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/dry-run-duplicate",
		"title":      "Overwrite attempt",
		"body":       "This must not be previewed as creatable.",
		"tags":       []any{"second"},
		"categories": []any{"tests"},
		"dry_run":    true,
	})
	if !preview.IsError {
		raw, _ := json.Marshal(preview.Content)
		t.Fatalf("dry-run create_page on existing slug should fail: %s", raw)
	}
	raw, _ := json.Marshal(preview.Content)
	if !strings.Contains(string(raw), "already_exists") {
		t.Fatalf("dry-run create_page on existing slug must return already_exists, got: %s", raw)
	}

	path := filepath.Join(contentRoot, "posts", "dry-run-duplicate", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if !strings.Contains(string(data), "Original") {
		t.Fatalf("dry-run must not touch original content, got:\n%s", string(data))
	}
}

func TestCreatePageSymlinkBlocked(t *testing.T) {
	contentRoot := t.TempDir()

	target := t.TempDir()
	symlinkPath := filepath.Join(contentRoot, "bad-slug")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "bad-slug",
		"title": "Bad Slug",
	})
	if !res.IsError {
		t.Fatal("expected error for symlink slug, got success")
	}
}

func TestCreatePageRejectsHostileSlugCorpus(t *testing.T) {
	cases := []struct {
		name string
		slug string
	}{
		{name: "raw traversal", slug: "../escape"},
		{name: "encoded traversal", slug: "%2e%2e/escape"},
		{name: "double encoded traversal", slug: "%252e%252e/escape"},
		{name: "backslash traversal", slug: `..\\escape`},
		{name: "absolute path", slug: "/tmp/escape"},
		{name: "unicode confusable slash", slug: "posts∕escape"},
		{name: "control character", slug: "posts/\x07escape"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			session, _, done := newTestServer(t, contentRoot)
			defer done()

			res := callTool(t, session, "create_page", map[string]any{
				"slug":       tc.slug,
				"title":      "Hostile slug",
				"body":       "",
				"tags":       []any{},
				"categories": []any{},
			})
			if !res.IsError {
				t.Fatalf("create_page(%q): want invalid_params, got success", tc.slug)
			}
			if raw := marshalContent(t, res); !strings.Contains(raw, "invalid_params") {
				t.Fatalf("create_page(%q) raw error = %s, want invalid_params", tc.slug, raw)
			}

			entries, err := os.ReadDir(contentRoot)
			if err != nil {
				t.Fatalf("ReadDir(contentRoot): %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("create_page(%q) wrote unexpected entries under content root: %v", tc.slug, entries)
			}
		})
	}
}

func TestUpdatePageRejectsHostileSlugCorpus(t *testing.T) {
	cases := []struct {
		name string
		slug string
	}{
		{name: "raw traversal", slug: "../escape"},
		{name: "encoded traversal", slug: "%2e%2e/escape"},
		{name: "double encoded traversal", slug: "%252e%252e/escape"},
		{name: "backslash traversal", slug: `..\\escape`},
		{name: "absolute path", slug: "/tmp/escape"},
		{name: "unicode confusable slash", slug: "posts∕escape"},
		{name: "control character", slug: "posts/\x07escape"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			victimDir := filepath.Join(contentRoot, "posts", "victim")
			if err := os.MkdirAll(victimDir, 0o755); err != nil {
				t.Fatalf("MkdirAll victim: %v", err)
			}
			victimPath := filepath.Join(victimDir, "index.md")
			original := []byte("---\ntitle: Victim\n---\nDo not touch me.\n")
			if err := os.WriteFile(victimPath, original, 0o644); err != nil {
				t.Fatalf("WriteFile victim: %v", err)
			}

			session, _, done := newTestServer(t, contentRoot)
			defer done()

			res := callTool(t, session, "update_page", map[string]any{
				"slug":  tc.slug,
				"title": "Hostile update",
			})
			if !res.IsError {
				t.Fatalf("update_page(%q): want rejection, got success", tc.slug)
			}
			if raw := marshalContent(t, res); !strings.Contains(raw, "invalid_params") && !strings.Contains(raw, "not_found") {
				t.Fatalf("update_page(%q) raw error = %s, want invalid_params or not_found", tc.slug, raw)
			}

			got, err := os.ReadFile(victimPath)
			if err != nil {
				t.Fatalf("ReadFile victim: %v", err)
			}
			if string(got) != string(original) {
				t.Fatalf("update_page(%q) mutated victim page:\n%s", tc.slug, string(got))
			}
		})
	}
}

func TestDeletePageRejectsHostileSlugCorpus(t *testing.T) {
	cases := []struct {
		name string
		slug string
	}{
		{name: "raw traversal", slug: "../escape"},
		{name: "encoded traversal", slug: "%2e%2e/escape"},
		{name: "double encoded traversal", slug: "%252e%252e/escape"},
		{name: "backslash traversal", slug: `..\\escape`},
		{name: "absolute path", slug: "/tmp/escape"},
		{name: "unicode confusable slash", slug: "posts∕escape"},
		{name: "control character", slug: "posts/\x07escape"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			victimDir := filepath.Join(contentRoot, "posts", "victim")
			if err := os.MkdirAll(victimDir, 0o755); err != nil {
				t.Fatalf("MkdirAll victim: %v", err)
			}
			victimPath := filepath.Join(victimDir, "index.md")
			original := []byte("---\ntitle: Victim\n---\nDo not delete me.\n")
			if err := os.WriteFile(victimPath, original, 0o644); err != nil {
				t.Fatalf("WriteFile victim: %v", err)
			}

			session, _, done := newTestServer(t, contentRoot)
			defer done()

			res := callTool(t, session, "delete_page", map[string]any{
				"slug": tc.slug,
			})
			if !res.IsError {
				t.Fatalf("delete_page(%q): want rejection, got success", tc.slug)
			}
			if raw := marshalContent(t, res); !strings.Contains(raw, "invalid_params") && !strings.Contains(raw, "not_found") {
				t.Fatalf("delete_page(%q) raw error = %s, want invalid_params or not_found", tc.slug, raw)
			}

			got, err := os.ReadFile(victimPath)
			if err != nil {
				t.Fatalf("ReadFile victim: %v", err)
			}
			if string(got) != string(original) {
				t.Fatalf("delete_page(%q) mutated victim page:\n%s", tc.slug, string(got))
			}
		})
	}
}

func TestCreatePageReservedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "_index",
		"title": "Index",
	})
	if !res.IsError {
		t.Fatal("expected error for reserved slug _index, got success")
	}
}

func TestDeletePageRateLimit(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Create 6 pages. Delete the first 5 (each succeeds). The 6th delete targets
	// a page that still exists but must be blocked by the rate limiter.
	for i := 0; i < 6; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("rate-post-%d", i), "title": "Rate Post",
			"body": "body", "tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d failed: %s", i, raw)
		}
	}
	for i := 0; i < 5; i++ {
		slug := fmt.Sprintf("rate-post-%d", i)
		res := callTool(t, session, "delete_page", map[string]any{
			"slug":              slug,
			"expected_revision": currentRevision(t, filepath.Join(contentRoot, slug, "index.md")),
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("delete %d expected success, got error: %s", i+1, raw)
		}
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "rate-post-5",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "rate-post-5", "index.md")),
	})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 6th delete, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded error, got: %s", raw)
	}
}

func TestCreatePageRateLimit(t *testing.T) {
	// #378: create_page/update_page/upload_page_asset share a per-caller
	// budget separate from delete_page's own (defense-in-depth mirroring
	// delete's existing pattern), layered on top of the broader per-scope
	// content.write limiter enforced at the OAuth/HTTP layer.
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 3
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	for i := 0; i < 3; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("rl-post-%d", i), "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d expected success, got error: %s", i, raw)
		}
	}

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "rl-post-3", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 4th create_page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded error, got: %s", raw)
	}
}

func TestUpdatePageSharesRateLimitBudgetWithCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 2
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// First call (create) consumes 1 of the 2-per-minute budget.
	if res := callTool(t, session, "create_page", map[string]any{
		"slug": "shared-budget", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page expected success, got error: %s", raw)
	}

	// Second call (update, same caller) consumes the last slot.
	res := callTool(t, session, "update_page", map[string]any{
		"slug": "shared-budget", "title": "Updated",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "shared-budget", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page expected success, got error: %s", raw)
	}

	// Third call (update again) must be blocked — the budget is shared
	// across tool types, not per-tool.
	res = callTool(t, session, "update_page", map[string]any{
		"slug": "shared-budget", "title": "Updated Again",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "shared-budget", "index.md")),
	})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 3rd mutation sharing the budget, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded error, got: %s", raw)
	}
}

func TestDeleteAndCreateRateLimitsAreIndependent(t *testing.T) {
	// delete_page's DestructivePerMin budget and create/update/upload's
	// CreateUpdatePerMin budget must not share state — exhausting one must
	// not block the other.
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.DestructivePerMin = 1
	rl.CreateUpdatePerMin = 60
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	for i := 0; i < 2; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("indep-%d", i), "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d expected success, got error: %s", i, raw)
		}
	}

	if res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "indep-0",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "indep-0", "index.md")),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first delete_page expected success, got error: %s", raw)
	}

	// Second delete must be blocked (DestructivePerMin=1), but create_page
	// must still work — the two budgets are independent.
	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "indep-1",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "indep-1", "index.md")),
	})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 2nd delete_page, got success")
	}

	res = callTool(t, session, "create_page", map[string]any{
		"slug": "indep-2", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page must not be blocked by delete_page's exhausted budget: %s", raw)
	}
}

// TestCreatePageExposesRateLimitRemaining is a regression test for #466:
// rate_limit_remaining must decrease with each successful mutation sharing
// the same per-caller budget, instead of forcing an agent to infer safe
// pacing from the tool description alone.
func TestCreatePageExposesRateLimitRemaining(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 3
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	var remaining []float64
	for i := 0; i < 3; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("rl-remaining-%d", i), "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d expected success, got error: %s", i, raw)
		}
		out := decodeWriteContent(t, res)
		rem, ok := out["rate_limit_remaining"].(float64)
		if !ok {
			t.Fatalf("create_page %d: rate_limit_remaining = %#v, want present numeric field", i, out["rate_limit_remaining"])
		}
		bucket, ok := decodeWriteData(t, res)["rate_limit"].(map[string]any)
		if !ok {
			t.Fatalf("create_page %d: data.rate_limit = %#v, want object", i, decodeWriteData(t, res)["rate_limit"])
		}
		if got := bucket["remaining"]; got != rem {
			t.Fatalf("create_page %d: data.rate_limit.remaining = %v, want %v", i, got, rem)
		}
		if got := bucket["scope"]; got != "create_update_upload" {
			t.Fatalf("create_page %d: data.rate_limit.scope = %v, want create_update_upload", i, got)
		}
		remaining = append(remaining, rem)
	}
	for i := 1; i < len(remaining); i++ {
		if remaining[i] >= remaining[i-1] {
			t.Fatalf("rate_limit_remaining did not decrease across calls: %v", remaining)
		}
	}
}

// TestDeletePageRateLimitExceededIncludesRetryAfterSeconds is a regression
// test for #466: the throttled error's structured resolution must surface a
// concrete retry_after_seconds instead of only "retry_later" with no numeric
// hint.
func TestDeletePageRateLimitExceededIncludesRetryAfterSeconds(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.DestructivePerMin = 1
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	for i := 0; i < 2; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": fmt.Sprintf("rl-retry-%d", i), "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page %d expected success, got error: %s", i, raw)
		}
	}

	if res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "rl-retry-0",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "rl-retry-0", "index.md")),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first delete_page expected success, got error: %s", raw)
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "rl-retry-1",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "rl-retry-1", "index.md")),
	})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 2nd delete_page, got success")
	}
	m := decodeWriteErrorEnvelope(t, res)
	errs, ok := m["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("errors = %#v, want at least one error", m["errors"])
	}
	first, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("errors[0] type = %T", errs[0])
	}
	resolution, ok := first["resolution"].(map[string]any)
	if !ok {
		t.Fatalf("errors[0].resolution = %#v, want present", first["resolution"])
	}
	retryAfter, ok := resolution["retry_after_seconds"].(float64)
	if !ok || retryAfter <= 0 {
		t.Fatalf("resolution.retry_after_seconds = %#v, want a positive number", resolution["retry_after_seconds"])
	}
}

func TestDeletePageExposesLifecycleState(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(filepath.Join(siteRoot, "to-delete"), 0o755); err != nil {
		t.Fatalf("MkdirAll(siteRoot): %v", err)
	}
	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "to-delete",
		"title":      "Delete Me",
		"body":       "",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		raw, _ := json.Marshal(createRes.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "to-delete",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "to-delete", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	assertWritePageState(t, dataEnvelope["state"], "deleted", "not_applicable", "removed", "removed")
}

func TestUpdatePageNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "nonexistent",
		"title": "New Title",
	})
	if !res.IsError {
		t.Fatal("expected not_found error for nonexistent page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found error, got: %s", raw)
	}
}

func TestCreatePageIdempotencyKeyReturnsOriginalResultWithoutRewriting(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	args := map[string]any{
		"slug":            "idem-create",
		"title":           "Original",
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "idem-create-1",
	}
	first := callTool(t, session, "create_page", args)
	if first.IsError {
		t.Fatalf("first create_page failed: %s", marshalContent(t, first))
	}

	path := filepath.Join(contentRoot, "idem-create", "index.md")
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("WriteFile tampered: %v", err)
	}

	second := callTool(t, session, "create_page", args)
	if second.IsError {
		t.Fatalf("second create_page replay failed: %s", marshalContent(t, second))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after replay: %v", err)
	}
	if string(raw) != "tampered" {
		t.Fatalf("create_page replay should not rewrite file, got %q", string(raw))
	}
	// #464: new_revision must survive an idempotency replay since the cached
	// output (including new_revision) is stored and returned verbatim, not
	// recomputed against the (now-tampered) file on disk.
	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if firstOut["new_revision"] == "" || firstOut["new_revision"] != secondOut["new_revision"] {
		t.Fatalf("new_revision changed across replay: first=%v second=%v", firstOut["new_revision"], secondOut["new_revision"])
	}
}

// TestCreatePageIdempotencyKeyRaceOnConcurrentRetries proves the idempotency
// replay check happens under the content lock. If the check ran before the
// lock, two truly concurrent retries with the same key — the exact
// uncertain-delivery scenario idempotency_key exists to protect — could both
// miss the cache and race: the loser would see already_exists instead of the
// intended idempotent replay.
func TestCreatePageIdempotencyKeyRaceOnConcurrentRetries(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	args := map[string]any{
		"slug":            "idem-race",
		"title":           "Original",
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "idem-race-1",
	}

	hugosite.ContentMu.Lock()

	results := make(chan *mcp.CallToolResult, 2)
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			results <- callTool(t, session, "create_page", args)
		}()
	}

	waitForStartSignals(t, started, 2)
	hugosite.ContentMu.Unlock()
	wg.Wait()
	close(results)

	for res := range results {
		if res.IsError {
			t.Fatalf("concurrent create_page retry with same idempotency_key should not fail: %s", marshalContent(t, res))
		}
	}
}

func TestUpdatePageIdempotencyKeyReturnsOriginalResultWithoutReapplying(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-update",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "idem-update",
		"title":             "Updated",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-update", "index.md")),
		"idempotency_key":   "idem-update-1",
	}
	first := callTool(t, session, "update_page", args)
	if first.IsError {
		t.Fatalf("first update_page failed: %s", marshalContent(t, first))
	}

	path := filepath.Join(contentRoot, "idem-update", "index.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Mutated\n---\nMutated"), 0o644); err != nil {
		t.Fatalf("WriteFile mutated: %v", err)
	}

	second := callTool(t, session, "update_page", args)
	if second.IsError {
		t.Fatalf("second update_page replay failed: %s", marshalContent(t, second))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after replay: %v", err)
	}
	if !strings.Contains(string(raw), "title: Mutated") {
		t.Fatalf("update_page replay should not reapply update, got %q", string(raw))
	}
}

func TestDeletePageIdempotencyKeyReturnsOriginalResultWithoutReapplying(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-delete",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "idem-delete",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-delete", "index.md")),
		"idempotency_key":   "idem-delete-1",
	}
	first := callTool(t, session, "delete_page", args)
	if first.IsError {
		t.Fatalf("first delete_page failed: %s", marshalContent(t, first))
	}

	dir := filepath.Join(contentRoot, "idem-delete")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll recreated dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("recreated"), 0o644); err != nil {
		t.Fatalf("WriteFile recreated: %v", err)
	}

	second := callTool(t, session, "delete_page", args)
	if second.IsError {
		t.Fatalf("second delete_page replay failed: %s", marshalContent(t, second))
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err != nil {
		t.Fatalf("replayed delete_page should not re-delete recreated file: %v", err)
	}
}

// TestDeletePageIdempotencyKeyReplaysAfterSuccessfulDeletionWithoutRequiring
// The Resource To Still Exist is the exact live-audit failure from v1.6.4:
// after a successful delete, retrying the same delete with the same
// idempotency_key must replay the stored success result, not re-resolve the
// slug and fail with not_found merely because the first delete already
// removed it.
func TestDeletePageIdempotencyKeyReplaysAfterSuccessfulDeletionWhenSlugIsGone(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/idem-delete-gone",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "posts/idem-delete-gone",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "idem-delete-gone", "index.md")),
		"idempotency_key":   "idem-delete-gone-1",
	}
	first := callTool(t, session, "delete_page", args)
	if first.IsError {
		t.Fatalf("first delete_page failed: %s", marshalContent(t, first))
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "idem-delete-gone")); !os.IsNotExist(err) {
		t.Fatalf("slug directory still exists after first delete: stat err = %v, want IsNotExist", err)
	}

	second := callTool(t, session, "delete_page", args)
	if second.IsError {
		t.Fatalf("second delete_page replay failed: %s", marshalContent(t, second))
	}

	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("delete_page replay envelope changed after resource was gone\nfirst:  %#v\nsecond: %#v", firstOut, secondOut)
	}
}

func TestUpdatePageIdempotencyKeyRejectsDivergentReuse(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-conflict",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	key := "idem-conflict-1"
	first := callTool(t, session, "update_page", map[string]any{
		"slug":              "idem-conflict",
		"title":             "Changed",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-conflict", "index.md")),
		"idempotency_key":   key,
	})
	if first.IsError {
		t.Fatalf("first update_page failed: %s", marshalContent(t, first))
	}

	second := callTool(t, session, "update_page", map[string]any{
		"slug":              "idem-conflict",
		"title":             "Changed again",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-conflict", "index.md")),
		"idempotency_key":   key,
	})
	if !second.IsError {
		t.Fatal("reusing idempotency_key with different update input should fail")
	}
	if raw := marshalContent(t, second); !strings.Contains(raw, "idempotency_conflict") {
		t.Fatalf("divergent idempotency reuse error = %s", raw)
	}
}

func TestUpdatePageRequiresExpectedRevisionForWrite(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "needs-revision",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "needs-revision",
		"title": "Changed",
	})
	if !res.IsError {
		t.Fatal("update_page without expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "expected_revision is required") {
		t.Fatalf("update_page missing expected_revision error = %s", raw)
	}
	m := decodeWriteErrorEnvelope(t, res)
	wantRemaining := float64(config.Default().RateLimit.CreateUpdatePerMin - 2) // create_page + failed update_page each consume one token
	if got := m["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("update_page missing expected_revision rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("update_page missing expected_revision data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
}

func TestCreatePageEarlyValidationErrorReportsRealQuota(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatal("create_page with empty slug should fail")
	}
	m := decodeWriteErrorEnvelope(t, res)
	wantRemaining := float64(config.Default().RateLimit.CreateUpdatePerMin)
	if got := m["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("create_page empty slug rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("create_page empty slug data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	bucket, ok := data["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("create_page empty slug data.rate_limit = %#v, want object", data["rate_limit"])
	}
	if got := bucket["remaining"]; got != wantRemaining {
		t.Fatalf("create_page empty slug data.rate_limit.remaining = %v, want %v", got, wantRemaining)
	}
	if got := bucket["scope"]; got != "create_update_upload" {
		t.Fatalf("create_page empty slug data.rate_limit.scope = %v, want create_update_upload", got)
	}
}

func TestUpdatePageRejectsStaleExpectedRevision(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "stale-update",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "stale-update",
		"title":             "Changed",
		"expected_revision": "sha256:stale",
	})
	if !res.IsError {
		t.Fatal("update_page with stale expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("update_page stale revision error = %s", raw)
	}
	m := decodeWriteErrorEnvelope(t, res)
	wantRemaining := float64(config.Default().RateLimit.CreateUpdatePerMin - 2) // create_page + failed stale update_page
	if got := m["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("update_page stale revision rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("update_page stale revision data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	bucket, ok := data["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("update_page stale revision data.rate_limit = %#v, want object", data["rate_limit"])
	}
	if got := bucket["remaining"]; got != wantRemaining {
		t.Fatalf("update_page stale revision data.rate_limit.remaining = %v, want %v", got, wantRemaining)
	}
	if got := bucket["scope"]; got != "create_update_upload" {
		t.Fatalf("update_page stale revision data.rate_limit.scope = %v, want create_update_upload", got)
	}
}

func TestDeletePageRequiresExpectedRevisionForWrite(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "needs-delete-revision",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	res := callTool(t, session, "delete_page", map[string]any{"slug": "needs-delete-revision"})
	if !res.IsError {
		t.Fatal("delete_page without expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "expected_revision is required") {
		t.Fatalf("delete_page missing expected_revision error = %s", raw)
	}
	m := decodeWriteErrorEnvelope(t, res)
	wantRemaining := float64(config.Default().RateLimit.DestructivePerMin) // limiter inspected but not consumed before this validation error
	if got := m["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("delete_page missing expected_revision rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("delete_page missing expected_revision data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	bucket, ok := data["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("delete_page missing expected_revision data.rate_limit = %#v, want object", data["rate_limit"])
	}
	if got := bucket["remaining"]; got != wantRemaining {
		t.Fatalf("delete_page missing expected_revision data.rate_limit.remaining = %v, want %v", got, wantRemaining)
	}
	if got := bucket["scope"]; got != "destructive" {
		t.Fatalf("delete_page missing expected_revision data.rate_limit.scope = %v, want destructive", got)
	}
}

func TestDeletePageWithoutSourceFileDoesNotRequireExpectedRevision(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// A bundle directory with no index*.md source file (e.g. assets-only,
	// or left behind by a partial/failed write). There is no revision to
	// protect, so delete_page must not demand expected_revision here —
	// otherwise such a directory could never be deleted again.
	dir := filepath.Join(contentRoot, "orphan-bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res := callTool(t, session, "delete_page", map[string]any{"slug": "orphan-bundle"})
	if res.IsError {
		t.Fatalf("delete_page on sourceless bundle should succeed: %s", marshalContent(t, res))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("orphan bundle directory should be removed, stat err = %v", err)
	}
}

func TestDeletePageRejectsStaleExpectedRevision(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "stale-delete",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "stale-delete",
		"expected_revision": "sha256:stale",
	})
	if !res.IsError {
		t.Fatal("delete_page with stale expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("delete_page stale revision error = %s", raw)
	}
}

func TestDeletePageDetectsRevisionChangeWhileWaitingForLock(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	const slug = "lock-race-delete"
	filePath := filepath.Join(contentRoot, slug, "index.md")

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       slug,
		"title":      "Race Target",
		"body":       "initial body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}
	expected := currentRevision(t, filePath)

	hugosite.ContentMu.Lock()
	defer hugosite.ContentMu.Unlock()

	resultCh := make(chan *mcp.CallToolResult, 1)
	started := make(chan struct{}, 1)
	go func() {
		started <- struct{}{}
		resultCh <- callTool(t, session, "delete_page", map[string]any{
			"slug":              slug,
			"expected_revision": expected,
		})
	}()

	waitForStartSignals(t, started, 1)
	if err := os.WriteFile(filePath, []byte("---\ntitle: Race Target\n---\nchanged while waiting"), 0o644); err != nil {
		t.Fatalf("WriteFile while lock held: %v", err)
	}

	hugosite.ContentMu.Unlock()
	res := <-resultCh
	hugosite.ContentMu.Lock()

	if !res.IsError {
		t.Fatal("delete_page should reject when revision changes while waiting for lock")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("delete_page waiting-lock revision error = %s", raw)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("page should remain on disk after revision conflict: %v", err)
	}
}

func TestUpdatePageExposesLifecycleState(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(filepath.Join(siteRoot, "my-post"), 0o755); err != nil {
		t.Fatalf("MkdirAll(siteRoot): %v", err)
	}
	cfg := config.Default()
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/my-post/", Title: "My Post", URL: "https://example.test/my-post/"})

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot, SiteIdx: siteIdx})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "my-post",
		"title":      "My Post",
		"body":       "Hello world.",
		"tags":       []any{"go"},
		"categories": []any{"tutorials"},
	})
	if createRes.IsError {
		raw, _ := json.Marshal(createRes.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "my-post",
		"title":             "New Title",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "my-post", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	assertWritePageState(t, dataEnvelope["state"], "present", "pending", "stale", "stale")
}

func TestCreatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestCreatePageEmptyTitle(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "valid-slug", "title": ""})
	if !res.IsError {
		t.Fatal("expected error for empty title")
	}
}

func TestUpdatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestDeletePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{"slug": ""})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

// TestCreatePageAlreadyExistsPreservesRequestContext is a regression test
// for #455: a failed create_page must still echo the caller's normalized
// slug/lang via request_context, and must omit (not empty-string) the
// resolved_lang/resolved_source_path fields that were never reached.
func TestCreatePageAlreadyExistsPreservesRequestContext(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	first := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dup", "title": "First", "body": "First body.", "lang": "fr",
		"tags": []any{"a"}, "categories": []any{"b"},
	})
	if first.IsError {
		raw, _ := json.Marshal(first.Content)
		t.Fatalf("initial create_page failed: %s", raw)
	}

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dup", "title": "Second", "body": "Second body.", "lang": "fr",
		"tags": []any{"a"}, "categories": []any{"b"},
	})
	if !res.IsError {
		t.Fatal("expected already_exists error on duplicate create_page")
	}
	m := decodeWriteContent(t, res)
	reqCtx, ok := m["request_context"].(map[string]any)
	if !ok {
		t.Fatalf("request_context type = %T, want populated object", m["request_context"])
	}
	if got := reqCtx["slug"]; got != "posts/dup" {
		t.Fatalf("request_context.slug = %v, want posts/dup", got)
	}
	if got := reqCtx["requested_lang"]; got != "fr" {
		t.Fatalf("request_context.requested_lang = %v, want fr", got)
	}
	if _, present := m["resolved_lang"]; present {
		t.Fatalf("resolved_lang = %v, want omitted on error", m["resolved_lang"])
	}
	if _, present := m["resolved_source_path"]; present {
		t.Fatalf("resolved_source_path = %v, want omitted on error", m["resolved_source_path"])
	}
	if _, present := m["slug"]; present {
		t.Fatalf("top-level slug = %v, want omitted on error (real value lives in request_context.slug)", m["slug"])
	}
}

// TestUpdatePageNotFoundPreservesRequestContext is #455's update_page case.
func TestUpdatePageNotFoundPreservesRequestContext(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{"slug": "posts/does-not-exist", "title": "T", "lang": "en"})
	if !res.IsError {
		t.Fatal("expected not_found error for update_page on a missing page")
	}
	m := decodeWriteContent(t, res)
	reqCtx, ok := m["request_context"].(map[string]any)
	if !ok {
		t.Fatalf("request_context type = %T, want populated object", m["request_context"])
	}
	if got := reqCtx["slug"]; got != "posts/does-not-exist" {
		t.Fatalf("request_context.slug = %v, want posts/does-not-exist", got)
	}
	if got := reqCtx["requested_lang"]; got != "en" {
		t.Fatalf("request_context.requested_lang = %v, want en", got)
	}
	if _, present := m["resolved_source_path"]; present {
		t.Fatalf("resolved_source_path = %v, want omitted on error", m["resolved_source_path"])
	}
	if _, present := m["slug"]; present {
		t.Fatalf("top-level slug = %v, want omitted on error (real value lives in request_context.slug)", m["slug"])
	}
}

// TestDeletePageNotFoundPreservesRequestContext is #455's delete_page case.
func TestDeletePageNotFoundPreservesRequestContext(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{"slug": "posts/does-not-exist"})
	if !res.IsError {
		t.Fatal("expected not_found error for delete_page on a missing page")
	}
	m := decodeWriteContent(t, res)
	reqCtx, ok := m["request_context"].(map[string]any)
	if !ok {
		t.Fatalf("request_context type = %T, want populated object", m["request_context"])
	}
	if got := reqCtx["slug"]; got != "posts/does-not-exist" {
		t.Fatalf("request_context.slug = %v, want posts/does-not-exist", got)
	}
	if got := m["rate_limit_remaining"]; got == nil {
		t.Fatal("rate_limit_remaining missing on delete_page not_found error")
	}
	data := decodeWriteErrorData(t, res)
	bucket, ok := data["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("delete_page not_found data.rate_limit = %#v, want object", data["rate_limit"])
	}
	if got := bucket["scope"]; got != "destructive" {
		t.Fatalf("delete_page not_found data.rate_limit.scope = %v, want %q", got, "destructive")
	}
	if got := data["rate_limit_remaining"]; got == nil {
		t.Fatal("delete_page not_found data.rate_limit_remaining missing")
	}
	if _, present := m["resolved_source_path"]; present {
		t.Fatalf("resolved_source_path = %v, want omitted on error", m["resolved_source_path"])
	}
	if _, present := m["slug"]; present {
		t.Fatalf("top-level slug = %v, want omitted on error (real value lives in request_context.slug)", m["slug"])
	}
}

func TestDeletePageAssetEarlyValidationErrorReportsRealQuota(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":     "posts/example",
		"filename": "image.png",
		"scope":    "bogus",
	})
	if !res.IsError {
		t.Fatal("delete_page_asset with invalid scope should fail")
	}
	m := decodeWriteErrorEnvelope(t, res)
	wantRemaining := float64(config.Default().RateLimit.DestructivePerMin)
	if got := m["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("delete_page_asset invalid scope rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("delete_page_asset invalid scope data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
	bucket, ok := data["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("delete_page_asset invalid scope data.rate_limit = %#v, want object", data["rate_limit"])
	}
	if got := bucket["remaining"]; got != wantRemaining {
		t.Fatalf("delete_page_asset invalid scope data.rate_limit.remaining = %v, want %v", got, wantRemaining)
	}
	if got := bucket["scope"]; got != "destructive" {
		t.Fatalf("delete_page_asset invalid scope data.rate_limit.scope = %v, want destructive", got)
	}
}

// TestCreateUpdateDeleteChainUsesNewRevisionWithoutIntermediateRead is a
// regression test for #464: create_page/update_page must return the
// resulting page's revision directly, so a following update_page/delete_page
// can use it as expected_revision without an intermediate read call
// (get_page, build_agent_context, etc.) just to discover it.
func TestCreateUpdateDeleteChainUsesNewRevisionWithoutIntermediateRead(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	created := callTool(t, session, "create_page", map[string]any{
		"slug":       "chain-me",
		"title":      "Original",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if created.IsError {
		raw, _ := json.Marshal(created.Content)
		t.Fatalf("create_page failed: %s", raw)
	}
	createdData := decodeWriteData(t, created)
	createRevision, _ := createdData["new_revision"].(string)
	if createRevision == "" {
		t.Fatalf("create_page data.new_revision missing: %#v", createdData)
	}
	wantAfterCreate := currentRevision(t, filepath.Join(contentRoot, "chain-me", "index.md"))
	if createRevision != wantAfterCreate {
		t.Fatalf("create_page new_revision = %q, want %q (matching the file actually on disk)", createRevision, wantAfterCreate)
	}

	updated := callTool(t, session, "update_page", map[string]any{
		"slug":              "chain-me",
		"title":             "Updated",
		"expected_revision": createRevision, // no intermediate read
	})
	if updated.IsError {
		raw, _ := json.Marshal(updated.Content)
		t.Fatalf("update_page failed using create_page's new_revision: %s", raw)
	}
	updatedData := decodeWriteData(t, updated)
	updateRevision, _ := updatedData["new_revision"].(string)
	if updateRevision == "" {
		t.Fatalf("update_page data.new_revision missing: %#v", updatedData)
	}
	if updateRevision == createRevision {
		t.Fatal("update_page new_revision must differ from create_page's — content changed")
	}
	wantAfterUpdate := currentRevision(t, filepath.Join(contentRoot, "chain-me", "index.md"))
	if updateRevision != wantAfterUpdate {
		t.Fatalf("update_page new_revision = %q, want %q (matching the file actually on disk)", updateRevision, wantAfterUpdate)
	}

	deleted := callTool(t, session, "delete_page", map[string]any{
		"slug":              "chain-me",
		"expected_revision": updateRevision, // no intermediate read
	})
	if deleted.IsError {
		raw, _ := json.Marshal(deleted.Content)
		t.Fatalf("delete_page failed using update_page's new_revision: %s", raw)
	}
}

func TestUpdatePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// create first
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-me",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// update title only
	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "update-me",
		"title":             "New Title",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "update-me", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	data, err := os.ReadFile(filepath.Join(contentRoot, "update-me", "index.md"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if !strings.Contains(string(data), "New Title") {
		t.Errorf("updated file missing new title: %s", data)
	}
	decoded := decodeWriteContent(t, res)
	dataEnvelope := decodeWriteData(t, res)
	assertRootOnlyField(t, decoded, dataEnvelope, "rate_limit_remaining")
	if got := dataEnvelope["source_key"]; got != "update-me" {
		t.Fatalf("update_page data.source_key = %v, want update-me", got)
	}
	if got := dataEnvelope["resolved_source_path"]; got != "content/update-me/index.md" {
		t.Fatalf("update_page data.resolved_source_path = %v, want content/update-me/index.md", got)
	}
}

// TestUpdatePageSetsFeaturedImageFrontmatterKey (#809) proves update_page's
// new featured_image parameter writes the theme's expected `featuredImage`
// frontmatter key verbatim — the only way, before this, to attach a
// generate_hero_image-produced image to a page so it gets the theme's
// list-card treatment.
func TestUpdatePageSetsFeaturedImageFrontmatterKey(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "hero-post",
		"title":      "Hero Post",
		"body":       "Body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "hero-post",
		"featured_image":    "/images/hero-post-featured.jpg",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "hero-post", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	data, err := os.ReadFile(filepath.Join(contentRoot, "hero-post", "index.md"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if !strings.Contains(string(data), `featuredImage: /images/hero-post-featured.jpg`) &&
		!strings.Contains(string(data), `featuredImage: "/images/hero-post-featured.jpg"`) {
		t.Errorf("updated file missing featuredImage key: %s", data)
	}
}

// TestUpdatePageRefreshesInMemoryFrontmatterRaw (#810) proves the in-memory
// SourceIndex entry's FrontmatterRaw reflects a field update_page just set
// (description, here) without requiring a full server reindex. Before the
// fix, update_page's post-write index.Upsert only ever patched "title" into
// FrontmatterRaw by hand — every other field (description, featured_image,
// draft) was correctly written to the file on disk but the in-memory copy
// kept serving its old value, which is what made check_ai_readiness /
// get_page_for_edit's readiness block report description_present:false
// immediately after a successful update_page that set description.
func TestUpdatePageRefreshesInMemoryFrontmatterRaw(t *testing.T) {
	contentRoot := t.TempDir()
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "needs-description",
		"title":      "Needs Description",
		"body":       "Body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	if existing, ok := idx.GetBySlug("needs-description"); !ok || existing.FrontmatterRaw["description"] != nil {
		t.Fatalf("expected no description in FrontmatterRaw right after create_page, got %#v", existing)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "needs-description",
		"description":       "A real description.",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "needs-description", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	updated, ok := idx.GetBySlug("needs-description")
	if !ok {
		t.Fatalf("page not found in in-memory index after update_page")
	}
	got, _ := updated.FrontmatterRaw["description"].(string)
	if got != "A real description." {
		t.Fatalf("in-memory FrontmatterRaw[\"description\"] = %q after update_page, want \"A real description.\" (without a full reindex, this is exactly the stale index.Upsert bug from #810)", got)
	}
}

// TestUpdatePageWarnsWhenTitleChangesWithStaleFeaturedImage is a regression
// test for the #812 follow-up: generate_hero_image bakes the title text
// directly into the image file, so a later update_page call that changes
// title without also refreshing featured_image leaves a hero image whose
// baked-in text no longer matches the page. update_page can't safely
// auto-regenerate the image itself (no image-generation dependency in this
// package, and not every hero image bakes the title verbatim), so it must
// at least warn instead of silently leaving the two out of sync.
func TestUpdatePageWarnsWhenTitleChangesWithStaleFeaturedImage(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "hero-drift",
		"title":      "Original Title",
		"body":       "Body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "hero-drift",
		"featured_image":    "/images/hero-drift-featured.jpg",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "hero-drift", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page (set featured_image) failed: %s", raw)
	}

	// Change the title without touching featured_image — the image's baked-in
	// text now potentially drifts from the page title.
	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "hero-drift",
		"title":             "A Completely Different Title",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "hero-drift", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page (title change) failed: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	warning, _ := dataEnvelope["warning"].(string)
	if !strings.Contains(warning, "featuredImage") || !strings.Contains(warning, "generate_hero_image") {
		t.Fatalf("update_page data.warning = %q, want it to flag the now-possibly-stale featuredImage and point at generate_hero_image", warning)
	}
}

// TestUpdatePageNoStaleFeaturedImageWarningWhenSetTogether proves the #812
// follow-up warning doesn't fire when a caller sets title and featured_image
// in the same call — there's nothing stale to flag since both changed together.
func TestUpdatePageNoStaleFeaturedImageWarningWhenSetTogether(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "hero-together",
		"title":      "Original Title",
		"body":       "Body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "hero-together",
		"title":             "New Title",
		"featured_image":    "/images/hero-together-featured.jpg",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "hero-together", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	warning, _ := dataEnvelope["warning"].(string)
	if strings.Contains(warning, "featuredImage") {
		t.Fatalf("update_page data.warning = %q, should not flag featuredImage when both title and featured_image were set together", warning)
	}
}

func TestUpdatePagePreservesComplexFrontmatter(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "complex-frontmatter")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(pageDir): %v", err)
	}
	pagePath := filepath.Join(pageDir, "index.fr.md")
	original := strings.TrimLeft(`
---
# editor-facing title comment
title: "Complex Example"
date: 2026-07-19T12:00:00Z
draft: false
aliases:
  - /old-complex/
seo:
  canonical: https://example.test/posts/complex-frontmatter/
  robots: index,follow
images:
  - src: /images/cover.png
    alt: Cover image
translations:
  en:
    title: Example
    summary: Summary
custom:
  nested:
    enabled: true
    weight: 7
    labels:
      - one
      - two
tags:
  - legacy
categories:
  - Infrastructure
description: "Initial description"
---

Original body.
`, "\n")
	if err := os.WriteFile(pagePath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile(pagePath): %v", err)
	}
	beforeFM, err := hugosite.ParseFrontmatterFile(pagePath)
	if err != nil {
		t.Fatalf("ParseFrontmatterFile(before): %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	expectedRevision := currentRevision(t, pagePath)
	dryRun := callTool(t, session, "update_page", map[string]any{
		"slug":    "posts/complex-frontmatter",
		"lang":    "fr",
		"title":   "Complex Example Updated",
		"body":    "Updated body.",
		"tags":    []any{"go", "hugo"},
		"dry_run": true,
	})
	if dryRun.IsError {
		raw, _ := json.Marshal(dryRun.Content)
		t.Fatalf("update_page dry_run failed: %s", raw)
	}
	dryRunData := decodeWriteData(t, dryRun)
	dryRunPayload, _ := dryRunData["diff"].(string)
	for _, needle := range []string{
		`+title: "Complex Example Updated"`,
		"+  - go",
		"+Updated body.",
	} {
		if !strings.Contains(dryRunPayload, needle) {
			t.Fatalf("dry_run diff missing %q:\n%s", needle, dryRunPayload)
		}
	}
	for _, untouched := range []string{"canonical:", "translations:", "custom:", "/old-complex/"} {
		if strings.Contains(dryRunPayload, untouched) {
			t.Fatalf("dry_run diff should not rewrite untouched complex section %q:\n%s", untouched, dryRunPayload)
		}
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/complex-frontmatter",
		"lang":              "fr",
		"title":             "Complex Example Updated",
		"body":              "Updated body.",
		"tags":              []any{"go", "hugo"},
		"expected_revision": expectedRevision,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	raw, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("ReadFile(pagePath): %v", err)
	}
	got := string(raw)
	for _, needle := range []string{
		`title: "Complex Example Updated"`,
		"Updated body.",
		"  - go",
		"  - hugo",
		"canonical: https://example.test/posts/complex-frontmatter/",
		"summary: Summary",
		"weight: 7",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("updated page missing %q:\n%s", needle, got)
		}
	}

	afterFM, err := hugosite.ParseFrontmatterFile(pagePath)
	if err != nil {
		t.Fatalf("ParseFrontmatterFile(after): %v", err)
	}
	delete(beforeFM, "title")
	delete(beforeFM, "tags")
	delete(afterFM, "title")
	delete(afterFM, "tags")
	if !reflect.DeepEqual(beforeFM, afterFM) {
		t.Fatalf("untouched frontmatter changed\nbefore: %#v\nafter:  %#v", beforeFM, afterFM)
	}

	for _, pair := range [][2]string{
		{"title:", "date:"},
		{"aliases:", "seo:"},
		{"seo:", "images:"},
		{"images:", "translations:"},
		{"translations:", "custom:"},
		{"custom:", "tags:"},
		{"tags:", "categories:"},
		{"categories:", "description:"},
	} {
		left, right := strings.Index(got, pair[0]), strings.Index(got, pair[1])
		if left < 0 || right < 0 {
			t.Fatalf("missing ordering marker %q or %q in:\n%s", pair[0], pair[1], got)
		}
		if left > right {
			t.Fatalf("frontmatter order drifted: %q now appears after %q in:\n%s", pair[0], pair[1], got)
		}
	}
}

func TestDeletePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "to-delete",
		"title":      "Delete Me",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "to-delete",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "to-delete", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	// The entire directory must be removed, not just index.md.
	if _, err := os.Stat(filepath.Join(contentRoot, "to-delete")); !os.IsNotExist(err) {
		t.Error("expected page directory to be fully removed")
	}
	decoded := decodeWriteContent(t, res)
	dataEnvelope := decodeWriteData(t, res)
	assertRootOnlyField(t, decoded, dataEnvelope, "rate_limit_remaining")
	if got := dataEnvelope["source_key"]; got != "to-delete" {
		t.Fatalf("delete_page data.source_key = %v, want to-delete", got)
	}
	if got := dataEnvelope["resolved_source_path"]; got != "content/to-delete/index.md" {
		t.Fatalf("delete_page data.resolved_source_path = %v, want content/to-delete/index.md", got)
	}
}

func TestDeletePageRemovesWholeDirectory(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "extra-files",
		"title":      "Extra Files",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Add an extra file inside the page directory (e.g. an uploaded asset).
	extra := filepath.Join(contentRoot, "extra-files", "image.png")
	if err := os.WriteFile(extra, []byte("fake png"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "extra-files",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "extra-files", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "extra-files")); !os.IsNotExist(err) {
		t.Error("expected directory with extra files to be fully removed")
	}
}

// TestUpdatePageMultilingualFile ensures update_page works when the page only
// has index.fr.md (no index.md) — the real-world case for bilingual sites.
func TestUpdatePageMultilingualFile(t *testing.T) {
	contentRoot := t.TempDir()

	// Write an index.fr.md directly (no index.md counterpart).
	pageDir := filepath.Join(contentRoot, "posts", "csp-nonce")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	original := "---\ntitle: Titre original\ndate: \"2026-04-15T00:00:00Z\"\n---\nContenu original."
	if err := os.WriteFile(frFile, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()
	expected := currentRevision(t, frFile)

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/csp-nonce",
		"title":             "Nouveau titre",
		"expected_revision": expected,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed on multilingual page: %s", raw)
	}

	// The fr file must be updated; no index.md should have been created.
	data, err := os.ReadFile(frFile)
	if err != nil {
		t.Fatalf("index.fr.md not found: %v", err)
	}
	if !strings.Contains(string(data), "Nouveau titre") {
		t.Errorf("index.fr.md not updated, got: %s", data)
	}
	if _, err := os.Stat(filepath.Join(pageDir, "index.md")); !os.IsNotExist(err) {
		t.Error("update_page must not create index.md when only index.fr.md exists")
	}
	dataEnvelope := decodeWriteData(t, res)
	if got := dataEnvelope["resolved_source_path"]; got != "content/posts/csp-nonce/index.fr.md" {
		t.Fatalf("update_page multilingual data.resolved_source_path = %v, want content/posts/csp-nonce/index.fr.md", got)
	}
	if got := dataEnvelope["resolved_lang"]; got != "fr" {
		t.Fatalf("update_page multilingual data.resolved_lang = %v, want fr", got)
	}
}

func TestUpdatePageAmbiguousLanguageStructuredError(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "posts/bilingual",
		"title": "Changed",
	})
	if !res.IsError {
		t.Fatal("update_page on multilingual page without lang should return error result")
	}
	m := decodeWriteErrorEnvelope(t, res)
	errors, ok := m["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("update_page errors = %#v", m["errors"])
	}
	err0 := errors[0].(map[string]any)
	if got := err0["code"]; got != "ambiguous_language" {
		t.Fatalf("update_page error code = %v, want ambiguous_language", got)
	}
	if got := err0["field"]; got != "lang" {
		t.Fatalf("update_page error field = %v, want lang", got)
	}
	resolution, ok := err0["resolution"].(map[string]any)
	if !ok {
		t.Fatalf("update_page resolution = %T", err0["resolution"])
	}
	allowed, ok := resolution["allowed_values"].([]any)
	if !ok || len(allowed) != 2 {
		t.Fatalf("update_page allowed_values = %#v", resolution["allowed_values"])
	}
}

func TestCreatePageAcceptsExplicitLang(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/bilingual",
		"lang":       "fr",
		"title":      "Bonjour",
		"body":       "Contenu",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page with explicit lang failed: %s", raw)
	}

	frPath := filepath.Join(contentRoot, "posts", "bilingual", "index.fr.md")
	if _, err := os.Stat(frPath); err != nil {
		t.Fatalf("expected explicit lang file at %s: %v", frPath, err)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "bilingual", "index.md")); !os.IsNotExist(err) {
		t.Fatal("create_page with explicit lang must not create default index.md")
	}
	dataEnvelope := decodeWriteData(t, res)
	if got := dataEnvelope["resolved_source_path"]; got != "content/posts/bilingual/index.fr.md" {
		t.Fatalf("create_page data.resolved_source_path = %v, want content/posts/bilingual/index.fr.md", got)
	}
	if got := dataEnvelope["resolved_lang"]; got != "fr" {
		t.Fatalf("create_page data.resolved_lang = %v, want fr", got)
	}
}

func TestCreatePageRejectsDuplicateExplicitLang(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	first := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/bilingual-duplicate",
		"lang":       "fr",
		"title":      "Bonjour",
		"body":       "Version initiale",
		"tags":       []any{},
		"categories": []any{},
	})
	if first.IsError {
		raw, _ := json.Marshal(first.Content)
		t.Fatalf("initial multilingual create_page failed: %s", raw)
	}

	second := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/bilingual-duplicate",
		"lang":       "fr",
		"title":      "Remplacement",
		"body":       "Ce contenu ne doit pas écraser le fichier français existant.",
		"tags":       []any{},
		"categories": []any{},
	})
	if !second.IsError {
		raw, _ := json.Marshal(second.Content)
		t.Fatalf("duplicate multilingual create_page should fail: %s", raw)
	}
	raw, _ := json.Marshal(second.Content)
	if !strings.Contains(string(raw), "already_exists") {
		t.Fatalf("duplicate multilingual create_page must return already_exists, got: %s", raw)
	}

	frPath := filepath.Join(contentRoot, "posts", "bilingual-duplicate", "index.fr.md")
	data, err := os.ReadFile(frPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", frPath, err)
	}
	content := string(data)
	if !strings.Contains(content, "Bonjour") || strings.Contains(content, "Remplacement") {
		t.Fatalf("duplicate multilingual create_page must preserve original fr file, got:\n%s", content)
	}
}

func TestCreatePageRejectsInvalidLang(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, lang := range []string{"../escape", "zh-Hant"} {
		res := callTool(t, session, "create_page", map[string]any{
			"slug":       "posts/bad-lang",
			"lang":       lang,
			"title":      "Bad",
			"body":       "body",
			"tags":       []any{},
			"categories": []any{},
		})
		if !res.IsError {
			t.Fatalf("create_page with invalid lang %q should fail", lang)
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "invalid_params") {
			t.Fatalf("create_page invalid lang %q must return invalid_params, got: %s", lang, raw)
		}
	}
}

// TestDeletePageMultilingualBundleWithoutLangIsAmbiguous is the core
// regression test for #682: previously, omitting lang on a bilingual bundle
// silently resolved to one language file (via the alphabetically-first-pick
// inspectDeleteSource helper) and then deleted the ENTIRE bundle directory —
// a real data-loss risk, since an agent intending to delete one translation
// could delete all of them. Deletion must now be rejected outright, matching
// update_page's existing ambiguous_language contract, and must not touch
// disk at all.
func TestDeletePageMultilingualBundleWithoutLangIsAmbiguous(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual-delete")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/bilingual-delete",
		"expected_revision": currentRevision(t, filepath.Join(pageDir, "index.en.md")),
	})
	if !res.IsError {
		t.Fatal("delete_page without lang on a bilingual bundle must fail with ambiguous_language, not silently pick one and delete both")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "ambiguous_language") {
		t.Fatalf("delete_page error = %s, want ambiguous_language", raw)
	}
	for _, lang := range []string{"fr", "en"} {
		if _, err := os.Stat(filepath.Join(pageDir, "index."+lang+".md")); err != nil {
			t.Errorf("index.%s.md must survive a rejected ambiguous delete: %v", lang, err)
		}
	}
}

// TestDeletePageOneLanguageSurvivesTheOther proves the actual fix for #682:
// deleting one language of a bilingual bundle with an explicit lang must
// remove only that language's source file — the other translation must
// still exist on disk AND still be resolvable via the in-memory source
// index (the blind spot a filesystem-only check would miss, since the old
// whole-slug idx.Delete would have dropped the survivor from the index even
// if the file itself were left on disk).
func TestDeletePageOneLanguageSurvivesTheOther(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual-delete")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/bilingual-delete",
		"lang":              "en",
		"expected_revision": currentRevision(t, filepath.Join(pageDir, "index.en.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page(lang=en) failed: %s", raw)
	}
	data := decodeWriteData(t, res)
	if got := data["status"]; got != "ok" {
		t.Fatalf("status = %v, want ok for a successful scoped multilingual delete", got)
	}
	if got, _ := data["bundle_fully_removed"].(bool); got {
		t.Error("bundle_fully_removed = true, want false — the fr translation still exists")
	}
	if _, present := data["bundle_fully_removed"]; !present {
		t.Fatal("bundle_fully_removed missing, want explicit false on partial multilingual delete")
	}

	if _, err := os.Stat(filepath.Join(pageDir, "index.en.md")); !os.IsNotExist(err) {
		t.Fatal("index.en.md must be removed")
	}
	if _, err := os.Stat(filepath.Join(pageDir, "index.fr.md")); err != nil {
		t.Fatalf("index.fr.md must survive: %v", err)
	}

	// The survivor must still be resolvable via the in-memory index, not
	// just present on disk — this is the check that catches a whole-slug
	// idx.Delete silently dropping it from the index even though the file
	// itself was left alone.
	if _, ok := idx.GetBySlugLang("posts/bilingual-delete", "fr"); !ok {
		t.Fatal("the fr translation must still be resolvable via the in-memory SourceIndex after deleting only en")
	}
	if _, ok := idx.GetBySlugLang("posts/bilingual-delete", "en"); ok {
		t.Fatal("the en translation must no longer be resolvable via the in-memory SourceIndex")
	}

	// Deleting the last remaining language must remove the whole bundle.
	res2 := callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/bilingual-delete",
		"lang":              "fr",
		"expected_revision": currentRevision(t, filepath.Join(pageDir, "index.fr.md")),
	})
	if res2.IsError {
		raw, _ := json.Marshal(res2.Content)
		t.Fatalf("delete_page(lang=fr, last remaining) failed: %s", raw)
	}
	data2 := decodeWriteData(t, res2)
	if got, _ := data2["bundle_fully_removed"].(bool); !got {
		t.Error("bundle_fully_removed = false, want true — fr was the last remaining language")
	}
	if _, err := os.Stat(pageDir); !os.IsNotExist(err) {
		t.Fatal("delete_page must remove the bundle directory once the last language is gone")
	}
}

// TestDeletePageRejectsPathTraversalLang is a regression test for a Strix
// finding on the first version of #682's fix: delete_page passed in.Lang
// directly into contentmodel.ResolvePageSource, which builds candidate
// paths with filepath.Join("index."+lang+".md") — an unvalidated lang like
// "../../victim" let a caller resolve (and then delete) a file entirely
// outside the requested slug's own bundle, bypassing the slug's own
// PathGuard check. lang must now be rejected by the same validateLangParam
// create_page/update_page already use before it ever reaches path
// resolution, and the victim page must survive untouched.
func TestDeletePageRejectsPathTraversalLang(t *testing.T) {
	contentRoot := t.TempDir()

	victimDir := filepath.Join(contentRoot, "posts", "victim")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatalf("MkdirAll victim: %v", err)
	}
	victimPath := filepath.Join(victimDir, "index.md")
	if err := os.WriteFile(victimPath, []byte("---\ntitle: Victim\n---\nDo not delete me."), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	attackerDir := filepath.Join(contentRoot, "posts", "attacker")
	if err := os.MkdirAll(attackerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll attacker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attackerDir, "index.md"), []byte("---\ntitle: Attacker\n---\nBody."), 0o644); err != nil {
		t.Fatalf("WriteFile attacker: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/attacker",
		"lang":              "../../victim",
		"expected_revision": currentRevision(t, victimPath),
	})
	if !res.IsError {
		t.Fatal("delete_page with a path-traversal lang must fail with invalid_params, not resolve outside the requested slug")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("delete_page error = %s, want invalid_params", raw)
	}

	if _, err := os.Stat(victimPath); err != nil {
		t.Fatalf("victim page must survive a rejected path-traversal lang: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attackerDir, "index.md")); err != nil {
		t.Fatalf("attacker's own page must be untouched by a rejected call: %v", err)
	}
}

// TestDeletePageInvalidLangRejectedNotWholeBundleFallback is a regression
// test for a second Strix finding on the first version of #682's fix: when
// lang was explicitly given but didn't match any file on disk,
// source_file_not_found was downgraded to the same empty-resolvedSource
// case used for genuinely source-less (public-only) content — which skips
// the expected_revision requirement entirely and drives the whole-bundle
// deletion branch. An explicit, non-matching lang must now be rejected
// outright, leaving every language file on disk untouched, instead of
// silently wiping the whole bundle with no revision guard.
func TestDeletePageInvalidLangRejectedNotWholeBundleFallback(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual-delete")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Deliberately no expected_revision — the old bug's whole-bundle
	// fallback would have skipped the revision requirement entirely.
	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/bilingual-delete",
		"lang": "de",
	})
	if !res.IsError {
		t.Fatal("delete_page with a non-existent lang on a real bundle must fail, not fall back to whole-bundle deletion")
	}

	for _, lang := range []string{"fr", "en"} {
		if _, err := os.Stat(filepath.Join(pageDir, "index."+lang+".md")); err != nil {
			t.Errorf("index.%s.md must survive a rejected invalid-lang delete: %v", lang, err)
		}
	}
}

// TestDeletePageRejectsSymlinkSwapBeforeUnlink is a regression test for a
// Strix finding on the #682 fix: the new single-file delete branch called
// os.Remove(resolvedSource.SourcePath) without revalidating for a symlink
// swap between the earlier SafeJoin/resolve and the actual unlink, unlike
// every other write path (create_page, update_page, upload_page_asset,
// rollback_change) which all call pg.RevalidateForWrite immediately before
// touching disk. If the slug directory is replaced with a symlink pointing
// outside content_root after validation, delete_page must refuse to follow
// it rather than deleting a file under the symlink target.
func TestDeletePageRejectsSymlinkSwapBeforeUnlink(t *testing.T) {
	contentRoot := t.TempDir()

	pageDir := filepath.Join(contentRoot, "posts", "escape-delete")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.md"), []byte("---\ntitle: Real\n---\nBody."), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Swap the slug directory for a symlink pointing outside content_root,
	// with a bait index.md at the target so resolution and the revision
	// read still succeed — mimicking a race won before delete_page's unlink.
	target := t.TempDir()
	targetIndex := filepath.Join(target, "index.md")
	if err := os.WriteFile(targetIndex, []byte("---\ntitle: Bait\n---\nOutside content root."), 0o644); err != nil {
		t.Fatalf("WriteFile bait: %v", err)
	}
	if err := os.RemoveAll(pageDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.Symlink(target, pageDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/escape-delete",
		"expected_revision": currentRevision(t, targetIndex),
	})
	if !res.IsError {
		t.Fatal("delete_page must reject a slug directory swapped for a symlink, got success")
	}
	if _, err := os.Stat(targetIndex); err != nil {
		t.Fatalf("bait file outside content_root must survive a rejected symlink-swap delete: %v", err)
	}
}

// TestUpdatePageDryRunMultilingualPath verifies that the dry_run diff header
// names the resolved file (index.fr.md) not the hardcoded fallback (index.md).
// Regression for issue #257.
func TestUpdatePageDryRunMultilingualPath(t *testing.T) {
	contentRoot := t.TempDir()

	pageDir := filepath.Join(contentRoot, "posts", "csp-nonce")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"),
		[]byte("---\ntitle: Titre\ndate: \"2026-01-01T00:00:00Z\"\n---\nCorps."), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":    "posts/csp-nonce",
		"title":   "Nouveau titre",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run failed: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	body := string(raw)
	if !strings.Contains(body, "index.fr.md") {
		t.Errorf("dry_run diff header must reference index.fr.md, got: %s", body)
	}
	if strings.Contains(body, "posts/csp-nonce/index.md\"") {
		t.Errorf("dry_run diff header must not hardcode index.md for multilingual pages, got: %s", body)
	}
}

// TestDeletePageCleansPublicDir verifies that delete_page also removes the
// rendered output directory from public/ so no zombie page survives.
func TestDeletePageCleansPublicDir(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	// Create source page.
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/zombie-test",
		"title":      "Zombie Test",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Simulate a prior Hugo build by creating the public output directory.
	publicPageDir := filepath.Join(siteRoot, "posts", "zombie-test")
	if err := os.MkdirAll(publicPageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll public dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicPageDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("WriteFile public html: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/zombie-test",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "zombie-test", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "zombie-test")); !os.IsNotExist(err) {
		t.Error("source directory must be removed")
	}
	if _, err := os.Stat(publicPageDir); !os.IsNotExist(err) {
		t.Error("public directory must be removed by delete_page to prevent zombie")
	}
}

// TestDeletePageRemovesOrphanedHeroImage verifies that delete_page also
// removes the {slug}-featured.jpg hero image generate_hero_image would have
// written to {HugoRoot}/static/images/ (#606). That file lives outside the
// page's own content bundle directory, keyed only by slug, so plain
// os.RemoveAll(dir) never reaches it; without this cleanup it accumulates as
// an orphaned file on disk every time a page with a generated hero image is
// deleted. generate_hero_image itself isn't exercised here (it depends on an
// HTTP fetch / local image rendering pipeline out of scope for this test) —
// instead the hero image file is faked directly at the exact path
// generate_hero_image would have produced, which is sufficient to prove
// delete_page's cleanup logic finds and removes it.
func TestDeletePageRemovesOrphanedHeroImage(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{HugoRoot: hugoRoot})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/hero-orphan",
		"title":      "Hero Orphan",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Fake the hero image generate_hero_image would have written.
	imagesDir := filepath.Join(hugoRoot, "static", "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll images dir: %v", err)
	}
	heroPath := filepath.Join(imagesDir, "posts/hero-orphan-featured.jpg")
	if err := os.MkdirAll(filepath.Dir(heroPath), 0o755); err != nil {
		t.Fatalf("MkdirAll hero image parent dir: %v", err)
	}
	if err := os.WriteFile(heroPath, []byte("fake-jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile hero image: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/hero-orphan",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "hero-orphan", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "hero-orphan")); !os.IsNotExist(err) {
		t.Error("source directory must be removed")
	}
	if _, err := os.Stat(heroPath); !os.IsNotExist(err) {
		t.Error("hero image must be removed by delete_page to avoid an orphaned file (#606)")
	}
}

// TestDeletePageWithoutHeroImageSucceeds verifies delete_page still succeeds
// cleanly (no warning) when HugoRoot is configured but no hero image was
// ever generated for the deleted slug — the common case, since not every
// page gets a generate_hero_image call (#606).
func TestDeletePageWithoutHeroImageSucceeds(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{HugoRoot: hugoRoot})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/no-hero",
		"title":      "No Hero",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/no-hero",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "no-hero", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}
	data := decodeWriteData(t, res)
	if w, ok := data["warning"]; ok && w != "" {
		t.Errorf("expected no warning when no hero image exists, got %q", w)
	}
}

// TestCreatePageSlugNormalization verifies that create_page strips leading and
// trailing slashes from the slug so agents that pass /posts/foo/ and posts/foo
// both reach the same content directory (#265).
func TestCreatePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "/posts/normalized/", "title": "Normalized", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page with leading/trailing slashes failed: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "normalized", "index.md")); err != nil {
		t.Errorf("expected file at posts/normalized/index.md after slug normalization: %v", err)
	}
}

// TestUpdatePageSlugNormalization verifies that update_page accepts a slug with
// leading and trailing slashes and resolves to the same page (#265).
func TestUpdatePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/update-me", "title": "Update Me", "body": "original",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":              "/posts/update-me/",
		"title":             "Updated",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "update-me", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page with leading/trailing slashes failed: %s", raw)
	}
}

// TestDeletePageSlugNormalization verifies that delete_page accepts a slug with
// leading and trailing slashes and removes the correct directory (#265).
func TestDeletePageSlugNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/slash-test", "title": "Slash Test", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "/posts/slash-test/",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "slash-test", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page with leading/trailing slashes failed: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "slash-test")); !os.IsNotExist(err) {
		t.Error("expected page directory to be removed after slug-normalized delete")
	}
}

// TestDeletePageNotFoundOnDoubleDeletion verifies that a second delete on an
// already-deleted slug returns not_found instead of silent success (#266).
func TestDeletePageNotFoundOnDoubleDeletion(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/double-delete", "title": "Double Delete", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/double-delete",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "double-delete", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first delete_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{"slug": "posts/double-delete"})
	if !res.IsError {
		t.Fatal("second delete_page should return not_found, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found error on double deletion, got: %s", raw)
	}
}

// TestDeletePageDryRun verifies that delete_page with dry_run=true returns the
// page content and an empty backlinks list without removing the file (#267).
func TestDeletePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-me", "title": "Dry Run", "body": "preview body",
		"tags": []any{"go"}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dry-run-me", "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run failed: %s", raw)
	}

	m := decodeWriteData(t, res)
	if m["dry_run"] != true {
		t.Errorf("expected data.dry_run=true in response, got %v", m["dry_run"])
	}
	content, _ := m["content"].(string)
	if !strings.Contains(content, "Dry Run") {
		t.Errorf("dry_run data.content should contain page frontmatter, got: %q", content)
	}
	if _, ok := m["backlinks"]; !ok {
		t.Error("dry_run response must include data.backlinks key")
	}

	// #466 regression: delete_page's dry_run must report the caller's actual
	// remaining rate-limit budget, not a false 0 — dry_run doesn't consume
	// the budget, so on a fresh caller this must equal the configured burst
	// (5, config.Default()'s DestructivePerMin), not the zero value a
	// forgotten field assignment would produce.
	root := decodeWriteContent(t, res)
	remaining, ok := root["rate_limit_remaining"].(float64)
	if !ok || remaining != float64(config.Default().RateLimit.DestructivePerMin) {
		t.Errorf("dry_run rate_limit_remaining = %#v, want %d (fresh, unconsumed budget)", root["rate_limit_remaining"], config.Default().RateLimit.DestructivePerMin)
	}

	// File must not have been removed.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "dry-run-me", "index.md")); err != nil {
		t.Errorf("dry_run must not delete the file: %v", err)
	}
}

// TestDeletePageDryRunCompactOmitsFullSourceBody is a regression test for
// #687: a preview-oriented compact dry-run must not return the entire source
// body by default. It should still expose deletion scope and backlink count.
func TestDeletePageDryRunCompactOmitsFullSourceBody(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-compact", "title": "Dry Run Compact", "body": strings.Repeat("body ", 200),
		"tags": []any{"go"}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":          "posts/dry-run-compact",
		"dry_run":       true,
		"response_mode": "compact",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page compact dry_run failed: %s", raw)
	}

	data := decodeWriteData(t, res)
	if _, ok := data["content"]; ok {
		t.Fatalf("delete_page compact dry_run content = %#v, want omitted", data["content"])
	}
	if _, ok := data["backlinks"]; ok {
		t.Fatalf("delete_page compact dry_run backlinks = %#v, want omitted", data["backlinks"])
	}
	if got := data["backlinks_count"]; got != float64(0) {
		t.Fatalf("delete_page compact dry_run backlinks_count = %v, want 0", got)
	}
	if got := data["resolved_source_path"]; got != "content/posts/dry-run-compact/index.md" {
		t.Fatalf("delete_page compact dry_run resolved_source_path = %v, want content/posts/dry-run-compact/index.md", got)
	}
}

func TestDeletePageDryRunReportsGeneratedHeroImage(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{HugoRoot: hugoRoot})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-hero", "title": "Dry Run Hero", "body": "preview body",
		"tags": []any{"go"}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	heroPath := filepath.Join(hugoRoot, "static", "images", "posts", "dry-run-hero-featured.jpg")
	if err := os.MkdirAll(filepath.Dir(heroPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(heroPath, []byte("hero-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dry-run-hero", "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run failed: %s", raw)
	}

	data := decodeWriteData(t, res)
	generated, ok := data["generated_assets"].([]any)
	if !ok || len(generated) != 1 {
		t.Fatalf("delete_page dry_run generated_assets = %#v, want one generated asset", data["generated_assets"])
	}
	ga, ok := generated[0].(map[string]any)
	if !ok {
		t.Fatalf("generated_assets[0] type = %T, want map[string]any", generated[0])
	}
	if got := ga["path"]; got != "static/images/posts/dry-run-hero-featured.jpg" {
		t.Fatalf("generated_assets[0].path = %v, want static/images/posts/dry-run-hero-featured.jpg", got)
	}
	if got := ga["kind"]; got != "global_static" {
		t.Fatalf("generated_assets[0].kind = %v, want global_static", got)
	}
}

func TestDeletePageDryRunPredictsBundleRemovalScope(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual-dry-run")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\ndate: 2026-07-26\n---\nBonjour"), 0o644); err != nil {
		t.Fatalf("WriteFile fr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.en.md"), []byte("---\ntitle: EN\ndate: 2026-07-26\n---\nHello"), 0o644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug":    "posts/bilingual-dry-run",
		"lang":    "en",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run(lang=en) failed: %s", raw)
	}
	data := decodeWriteData(t, res)
	if _, present := data["bundle_fully_removed"]; present {
		t.Fatalf("dry_run must not emit bundle_fully_removed, got %#v", data["bundle_fully_removed"])
	}
	if got := data["bundle_will_be_fully_removed"]; got != false {
		t.Fatalf("bundle_will_be_fully_removed = %v, want false when another language survives", got)
	}

	if err := os.Remove(filepath.Join(pageDir, "index.en.md")); err != nil {
		t.Fatalf("remove en fixture: %v", err)
	}
	res = callTool(t, session, "delete_page", map[string]any{
		"slug":    "posts/bilingual-dry-run",
		"lang":    "fr",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run(lang=fr,last) failed: %s", raw)
	}
	data = decodeWriteData(t, res)
	if got := data["bundle_will_be_fully_removed"]; got != true {
		t.Fatalf("bundle_will_be_fully_removed = %v, want true when the last language would remove the bundle", got)
	}
}

// TestDeletePageRealDeleteOmitsBacklinksCount is a regression test for a
// contract-drift bug introduced alongside #687's dry-run backlinks_count
// field: it was declared as a plain int with no omitempty, so a real
// (non-dry-run) delete — which never runs a backlink scan at all — still
// unconditionally emitted "backlinks_count": 0 in its response, reading as
// "verified zero backlinks" when in fact no such check ever happened.
// backlinks_count must only appear when it was actually computed, i.e. on
// a dry_run response.
func TestDeletePageRealDeleteOmitsBacklinksCount(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/real-delete-no-backlinks-count", "title": "Real Delete", "body": "hello",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/real-delete-no-backlinks-count",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "real-delete-no-backlinks-count", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	data := decodeWriteData(t, res)
	if _, ok := data["backlinks_count"]; ok {
		t.Fatalf("real delete_page response has backlinks_count = %#v, want omitted (never computed for a real delete)", data["backlinks_count"])
	}
}

// TestDeletePageDryRunNotFound verifies that dry_run on a non-existent slug
// returns not_found (#267).
func TestDeletePageDryRunNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/does-not-exist", "dry_run": true,
	})
	if !res.IsError {
		t.Fatal("delete_page dry_run on non-existent slug should return not_found")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found, got: %s", raw)
	}
}

// TestDeletePageSlugNormalizationSourceIndex verifies that delete_page with a
// slash-wrapped slug correctly removes the source-index entry. Without the
// strings.Trim fix, idx.Delete("/posts/x/") would miss the key "posts/x" that
// create_page stored, leaving a stale index entry (#265).
func TestDeletePageSlugNormalizationSourceIndex(t *testing.T) {
	contentRoot := t.TempDir()
	session, srcIdx, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/norm-idx-test", "title": "Norm Idx", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	if _, ok := srcIdx.GetBySlug("posts/norm-idx-test"); !ok {
		t.Fatal("source index should contain page after create_page")
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "/posts/norm-idx-test/",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "norm-idx-test", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page with slashed slug failed: %s", raw)
	}

	if _, ok := srcIdx.GetBySlug("posts/norm-idx-test"); ok {
		t.Error("source index must not retain entry after delete_page with slashed slug")
	}
}

// TestDeletePageDryRunWithBacklinks verifies that dry_run returns actual
// backlinks when a site.Index is wired in (#267).
func TestDeletePageDryRunWithBacklinks(t *testing.T) {
	contentRoot := t.TempDir()

	// Build a minimal site.Index: target page + a page that links to it.
	// Both pages must be in the index: buildReverseMap only stores a backlink
	// if the target page is found via GetBySlug AND classified as content.
	cfg := config.Default()
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{
		Slug:    "/posts/dry-run-bl/",
		Title:   "BL Target",
		URL:     "https://example.test/posts/dry-run-bl/",
		RawHTML: `<article><p>no outgoing links</p></article>`,
	})
	siteIdx.UpsertPage(site.Page{
		Slug:    "/posts/linker/",
		Title:   "Linker Page",
		URL:     "https://example.test/posts/linker/",
		RawHTML: `<article><a href="/posts/dry-run-bl/">go to target</a></article>`,
	})

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteIdx: siteIdx})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/dry-run-bl", "title": "BL Target", "body": "body",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/dry-run-bl", "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page dry_run failed: %s", raw)
	}

	m := decodeWriteData(t, res)
	bls, ok := m["backlinks"].([]any)
	if !ok {
		t.Fatalf("dry_run response must include data.backlinks array, got %T: %v", m["backlinks"], m["backlinks"])
	}
	if len(bls) != 1 {
		t.Fatalf("expected 1 backlink, got %d: %v", len(bls), bls)
	}
	bl, _ := bls[0].(map[string]any)
	if bl["slug"] != "/posts/linker/" {
		t.Errorf("backlink slug = %q, want /posts/linker/", bl["slug"])
	}
}

// TestDeletePagePublicCleanupWarning verifies that when the public output
// directory cannot be removed (e.g. parent dir is read-only), delete_page
// still succeeds but surfaces a warning in the response (#239).
func TestDeletePagePublicCleanupWarning(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod tricks don't apply as root")
	}
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/read-only-zombie", "title": "RO Zombie",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// Create the public output dir then make its parent read-only so RemoveAll fails.
	publicPageDir := filepath.Join(siteRoot, "posts", "read-only-zombie")
	if err := os.MkdirAll(publicPageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	postsDir := filepath.Join(siteRoot, "posts")
	if err := os.Chmod(postsDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(postsDir, 0o755) })

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/read-only-zombie",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "read-only-zombie", "index.md")),
	})

	// Must restore before any further assertions so t.TempDir cleanup can proceed.
	_ = os.Chmod(postsDir, 0o755)

	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not hard-fail on public cleanup error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Errorf("expected a warning in response when public cleanup fails, got: %s", raw)
	}
}

// TestDeletePageDBWarning verifies that when the derived DB cannot be updated
// (e.g. the connection is closed), delete_page still removes the source file
// and surfaces a warning rather than failing hard (#242).
func TestDeletePageDBWarning(t *testing.T) {
	contentRoot := t.TempDir()

	siteDB, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	// Close the DB so any operation on it returns "sql: database is closed".
	siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/db-warning-test", "title": "DB Warning",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "posts/db-warning-test",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "posts", "db-warning-test", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not hard-fail on DB error: %s", raw)
	}

	// Source must be gone.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "db-warning-test")); !os.IsNotExist(err) {
		t.Error("source directory must be removed even when DB update fails")
	}

	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Errorf("expected a warning in response when DB delete fails, got: %s", raw)
	}
	if got := decodeWriteData(t, res)["status"]; got != "partial_success" {
		t.Errorf("expected partial_success status when DB delete fails, got: %v", got)
	}
}

func TestCreatePageDBWarning(t *testing.T) {
	contentRoot := t.TempDir()

	siteDB, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/create-db-warning", "title": "DB Warning",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page must not hard-fail on DB sync error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Fatalf("expected warning when create DB sync fails, got: %s", raw)
	}
	if got := decodeWriteData(t, res)["status"]; got != "partial_success" {
		t.Fatalf("expected partial_success status when create DB sync fails, got: %v", got)
	}
}

func TestUpdatePageDBWarning(t *testing.T) {
	contentRoot := t.TempDir()

	siteDB, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/update-db-warning", "title": "DB Warning",
		"body": "body", "tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}
	expected := currentRevision(t, filepath.Join(contentRoot, "posts", "update-db-warning", "index.md"))
	siteDB.Close()

	res = callTool(t, session, "update_page", map[string]any{
		"slug": "posts/update-db-warning", "title": "Updated", "expected_revision": expected,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page must not hard-fail on DB sync error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "warning") {
		t.Fatalf("expected warning when update DB sync fails, got: %s", raw)
	}
	if got := decodeWriteData(t, res)["status"]; got != "partial_success" {
		t.Fatalf("expected partial_success status when update DB sync fails, got: %v", got)
	}
}

func TestCreatePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "dry-post",
		"title":      "Dry Post",
		"body":       "Preview only.",
		"tags":       []any{},
		"categories": []any{},
		"dry_run":    true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page dry_run returned error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "dry_run") && !strings.Contains(string(raw), "Dry Post") {
		t.Fatalf("create_page dry_run missing content preview: %s", raw)
	}
	// File must NOT exist on disk
	if _, err := os.Stat(filepath.Join(contentRoot, "dry-post", "index.md")); !os.IsNotExist(err) {
		t.Error("create_page dry_run must not write file to disk")
	}
}

func TestUpdatePageDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Create a real page first
	if r := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-dry",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":    "update-dry",
		"title":   "New Title",
		"dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run returned error: %s", raw)
	}
	raw, _ := json.Marshal(res.Content)
	// Diff should show the title change
	if !strings.Contains(string(raw), "New Title") {
		t.Fatalf("update_page dry_run diff missing new title: %s", raw)
	}
	// On-disk file must still have the original title
	data, err := os.ReadFile(filepath.Join(contentRoot, "update-dry", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Original Title") {
		t.Errorf("update_page dry_run must not write to disk; file = %q", data)
	}
}

// TestCreatePageDryRunDoesNotConsumeQuota is a regression test for #588: an
// audit found create_page/update_page/upload_page_asset called
// limiter.Allow() before checking in.DryRun, unlike delete_page/
// delete_page_asset (which established the "dry_run must never consume the
// mutation budget" invariant at #466/#575). A small budget makes any
// unexpected decrement visible immediately.
func TestCreatePageDryRunDoesNotConsumeQuota(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 2
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	var remaining []float64
	for i := 0; i < 5; i++ {
		res := callTool(t, session, "create_page", map[string]any{
			"slug":       "dry-quota-post",
			"title":      "Dry Post",
			"body":       "Preview only.",
			"tags":       []any{},
			"categories": []any{},
			"dry_run":    true,
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("create_page dry_run %d returned error: %s", i, raw)
		}
		root := decodeWriteContent(t, res)
		rem, ok := root["rate_limit_remaining"].(float64)
		if !ok {
			t.Fatalf("create_page dry_run %d: rate_limit_remaining missing", i)
		}
		remaining = append(remaining, rem)
	}
	for i := 1; i < len(remaining); i++ {
		if remaining[i] != remaining[0] {
			t.Fatalf("create_page dry_run consumed quota: rate_limit_remaining sequence = %v, want constant at %v (dry_run must never call limiter.Allow())", remaining, remaining[0])
		}
	}
	if remaining[0] != float64(rl.CreateUpdatePerMin) {
		t.Fatalf("create_page dry_run rate_limit_remaining = %v, want full fresh budget %d", remaining[0], rl.CreateUpdatePerMin)
	}
}

// TestUpdatePageDryRunDoesNotConsumeQuota is #588's update_page counterpart
// to TestCreatePageDryRunDoesNotConsumeQuota.
func TestUpdatePageDryRunDoesNotConsumeQuota(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 3 // 1 slot consumed by the real create_page setup below, leaving budget headroom to observe
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	if r := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-dry-quota",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	var remaining []float64
	for i := 0; i < 5; i++ {
		res := callTool(t, session, "update_page", map[string]any{
			"slug":    "update-dry-quota",
			"title":   "New Title",
			"dry_run": true,
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("update_page dry_run %d returned error: %s", i, raw)
		}
		root := decodeWriteContent(t, res)
		rem, ok := root["rate_limit_remaining"].(float64)
		if !ok {
			t.Fatalf("update_page dry_run %d: rate_limit_remaining missing", i)
		}
		remaining = append(remaining, rem)
	}
	for i := 1; i < len(remaining); i++ {
		if remaining[i] != remaining[0] {
			t.Fatalf("update_page dry_run consumed quota: rate_limit_remaining sequence = %v, want constant at %v (dry_run must never call limiter.Allow())", remaining, remaining[0])
		}
	}
	// One real (non-dry-run) create_page call already consumed 1 of the
	// shared CreateUpdatePerMin budget above, so the fresh remainder here is
	// rl.CreateUpdatePerMin-1, not the full budget.
	if want := float64(rl.CreateUpdatePerMin - 1); remaining[0] != want {
		t.Fatalf("update_page dry_run rate_limit_remaining = %v, want %v (budget after the one real create_page call)", remaining[0], want)
	}
}

// TestCreatePageAtomicWriteCheckedRejectsSymlink verifies that create_page
// fails (and does not write outside content_root) when the target slug
// directory is a symlink pointing outside — protecting the T2/T3 write window
// addressed by AtomicWriteChecked (#233).
func TestCreatePageAtomicWriteCheckedRejectsSymlink(t *testing.T) {
	contentRoot := t.TempDir()
	target := t.TempDir()

	// Pre-create the slug dir as a symlink to a dir outside contentRoot.
	symlinkDir := filepath.Join(contentRoot, "escape-post")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "escape-post",
		"title": "Escape Post",
	})
	if !res.IsError {
		t.Fatal("expected error when slug dir is a symlink, got success")
	}
	// No file must be written to the symlink target.
	if _, err := os.Stat(filepath.Join(target, "index.md")); !os.IsNotExist(err) {
		t.Error("index.md was written to symlink target — content root escape not prevented")
	}
}

// TestUpdatePageAtomicWriteCheckedRejectsSymlink verifies that update_page
// fails and does not write outside content_root when the slug directory is
// a symlink (#233).
func TestUpdatePageAtomicWriteCheckedRejectsSymlink(t *testing.T) {
	contentRoot := t.TempDir()

	// Create a real page first so it appears in the source index.
	realDir := filepath.Join(contentRoot, "symlink-me")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := "---\ntitle: Original\ndate: \"2026-01-01T00:00:00Z\"\n---\nBody."
	if err := os.WriteFile(filepath.Join(realDir, "index.md"), []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Confirm update succeeds before swapping.
	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "symlink-me",
		"title":             "Updated",
		"expected_revision": currentRevision(t, filepath.Join(realDir, "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page setup failed: %s", raw)
	}

	// Replace the real dir with a symlink pointing outside contentRoot.
	target := t.TempDir()
	if err := os.RemoveAll(realDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.Symlink(target, realDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	res = callTool(t, session, "update_page", map[string]any{
		"slug":  "symlink-me",
		"title": "Should Not Write",
	})
	if !res.IsError {
		t.Fatal("expected error when slug dir swapped to symlink, got success")
	}
	// No file must be written to the symlink target.
	if _, err := os.Stat(filepath.Join(target, "index.md")); !os.IsNotExist(err) {
		t.Error("index.md was written to symlink target — content root escape not prevented")
	}
}

// TestDeletePageAuditLogErrorSurfacedAsWarning verifies that when the audit log
// cannot be written (e.g. it exists as a directory), delete_page still succeeds
// and surfaces the failure in the response Warning field rather than returning
// an error (#235).
func TestDeletePageAuditLogErrorSurfacedAsWarning(t *testing.T) {
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteRoot: siteRoot})
	defer done()

	// Create a page to delete.
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "audit-test-page",
		"title":      "Audit Test",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	// Simulate a public output directory to verify it is cleaned up too.
	publicDir := filepath.Join(siteRoot, "audit-test-page")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll public dir: %v", err)
	}

	// Create .mcp-audit.log as a directory to make it unusable as a file.
	auditLogPath := filepath.Join(contentRoot, ".mcp-audit.log")
	if err := os.MkdirAll(auditLogPath, 0o755); err != nil {
		t.Fatalf("MkdirAll audit log dir: %v", err)
	}

	res = callTool(t, session, "delete_page", map[string]any{
		"slug":              "audit-test-page",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "audit-test-page", "index.md")),
	})

	// Must NOT return an error — deletion is committed.
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page must not fail when audit log write fails: %s", raw)
	}

	// Source directory must be gone.
	if _, err := os.Stat(filepath.Join(contentRoot, "audit-test-page")); !os.IsNotExist(err) {
		t.Error("source directory must be removed")
	}

	// Public directory must be gone.
	if _, err := os.Stat(publicDir); !os.IsNotExist(err) {
		t.Error("public directory must be removed")
	}

	// Response must contain a warning mentioning audit_error.
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "audit_error") {
		t.Errorf("expected 'audit_error' in response warning, got: %s", raw)
	}
}

// TestUpdatePageTagsDeltaOnDryRun is the core regression test for #645:
// update_page's tags/categories are a whole-list replacement, but the
// response should report a per-term added/removed/unchanged breakdown —
// the same "which parts of my request would actually apply" visibility
// plan_content_change gives its add_tag/remove_tag vocabulary, narrowly
// scoped to tags/categories. Checked on dry_run specifically, since that's
// the diagnostic-quality gap the issue raised.
func TestUpdatePageTagsDeltaOnDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if r := callTool(t, session, "create_page", map[string]any{
		"slug": "delta-dry", "title": "T", "body": "B",
		"tags": []any{"go", "hugo"}, "categories": []any{"tech"},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug": "delta-dry", "tags": []any{"hugo", "mcp"}, "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run returned error: %s", raw)
	}
	data := decodeWriteData(t, res)
	tagsDelta, ok := data["tags_delta"].(map[string]any)
	if !ok {
		t.Fatalf("data.tags_delta missing or wrong type: %#v", data)
	}
	assertStringSet(t, "tags_delta.added", tagsDelta["added"], "mcp")
	assertStringSet(t, "tags_delta.removed", tagsDelta["removed"], "go")
	assertStringSet(t, "tags_delta.unchanged", tagsDelta["unchanged"], "hugo")

	// categories omitted from the request entirely: no categories_delta at all.
	if _, present := data["categories_delta"]; present {
		t.Errorf("data.categories_delta present when categories was omitted from the request: %#v", data["categories_delta"])
	}
}

// TestUpdatePageCategoriesDeltaPopulatedOnRealWrite confirms tags_delta/
// categories_delta are populated on a real (non-dry-run) write too, not
// only on dry_run.
func TestUpdatePageCategoriesDeltaPopulatedOnRealWrite(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if r := callTool(t, session, "create_page", map[string]any{
		"slug": "delta-real", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{"news", "tech"},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug": "delta-real", "categories": []any{"tech", "guides"},
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "delta-real", "index.md")),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page expected success, got error: %s", raw)
	}
	data := decodeWriteData(t, res)
	categoriesDelta, ok := data["categories_delta"].(map[string]any)
	if !ok {
		t.Fatalf("data.categories_delta missing or wrong type: %#v", data)
	}
	assertStringSet(t, "categories_delta.added", categoriesDelta["added"], "guides")
	assertStringSet(t, "categories_delta.removed", categoriesDelta["removed"], "news")
	assertStringSet(t, "categories_delta.unchanged", categoriesDelta["unchanged"], "tech")
}

// TestUpdatePageTagsDeltaEmptyListClearsAll confirms an explicit empty
// tags list (a valid "clear them all" request, distinct from omitting the
// key entirely) reports every existing tag as removed, none unchanged.
func TestUpdatePageTagsDeltaEmptyListClearsAll(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if r := callTool(t, session, "create_page", map[string]any{
		"slug": "delta-clear", "title": "T", "body": "B",
		"tags": []any{"go", "hugo"}, "categories": []any{},
	}); r.IsError {
		raw, _ := json.Marshal(r.Content)
		t.Fatalf("create_page setup failed: %s", raw)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug": "delta-clear", "tags": []any{}, "dry_run": true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page dry_run returned error: %s", raw)
	}
	data := decodeWriteData(t, res)
	tagsDelta, ok := data["tags_delta"].(map[string]any)
	if !ok {
		t.Fatalf("data.tags_delta missing or wrong type for an explicit empty tags list: %#v", data)
	}
	assertStringSet(t, "tags_delta.removed", tagsDelta["removed"], "go", "hugo")
	if v, present := tagsDelta["added"]; present {
		t.Errorf("tags_delta.added = %v, want omitted for an empty tags list", v)
	}
	if v, present := tagsDelta["unchanged"]; present {
		t.Errorf("tags_delta.unchanged = %v, want omitted when clearing all tags", v)
	}
}

func assertStringSet(t *testing.T, label string, v any, want ...string) {
	t.Helper()
	var got []string
	if v != nil {
		arr, ok := v.([]any)
		if !ok {
			t.Fatalf("%s = %#v (%T), want []string-like", label, v, v)
		}
		for _, item := range arr {
			s, _ := item.(string)
			got = append(got, s)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("%s = %v, missing expected member %q", label, got, w)
		}
	}
}
