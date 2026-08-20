package write

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// #1196: unit-level coverage of chunkedUploadStore's ordering/idempotency/
// isolation invariants, mirroring idempotencyStore's own direct-store test
// style (idempotency_test.go) rather than going through a full MCP session
// for every case.

func TestChunkedUploadStoreIdempotentChunkReplayIsNoop(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, entry, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 10, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	data := []byte("0123456789")
	result, next, err := store.appendChunk(id, "caller-a", 0, data, now)
	if err != nil {
		t.Fatalf("appendChunk (first): %v", err)
	}
	if result != chunkAccepted || next != 10 {
		t.Fatalf("first chunk: result=%v next=%d, want accepted/10", result, next)
	}

	// Exact replay: same offset, byte-identical content.
	result, next, err = store.appendChunk(id, "caller-a", 0, data, now)
	if err != nil {
		t.Fatalf("appendChunk (replay): %v", err)
	}
	if result != chunkIdempotentReplay || next != 10 {
		t.Fatalf("replay chunk: result=%v next=%d, want idempotentReplay/10", result, next)
	}

	// Staging file must contain the data exactly once, not duplicated.
	staged, err := os.ReadFile(entry.StagingPath)
	if err != nil {
		t.Fatalf("read staging file: %v", err)
	}
	if string(staged) != string(data) {
		t.Fatalf("staging file = %q, want %q (replay must not duplicate bytes)", staged, data)
	}
}

// TestChunkedUploadStoreOverlappingRetryDoesNotDoubleAppend reproduces the
// TOCTOU window an earlier version of appendChunk had: it released the
// store lock before writing to the staging file and re-acquired it after,
// so two calls for the same offset that overlap in that window could both
// pass the offset==ReceivedBytes check before either had incremented it —
// exactly what a client retrying a chunk after a timeout (the case
// chunkIdempotentReplay exists to serve) would do if its first attempt was
// merely slow, not actually lost. Both appends would land in the staging
// file and ReceivedBytes would overshoot SizeBytes, permanently breaking
// the upload (commit then fails incomplete_upload forever). Run with
// -race to also confirm no data race, though the double-append itself is a
// logical race -race cannot detect on its own (every map access here is
// properly mutex-guarded).
func TestChunkedUploadStoreOverlappingRetryDoesNotDoubleAppend(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	data := []byte("0123456789")
	id, entry, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", int64(len(data)), now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]chunkResult, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = store.appendChunk(id, "caller-a", 0, data, now)
		}(i)
	}
	wg.Wait()

	successes, replays := 0, 0
	for i := range 2 {
		if errs[i] != nil {
			t.Fatalf("appendChunk goroutine %d: unexpected error %v", i, errs[i])
		}
		switch results[i] {
		case chunkAccepted:
			successes++
		case chunkIdempotentReplay:
			replays++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("expected exactly one accepted and one idempotent-replay result, got successes=%d replays=%d", successes, replays)
	}

	staged, err := os.ReadFile(entry.StagingPath)
	if err != nil {
		t.Fatalf("read staging file: %v", err)
	}
	if string(staged) != string(data) {
		t.Fatalf("staging file = %q (len %d), want %q (len %d) — chunk was double-appended", staged, len(staged), data, len(data))
	}
	if entry.ReceivedBytes != int64(len(data)) {
		t.Fatalf("entry.ReceivedBytes = %d, want %d", entry.ReceivedBytes, len(data))
	}
}

func TestChunkedUploadStoreConflictingChunkAtSameOffsetRejected(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, _, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 10, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, _, err := store.appendChunk(id, "caller-a", 0, []byte("0123456789"), now); err != nil {
		t.Fatalf("appendChunk (first): %v", err)
	}
	_, _, err = store.appendChunk(id, "caller-a", 0, []byte("9999999999"), now)
	if err == nil {
		t.Fatal("expected chunk_conflict for different content at an already-received offset")
	}
	if got := err.Error(); !strings.Contains(got, "chunk_conflict") {
		t.Fatalf("error = %q, want chunk_conflict", got)
	}
}

func TestChunkedUploadStoreOutOfOrderChunkRejected(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, _, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 10, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Skips offset 0 entirely.
	_, _, err = store.appendChunk(id, "caller-a", 5, []byte("56789"), now)
	if err == nil {
		t.Fatal("expected out_of_order for a chunk that skips the expected offset")
	}
	if got := err.Error(); !strings.Contains(got, "out_of_order") {
		t.Fatalf("error = %q, want out_of_order", got)
	}
}

func TestChunkedUploadStoreOverLimitChunkRejected(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, _, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 5, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, _, err = store.appendChunk(id, "caller-a", 0, []byte("0123456789"), now)
	if err == nil {
		t.Fatal("expected invalid_params: chunk exceeding declared size_bytes must be rejected")
	}
}

func TestChunkedUploadStorePrincipalIsolation(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, _, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 10, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// A different caller must not be able to see, chunk, or commit
	// caller-a's upload — not_found, not a distinguishable permission error
	// (so probing can't tell "wrong owner" from "doesn't exist").
	if _, ok := store.get(id, "caller-b", now); ok {
		t.Fatal("caller-b could read caller-a's upload entry")
	}
	if _, _, err := store.appendChunk(id, "caller-b", 0, []byte("0123456789"), now); err == nil {
		t.Fatal("caller-b was able to append a chunk to caller-a's upload")
	}
	if _, ok := store.consume(id, "caller-b", now); ok {
		t.Fatal("caller-b was able to consume (commit) caller-a's upload")
	}

	// caller-a's own access must still work.
	if _, ok := store.get(id, "caller-a", now); !ok {
		t.Fatal("caller-a could not read its own upload entry")
	}
}

func TestChunkedUploadStoreTTLExpiryRemovesEntryAndStagingFile(t *testing.T) {
	dir := t.TempDir()
	store := newChunkedUploadStore(1 * time.Minute)
	begun := time.Now().UTC()
	id, entry, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 10, begun)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := os.Stat(entry.StagingPath); err != nil {
		t.Fatalf("staging file missing right after begin: %v", err)
	}

	past := begun.Add(2 * time.Minute)
	if _, ok := store.get(id, "caller-a", past); ok {
		t.Fatal("expired upload entry was still returned")
	}
	if _, err := os.Stat(entry.StagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging file survived TTL expiry: stat err = %v", err)
	}
}

func TestChunkedUploadStoreCommitConsumeLeavesNoStagingFileBehind(t *testing.T) {
	// Simulates the real commit path's contract: consume() (used after a
	// successful os.Rename) must not attempt to remove StagingPath — it no
	// longer exists at that path, the whole point of the rename.
	dir := t.TempDir()
	store := newChunkedUploadStore(chunkedUploadTTL)
	now := time.Now().UTC()
	id, entry, err := store.begin("caller-a", "posts/example", dir, "hero.png", ".png", "image/png", "", 5, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, _, err := store.appendChunk(id, "caller-a", 0, []byte("hello"), now); err != nil {
		t.Fatalf("appendChunk: %v", err)
	}
	destPath := filepath.Join(dir, entry.Filename)
	if err := os.Rename(entry.StagingPath, destPath); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, ok := store.consume(id, "caller-a", now); !ok {
		t.Fatal("consume did not find the entry")
	}
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("committed file missing after consume: %v", err)
	}
	if _, ok := store.get(id, "caller-a", now); ok {
		t.Fatal("upload_id still resolvable after commit — must be single-use")
	}
}

func TestSweepOrphanedUploadStagingRemovesStrayPartFiles(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "posts", "example")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "index.md"), []byte("---\ntitle: A\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(bundleDir, uploadStagingName("upload_deadbeef"))
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := sweepOrphanedUploadStaging(root)
	if err != nil {
		t.Fatalf("sweepOrphanedUploadStaging: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned staging file survived sweep: stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "index.md")); err != nil {
		t.Fatalf("sweep must not touch real content: %v", err)
	}
}
