package write_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// minimalPNG is enough leading bytes for http.DetectContentType to sniff
// image/png; it need not be a structurally complete PNG.
var minimalPNG = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0, 0, 0, 0}

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func writeBundle(t *testing.T, contentRoot, slug string) {
	t.Helper()
	dir := filepath.Join(contentRoot, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("---\ntitle: Article\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUploadPageAssetSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}
	out := decodeWriteContent(t, res)
	dataEnvelope := decodeWriteData(t, res)
	assertWriteSuccessCompatAlias(t, out, dataEnvelope, "rate_limit_remaining")
	if dataEnvelope["source_key"] != "posts/article" {
		t.Fatalf("upload_page_asset data.source_key = %v, want posts/article", dataEnvelope["source_key"])
	}
	if dataEnvelope["content_type"] != "image/png" {
		t.Fatalf("upload_page_asset data.content_type = %v, want image/png", dataEnvelope["content_type"])
	}
	if size, _ := dataEnvelope["size_bytes"].(float64); size != float64(len(minimalPNG)) {
		t.Fatalf("upload_page_asset data.size_bytes = %v, want %d", dataEnvelope["size_bytes"], len(minimalPNG))
	}
	data, err := os.ReadFile(filepath.Join(contentRoot, "posts", "article", "cover.png"))
	if err != nil {
		t.Fatalf("uploaded file not found: %v", err)
	}
	if string(data) != string(minimalPNG) {
		t.Fatal("uploaded file content mismatch")
	}
}

func TestUploadPageAssetRejectsSVG(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.svg",
		"content_base64": b64([]byte("<svg></svg>")),
	})
	if !res.IsError {
		t.Fatal("upload_page_asset: want error for .svg, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("upload_page_asset svg error = %s, want invalid_params", raw)
	}
}

func TestUploadPageAssetRejectsMimeMismatch(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64([]byte("this is plain text, not a png")),
	})
	if !res.IsError {
		t.Fatal("upload_page_asset: want error for mime mismatch, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("upload_page_asset mime-mismatch error = %s, want invalid_params", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); !os.IsNotExist(err) {
		t.Fatal("upload_page_asset must not write a file when sniffing rejects the content")
	}
}

func TestUploadPageAssetRejectsExistingFilename(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	args := map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}
	if res := callTool(t, session, "upload_page_asset", args); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first upload_page_asset returned error: %s", raw)
	}

	res := callTool(t, session, "upload_page_asset", args)
	if !res.IsError {
		t.Fatal("upload_page_asset: want already_exists on second write to same filename, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "already_exists") {
		t.Fatalf("upload_page_asset duplicate-filename error = %s, want already_exists", raw)
	}
}

func TestUploadPageAssetNotABundle(t *testing.T) {
	contentRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(contentRoot, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "pages", "about.md"), []byte("---\ntitle: About\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "pages/about",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	})
	if !res.IsError {
		t.Fatal("upload_page_asset: want not_a_bundle for single-file page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_a_bundle") {
		t.Fatalf("upload_page_asset error = %s, want not_a_bundle", raw)
	}
}

func TestUploadPageAssetDryRunDoesNotWrite(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
		"dry_run":        true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset dry_run returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if dryRun, _ := dataEnvelope["dry_run"].(bool); !dryRun {
		t.Fatalf("upload_page_asset dry_run response data.dry_run = %v, want true", dataEnvelope["dry_run"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); !os.IsNotExist(err) {
		t.Fatal("upload_page_asset dry_run must not write a file")
	}
}

// TestUploadPageAssetDryRunDoesNotConsumeQuota is #588's upload_page_asset
// counterpart to TestDeletePageAssetDryRunDoesNotConsumeDestructiveQuota — an
// audit found upload_page_asset called limiter.Allow() before checking
// in.DryRun (the shared create/update quota, not the destructive one).
func TestUploadPageAssetDryRunDoesNotConsumeQuota(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 2
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	var remaining []float64
	for i := 0; i < 5; i++ {
		res := callTool(t, session, "upload_page_asset", map[string]any{
			"slug":           "posts/article",
			"filename":       "quota-cover.png",
			"content_base64": b64(minimalPNG),
			"dry_run":        true,
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("upload_page_asset dry_run %d returned error: %s", i, raw)
		}
		data := decodeWriteData(t, res)
		rem, ok := data["rate_limit_remaining"].(float64)
		if !ok {
			t.Fatalf("upload_page_asset dry_run %d: rate_limit_remaining missing", i)
		}
		remaining = append(remaining, rem)
	}
	for i := 1; i < len(remaining); i++ {
		if remaining[i] != remaining[0] {
			t.Fatalf("upload_page_asset dry_run consumed quota: rate_limit_remaining sequence = %v, want constant at %v (dry_run must never call limiter.Allow())", remaining, remaining[0])
		}
	}
	if remaining[0] != float64(rl.CreateUpdatePerMin) {
		t.Fatalf("upload_page_asset dry_run rate_limit_remaining = %v, want full fresh budget %d", remaining[0], rl.CreateUpdatePerMin)
	}
}

// TestUploadPageAssetNonDryRunAlreadyExistsStillConsumesQuota is a
// discriminating regression test for #588's review: the dry-run fix must be
// a surgical guard (skip Allow() only when in.DryRun), not a wholesale move
// of Allow() past the whole dry-run block — a move would have also skipped
// Allow() for non-dry-run failures like already_exists, silently changing
// existing, intentional quota-consumption behavior with no other test
// catching it.
func TestUploadPageAssetNonDryRunAlreadyExistsStillConsumesQuota(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 5
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "existing.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first upload_page_asset returned error: %s", raw)
	}

	// Second (non-dry-run) call to the same filename fails already_exists —
	// this must still consume 1 token, exactly as it did before #588.
	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "existing.png",
		"content_base64": b64(minimalPNG),
	})
	if !res.IsError {
		t.Fatal("second upload_page_asset to the same filename should fail already_exists")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "already_exists") {
		t.Fatalf("upload_page_asset second call error = %s, want already_exists", raw)
	}
	data := decodeWriteErrorData(t, res)
	wantRemaining := float64(rl.CreateUpdatePerMin - 2) // first (successful) upload + second (already_exists) each consume one token
	if got := data["rate_limit_remaining"]; got != wantRemaining {
		t.Fatalf("upload_page_asset already_exists data.rate_limit_remaining = %v, want %v", got, wantRemaining)
	}
}

func TestUploadPageAssetDuplicateContentIsAdvisoryOnly(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("first upload_page_asset returned error: %s", raw)
	}

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover-copy.png",
		"content_base64": b64(minimalPNG),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("second upload_page_asset (identical content, new filename) returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if dataEnvelope["duplicate_of"] != "cover.png" {
		t.Fatalf("upload_page_asset data.duplicate_of = %v, want cover.png", dataEnvelope["duplicate_of"])
	}
	// The write must still happen under the requested filename — duplicate
	// detection is advisory, not a substitute for the caller's explicit intent.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover-copy.png")); err != nil {
		t.Fatalf("upload_page_asset must still write cover-copy.png despite duplicate content: %v", err)
	}
}

func TestUploadPageAssetTrimsFilenameConsistently(t *testing.T) {
	// Regression: validateAssetFilename validated a trimmed copy of the
	// filename but the handler used to write/echo the raw, untrimmed input,
	// so "cover.png\n" passed the safe-charset regex on its trimmed form
	// while still writing a file literally named "cover.png\n" on disk.
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png\n",
		"content_base64": b64(minimalPNG),
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset with whitespace-padded filename returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if dataEnvelope["filename"] != "cover.png" {
		t.Fatalf("upload_page_asset data.filename = %v, want trimmed \"cover.png\"", dataEnvelope["filename"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); err != nil {
		t.Fatalf("expected file written under trimmed name %q: %v", "cover.png", err)
	}
	entries, err := os.ReadDir(filepath.Join(contentRoot, "posts", "article"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "index.md" && e.Name() != "cover.png" {
			t.Fatalf("unexpected file written with untrimmed name: %q", e.Name())
		}
	}
}

func TestUploadPageAssetRejectsPathTraversalFilename(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, filename := range []string{"../evil.png", "sub/dir.png", ".hidden.png"} {
		res := callTool(t, session, "upload_page_asset", map[string]any{
			"slug":           "posts/article",
			"filename":       filename,
			"content_base64": b64(minimalPNG),
		})
		if !res.IsError {
			t.Fatalf("upload_page_asset: want error for unsafe filename %q, got success", filename)
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "invalid_params") {
			t.Fatalf("upload_page_asset filename %q error = %s, want invalid_params", filename, raw)
		}
	}
}

func TestDeletePageAssetSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	upload := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	})
	if upload.IsError {
		raw, _ := json.Marshal(upload.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}
	sha256 := decodeWriteData(t, upload)["sha256"].(string)

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"expected_sha256": sha256,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page_asset returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if dataEnvelope["sha256"] != sha256 {
		t.Fatalf("delete_page_asset data.sha256 = %v, want %v", dataEnvelope["sha256"], sha256)
	}
	if dataEnvelope["source_key"] != "posts/article" {
		t.Fatalf("delete_page_asset data.source_key = %v, want posts/article", dataEnvelope["source_key"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); !os.IsNotExist(err) {
		t.Fatal("delete_page_asset must remove the file")
	}
}

func TestDeletePageAssetHashMismatchFailsWithRevisionConflict(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"expected_sha256": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	if !res.IsError {
		t.Fatal("delete_page_asset: want error for hash mismatch, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "revision_conflict") {
		t.Fatalf("delete_page_asset hash-mismatch error = %s, want revision_conflict", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); err != nil {
		t.Fatalf("delete_page_asset must not delete the file on a hash mismatch: %v", err)
	}
}

func TestDeletePageAssetRequiresExpectedHashOrRevision(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":     "posts/article",
		"filename": "cover.png",
	})
	if !res.IsError {
		t.Fatal("delete_page_asset: want error when neither expected_sha256 nor expected_revision is given, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("delete_page_asset missing-guard error = %s, want invalid_params", raw)
	}
}

func TestDeletePageAssetReferencedGuardBlocksUnlessForced(t *testing.T) {
	contentRoot := t.TempDir()
	dir := filepath.Join(contentRoot, "posts", "article")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Body references cover.png directly — the guard must detect this.
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("---\ntitle: Article\n---\n![cover](cover.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	upload := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	})
	if upload.IsError {
		raw, _ := json.Marshal(upload.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}
	sha256 := decodeWriteData(t, upload)["sha256"].(string)

	blocked := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"expected_sha256": sha256,
	})
	if !blocked.IsError {
		t.Fatal("delete_page_asset: want asset_referenced error, got success")
	}
	raw, _ := json.Marshal(blocked.Content)
	if !strings.Contains(string(raw), "asset_referenced") {
		t.Fatalf("delete_page_asset referenced-guard error = %s, want asset_referenced", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "cover.png")); err != nil {
		t.Fatalf("delete_page_asset must not delete a referenced asset without force=true: %v", err)
	}

	forced := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"expected_sha256": sha256,
		"force":           true,
	})
	if forced.IsError {
		raw, _ := json.Marshal(forced.Content)
		t.Fatalf("delete_page_asset with force=true returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, forced)
	if dataEnvelope["referenced"] != true {
		t.Fatalf("delete_page_asset data.referenced = %v, want true (force overrides the guard, doesn't hide the fact)", dataEnvelope["referenced"])
	}
	if _, err := os.Stat(filepath.Join(dir, "cover.png")); !os.IsNotExist(err) {
		t.Fatal("delete_page_asset with force=true must delete the file")
	}
}

func TestDeletePageAssetDryRunPreviewsWithoutDeleting(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":     "posts/article",
		"filename": "cover.png",
		"dry_run":  true,
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page_asset dry_run returned error: %s", raw)
	}
	dataEnvelope := decodeWriteData(t, res)
	if dataEnvelope["dry_run"] != true {
		t.Fatalf("delete_page_asset dry_run response data.dry_run = %v, want true", dataEnvelope["dry_run"])
	}
	if sha, _ := dataEnvelope["sha256"].(string); sha == "" {
		t.Fatal("delete_page_asset dry_run must report the asset's sha256")
	}
	if dataEnvelope["referenced"] == true {
		t.Fatalf("delete_page_asset dry_run data.referenced = %v, want false (fixture body doesn't mention cover.png)", dataEnvelope["referenced"])
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "cover.png")); err != nil {
		t.Fatalf("delete_page_asset dry_run must not delete the file: %v", err)
	}
}

// TestDeletePageAssetDryRunDoesNotConsumeDestructiveQuota is a regression
// test for #575: a live Claude.ai audit observed rate_limit_remaining drop
// (5→4) on delete_page_asset before the real (non-dry-run) call, suggesting
// dry_run itself consumed the destructive quota. The handler's own code
// comment says fetching the limiter doesn't consume budget and dry_run
// returns before calling limiter.Allow() — this test proves that empirically
// with a small budget so any unexpected decrement is visible immediately,
// ruling out (or catching) a token-bucket-refill artifact masking a real bug.
func TestDeletePageAssetDryRunDoesNotConsumeDestructiveQuota(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	rl := config.Default().RateLimit
	rl.DestructivePerMin = 2
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	if res := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	}); res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}

	var remaining []float64
	for i := 0; i < 5; i++ {
		res := callTool(t, session, "delete_page_asset", map[string]any{
			"slug":     "posts/article",
			"filename": "cover.png",
			"dry_run":  true,
		})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("delete_page_asset dry_run %d returned error: %s", i, raw)
		}
		data := decodeWriteData(t, res)
		rem, ok := data["rate_limit_remaining"].(float64)
		if !ok {
			t.Fatalf("delete_page_asset dry_run %d: rate_limit_remaining missing", i)
		}
		remaining = append(remaining, rem)
	}
	for i := 1; i < len(remaining); i++ {
		if remaining[i] != remaining[0] {
			t.Fatalf("delete_page_asset dry_run consumed destructive quota: rate_limit_remaining sequence = %v, want constant at %v (dry_run must never call limiter.Allow())", remaining, remaining[0])
		}
	}
	if remaining[0] != float64(rl.DestructivePerMin) {
		t.Fatalf("delete_page_asset dry_run rate_limit_remaining = %v, want full fresh budget %d", remaining[0], rl.DestructivePerMin)
	}
}

func TestDeletePageAssetIdempotencyKeyReturnsOriginalResultWithoutDeletingTwice(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	upload := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":           "posts/article",
		"filename":       "cover.png",
		"content_base64": b64(minimalPNG),
	})
	if upload.IsError {
		raw, _ := json.Marshal(upload.Content)
		t.Fatalf("upload_page_asset returned error: %s", raw)
	}
	sha256 := decodeWriteData(t, upload)["sha256"].(string)

	args := map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"expected_sha256": sha256,
		"idempotency_key": "delete-cover-once",
	}
	first := callTool(t, session, "delete_page_asset", args)
	if first.IsError {
		raw, _ := json.Marshal(first.Content)
		t.Fatalf("first delete_page_asset returned error: %s", raw)
	}

	// This is the actual uncertain-delivery scenario the idempotency_key
	// promise exists for: the file is genuinely gone (deleted by the first
	// call) and the caller never gets to see the first response, so it
	// retries with nothing recreated at the path. The idempotency replay
	// lookup must be reachable without the file existing — if it depended
	// on a not_found gate the way a fresh delete attempt does, this replay
	// would incorrectly fail with not_found instead of returning the cached
	// original result.
	replay := callTool(t, session, "delete_page_asset", args)
	if replay.IsError {
		raw, _ := json.Marshal(replay.Content)
		t.Fatalf("replayed delete_page_asset (file genuinely gone, nothing recreated) returned error: %s", raw)
	}
	firstOut := decodeWriteContent(t, first)
	replayOut := decodeWriteContent(t, replay)
	if firstOut["sha256"] != replayOut["sha256"] {
		t.Fatalf("replay sha256 = %v, want %v (identical to original result)", replayOut["sha256"], firstOut["sha256"])
	}
}

func TestDeletePageAssetNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "does-not-exist.png",
		"expected_sha256": "irrelevant",
	})
	if !res.IsError {
		t.Fatal("delete_page_asset: want not_found for a missing asset, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Fatalf("delete_page_asset missing-asset error = %s, want not_found", raw)
	}
}

func TestDeletePageAssetRejectsIndexFile(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "index.md",
		"expected_sha256": "irrelevant",
	})
	if !res.IsError {
		t.Fatal("delete_page_asset: want error when filename names the page's own content file, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "invalid_params") {
		t.Fatalf("delete_page_asset index.md error = %s, want invalid_params", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "index.md")); err != nil {
		t.Fatalf("delete_page_asset must not delete index.md: %v", err)
	}
}
