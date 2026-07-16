package write_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newCombinedTestServer registers both write tools and build_site on the same
// MCP server, since they coordinate through the shared package-level
// hugosite.ContentMu — the same mutation-coordination model documented in
// docs/mutation-coordination-model.md (#374).
func newCombinedTestServer(t *testing.T, contentRoot, hugoRoot, siteRoot string) (*mcp.ClientSession, func()) {
	t.Helper()
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
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = siteRoot

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg, nil)
	admin.RegisterBuild(s, cfg)

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

func firstErrorCode(t *testing.T, envelope map[string]any) string {
	t.Helper()
	errs, ok := envelope["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("envelope has no errors[]: %#v", envelope)
	}
	first, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("errors[0] type = %T", errs[0])
	}
	code, _ := first["code"].(string)
	return code
}

// TestConcurrentUpdatePageSamePageDeterministicOutcome proves the same-page
// race the mutation-coordination model must resolve deterministically:
// two concurrent update_page calls against the same slug, both captured with
// the same (now-stale-for-one-of-them) expected_revision, must never both
// succeed and never corrupt the file — exactly one succeeds, the other fails
// with a deterministic revision_conflict.
func TestConcurrentUpdatePageSamePageDeterministicOutcome(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "coord-same-page", "title": "Original", "body": "Body v0",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}
	pagePath := filepath.Join(contentRoot, "coord-same-page", "index.md")
	rev := currentRevision(t, pagePath)

	args := map[string]any{
		"slug":              "coord-same-page",
		"body":              "Body v1",
		"expected_revision": rev,
	}

	hugosite.ContentMu.Lock()
	results := make(chan *mcp.CallToolResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- callTool(t, session, "update_page", args)
		}()
	}
	time.Sleep(150 * time.Millisecond)
	hugosite.ContentMu.Unlock()
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for res := range results {
		if res.IsError {
			env := decodeWriteErrorEnvelope(t, res)
			if code := firstErrorCode(t, env); code == "revision_conflict" {
				conflicts++
				continue
			}
			t.Fatalf("unexpected error (want only revision_conflict): %s", marshalContent(t, res))
		}
		successes++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-page race must resolve to exactly one success and one revision_conflict, got successes=%d conflicts=%d", successes, conflicts)
	}
}

// TestConcurrentBundleLanguageWritesBothSucceed proves the same-bundle race:
// two concurrent creates of different language variants in the same bundle
// directory (index.fr.md, index.es.md alongside an existing index.md) do not
// conflict with each other and both land correctly — the shared ContentMu
// lock serializes the two writes but does not reject either of them, since
// they target different files.
func TestConcurrentBundleLanguageWritesBothSucceed(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "coord-bundle", "title": "English", "body": "EN body",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page (en) failed: %s", marshalContent(t, create))
	}

	// Both goroutines create brand-new bundle-member files (fr, es) — this
	// avoids exercising update_page's by-language disambiguation lookup
	// (a separate, already-tested concern) and isolates exactly what this
	// test is about: concurrent writes to different files in the same
	// bundle directory must not race on directory creation or clobber each
	// other's file.
	var wg sync.WaitGroup
	results := make(chan *mcp.CallToolResult, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results <- callTool(t, session, "create_page", map[string]any{
			"slug": "coord-bundle", "lang": "fr", "title": "Francais", "body": "FR body",
			"tags": []any{}, "categories": []any{},
		})
	}()
	go func() {
		defer wg.Done()
		results <- callTool(t, session, "create_page", map[string]any{
			"slug": "coord-bundle", "lang": "es", "title": "Espanol", "body": "ES body",
			"tags": []any{}, "categories": []any{},
		})
	}()
	wg.Wait()
	close(results)

	for res := range results {
		if res.IsError {
			t.Fatalf("concurrent same-bundle language writes must both succeed: %s", marshalContent(t, res))
		}
	}

	enPath := filepath.Join(contentRoot, "coord-bundle", "index.md")
	frPath := filepath.Join(contentRoot, "coord-bundle", "index.fr.md")
	esPath := filepath.Join(contentRoot, "coord-bundle", "index.es.md")
	for _, p := range []string{enPath, frPath, esPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected bundle file %s to exist after concurrent writes: %v", p, err)
		}
	}
}

// TestBuildSiteDeterministicallyRejectsWhileMutationInFlight and
// TestUpdatePageWaitsThenSucceedsWhileBuildInFlight prove the write-vs-build
// race is resolved deterministically in both directions, using a directly
// held ContentMu lock to simulate "the other operation is already in
// flight" without relying on goroutine scheduling luck.
func TestBuildSiteDeterministicallyRejectsWhileMutationInFlight(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()
	siteRoot := t.TempDir()
	session, done := newCombinedTestServer(t, contentRoot, hugoRoot, siteRoot)
	defer done()

	hugosite.ContentMu.Lock()
	defer hugosite.ContentMu.Unlock()

	res := callTool(t, session, "build_site", map[string]any{})
	if !res.IsError {
		t.Fatal("build_site must deterministically fail while a mutation holds ContentMu")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "build_in_progress") {
		t.Fatalf("build_site error = %q, want prefix containing %q", text, "build_in_progress")
	}
}

func TestUpdatePageWaitsThenSucceedsWhileBuildInFlight(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()
	siteRoot := t.TempDir()
	session, done := newCombinedTestServer(t, contentRoot, hugoRoot, siteRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "coord-write-vs-build", "title": "Original", "body": "Body v0",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}
	pagePath := filepath.Join(contentRoot, "coord-write-vs-build", "index.md")
	rev := currentRevision(t, pagePath)

	// Simulate build_site already holding the lock: update_page must queue
	// (its 10s retry loop) rather than observe a torn write, and must
	// succeed once the simulated build releases the lock.
	hugosite.ContentMu.Lock()
	resultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		resultCh <- callTool(t, session, "update_page", map[string]any{
			"slug": "coord-write-vs-build", "body": "Body v1", "expected_revision": rev,
		})
	}()
	time.Sleep(150 * time.Millisecond)
	hugosite.ContentMu.Unlock()

	select {
	case res := <-resultCh:
		if res.IsError {
			t.Fatalf("update_page should succeed once the build lock is released: %s", marshalContent(t, res))
		}
	case <-time.After(11 * time.Second):
		t.Fatal("update_page did not return within its documented 10s retry window")
	}
}
