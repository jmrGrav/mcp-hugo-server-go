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
	admin.RegisterBuild(s, cfg, nil)

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

type namedCallResult struct {
	op  string
	res *mcp.CallToolResult
}

func waitForStarts(t *testing.T, started <-chan struct{}, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for goroutine start signal %d/%d", i+1, want)
		}
	}
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
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			results <- callTool(t, session, "update_page", args)
		}()
	}
	waitForStarts(t, started, 2)
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

// TestConcurrentUpdateAndDeleteSamePageDeterministicOutcome extends the
// same-target race matrix from #692 to update_page vs delete_page on a
// single-language bundle. Because the runtime intentionally serializes
// mutations behind hugosite.ContentMu, the correct contract is not "both run
// concurrently to disk"; it is "both start together, one wins the lock, and
// the loser observes the winner's committed state cleanly". Valid outcomes:
// - update wins -> delete sees revision_conflict
// - delete wins -> update sees not_found
// Anything else risks silent lost writes or partial deletion.
func TestConcurrentUpdateAndDeleteSamePageDeterministicOutcome(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	const slug = "coord-update-delete"
	create := callTool(t, session, "create_page", map[string]any{
		"slug": slug, "title": "Original", "body": "Body v0",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}
	pagePath := filepath.Join(contentRoot, slug, "index.md")
	rev := currentRevision(t, pagePath)

	hugosite.ContentMu.Lock()
	results := make(chan namedCallResult, 2)
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "update", res: callTool(t, session, "update_page", map[string]any{
			"slug": slug, "body": "Body update wins", "expected_revision": rev,
		})}
	}()
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "delete", res: callTool(t, session, "delete_page", map[string]any{
			"slug": slug, "expected_revision": rev,
		})}
	}()
	waitForStarts(t, started, 2)
	hugosite.ContentMu.Unlock()
	wg.Wait()
	close(results)

	seen := map[string]*mcp.CallToolResult{}
	for r := range results {
		seen[r.op] = r.res
	}
	updateRes, deleteRes := seen["update"], seen["delete"]
	if updateRes == nil || deleteRes == nil {
		t.Fatalf("missing race results: %#v", seen)
	}

	switch {
	case !updateRes.IsError && deleteRes.IsError:
		if code := firstErrorCode(t, decodeWriteErrorEnvelope(t, deleteRes)); code != "revision_conflict" {
			t.Fatalf("delete loser code = %q, want revision_conflict", code)
		}
		raw, err := os.ReadFile(pagePath)
		if err != nil {
			t.Fatalf("winner page should remain readable: %v", err)
		}
		if !strings.Contains(string(raw), "Body update wins") {
			t.Fatalf("winner content = %q, want update body", string(raw))
		}
	case updateRes.IsError && !deleteRes.IsError:
		if code := firstErrorCode(t, decodeWriteErrorEnvelope(t, updateRes)); code != "not_found" {
			t.Fatalf("update loser code = %q, want not_found", code)
		}
		if _, err := os.Stat(filepath.Join(contentRoot, slug)); !os.IsNotExist(err) {
			t.Fatalf("bundle must be gone after delete winner, stat err = %v", err)
		}
	default:
		t.Fatalf("want exactly one winner, got update_error=%v delete_error=%v", updateRes.IsError, deleteRes.IsError)
	}
}

// TestConcurrentUpdateAndDeleteBilingualVariantDeterministicOutcome extends
// #692 to a multilingual bundle where both operations explicitly target the
// same non-default language. Valid outcomes:
//   - update(lang=fr) wins -> delete(lang=fr) sees revision_conflict and both
//     translations survive
//   - delete(lang=fr) wins -> update(lang=fr) sees not_found and only the
//     default-language file survives
//
// This guards against the more subtle bundle-level partial-delete failures
// the live audit warned about.
func TestConcurrentUpdateAndDeleteBilingualVariantDeterministicOutcome(t *testing.T) {
	contentRoot := t.TempDir()
	slug := "posts/coord-bilingual-race"
	pageDir := filepath.Join(contentRoot, slug)
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.md"), []byte("---\ntitle: EN\n---\nHello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile index.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: FR\n---\nBonjour\n"), 0o644); err != nil {
		t.Fatalf("WriteFile index.fr.md: %v", err)
	}

	session, idx, done := newTestServer(t, contentRoot)
	defer done()
	frPath := filepath.Join(pageDir, "index.fr.md")
	rev := currentRevision(t, frPath)

	hugosite.ContentMu.Lock()
	results := make(chan namedCallResult, 2)
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "update", res: callTool(t, session, "update_page", map[string]any{
			"slug": slug, "lang": "fr", "body": "Bonjour mis a jour", "expected_revision": rev,
		})}
	}()
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "delete", res: callTool(t, session, "delete_page", map[string]any{
			"slug": slug, "lang": "fr", "expected_revision": rev,
		})}
	}()
	waitForStarts(t, started, 2)
	hugosite.ContentMu.Unlock()
	wg.Wait()
	close(results)

	seen := map[string]*mcp.CallToolResult{}
	for r := range results {
		seen[r.op] = r.res
	}
	updateRes, deleteRes := seen["update"], seen["delete"]
	if updateRes == nil || deleteRes == nil {
		t.Fatalf("missing race results: %#v", seen)
	}

	switch {
	case !updateRes.IsError && deleteRes.IsError:
		if code := firstErrorCode(t, decodeWriteErrorEnvelope(t, deleteRes)); code != "revision_conflict" {
			t.Fatalf("delete loser code = %q, want revision_conflict", code)
		}
		if _, err := os.Stat(filepath.Join(pageDir, "index.md")); err != nil {
			t.Fatalf("default language must survive updated-fr winner: %v", err)
		}
		raw, err := os.ReadFile(frPath)
		if err != nil {
			t.Fatalf("fr file should survive update winner: %v", err)
		}
		if !strings.Contains(string(raw), "Bonjour mis a jour") {
			t.Fatalf("fr winner content = %q, want updated body", string(raw))
		}
		if _, ok := idx.GetBySlugLang(slug, "fr"); !ok {
			t.Fatal("fr translation must still be indexed after update winner")
		}
	case updateRes.IsError && !deleteRes.IsError:
		if code := firstErrorCode(t, decodeWriteErrorEnvelope(t, updateRes)); code != "not_found" {
			t.Fatalf("update loser code = %q, want not_found", code)
		}
		if _, err := os.Stat(filepath.Join(pageDir, "index.md")); err != nil {
			t.Fatalf("default language must survive fr delete winner: %v", err)
		}
		if _, err := os.Stat(frPath); !os.IsNotExist(err) {
			t.Fatalf("fr source must be gone after delete winner, stat err = %v", err)
		}
		if _, ok := idx.GetBySlugLang(slug, "fr"); ok {
			t.Fatal("fr translation must be removed from SourceIndex after delete winner")
		}
		if _, ok := idx.GetDefaultBySlug(slug); !ok {
			t.Fatal("default-language translation must remain resolvable via GetDefaultBySlug after fr delete winner")
		}
	default:
		t.Fatalf("want exactly one winner, got update_error=%v delete_error=%v", updateRes.IsError, deleteRes.IsError)
	}
}

// TestConcurrentUploadAssetAndDeleteSameBundleDeterministicOutcome covers the
// third audited race class from #692. Because upload_page_asset does not
// change the page revision, two serialized outcomes are both valid:
//   - delete wins first -> upload sees not_found and the bundle is gone
//   - upload wins first -> upload succeeds, then delete still succeeds and
//     removes the newly written asset with the bundle
//
// The invariant is no orphaned asset and no partially deleted bundle.
func TestConcurrentUploadAssetAndDeleteSameBundleDeterministicOutcome(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	const slug = "posts/coord-upload-delete"
	create := callTool(t, session, "create_page", map[string]any{
		"slug": slug, "title": "Original", "body": "Body v0",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, create))
	}
	rev := currentRevision(t, filepath.Join(contentRoot, slug, "index.md"))

	hugosite.ContentMu.Lock()
	results := make(chan namedCallResult, 2)
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "upload", res: callTool(t, session, "upload_page_asset", map[string]any{
			"slug": slug, "filename": "cover.png", "content_base64": b64(minimalPNG),
		})}
	}()
	go func() {
		defer wg.Done()
		started <- struct{}{}
		results <- namedCallResult{op: "delete", res: callTool(t, session, "delete_page", map[string]any{
			"slug": slug, "expected_revision": rev,
		})}
	}()
	waitForStarts(t, started, 2)
	hugosite.ContentMu.Unlock()
	wg.Wait()
	close(results)

	seen := map[string]*mcp.CallToolResult{}
	for r := range results {
		seen[r.op] = r.res
	}
	uploadRes, deleteRes := seen["upload"], seen["delete"]
	if uploadRes == nil || deleteRes == nil {
		t.Fatalf("missing race results: %#v", seen)
	}

	if deleteRes.IsError {
		t.Fatalf("delete_page must be the final winner in upload/delete bundle race, got error: %s", marshalContent(t, deleteRes))
	}
	if _, err := os.Stat(filepath.Join(contentRoot, slug)); !os.IsNotExist(err) {
		t.Fatalf("bundle must be gone after delete winner, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, slug, "cover.png")); !os.IsNotExist(err) {
		t.Fatalf("asset must not survive delete winner, stat err = %v", err)
	}
	if uploadRes.IsError {
		if code := firstErrorCode(t, decodeWriteErrorEnvelope(t, uploadRes)); code != "not_found" {
			t.Fatalf("upload loser code = %q, want not_found", code)
		}
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
	started := make(chan struct{}, 1)
	go func() {
		started <- struct{}{}
		resultCh <- callTool(t, session, "update_page", map[string]any{
			"slug": "coord-write-vs-build", "body": "Body v1", "expected_revision": rev,
		})
	}()
	waitForStarts(t, started, 1)
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
