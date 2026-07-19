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
	if o.RateLimit != nil {
		cfg.RateLimit = *o.RateLimit
	}

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

func assertWriteSuccessCompatAlias(t *testing.T, root, data map[string]any, field string) {
	t.Helper()
	if got, want := root[field], data[field]; got != want {
		t.Fatalf("%s root/data mismatch: root=%v data=%v", field, got, want)
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
	assertWriteSuccessCompatAlias(t, out, dataEnvelope, "slug")
	assertWriteSuccessCompatAlias(t, out, dataEnvelope, "source_key")
	assertWriteSuccessCompatAlias(t, out, dataEnvelope, "resolved_source_path")
	assertWriteSuccessCompatAlias(t, out, dataEnvelope, "rate_limit_remaining")
	assertWritePageState(t, out["state"], "present", "pending", "not_yet_available", "source_only")
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
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != "content/my-post/index.md" {
		t.Fatalf("create_page resolved_source_path = %v, want content/my-post/index.md", got)
	}
	if got := decoded["resolved_lang"]; got != "" {
		t.Fatalf("create_page resolved_lang = %v, want empty default lang", got)
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
	out := decodeWriteContent(t, res)
	warning, _ := out["warning"].(string)
	if !strings.Contains(warning, "permission_denied") {
		t.Fatalf("create_page warning = %q, want it to mention the last failed build_site attempt", warning)
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
	out := decodeWriteContent(t, res)
	if warning, _ := out["warning"].(string); warning != "" {
		t.Fatalf("create_page warning = %q, want empty when the last build_site attempt succeeded", warning)
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
	out := decodeWriteContent(t, res)
	assertWritePageState(t, out["state"], "deleted", "not_applicable", "removed", "removed")
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
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- callTool(t, session, "create_page", args)
		}()
	}

	time.Sleep(150 * time.Millisecond)
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
	go func() {
		resultCh <- callTool(t, session, "delete_page", map[string]any{
			"slug":              slug,
			"expected_revision": expected,
		})
	}()

	time.Sleep(150 * time.Millisecond)
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
	out := decodeWriteContent(t, res)
	assertWritePageState(t, out["state"], "present", "pending", "stale", "stale")
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
	if _, present := m["resolved_source_path"]; present {
		t.Fatalf("resolved_source_path = %v, want omitted on error", m["resolved_source_path"])
	}
	if _, present := m["slug"]; present {
		t.Fatalf("top-level slug = %v, want omitted on error (real value lives in request_context.slug)", m["slug"])
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
	createdOut := decodeWriteContent(t, created)
	createRevision, _ := createdOut["new_revision"].(string)
	if createRevision == "" {
		t.Fatalf("create_page new_revision missing: %#v", createdOut)
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
	updatedOut := decodeWriteContent(t, updated)
	updateRevision, _ := updatedOut["new_revision"].(string)
	if updateRevision == "" {
		t.Fatalf("update_page new_revision missing: %#v", updatedOut)
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
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "source_key")
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "resolved_source_path")
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "rate_limit_remaining")
	if got := decoded["source_key"]; got != "update-me" {
		t.Fatalf("update_page source_key = %v, want update-me", got)
	}
	if got := decoded["resolved_source_path"]; got != "content/update-me/index.md" {
		t.Fatalf("update_page resolved_source_path = %v, want content/update-me/index.md", got)
	}
	if got := dataEnvelope["resolved_source_path"]; got != "content/update-me/index.md" {
		t.Fatalf("update_page data.resolved_source_path = %v, want content/update-me/index.md", got)
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
	dryRunEnvelope := decodeWriteContent(t, dryRun)
	dryRunPayload, _ := dryRunEnvelope["diff"].(string)
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
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "source_key")
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "resolved_source_path")
	assertWriteSuccessCompatAlias(t, decoded, dataEnvelope, "rate_limit_remaining")
	if got := decoded["source_key"]; got != "to-delete" {
		t.Fatalf("delete_page source_key = %v, want to-delete", got)
	}
	if got := decoded["resolved_source_path"]; got != "content/to-delete/index.md" {
		t.Fatalf("delete_page resolved_source_path = %v, want content/to-delete/index.md", got)
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
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != "content/posts/csp-nonce/index.fr.md" {
		t.Fatalf("update_page multilingual resolved_source_path = %v, want content/posts/csp-nonce/index.fr.md", got)
	}
	if got := decoded["resolved_lang"]; got != "fr" {
		t.Fatalf("update_page multilingual resolved_lang = %v, want fr", got)
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
	decoded := decodeWriteContent(t, res)
	if got := decoded["resolved_source_path"]; got != "content/posts/bilingual/index.fr.md" {
		t.Fatalf("create_page resolved_source_path = %v, want content/posts/bilingual/index.fr.md", got)
	}
	if got := decoded["resolved_lang"]; got != "fr" {
		t.Fatalf("create_page resolved_lang = %v, want fr", got)
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

func TestDeletePageMultilingualBundleStillSucceeds(t *testing.T) {
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
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page on multilingual bundle failed: %s", raw)
	}
	if _, err := os.Stat(pageDir); !os.IsNotExist(err) {
		t.Fatal("delete_page must remove multilingual bundle directory")
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

	m := decodeWriteContent(t, res)
	if m["dry_run"] != true {
		t.Errorf("expected dry_run=true in response, got %v", m["dry_run"])
	}
	content, _ := m["content"].(string)
	if !strings.Contains(content, "Dry Run") {
		t.Errorf("dry_run content should contain page frontmatter, got: %q", content)
	}
	if _, ok := m["backlinks"]; !ok {
		t.Error("dry_run response must include backlinks key")
	}

	// #466 regression: delete_page's dry_run must report the caller's actual
	// remaining rate-limit budget, not a false 0 — dry_run doesn't consume
	// the budget, so on a fresh caller this must equal the configured burst
	// (5, config.Default()'s DestructivePerMin), not the zero value a
	// forgotten field assignment would produce.
	remaining, ok := m["rate_limit_remaining"].(float64)
	if !ok || remaining != float64(config.Default().RateLimit.DestructivePerMin) {
		t.Errorf("dry_run rate_limit_remaining = %#v, want %d (fresh, unconsumed budget)", m["rate_limit_remaining"], config.Default().RateLimit.DestructivePerMin)
	}

	// File must not have been removed.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "dry-run-me", "index.md")); err != nil {
		t.Errorf("dry_run must not delete the file: %v", err)
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

	m := decodeWriteContent(t, res)
	bls, ok := m["backlinks"].([]any)
	if !ok {
		t.Fatalf("dry_run response must include backlinks array, got %T: %v", m["backlinks"], m["backlinks"])
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
	if got := decodeWriteContent(t, res)["status"]; got != "partial_success" {
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
	if got := decodeWriteContent(t, res)["status"]; got != "partial_success" {
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
	if got := decodeWriteContent(t, res)["status"]; got != "partial_success" {
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
