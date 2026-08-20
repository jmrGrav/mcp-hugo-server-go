package write

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// validateExpectedSha256 validates the caller-optional expected-hash fields
// on begin/commit against the same "sha256:<64 lowercase hex>" shape
// contentmodel.SourceRevisionBytes produces, so a malformed value is
// rejected up front with invalid_params instead of silently never matching
// at commit.
func validateExpectedSha256(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if len(v) != len("sha256:")+64 || !strings.HasPrefix(v, "sha256:") {
		return "", fmt.Errorf("invalid_params: sha256 must be of the form \"sha256:<64 lowercase hex characters>\", matching the format returned by upload_page_asset/list_page_assets")
	}
	for _, r := range v[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("invalid_params: sha256 must be of the form \"sha256:<64 lowercase hex characters>\", matching the format returned by upload_page_asset/list_page_assets")
		}
	}
	return v, nil
}

type beginPageAssetUploadInput struct {
	Slug      string `json:"slug"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	Sha256    string `json:"sha256,omitempty"`
}

type beginPageAssetUploadOutput struct {
	toolcontract.ToolResponse[beginPageAssetUploadData]
	RateLimitRemaining int `json:"rate_limit_remaining"`
}

type beginPageAssetUploadData struct {
	UploadID              string           `json:"upload_id,omitempty"`
	Slug                  string           `json:"slug,omitempty"`
	SourceKey             string           `json:"source_key,omitempty"`
	Filename              string           `json:"filename,omitempty"`
	ContentType           string           `json:"content_type,omitempty"`
	NextOffset            int64            `json:"next_offset"`
	RecommendedChunkBytes int64            `json:"recommended_chunk_bytes,omitempty"`
	MaxAssetBytes         int64            `json:"max_asset_bytes,omitempty"`
	ExpiresAt             string           `json:"expires_at,omitempty"`
	RateLimit             *rateLimitBucket `json:"rate_limit,omitempty"`
	RateLimitRemaining    int              `json:"rate_limit_remaining,omitempty"`
}

func newBeginPageAssetUploadOutput(data beginPageAssetUploadData, rateLimitRemaining int) beginPageAssetUploadOutput {
	return beginPageAssetUploadOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

// registerBeginPageAssetUpload registers begin_asset_upload (#1196).
// Shares upload_page_asset's create/update quota bucket (mutationMu/
// mutationLimiters) — one token charged here, one more at commit; none per
// chunk (see upload_asset_chunk's own comment).
func registerBeginPageAssetUpload(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, mutationMu *sync.Mutex, mutationLimiters map[string]*rate.Limiter, uploads *chunkedUploadStore) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "begin_asset_upload",
		Title: "Begin chunked page asset upload",
		Description: "Start a chunked upload of a file (image, etc.) into an existing Hugo page bundle directory, for assets too large for upload_page_asset's inline base64 (which is comfortable up to roughly 1MiB and hard-capped well below the full 10MiB asset_max_bytes). " +
			"Follow with upload_asset_chunk calls (strictly in order, starting at next_offset) and finish with commit_asset_upload. " +
			"size_bytes is validated against asset_max_bytes (10MiB) immediately, before any bytes are transferred, so an oversized request fails fast at begin rather than after a long chunk sequence. " +
			"Optional sha256 (format \"sha256:<64 lowercase hex>\") is checked at commit if provided; omit it to skip that verification. " +
			"recommended_chunk_bytes is advisory only — any chunk size is accepted as long as chunks stay strictly ordered and the total never exceeds size_bytes. " +
			"The upload is scoped to your own session: only the same caller that began it can send chunks or commit it; another caller's upload_id always reports not_found, never a permission-denied distinction, so a probing caller can't learn whether an id merely doesn't exist or belongs to someone else. " +
			"expires_at is 15 minutes from begin; an upload not committed by then is abandoned and its staged bytes are discarded — nothing under content/ becomes visible from an expired or never-committed upload, matching upload_page_asset's own all-or-nothing write. " +
			"Only leaf page bundles (content/<slug>/index.md) have an asset directory; single-file pages (content/<slug>.md) fail with not_a_bundle, same as upload_page_asset. Allowed types: png, jpg, jpeg, gif, webp, svg. " +
			"rate_limit_remaining reports the caller's remaining budget on the shared create_page/update_page/upload_page_asset quota (#466); begin and commit each charge one token, chunks charge none. Requires write.",
		InputSchema:  tools.MustSchema[beginPageAssetUploadInput](),
		OutputSchema: tools.MustSchema[beginPageAssetUploadOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in beginPageAssetUploadInput) (*mcp.CallToolResult, beginPageAssetUploadOutput, error) {
		slug := normalizeInputSlug(in.Slug)
		filenameRaw := strings.TrimSpace(in.Filename)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: slug, Filename: filenameRaw})
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		if slug == "" {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		filename, ext, wantMIME, err := validateAssetFilename(in.Filename)
		if err != nil {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(err)
		}
		if in.SizeBytes <= 0 {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: size_bytes must be greater than 0"))
		}
		if in.SizeBytes > maxAssetBytes {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: size_bytes (%d) exceeds the maximum allowed asset size (%d bytes)", in.SizeBytes, maxAssetBytes))
		}
		expectedSha256, err := validateExpectedSha256(in.Sha256)
		if err != nil {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(err)
		}

		// Eligibility pre-check, same race-free-but-not-authoritative pattern
		// as upload_page_asset (#887): brief RLock, re-checked authoritatively
		// under a write-ish operation later (here, the staging file create
		// itself is exclusive-create, so a concurrent delete_page racing this
		// begin just leaves an orphaned staging file for the next sweep/TTL
		// to clean up rather than corrupting anything).
		hugosite.ContentMu.RLock()
		preLockErr := validateBundleSlug(idx, slug)
		hugosite.ContentMu.RUnlock()
		if preLockErr != nil {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(preLockErr)
		}

		if !limiter.Allow() {
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(rateLimitExceededErr("begin_asset_upload", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		dir, err := pg.SafeJoin(slug)
		if err != nil {
			slog.Warn("begin_asset_upload: path validation failed", "slug", slug, "error", err)
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: path validation failed"))
		}
		if err := pg.RevalidateForWrite(dir); err != nil {
			slog.Warn("begin_asset_upload: symlink-swap detected before staging", "slug", slug, "error", err)
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}

		now := time.Now().UTC()
		uploadID, entry, err := uploads.begin(isolationCallerKey(ctx), slug, dir, filename, ext, wantMIME, expectedSha256, in.SizeBytes, now)
		if err != nil {
			slog.Error("begin_asset_upload: failed to create staging file", "slug", slug, "error", err)
			return nil, beginPageAssetUploadOutput{}, wrapErrWithLimiter(err)
		}

		return nil, newBeginPageAssetUploadOutput(beginPageAssetUploadData{
			UploadID:              uploadID,
			Slug:                  canonicalPublicSlug(slug),
			SourceKey:             slug,
			Filename:              filename,
			ContentType:           wantMIME,
			NextOffset:            0,
			RecommendedChunkBytes: recommendedChunkBytes,
			MaxAssetBytes:         maxAssetBytes,
			ExpiresAt:             entry.ExpiresAt.Format(time.RFC3339),
			RateLimit:             ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, now)),
		}, rateLimitRemaining(limiter)), nil
	}))
}

type uploadPageAssetChunkInput struct {
	UploadID      string `json:"upload_id"`
	Offset        int64  `json:"offset"`
	ContentBase64 string `json:"content_base64"`
}

type uploadPageAssetChunkOutput struct {
	toolcontract.ToolResponse[uploadPageAssetChunkData]
}

type uploadPageAssetChunkData struct {
	UploadID      string `json:"upload_id,omitempty"`
	ReceivedBytes int64  `json:"received_bytes"`
	NextOffset    int64  `json:"next_offset"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Complete      bool   `json:"complete,omitempty"`
	Replayed      bool   `json:"replayed,omitempty"`
}

func newUploadPageAssetChunkOutput(data uploadPageAssetChunkData) uploadPageAssetChunkOutput {
	return uploadPageAssetChunkOutput{ToolResponse: writeSuccessEnvelope(data)}
}

// registerUploadPageAssetChunk registers upload_asset_chunk (#1196).
// Deliberately charges no rate-limit token per call: begin already charged
// one token and validated size_bytes against asset_max_bytes, so the total
// bytes any single upload can ever move is already bounded regardless of
// how many chunk calls it takes — metering per-chunk here would only make
// a large upload artificially expensive against the same quota
// create_page/update_page share, without closing any DoS gap begin's
// size_bytes cap doesn't already close.
func registerUploadPageAssetChunk(s *mcp.Server, uploads *chunkedUploadStore) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "upload_asset_chunk",
		Title: "Upload a chunk of a page asset",
		Description: "Send one chunk of an in-progress begin_asset_upload. offset must exactly equal the next_offset from the previous begin/upload_asset_chunk response — chunks are strictly ordered, there is no random-access write. " +
			"Retrying the exact same chunk (same offset, byte-identical content) after a timeout or uncertain delivery is always safe and returns the same next_offset without re-writing anything (replayed:true in the response). Resending a different chunk at an offset that already has different content on file fails with chunk_conflict — start a new begin_asset_upload if the source data itself changed mid-transfer. " +
			"A chunk that would push received bytes past the size_bytes declared at begin fails with invalid_params before anything is written. " +
			"complete:true means received_bytes has reached size_bytes and the upload is ready for commit_asset_upload — it does not mean the asset is validated or visible yet; nothing under content/ changes until commit succeeds. " +
			"Charges no rate-limit quota (begin and commit each charge one token on the shared create_page/update_page/upload_page_asset budget; chunks are free since begin's size_bytes check already bounds the total transfer). Requires write.",
		InputSchema:  tools.MustSchema[uploadPageAssetChunkInput](),
		OutputSchema: tools.MustSchema[uploadPageAssetChunkOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in uploadPageAssetChunkInput) (*mcp.CallToolResult, uploadPageAssetChunkOutput, error) {
		uploadID := strings.TrimSpace(in.UploadID)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{})
		}
		if uploadID == "" {
			return nil, uploadPageAssetChunkOutput{}, wrapErr(fmt.Errorf("invalid_params: upload_id must not be empty"))
		}
		if in.Offset < 0 {
			return nil, uploadPageAssetChunkOutput{}, wrapErr(fmt.Errorf("invalid_params: offset must not be negative"))
		}
		data, err := decodeAssetBase64(in.ContentBase64)
		if err != nil {
			return nil, uploadPageAssetChunkOutput{}, wrapErr(err)
		}
		if len(data) == 0 {
			return nil, uploadPageAssetChunkOutput{}, wrapErr(fmt.Errorf("invalid_params: chunk content_base64 decodes to zero bytes"))
		}

		callerKey := isolationCallerKey(ctx)
		result, nextOffset, err := uploads.appendChunk(uploadID, callerKey, in.Offset, data, time.Now().UTC())
		if err != nil {
			return nil, uploadPageAssetChunkOutput{}, wrapErr(err)
		}

		entry, ok := uploads.get(uploadID, callerKey, time.Now().UTC())
		var sizeBytes int64
		if ok {
			sizeBytes = entry.SizeBytes
		}

		return nil, newUploadPageAssetChunkOutput(uploadPageAssetChunkData{
			UploadID:      uploadID,
			ReceivedBytes: nextOffset,
			NextOffset:    nextOffset,
			SizeBytes:     sizeBytes,
			Complete:      sizeBytes > 0 && nextOffset == sizeBytes,
			Replayed:      result == chunkIdempotentReplay,
		}), nil
	}))
}

// registerCommitPageAssetUpload registers commit_asset_upload (#1196).
// Routes assembled bytes through validateAssetBytes — the exact same gate
// upload_page_asset uses (extracted in #1202 specifically for this) — so a
// chunked upload can never bypass the MIME-sniff/SVG-sanitizer/size checks
// the inline path enforces.
func registerCommitPageAssetUpload(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, idem *idempotencyStore, mutationMu *sync.Mutex, mutationLimiters map[string]*rate.Limiter, changeSets *changeset.Registry, uploads *chunkedUploadStore) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "commit_asset_upload",
		Title: "Commit a chunked page asset upload",
		Description: "Finish a begin_asset_upload/upload_asset_chunk sequence, writing the assembled file into its destination page bundle. Fails with incomplete_upload if received bytes don't yet equal the size_bytes declared at begin — finish sending chunks first. " +
			"The assembled bytes go through the exact same validation as upload_page_asset: for png/jpg/jpeg/gif/webp the bytes are sniffed to confirm they match the declared extension (never trusting the caller's claim), and svg uploads are checked by the same strict structural parser (see upload_page_asset's description for the full svg allowlist/rejection list) — never a separate, potentially weaker check. " +
			"If expected_sha256 was given at begin, a mismatch here fails with sha256_mismatch and discards the upload (start over with begin_asset_upload; the same upload_id cannot be retried after this). " +
			"This tool never overwrites: fails with already_exists if filename is already taken in this bundle, same as upload_page_asset. " +
			"Callers may provide idempotency_key to safely replay the exact same commit after a timeout or uncertain delivery. " +
			"rate_limit_remaining reports the caller's remaining budget on the shared create_page/update_page/upload_page_asset quota (#466); begin and commit each charge one token. Requires write.",
		InputSchema:  tools.MustSchema[commitPageAssetUploadInput](),
		OutputSchema: tools.MustSchema[uploadPageAssetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in commitPageAssetUploadInput) (*mcp.CallToolResult, uploadPageAssetOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		uploadID := strings.TrimSpace(in.UploadID)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{})
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		if uploadID == "" {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: upload_id must not be empty"))
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(err)
		}
		resolvedChangeSetID, err := changeSets.Resolve(ctx, in.ChangeSetID, time.Now().UTC())
		if err != nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(err)
		}

		isolationKey := isolationCallerKey(ctx)
		now := time.Now().UTC()
		entry, ok := uploads.get(uploadID, isolationKey, now)
		if !ok {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("not_found: upload_id %q not found or expired", uploadID))
		}
		if entry.ReceivedBytes != entry.SizeBytes {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("incomplete_upload: received %d of %d declared bytes; keep calling upload_asset_chunk from next_offset before committing", entry.ReceivedBytes, entry.SizeBytes))
		}

		if !in.DryRun && !limiter.Allow() {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(rateLimitExceededErr("commit_asset_upload", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		stagedData, readErr := os.ReadFile(entry.StagingPath)
		if readErr != nil {
			slog.Error("commit_asset_upload: failed to read staged bytes", "upload_id", uploadID, "error", readErr)
			uploads.abandon(uploadID, isolationKey, now)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read staged upload data"))
		}
		if err := validateAssetBytes(stagedData, entry.Ext, entry.WantMIME); err != nil {
			uploads.abandon(uploadID, isolationKey, now)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(err)
		}
		actualHash := contentmodel.SourceRevisionBytes(stagedData)
		if entry.ExpectedSha256 != "" && entry.ExpectedSha256 != actualHash {
			uploads.abandon(uploadID, isolationKey, now)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("sha256_mismatch: assembled upload hash %s does not match sha256 declared at begin_asset_upload (%s)", actualHash, entry.ExpectedSha256))
		}
		expectedSha256, err := validateExpectedSha256(in.ExpectedSha256)
		if err != nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(err)
		}
		if expectedSha256 != "" && expectedSha256 != actualHash {
			uploads.abandon(uploadID, isolationKey, now)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("sha256_mismatch: assembled upload hash %s does not match commit's own expected_sha256 (%s)", actualHash, expectedSha256))
		}

		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			h, hashErr := requestHash(struct {
				Slug     string `json:"slug"`
				Filename string `json:"filename"`
				Sha256   string `json:"sha256"`
			}{Slug: entry.Slug, Filename: entry.Filename, Sha256: actualHash})
			if hashErr != nil {
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = h
			var cached uploadPageAssetOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "commit_asset_upload", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filepath.Join(entry.Dir, entry.Filename))
		if in.DryRun {
			return nil, newUploadPageAssetOutput(uploadPageAssetData{
				Status:      "would_create",
				Slug:        canonicalPublicSlug(entry.Slug),
				SourceKey:   entry.Slug,
				Filename:    entry.Filename,
				Path:        logicalPath,
				ContentType: entry.WantMIME,
				SizeBytes:   len(stagedData),
				Sha256:      actualHash,
				DryRun:      true,
				RateLimit:   ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, now)),
			}, rateLimitRemaining(limiter)), nil
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("commit_asset_upload: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("commit_asset_upload: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("commit_asset_upload: lock_released")
		}()

		if err := validateBundleSlug(idx, entry.Slug); err != nil {
			uploads.abandon(uploadID, isolationKey, now)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(err)
		}
		destPath, err := pg.SafeJoin(filepath.Join(entry.Slug, entry.Filename))
		if err != nil {
			slog.Warn("commit_asset_upload: path validation failed", "slug", entry.Slug, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: path validation failed"))
		}
		if err := pg.RevalidateForWrite(destPath); err != nil {
			slog.Warn("commit_asset_upload: symlink-swap detected before write", "slug", entry.Slug, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if _, statErr := os.Stat(destPath); statErr == nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("already_exists: asset already exists at %q", entry.Filename))
		} else if !os.IsNotExist(statErr) {
			slog.Error("commit_asset_upload: stat failed", "slug", entry.Slug, "filename", entry.Filename, "error", statErr)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to inspect destination path"))
		}

		duplicateOf, dupErr := findDuplicateAsset(entry.Dir, stagedData)
		if dupErr != nil {
			slog.Warn("commit_asset_upload: duplicate scan failed", "slug", entry.Slug, "error", dupErr)
		}

		// Same-directory rename: entry.StagingPath and destPath share entry.Dir
		// as their parent, so this is always on the same filesystem/mount and
		// atomic by construction — no copy-then-delete fallback needed.
		if err := os.Rename(entry.StagingPath, destPath); err != nil {
			slog.Error("commit_asset_upload: rename failed", "slug", entry.Slug, "filename", entry.Filename, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to finalize asset"))
		}
		// The staging file is gone (renamed to its final destination), so
		// consume (not abandon, which would also try to remove StagingPath).
		uploads.consume(uploadID, isolationKey, now)

		out := newUploadPageAssetOutput(uploadPageAssetData{
			Status:      "created",
			Slug:        canonicalPublicSlug(entry.Slug),
			SourceKey:   entry.Slug,
			Filename:    entry.Filename,
			Path:        logicalPath,
			ContentType: entry.WantMIME,
			SizeBytes:   len(stagedData),
			Sha256:      actualHash,
			DuplicateOf: duplicateOf,
			RateLimit:   ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, now)),
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "commit_asset_upload", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("commit_asset_upload: could not persist idempotency result", "slug", entry.Slug, "error", err)
			}
		}
		changeSets.RecordMutation(resolvedChangeSetID, mutationCallerKey(ctx), "commit_asset_upload", entry.Slug, "create", time.Now().UTC())
		return nil, out, nil
	}))
}

type commitPageAssetUploadInput struct {
	UploadID       string `json:"upload_id"`
	ExpectedSha256 string `json:"expected_sha256,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ChangeSetID (#1135) — see createPageInput's field of the same name.
	ChangeSetID string `json:"change_set_id,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
}
