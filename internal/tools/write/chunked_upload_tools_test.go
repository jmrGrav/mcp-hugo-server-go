package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

// helloWorldWebP-ish payload: not a structurally valid WebP, but long
// enough (>512 bytes, http.DetectContentType's sniff window) with a
// well-formed RIFF/WEBP header so the sniffer recognizes it as image/webp,
// mirroring minimalPNG's "just enough bytes to sniff correctly" approach in
// page_assets_test.go. Used to exercise the chunked path across more than
// one chunk without needing a real multi-hundred-KB fixture file.
func fakeWebP(totalSize int) []byte {
	header := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	data := make([]byte, totalSize)
	copy(data, header)
	for i := len(header); i < len(data); i++ {
		data[i] = byte(i % 251)
	}
	return data
}

func TestChunkedUploadEndToEndSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payload := fakeWebP(2500) // spans multiple 1000-byte chunks
	wantHash := contentmodel.SourceRevisionBytes(payload)

	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.webp",
		"size_bytes": len(payload),
		"sha256":     wantHash,
	})
	if beginRes.IsError {
		t.Fatalf("begin_asset_upload error: %s", marshalContent(t, beginRes))
	}
	beginData := decodeWriteData(t, beginRes)
	uploadID, _ := beginData["upload_id"].(string)
	if uploadID == "" {
		t.Fatalf("begin_asset_upload returned empty upload_id: %+v", beginData)
	}
	if got := beginData["max_asset_bytes"]; got == nil {
		t.Fatal("begin_asset_upload missing max_asset_bytes")
	}

	const chunkSize = 1000
	offset := 0
	for offset < len(payload) {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		chunkRes := callTool(t, session, "upload_asset_chunk", map[string]any{
			"upload_id":      uploadID,
			"offset":         offset,
			"content_base64": b64(payload[offset:end]),
		})
		if chunkRes.IsError {
			t.Fatalf("upload_asset_chunk at offset %d error: %s", offset, marshalContent(t, chunkRes))
		}
		chunkData := decodeWriteData(t, chunkRes)
		nextOffset, _ := chunkData["next_offset"].(float64)
		if int(nextOffset) != end {
			t.Fatalf("chunk at offset %d: next_offset = %v, want %d", offset, chunkData["next_offset"], end)
		}
		offset = end
	}

	commitRes := callTool(t, session, "commit_asset_upload", map[string]any{
		"upload_id": uploadID,
	})
	if commitRes.IsError {
		t.Fatalf("commit_asset_upload error: %s", marshalContent(t, commitRes))
	}
	commitData := decodeWriteData(t, commitRes)
	if commitData["status"] != "created" {
		t.Fatalf("commit data.status = %v, want created", commitData["status"])
	}
	if commitData["sha256"] != wantHash {
		t.Fatalf("commit data.sha256 = %v, want %v", commitData["sha256"], wantHash)
	}
	if commitData["content_type"] != "image/webp" {
		t.Fatalf("commit data.content_type = %v, want image/webp", commitData["content_type"])
	}

	written, err := os.ReadFile(filepath.Join(contentRoot, "posts", "article", "hero.webp"))
	if err != nil {
		t.Fatalf("committed asset not found on disk: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatal("committed asset bytes do not match the uploaded payload")
	}

	// upload_id must be single-use: a second commit attempt must fail, not
	// silently succeed again or resurrect a consumed upload.
	replayRes := callTool(t, session, "commit_asset_upload", map[string]any{
		"upload_id": uploadID,
	})
	if !replayRes.IsError {
		t.Fatal("second commit_asset_upload with the same upload_id unexpectedly succeeded")
	}
}

func TestChunkedUploadRejectsWrongSha256AtCommit(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payload := fakeWebP(600)
	wrongHash := "sha256:" + strings.Repeat("a", 64)

	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.webp",
		"size_bytes": len(payload),
		"sha256":     wrongHash,
	})
	if beginRes.IsError {
		t.Fatalf("begin_asset_upload error: %s", marshalContent(t, beginRes))
	}
	uploadID := decodeWriteData(t, beginRes)["upload_id"].(string)

	chunkRes := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id":      uploadID,
		"offset":         0,
		"content_base64": b64(payload),
	})
	if chunkRes.IsError {
		t.Fatalf("upload_asset_chunk error: %s", marshalContent(t, chunkRes))
	}

	commitRes := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": uploadID})
	if !commitRes.IsError {
		t.Fatal("commit_asset_upload with mismatched sha256 unexpectedly succeeded")
	}
	if raw := marshalContent(t, commitRes); !strings.Contains(raw, "sha256_mismatch") {
		t.Fatalf("error should be sha256_mismatch, got: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "hero.webp")); !os.IsNotExist(err) {
		t.Fatal("asset must not exist on disk after a failed sha256_mismatch commit")
	}

	// The failed commit must have discarded the upload (abandon), not left
	// it retryable with the same doomed sha256.
	retryRes := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": uploadID})
	if !retryRes.IsError {
		t.Fatal("commit_asset_upload succeeded on retry after sha256_mismatch discarded the upload")
	}
}

func TestChunkedUploadRejectsMismatchedExtensionContent(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	// Declares .png but the bytes sniff as image/webp — must be caught at
	// commit by validateAssetBytes, the exact gate upload_page_asset uses.
	payload := fakeWebP(600)

	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.png",
		"size_bytes": len(payload),
	})
	if beginRes.IsError {
		t.Fatalf("begin_asset_upload error: %s", marshalContent(t, beginRes))
	}
	uploadID := decodeWriteData(t, beginRes)["upload_id"].(string)

	chunkRes := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id":      uploadID,
		"offset":         0,
		"content_base64": b64(payload),
	})
	if chunkRes.IsError {
		t.Fatalf("upload_asset_chunk error: %s", marshalContent(t, chunkRes))
	}

	commitRes := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": uploadID})
	if !commitRes.IsError {
		t.Fatal("commit_asset_upload with mismatched extension/content unexpectedly succeeded")
	}
	if raw := marshalContent(t, commitRes); !strings.Contains(raw, "invalid_params") || !strings.Contains(raw, "mismatch") {
		t.Fatalf("error should report a filename/content mismatch, got: %s", raw)
	}
}

func TestChunkedUploadOutOfOrderChunkRejected(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payload := fakeWebP(600)
	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.webp",
		"size_bytes": len(payload),
	})
	uploadID := decodeWriteData(t, beginRes)["upload_id"].(string)

	res := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id":      uploadID,
		"offset":         100, // skips offset 0
		"content_base64": b64(payload[100:200]),
	})
	if !res.IsError {
		t.Fatal("out-of-order chunk unexpectedly accepted")
	}
	if raw := marshalContent(t, res); !strings.Contains(raw, "out_of_order") {
		t.Fatalf("error should be out_of_order, got: %s", raw)
	}
}

func TestChunkedUploadDuplicateChunkIdempotentVsConflicting(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payload := fakeWebP(600)
	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.webp",
		"size_bytes": len(payload),
	})
	uploadID := decodeWriteData(t, beginRes)["upload_id"].(string)

	first := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id": uploadID, "offset": 0, "content_base64": b64(payload[:300]),
	})
	if first.IsError {
		t.Fatalf("first chunk error: %s", marshalContent(t, first))
	}

	// Identical replay: must succeed as a no-op.
	replay := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id": uploadID, "offset": 0, "content_base64": b64(payload[:300]),
	})
	if replay.IsError {
		t.Fatalf("identical replayed chunk should succeed, got error: %s", marshalContent(t, replay))
	}
	replayData := decodeWriteData(t, replay)
	if replayData["replayed"] != true {
		t.Fatalf("replayed chunk data.replayed = %v, want true", replayData["replayed"])
	}

	// Different content at the same already-received offset: must be
	// rejected as a conflict, never silently accepted or overwritten.
	differentContent := make([]byte, 300)
	copy(differentContent, payload[:300])
	differentContent[299] ^= 0xFF
	conflict := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id": uploadID, "offset": 0, "content_base64": b64(differentContent),
	})
	if !conflict.IsError {
		t.Fatal("conflicting chunk at an already-received offset unexpectedly accepted")
	}
	if raw := marshalContent(t, conflict); !strings.Contains(raw, "chunk_conflict") {
		t.Fatalf("error should be chunk_conflict, got: %s", raw)
	}
}

func TestChunkedUploadOverLimitRejectedAtBegin(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "huge.png",
		"size_bytes": 10<<20 + 1, // one byte over the 10MiB cap
	})
	if !res.IsError {
		t.Fatal("begin_asset_upload with size_bytes over the cap unexpectedly succeeded")
	}
	if raw := marshalContent(t, res); !strings.Contains(raw, "invalid_params") {
		t.Fatalf("error should be invalid_params, got: %s", raw)
	}

	// Nothing should have been staged inside the bundle directory.
	entries, err := os.ReadDir(filepath.Join(contentRoot, "posts", "article"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upload-") {
			t.Fatalf("an over-limit begin call left a staging file behind: %s", e.Name())
		}
	}
}

func TestChunkedUploadNeverCommittedLeavesNoVisibleResidue(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payload := fakeWebP(600)
	beginRes := callTool(t, session, "begin_asset_upload", map[string]any{
		"slug":       "posts/article",
		"filename":   "hero.webp",
		"size_bytes": len(payload),
	})
	uploadID := decodeWriteData(t, beginRes)["upload_id"].(string)
	chunkRes := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id": uploadID, "offset": 0, "content_base64": b64(payload),
	})
	if chunkRes.IsError {
		t.Fatalf("chunk error: %s", marshalContent(t, chunkRes))
	}

	// Never call commit. The final asset filename must not exist, and
	// list_page_assets-equivalent inspection (a directory listing) must
	// never show the real "hero.webp" name — only the dot-prefixed staging
	// file, which is not a servable asset.
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "article", "hero.webp")); !os.IsNotExist(err) {
		t.Fatal("uncommitted upload made the real asset filename visible on disk")
	}
	entries, err := os.ReadDir(filepath.Join(contentRoot, "posts", "article"))
	if err != nil {
		t.Fatal(err)
	}
	var sawStaging bool
	for _, e := range entries {
		if e.Name() == "hero.webp" {
			t.Fatal("uncommitted upload's real filename appeared in the bundle directory listing")
		}
		if strings.HasPrefix(e.Name(), ".upload-") {
			sawStaging = true
		}
	}
	if !sawStaging {
		t.Fatal("expected a .upload-*.part staging file after a chunk was sent but never committed")
	}
}

// TestChunkedUploadUnrecognizedUploadIDIsNotFound confirms the tool layer
// (not just the store's unit tests) rejects an unrecognized upload_id with
// not_found rather than any other status. See
// TestChunkedUploadStorePrincipalIsolation for the direct proof that a
// *different* caller's upload_id is rejected the same way — that requires
// distinct caller identities, which this in-memory test harness (no OAuth
// context) cannot produce at the tool-call level.
// TestChunkedUploadConcurrentUploadsAreIsolated interleaves two independent
// begin/chunk/commit sequences for two different files in the same bundle,
// confirming their offsets, staging files, and final bytes never cross-
// contaminate — the concurrent-uploads scenario the issue calls out
// explicitly ("isolation by upload_id/principal").
func TestChunkedUploadConcurrentUploadsAreIsolated(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	payloadA := fakeWebP(900)
	payloadB := make([]byte, 900)
	copy(payloadB, []byte("RIFF\x00\x00\x00\x00WEBPVP8 "))
	for i := len(payloadB) - 1; i >= 20; i-- {
		payloadB[i] = byte((i * 7) % 251) // distinct byte pattern from payloadA
	}

	beginA := decodeWriteData(t, callTool(t, session, "begin_asset_upload", map[string]any{
		"slug": "posts/article", "filename": "a.webp", "size_bytes": len(payloadA),
	}))
	beginB := decodeWriteData(t, callTool(t, session, "begin_asset_upload", map[string]any{
		"slug": "posts/article", "filename": "b.webp", "size_bytes": len(payloadB),
	}))
	idA, idB := beginA["upload_id"].(string), beginB["upload_id"].(string)
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("expected two distinct non-empty upload ids, got %q and %q", idA, idB)
	}

	const chunkSize = 300
	// Interleave chunk delivery: A[0:300], B[0:300], A[300:600], B[300:600], ...
	for offset := 0; offset < len(payloadA); offset += chunkSize {
		end := offset + chunkSize
		resA := callTool(t, session, "upload_asset_chunk", map[string]any{
			"upload_id": idA, "offset": offset, "content_base64": b64(payloadA[offset:end]),
		})
		if resA.IsError {
			t.Fatalf("upload A chunk at %d: %s", offset, marshalContent(t, resA))
		}
		resB := callTool(t, session, "upload_asset_chunk", map[string]any{
			"upload_id": idB, "offset": offset, "content_base64": b64(payloadB[offset:end]),
		})
		if resB.IsError {
			t.Fatalf("upload B chunk at %d: %s", offset, marshalContent(t, resB))
		}
	}

	commitA := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": idA})
	if commitA.IsError {
		t.Fatalf("commit A: %s", marshalContent(t, commitA))
	}
	commitB := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": idB})
	if commitB.IsError {
		t.Fatalf("commit B: %s", marshalContent(t, commitB))
	}

	gotA, err := os.ReadFile(filepath.Join(contentRoot, "posts", "article", "a.webp"))
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(filepath.Join(contentRoot, "posts", "article", "b.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != string(payloadA) {
		t.Fatal("committed a.webp bytes do not match its own upload's payload (cross-contamination)")
	}
	if string(gotB) != string(payloadB) {
		t.Fatal("committed b.webp bytes do not match its own upload's payload (cross-contamination)")
	}
}

func TestChunkedUploadUnrecognizedUploadIDIsNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	commitRes := callTool(t, session, "commit_asset_upload", map[string]any{"upload_id": "upload_does-not-exist"})
	if !commitRes.IsError {
		t.Fatal("commit with a bogus upload_id unexpectedly succeeded")
	}
	if raw := marshalContent(t, commitRes); !strings.Contains(raw, "not_found") {
		t.Fatalf("error should be not_found for an unrecognized upload_id, got: %s", raw)
	}

	chunkRes := callTool(t, session, "upload_asset_chunk", map[string]any{
		"upload_id": "upload_does-not-exist", "offset": 0, "content_base64": b64([]byte("x")),
	})
	if !chunkRes.IsError {
		t.Fatal("chunk with a bogus upload_id unexpectedly succeeded")
	}
	if raw := marshalContent(t, chunkRes); !strings.Contains(raw, "not_found") {
		t.Fatalf("error should be not_found for an unrecognized upload_id, got: %s", raw)
	}
}
