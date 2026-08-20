package write

// Chunked page-asset upload (#1196), split off #1190: begin_asset_upload
// / upload_asset_chunk / commit_asset_upload make the top half of
// the advertised asset_max_bytes range (up to maxAssetBytes) reachable
// through the MCP protocol, not just the ~1MiB-ish inline base64 ceiling
// upload_page_asset practically has. Design (see the issue and PR #1202,
// which extracted validateAssetBytes specifically so this file's commit
// path can share it):
//
//   - Chunks are staged directly inside the destination bundle directory as
//     a dot-prefixed file (content/<slug>/.upload-<id>.part) — never a
//     separate temp root outside ContentRoot. This keeps the final commit a
//     same-directory os.Rename, which is atomic by definition (no
//     cross-filesystem/cross-mount question at all). assetFilenamePattern
//     already rejects any committed asset name starting with ".", so a
//     staging file can never collide with or be mistaken for a real asset,
//     and hugosite's SourceIndex walk only matches "*.md" so it's invisible
//     to the content index regardless.
//   - The store is in-memory only, like planStore/bundlePlanStore — nothing
//     needs to resume an upload across a server restart (the issue's own
//     "interruption leaves the site untouched" requirement is satisfied by
//     the staging file simply never being committed), so there is no SQLite
//     persistence path here and no reconciliation on restart. Instead,
//     sweepOrphanedUploadStaging removes every stray .upload-*.part file
//     under ContentRoot once at server startup: since the store always
//     starts empty in a fresh process, any such file found is by definition
//     an orphan left by a previous process life.
//   - Upload ownership is scoped to isolationCallerKey (the exact bearer
//     token), mirroring plan/rollback-snapshot ownership (#932/#934) rather
//     than the broader per-principal rate-limit key — a second session
//     under the same OAuth principal must not be able to see or continue
//     another session's in-flight upload.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

// chunkedUploadTTL bounds how long an incomplete upload's staging file and
// store entry survive before being treated as abandoned. 15 minutes matches
// the issue's own suggested TTL — long enough for a real multi-MB transfer
// over a slow connection, short enough that an abandoned upload's staging
// file doesn't linger indefinitely inside a live bundle directory.
const chunkedUploadTTL = 15 * time.Minute

// recommendedChunkBytes is advisory only (returned in begin's response to
// help a well-behaved caller pick a chunk size); nothing here enforces it —
// the real bound is SizeBytes, validated against maxAssetBytes at begin and
// against cumulative received bytes on every chunk.
const recommendedChunkBytes = 512 << 10 // 512 KiB

const (
	uploadStagingPrefix = ".upload-"
	uploadStagingSuffix = ".part"
)

// uploadStagingName returns the dot-prefixed staging filename for uploadID
// inside its destination bundle directory. Never passed through
// PathGuard.SafeJoin (which rejects any dot-led path component by design —
// see pathguard.go) since it is not itself a caller-addressable asset path;
// it is always joined onto a directory that was already validated via
// SafeJoin/validateBundleSlug.
func uploadStagingName(uploadID string) string {
	return uploadStagingPrefix + uploadID + uploadStagingSuffix
}

// chunkRecord is one chunk this upload has durably accepted, keyed by its
// starting offset — kept so a retried chunk call (same offset, same bytes)
// can be recognized as an idempotent no-op instead of either re-appending
// duplicate bytes or being misread as an out-of-order write.
type chunkRecord struct {
	Length int64
	Sha256 string
}

// chunkedUploadEntry is one begin_asset_upload's server-held state.
type chunkedUploadEntry struct {
	CallerKey      string
	Slug           string
	Dir            string // validated bundle directory (pg.SafeJoin(slug) result, captured at begin)
	Filename       string
	Ext            string
	WantMIME       string
	SizeBytes      int64
	ExpectedSha256 string // optional, "" if caller didn't declare one at begin
	StagingPath    string
	ReceivedBytes  int64
	Chunks         map[int64]chunkRecord
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type chunkedUploadStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*chunkedUploadEntry
}

func newChunkedUploadStore(ttl time.Duration) *chunkedUploadStore {
	return &chunkedUploadStore{ttl: ttl, entries: make(map[string]*chunkedUploadEntry)}
}

// newUploadID mirrors newPlanID's shape (crypto/rand + hex, prefixed) — see
// content_plan.go.
func newUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "upload_" + hex.EncodeToString(b), nil
}

// pruneExpiredLocked removes every entry past its TTL, deleting its staging
// file as a side effect. Called opportunistically at the start of every
// store operation (mirrors previewstore.Store's lazy-expiry-on-access
// pattern) so abandoned uploads don't accumulate indefinitely even if no
// caller ever revisits them.
func (s *chunkedUploadStore) pruneExpiredLocked(now time.Time) {
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			_ = os.Remove(entry.StagingPath)
			delete(s.entries, id)
		}
	}
}

// begin creates a new upload entry and its empty staging file. Returns
// invalid_params if a staging file with the generated id somehow already
// exists (astronomically unlikely with 16 random bytes, but checked rather
// than silently truncating something else's data).
func (s *chunkedUploadStore) begin(callerKey, slug, dir, filename, ext, wantMIME, expectedSha256 string, sizeBytes int64, now time.Time) (id string, entry *chunkedUploadEntry, err error) {
	id, err = newUploadID()
	if err != nil {
		return "", nil, fmt.Errorf("internal_error: failed to generate upload id")
	}
	stagingPath := filepath.Join(dir, uploadStagingName(id))
	f, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("write_error: failed to create upload staging file")
	}
	f.Close()

	entry = &chunkedUploadEntry{
		CallerKey:      callerKey,
		Slug:           slug,
		Dir:            dir,
		Filename:       filename,
		Ext:            ext,
		WantMIME:       wantMIME,
		SizeBytes:      sizeBytes,
		ExpectedSha256: expectedSha256,
		StagingPath:    stagingPath,
		Chunks:         make(map[int64]chunkRecord),
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	s.entries[id] = entry
	return id, entry, nil
}

// chunkResult distinguishes a genuinely new chunk from an idempotent replay,
// so the caller can decide whether to append bytes to the staging file.
type chunkResult int

const (
	chunkAccepted chunkResult = iota
	chunkIdempotentReplay
)

// appendChunk validates and (if new) appends one chunk's bytes to id's
// staging file, enforcing strict offset ordering with one deliberate
// exception: a chunk whose offset exactly matches a chunk already recorded
// is treated as a replay — accepted as a no-op if its bytes are byte-for-
// byte identical to what was already received there (same length, same
// hash), rejected as a conflict otherwise. A gap (offset does not match the
// current end of the upload, and does not match any already-received
// chunk's start) is rejected as out_of_order.
func (s *chunkedUploadStore) appendChunk(id, callerKey string, offset int64, data []byte, now time.Time) (result chunkResult, nextOffset int64, err error) {
	s.mu.Lock()
	s.pruneExpiredLocked(now)
	entry, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return 0, 0, fmt.Errorf("not_found: upload_id %q not found or expired", id)
	}
	if entry.CallerKey != callerKey {
		s.mu.Unlock()
		return 0, 0, fmt.Errorf("not_found: upload_id %q not found or expired", id)
	}

	if rec, seen := entry.Chunks[offset]; seen {
		s.mu.Unlock()
		if rec.Length == int64(len(data)) && rec.Sha256 == contentmodel.SourceRevisionBytes(data) {
			return chunkIdempotentReplay, entry.ReceivedBytes, nil
		}
		return 0, 0, fmt.Errorf("chunk_conflict: a different chunk was already received at offset %d; upload_asset_chunk is not overwrite-capable — start a new upload if the source data changed", offset)
	}
	if offset != entry.ReceivedBytes {
		nb := entry.ReceivedBytes
		s.mu.Unlock()
		return 0, 0, fmt.Errorf("out_of_order: expected offset %d, got %d; chunks must be uploaded strictly in order starting from next_offset", nb, offset)
	}
	if entry.ReceivedBytes+int64(len(data)) > entry.SizeBytes {
		s.mu.Unlock()
		return 0, 0, fmt.Errorf("invalid_params: chunk would push received bytes (%d) past the declared size_bytes (%d) for this upload", entry.ReceivedBytes+int64(len(data)), entry.SizeBytes)
	}
	stagingPath := entry.StagingPath
	s.mu.Unlock()

	f, ferr := os.OpenFile(stagingPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if ferr != nil {
		return 0, 0, fmt.Errorf("write_error: failed to open upload staging file")
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return 0, 0, fmt.Errorf("write_error: failed to write chunk to upload staging file")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-fetch: the entry could have expired and been pruned by a
	// concurrent call while the file write above was in flight (unlocked).
	entry, ok = s.entries[id]
	if !ok {
		return 0, 0, fmt.Errorf("not_found: upload_id %q not found or expired", id)
	}
	entry.Chunks[offset] = chunkRecord{Length: int64(len(data)), Sha256: contentmodel.SourceRevisionBytes(data)}
	entry.ReceivedBytes += int64(len(data))
	return chunkAccepted, entry.ReceivedBytes, nil
}

// get returns id's entry without consuming it, scoped to callerKey.
func (s *chunkedUploadStore) get(id, callerKey string, now time.Time) (*chunkedUploadEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	entry, ok := s.entries[id]
	if !ok || entry.CallerKey != callerKey {
		return nil, false
	}
	return entry, true
}

// consume removes id's entry from the store without touching its staging
// file — used on a successful commit, where the caller (commit's own
// handler) has already renamed the staging file into its final destination,
// so there is nothing left at StagingPath to clean up.
func (s *chunkedUploadStore) consume(id, callerKey string, now time.Time) (*chunkedUploadEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	entry, ok := s.entries[id]
	if !ok || entry.CallerKey != callerKey {
		return nil, false
	}
	delete(s.entries, id)
	return entry, true
}

// abandon removes id's entry and deletes its staging file — used when a
// commit attempt fails validation (e.g. sha256_mismatch) after the upload
// is otherwise complete, so a caller isn't left holding a doomed upload_id
// that will only ever fail the same way again until it separately expires.
func (s *chunkedUploadStore) abandon(id, callerKey string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	entry, ok := s.entries[id]
	if !ok || entry.CallerKey != callerKey {
		return
	}
	_ = os.Remove(entry.StagingPath)
	delete(s.entries, id)
}

// sweepOrphanedUploadStaging removes every stray chunked-upload staging file
// under contentRoot. Called once at server startup (write.Register): the
// in-memory chunkedUploadStore always starts empty in a fresh process, so
// any .upload-*.part file already on disk at that point is definitionally
// orphaned residue from a previous process life (a crash, a restart, or an
// upload the caller simply abandoned) — there is no live entry it could
// still belong to. Returns the count removed for startup logging.
func sweepOrphanedUploadStaging(contentRoot string) (int, error) {
	contentRoot = strings.TrimSpace(contentRoot)
	if contentRoot == "" {
		return 0, nil
	}
	if info, err := os.Stat(contentRoot); err != nil || !info.IsDir() {
		return 0, nil
	}
	removed := 0
	err := filepath.WalkDir(contentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable entries, keep walking
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, uploadStagingPrefix) && strings.HasSuffix(name, uploadStagingSuffix) {
			if rmErr := os.Remove(path); rmErr == nil {
				removed++
			}
		}
		return nil
	})
	return removed, err
}
