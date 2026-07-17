package write

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

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

// maxAssetBytes bounds the decoded size of an uploaded page-bundle asset,
// matching the cap already used for generated hero images
// (internal/tools/admin/image.go's maxImageBytes) for consistency.
const maxAssetBytes = 10 << 20

// assetFilenamePattern requires a single path component (no "/") that does
// not start with "." (blocks hidden files and, since ".." starts with ".",
// directory traversal via the filename itself).
var assetFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$`)

// allowedAssetTypes intentionally excludes SVG: SVG can carry <script>,
// event-handler attributes, and external entity references that a simple
// allowlist or hand-rolled sanitizer cannot safely neutralize. Accepting SVG
// uploads needs a real sanitization story (a follow-up), not a v1 shortcut.
var allowedAssetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

type uploadPageAssetInput struct {
	Slug           string `json:"slug"`
	Filename       string `json:"filename"`
	ContentBase64  string `json:"content_base64"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type uploadPageAssetOutput struct {
	toolcontract.ToolResponse[map[string]any]
	Status      string `json:"status,omitempty"`
	Slug        string `json:"slug"`
	Filename    string `json:"filename"`
	Path        string `json:"path,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int    `json:"size_bytes,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

// validateAssetFilename checks that name is a single safe path component
// with an allowed image extension, returning the cleaned filename (trimmed
// of surrounding whitespace — the pattern match below only proves the
// *trimmed* form is safe, so callers must use this returned name, not the
// original input, for the write path and response), the lowercased
// extension, and the expected sniffed MIME type.
func validateAssetFilename(name string) (clean, ext, wantMIME string, err error) {
	clean = strings.TrimSpace(name)
	if !assetFilenamePattern.MatchString(clean) {
		return "", "", "", fmt.Errorf("invalid_params: filename must be a single path component matching %s", assetFilenamePattern.String())
	}
	ext = strings.ToLower(filepath.Ext(clean))
	wantMIME, ok := allowedAssetTypes[ext]
	if !ok {
		return "", "", "", fmt.Errorf("invalid_params: filename extension %q is not an allowed asset type (png, jpg, jpeg, gif, webp)", ext)
	}
	return clean, ext, wantMIME, nil
}

// decodeAndValidateAssetContent decodes base64 asset content, enforces the
// size cap on the decoded bytes, and sniffs the actual content to confirm it
// matches the extension the caller declared. Callers must never trust a
// caller-supplied content-type; only the sniffed bytes decide the MIME type.
func decodeAndValidateAssetContent(b64, ext, wantMIME string) ([]byte, error) {
	if strings.TrimSpace(b64) == "" {
		return nil, fmt.Errorf("invalid_params: content_base64 must not be empty")
	}
	// Reject absurdly large encoded payloads before allocating a decode buffer.
	if len(b64) > maxAssetBytes*2 {
		return nil, fmt.Errorf("invalid_params: asset content exceeds the maximum allowed size")
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("invalid_params: content_base64 is not valid base64")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("invalid_params: decoded asset content is empty")
	}
	if len(data) > maxAssetBytes {
		return nil, fmt.Errorf("invalid_params: decoded asset content exceeds %d bytes", maxAssetBytes)
	}
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	sniffed := http.DetectContentType(data[:sniffLen])
	base, _, _ := strings.Cut(sniffed, ";")
	if strings.TrimSpace(base) != wantMIME {
		return nil, fmt.Errorf("invalid_params: uploaded content does not match declared extension %q (sniffed %q)", ext, sniffed)
	}
	return data, nil
}

// validateBundleSlug confirms slug names an existing leaf page bundle
// (content/<slug>/index[.lang].md), not a single-file page. Single-file
// pages have no per-page directory: content/<slug>.md's parent directory is
// shared by every sibling page in that section, so writing an asset there
// would land in a directory the caller does not own.
func validateBundleSlug(idx *hugosite.SourceIndex, slug string) error {
	existing, ok := idx.GetBySlug(slug)
	if !ok {
		return fmt.Errorf("not_found: page not found")
	}
	if !strings.HasPrefix(filepath.Base(existing.FilePath), "index.") {
		return fmt.Errorf("not_a_bundle: slug %q is a single-file page with no bundle directory for assets", slug)
	}
	return nil
}

// findDuplicateAsset returns the filename of an existing sibling asset in
// dir whose content hash matches data, or "" if none matches. Used only as
// an advisory signal (per #348's "duplicate detection by hash where
// practical") — it does not block or skip the requested write. This runs
// under the global content lock, so siblings are size-filtered before any
// full read+hash: for an image-heavy bundle, a same-size collision is rare,
// making this usually zero extra reads instead of reading every sibling in
// full on every upload.
func findDuplicateAsset(dir string, data []byte) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	want := contentmodel.SourceRevisionBytes(data)
	wantSize := int64(len(data))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() != wantSize {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if contentmodel.SourceRevisionBytes(raw) == want {
			return name, nil
		}
	}
	return "", nil
}

// registerUploadPageAsset registers upload_page_asset. Separate function
// (mirrors registerListContentTypes's split from Register in the read
// package) called from Register with the idempotency store it already owns.
func registerUploadPageAsset(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, idem *idempotencyStore, mutationMu *sync.Mutex, mutationLimiters map[string]*rate.Limiter) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "upload_page_asset",
		Title: "Upload page asset",
		Description: "Write a new file (image, etc.) into an existing Hugo page bundle directory, alongside its index.md. " +
			"Only leaf page bundles (content/<slug>/index.md) have an asset directory; single-file pages (content/<slug>.md) fail with not_a_bundle. " +
			"Allowed types: png, jpg, jpeg, gif, webp. SVG is not supported yet: safe SVG sanitization needs a real parser, not an allowlist, and is deferred to a follow-up. " +
			"Content is provided as base64 in content_base64 (max 10MB decoded); the bytes are sniffed to confirm they actually match the declared extension, never trusting a caller-supplied content type. " +
			"This tool never overwrites: fails with already_exists if filename is already taken in this bundle. " +
			"If identical content already exists under a different filename in the same bundle, the response includes duplicate_of as an advisory only — the file is still written under the requested name. " +
			"Callers may provide idempotency_key to safely replay the exact same upload after a timeout or uncertain delivery. Requires content.write.",
		InputSchema:  tools.MustSchema[uploadPageAssetInput](),
		OutputSchema: tools.MustSchema[uploadPageAssetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in uploadPageAssetInput) (*mcp.CallToolResult, uploadPageAssetOutput, error) {
		slug := normalizeInputSlug(in.Slug)
		if slug == "" {
			return nil, uploadPageAssetOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		filename, ext, wantMIME, err := validateAssetFilename(in.Filename)
		if err != nil {
			return nil, uploadPageAssetOutput{}, err
		}
		callerKey := mutationCallerKey(ctx)
		if !callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin).Allow() {
			return nil, uploadPageAssetOutput{}, fmt.Errorf("rate_limit_exceeded: upload_page_asset is limited to %d per minute", cfg.RateLimit.CreateUpdatePerMin)
		}
		data, err := decodeAndValidateAssetContent(in.ContentBase64, ext, wantMIME)
		if err != nil {
			return nil, uploadPageAssetOutput{}, err
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("upload_page_asset: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("upload_page_asset: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, uploadPageAssetOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("upload_page_asset: lock_released")
		}()

		if err := validateBundleSlug(idx, slug); err != nil {
			return nil, uploadPageAssetOutput{}, err
		}
		dir, err := pg.SafeJoin(slug)
		if err != nil {
			slog.Warn("upload_page_asset: path validation failed", "slug", slug, "error", err)
			return nil, uploadPageAssetOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		filePath := filepath.Join(dir, filename)

		hash := contentmodel.SourceRevisionBytes(data)
		duplicateOf, dupErr := findDuplicateAsset(dir, data)
		if dupErr != nil {
			slog.Warn("upload_page_asset: duplicate scan failed", "slug", slug, "error", dupErr)
		}

		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			h, hashErr := requestHash(struct {
				Slug     string `json:"slug"`
				Filename string `json:"filename"`
				Sha256   string `json:"sha256"`
			}{Slug: slug, Filename: filename, Sha256: hash})
			if hashErr != nil {
				return nil, uploadPageAssetOutput{}, fmt.Errorf("internal_error: failed to hash idempotency request")
			}
			idemHash = h
			var cached uploadPageAssetOutput
			hit, replayErr := idem.replay("upload_page_asset", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, uploadPageAssetOutput{}, replayErr
			}
			if hit {
				return nil, cached, nil
			}
		}

		if _, statErr := os.Stat(filePath); statErr == nil {
			return nil, uploadPageAssetOutput{}, fmt.Errorf("already_exists: asset already exists at %q", filename)
		} else if !os.IsNotExist(statErr) {
			slog.Error("upload_page_asset: stat failed", "slug", slug, "filename", filename, "error", statErr)
			return nil, uploadPageAssetOutput{}, fmt.Errorf("read_error: failed to inspect destination path")
		}

		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
		if in.DryRun {
			return nil, uploadPageAssetOutput{
				ToolResponse: writeSuccessEnvelope(),
				Status:       "ok",
				Slug:         slug,
				Filename:     filename,
				Path:         logicalPath,
				ContentType:  wantMIME,
				SizeBytes:    len(data),
				Sha256:       hash,
				DuplicateOf:  duplicateOf,
				DryRun:       true,
			}, nil
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("upload_page_asset: symlink-swap detected before write", "slug", slug, "error", err)
			return nil, uploadPageAssetOutput{}, fmt.Errorf("security_error: symlink detected in write path")
		}
		if err := fileutil.AtomicCreateCheckedBytes(filePath, data, pg); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, uploadPageAssetOutput{}, fmt.Errorf("already_exists: asset already exists at %q", filename)
			}
			slog.Error("upload_page_asset: write failed", "slug", slug, "filename", filename, "error", err)
			return nil, uploadPageAssetOutput{}, fmt.Errorf("write_error: failed to write asset")
		}

		out := uploadPageAssetOutput{
			ToolResponse: writeSuccessEnvelope(),
			Status:       "ok",
			Slug:         slug,
			Filename:     filename,
			Path:         logicalPath,
			ContentType:  wantMIME,
			SizeBytes:    len(data),
			Sha256:       hash,
			DuplicateOf:  duplicateOf,
		}
		if idemHash != "" {
			if err := idem.remember("upload_page_asset", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("upload_page_asset: could not persist idempotency result", "slug", slug, "error", err)
			}
		}
		return nil, out, nil
	}))
}
