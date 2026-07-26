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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
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

// allowedAssetTypes. SVG (#571) is gated behind validateSVGContent's strict
// element/attribute allowlist, not just this extension check — see the
// ext == ".svg" branch in decodeAndValidateAssetContent. Go's
// http.DetectContentType has no SVG signature (it sniffs SVG bytes as
// text/plain or text/xml, never "image/svg+xml"), so the generic
// sniffed-vs-declared MIME check below doesn't apply to SVG; the structural
// validator is the actual "is this really SVG, and is it safe" check for
// that type.
var allowedAssetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

type uploadPageAssetInput struct {
	Slug           string `json:"slug"`
	Filename       string `json:"filename"`
	ContentBase64  string `json:"content_base64"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type uploadPageAssetOutput struct {
	toolcontract.ToolResponse[uploadPageAssetData]
	// RateLimitRemaining — see the comment on createPageOutput.RateLimitRemaining (#466, #520).
	// upload_page_asset shares the same per-caller mutation budget as
	// create_page/update_page, so the field means the same thing here.
	RateLimitRemaining int `json:"rate_limit_remaining"`
}

type uploadPageAssetData struct {
	Status      string           `json:"status,omitempty"`
	Slug        string           `json:"slug"`
	SourceKey   string           `json:"source_key,omitempty"`
	Filename    string           `json:"filename"`
	Path        string           `json:"path,omitempty"`
	ContentType string           `json:"content_type,omitempty"`
	SizeBytes   int              `json:"size_bytes,omitempty"`
	Sha256      string           `json:"sha256,omitempty"`
	DuplicateOf string           `json:"duplicate_of,omitempty"`
	DryRun      bool             `json:"dry_run,omitempty"`
	Warning     string           `json:"warning,omitempty"`
	RateLimit   *rateLimitBucket `json:"rate_limit,omitempty"`
	// RateLimitRemaining — see the comment on createPageData's field of the
	// same name (#520, #605).
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
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
		return "", "", "", fmt.Errorf("invalid_params: filename extension %q is not an allowed asset type (png, jpg, jpeg, gif, webp, svg)", ext)
	}
	return clean, ext, wantMIME, nil
}

// decodeAndValidateAssetContent decodes base64 asset content, enforces the
// size cap on the decoded bytes, and confirms it matches the extension the
// caller declared. Callers must never trust a caller-supplied content-type;
// for binary image types, only the sniffed bytes decide the MIME type. For
// ext == ".svg" (#571), byte-sniffing doesn't apply — Go's
// http.DetectContentType has no SVG signature — so validateSVGContent's
// strict structural allowlist is the actual "is this really SVG, and is it
// safe" check instead.
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
	if ext == ".svg" {
		if err := validateSVGContent(data); err != nil {
			return nil, fmt.Errorf("invalid_params: %w", err)
		}
		return data, nil
	}
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	sniffed := http.DetectContentType(data[:sniffLen])
	base, _, _ := strings.Cut(sniffed, ";")
	if strings.TrimSpace(base) != wantMIME {
		return nil, fmt.Errorf("invalid_params: filename/content_base64 mismatch: uploaded content does not match declared extension %q (sniffed %q)", ext, sniffed)
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
			"Allowed types: png, jpg, jpeg, gif, webp, svg. " +
			"Content is provided as base64 in content_base64 (max 10MB decoded); for png/jpg/jpeg/gif/webp the bytes are sniffed to confirm they actually match the declared extension, never trusting a caller-supplied content type. " +
			"svg uploads are validated by a strict structural parser instead (byte-sniffing can't distinguish SVG from arbitrary XML/text): only an allowlisted set of shape/text/gradient elements and attributes is accepted, event-handler attributes (onload, etc.), <script>, <foreignObject>, <style>, <image>, external href/xlink:href references (only a local \"#id\" fragment reference is allowed), and DOCTYPE/entity declarations are all rejected outright with invalid_svg — never silently stripped and written as a modified file. This is intentionally strict enough that a typical design-tool export (Figma/Illustrator/Inkscape <style> blocks, <metadata>, editor-namespace attributes) will likely fail invalid_svg; optimize/minify the SVG first if that happens. " +
			"This tool never overwrites: fails with already_exists if filename is already taken in this bundle. " +
			"If identical content already exists under a different filename in the same bundle, the response includes duplicate_of as an advisory only — the file is still written under the requested name. " +
			"Callers may provide idempotency_key to safely replay the exact same upload after a timeout or uncertain delivery. " +
			"rate_limit_remaining reports the caller's remaining budget on the shared create_page/update_page/upload_page_asset quota (#466); if exceeded, the error's resolution.retry_after_seconds gives a concrete wait time instead of forcing you to guess a safe pacing. Requires content.write.",
		InputSchema:  tools.MustSchema[uploadPageAssetInput](),
		OutputSchema: tools.MustSchema[uploadPageAssetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in uploadPageAssetInput) (*mcp.CallToolResult, uploadPageAssetOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		slug := normalizeInputSlug(in.Slug)
		wrapErr := func(err error, filename string) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{
				Slug:     slug,
				Filename: filename,
			})
		}
		if slug == "" {
			return nil, uploadPageAssetOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"), strings.TrimSpace(in.Filename))
		}
		filename, ext, wantMIME, err := validateAssetFilename(in.Filename)
		if err != nil {
			return nil, uploadPageAssetOutput{}, wrapErr(err, strings.TrimSpace(in.Filename))
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(err, rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		wrapErrWithLimiterAndInput := func(err error, filename string) error {
			return toolcontract.WithDataFields(
				wrapErrWithLimiter(err),
				map[string]any{
					"slug":     slug,
					"filename": filename,
				},
			)
		}
		// Allow() is skipped for dry-run (#588) but otherwise stays at its
		// original position, so every non-dry-run failure path below
		// (invalid content, already_exists, build_in_progress, etc.) keeps
		// consuming quota exactly as it did before, and the (potentially
		// expensive) base64 decode below still happens behind the limiter —
		// only the dry-run path changes here.
		if !in.DryRun && !limiter.Allow() {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(rateLimitExceededErr("upload_page_asset", cfg.RateLimit.CreateUpdatePerMin, limiter), filename), filename)
		}
		data, err := decodeAndValidateAssetContent(in.ContentBase64, ext, wantMIME)
		if err != nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(err, filename), filename)
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
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"), filename), filename)
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("upload_page_asset: lock_released")
		}()

		if err := validateBundleSlug(idx, slug); err != nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(err, filename), filename)
		}
		dir, err := pg.SafeJoin(slug)
		if err != nil {
			slog.Warn("upload_page_asset: path validation failed", "slug", slug, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("invalid_params: path validation failed"), filename), filename)
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
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("internal_error: failed to hash idempotency request"), filename), filename)
			}
			idemHash = h
			var cached uploadPageAssetOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "upload_page_asset", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(replayErr, filename), filename)
			}
			if hit {
				return nil, cached, nil
			}
		}

		if _, statErr := os.Stat(filePath); statErr == nil {
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("already_exists: asset already exists at %q", filename), filename), filename)
		} else if !os.IsNotExist(statErr) {
			slog.Error("upload_page_asset: stat failed", "slug", slug, "filename", filename, "error", statErr)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("read_error: failed to inspect destination path"), filename), filename)
		}

		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
		if in.DryRun {
			return nil, newUploadPageAssetOutput(uploadPageAssetData{
				Status:      "ok",
				Slug:        canonicalPublicSlug(slug),
				SourceKey:   slug,
				Filename:    filename,
				Path:        logicalPath,
				ContentType: wantMIME,
				SizeBytes:   len(data),
				Sha256:      hash,
				DuplicateOf: duplicateOf,
				DryRun:      true,
				RateLimit:   ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
			}, rateLimitRemaining(limiter)), nil
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("upload_page_asset: symlink-swap detected before write", "slug", slug, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("security_error: symlink detected in write path"), filename), filename)
		}
		if err := fileutil.AtomicCreateCheckedBytes(filePath, data, pg); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("already_exists: asset already exists at %q", filename), filename), filename)
			}
			slog.Error("upload_page_asset: write failed", "slug", slug, "filename", filename, "error", err)
			return nil, uploadPageAssetOutput{}, wrapErrWithLimiterAndInput(wrapErr(fmt.Errorf("write_error: failed to write asset"), filename), filename)
		}

		out := newUploadPageAssetOutput(uploadPageAssetData{
			Status:      "ok",
			Slug:        canonicalPublicSlug(slug),
			SourceKey:   slug,
			Filename:    filename,
			Path:        logicalPath,
			ContentType: wantMIME,
			SizeBytes:   len(data),
			Sha256:      hash,
			DuplicateOf: duplicateOf,
			RateLimit:   ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "upload_page_asset", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("upload_page_asset: could not persist idempotency result", "slug", slug, "error", err)
			}
		}
		return nil, out, nil
	}))
}

// deleteAssetFilenamePattern mirrors assetFilenamePattern's single-path-
// component/no-hidden-file safety guarantee, but doesn't restrict to image
// extensions — delete_page_asset must be able to remove any file a caller
// (or an older upload path) has left in a bundle directory, not just the
// image types upload_page_asset currently allows.
var deleteAssetFilenamePattern = assetFilenamePattern

// validateDeleteAssetFilename checks name is a single safe path component
// and rejects any index.<lang>.md filename — those are the page's own
// content file, not an asset, and deleting one belongs to delete_page, not
// this tool.
func validateDeleteAssetFilename(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if !deleteAssetFilenamePattern.MatchString(clean) {
		return "", fmt.Errorf("invalid_params: filename must be a single path component matching %s", deleteAssetFilenamePattern.String())
	}
	if clean == "index.md" || (strings.HasPrefix(clean, "index.") && strings.HasSuffix(clean, ".md")) {
		return "", fmt.Errorf("invalid_params: %q is the page's own content file, not an asset; use delete_page to remove the page itself", clean)
	}
	return clean, nil
}

// findAssetReferences scans every index.<lang>.md file in dir for a literal
// occurrence of filename, returning the language files (or "" for the
// language-less index.md) where it appears. Used to warn/guard against
// deleting an asset a page still visibly links to (#460) — a plain
// substring check is deliberately simple (an asset filename is already a
// narrow, mostly-unique token like "hero.png") rather than a full Markdown
// link parser, since a false positive only makes the guard slightly more
// conservative, never lets a referenced asset through silently.
func findAssetReferences(dir string, needles ...string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(needles))
	for _, needle := range needles {
		needle = strings.TrimSpace(needle)
		if needle != "" {
			filtered = append(filtered, needle)
		}
	}
	var referencedIn []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name != "index.md" && !(strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md")) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		text := string(raw)
		for _, needle := range filtered {
			if strings.Contains(text, needle) {
				referencedIn = append(referencedIn, name)
				break
			}
		}
	}
	return referencedIn, nil
}

type deletePageAssetInput struct {
	Slug             string `json:"slug"`
	Filename         string `json:"filename"`
	Scope            string `json:"scope,omitempty"`
	ExpectedSha256   string `json:"expected_sha256,omitempty"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	Force            bool   `json:"force,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	DryRun           bool   `json:"dry_run,omitempty"`
}

type deletePageAssetOutput struct {
	toolcontract.ToolResponse[deletePageAssetData]
	// RateLimitRemaining — see the comment on createPageOutput.RateLimitRemaining (#466, #520).
	RateLimitRemaining int `json:"rate_limit_remaining"`
	// RequestContext — see the comment on createPageOutput.RequestContext.
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
}

type deletePageAssetData struct {
	Status       string           `json:"status,omitempty"`
	Slug         string           `json:"slug,omitempty"`
	SourceKey    string           `json:"source_key,omitempty"`
	Scope        string           `json:"scope,omitempty"`
	Path         string           `json:"path,omitempty"`
	Kind         string           `json:"kind,omitempty"`
	Filename     string           `json:"filename,omitempty"`
	Sha256       string           `json:"sha256,omitempty"`
	DryRun       bool             `json:"dry_run,omitempty"`
	Referenced   *bool            `json:"referenced,omitempty"`
	ReferencedIn []string         `json:"referenced_in,omitempty"`
	Warning      string           `json:"warning,omitempty"`
	RateLimit    *rateLimitBucket `json:"rate_limit,omitempty"`
	// RateLimitRemaining — see the comment on createPageData's field of the
	// same name (#520, #605).
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
}

// newUploadPageAssetOutput/newDeletePageAssetOutput — see the comment on
// newCreatePageOutput (#520, #605): rateLimitRemaining is an explicit
// parameter, not read off data, since data never carries this field.
func newUploadPageAssetOutput(data uploadPageAssetData, rateLimitRemaining int) uploadPageAssetOutput {
	return uploadPageAssetOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

func newDeletePageAssetOutput(data deletePageAssetData, rateLimitRemaining int) deletePageAssetOutput {
	return deletePageAssetOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

const (
	deleteAssetScopeBundle    = "bundle"
	deleteAssetScopeGenerated = "generated"
)

type deleteAssetTarget struct {
	scope       string
	kind        string
	filePath    string
	logicalPath string
	filename    string
	referenceID string
}

func validateDeleteAssetScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return deleteAssetScopeBundle, nil
	}
	switch scope {
	case deleteAssetScopeBundle, deleteAssetScopeGenerated:
		return scope, nil
	default:
		return "", fmt.Errorf("invalid_params: scope must be one of bundle, generated")
	}
}

func resolveDeleteAssetTarget(pg *security.PathGuard, cfg config.Config, slug, filename, scope string) (deleteAssetTarget, error) {
	switch scope {
	case deleteAssetScopeBundle:
		dir, err := pg.SafeJoin(slug)
		if err != nil {
			return deleteAssetTarget{}, err
		}
		return deleteAssetTarget{
			scope:       deleteAssetScopeBundle,
			kind:        "page_bundle",
			filePath:    filepath.Join(dir, filename),
			filename:    filename,
			referenceID: filename,
		}, nil
	case deleteAssetScopeGenerated:
		if strings.TrimSpace(cfg.HugoRoot) == "" {
			return deleteAssetTarget{}, fmt.Errorf("not_found: generated assets are unavailable because hugo_root is not configured")
		}
		loc, err := admin.ResolveHeroImageLocation(cfg.HugoRoot, slug)
		if err != nil {
			return deleteAssetTarget{}, fmt.Errorf("invalid_params: path validation failed")
		}
		if filename != loc.Name {
			return deleteAssetTarget{}, fmt.Errorf("not_found: generated asset %q not found for slug %q", filename, slug)
		}
		return deleteAssetTarget{
			scope:       deleteAssetScopeGenerated,
			kind:        "global_static",
			filePath:    loc.AbsPath,
			logicalPath: loc.LogicalPath,
			filename:    filename,
			referenceID: generatedHeroReferenceID(slug),
		}, nil
	default:
		return deleteAssetTarget{}, fmt.Errorf("invalid_params: unsupported asset scope")
	}
}

func generatedHeroReferenceID(slug string) string {
	slug = strings.Trim(slug, "/")
	if slug == "" {
		return ""
	}
	return "/images/" + slug + admin.HeroImageSuffix
}

// registerDeletePageAsset registers delete_page_asset (#460). Shares
// delete_page's own destructive per-caller budget (deleteMu/deleteLimiters),
// not upload_page_asset's create/update quota — deleting is the destructive
// operation here, matching delete_page's own DestructiveHint.
func registerDeletePageAsset(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, idem *idempotencyStore, deleteMu *sync.Mutex, deleteLimiters map[string]*rate.Limiter) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "delete_page_asset",
		Title: "Delete page asset",
		Description: "Delete a file previously written into a Hugo page bundle directory by upload_page_asset. " +
			"By default this targets bundle-local files only; set `scope:\"generated\"` to explicitly target the generated hero image at {HugoRoot}/static/images/{slug}-featured.jpg instead of the bundle directory. " +
			"Non-dry-run calls require expected_sha256 (from upload_page_asset/list_page_assets) or expected_revision (the page bundle's own revision) as a concurrency guard; a mismatch fails with revision_conflict, telling the agent to re-check the current hash/revision via list_page_assets and retry. " +
			"Before deleting, the asset reference is checked against every index.<lang>.md file in the bundle: if referenced, the call fails with asset_referenced unless force=true is passed, so a still-linked image isn't silently broken. " +
			"dry_run previews the asset's sha256 and whether it's referenced, without requiring expected_sha256/expected_revision and without deleting anything. " +
			"Callers may provide idempotency_key to safely replay the exact same non-dry-run delete after a timeout or uncertain delivery. " +
			"This only removes the source asset, not any built public copy or CDN cache — unlike delete_page, it does not purge; the asset stays reachable at its old URL until the next build. " +
			"rate_limit_remaining reports the caller's remaining budget on delete_page's own destructive quota (#466), separate from create_page/update_page/upload_page_asset's shared quota. Requires content.write.",
		InputSchema:  tools.MustSchema[deletePageAssetInput](),
		OutputSchema: tools.MustSchema[deletePageAssetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in deletePageAssetInput) (*mcp.CallToolResult, deletePageAssetOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		slug := normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: slug})
		}
		if slug == "" {
			return nil, deletePageAssetOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		filename, err := validateDeleteAssetFilename(in.Filename)
		if err != nil {
			return nil, deletePageAssetOutput{}, wrapErr(err)
		}
		scope, err := validateDeleteAssetScope(in.Scope)
		if err != nil {
			return nil, deletePageAssetOutput{}, wrapErr(err)
		}
		if err := validateBundleSlug(idx, slug); err != nil {
			return nil, deletePageAssetOutput{}, wrapErr(err)
		}
		dir, err := pg.SafeJoin(slug)
		if err != nil {
			slog.Warn("delete_page_asset: path validation failed", "slug", slug, "error", err)
			return nil, deletePageAssetOutput{}, wrapErr(fmt.Errorf("invalid_params: path validation failed"))
		}
		target, err := resolveDeleteAssetTarget(pg, cfg, slug, filename, scope)
		if err != nil {
			return nil, deletePageAssetOutput{}, wrapErr(err)
		}
		filePath := target.filePath

		// Fetching the limiter doesn't consume budget (#466's dry-run fix
		// applies equally here).
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(deleteMu, deleteLimiters, callerKey, cfg.RateLimit.DestructivePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.DestructivePerMin, rateLimitScopeDestructive, time.Now().UTC()),
			)
		}

		if in.DryRun {
			data, readErr := os.ReadFile(filePath)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					return nil, deletePageAssetOutput{}, wrapErr(fmt.Errorf("not_found: asset %q not found in bundle %q", filename, slug))
				}
				slog.Error("delete_page_asset: dry-run read failed", "slug", slug, "filename", filename, "error", readErr)
				return nil, deletePageAssetOutput{}, wrapErr(fmt.Errorf("read_error: failed to read asset"))
			}
			referencedIn, refErr := findAssetReferences(dir, filename, target.referenceID)
			if refErr != nil {
				slog.Warn("delete_page_asset: reference scan failed", "slug", slug, "filename", filename, "error", refErr)
			}
			return nil, newDeletePageAssetOutput(deletePageAssetData{
				Status:       "ok",
				Slug:         canonicalPublicSlug(slug),
				SourceKey:    slug,
				Scope:        target.scope,
				Path:         target.logicalPath,
				Kind:         target.kind,
				Filename:     filename,
				Sha256:       contentmodel.SourceRevisionBytes(data),
				DryRun:       true,
				Referenced:   fileutil.BoolPtr(len(referencedIn) > 0),
				ReferencedIn: referencedIn,
				RateLimit:    ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.DestructivePerMin, rateLimitScopeDestructive, time.Now().UTC())),
			}, rateLimitRemaining(limiter)), nil
		}

		if in.ExpectedSha256 == "" && in.ExpectedRevision == "" {
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: expected_sha256 or expected_revision is required for non-dry-run delete_page_asset"))
		}

		if !limiter.Allow() {
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(rateLimitExceededErr("delete_page_asset", cfg.RateLimit.DestructivePerMin, limiter))
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("delete_page_asset: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("delete_page_asset: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("delete_page_asset: lock_released")
		}()

		// Idempotency replay is checked before any file read below, using
		// only the request's own params (slug/filename/expected_*/force) —
		// a replay of an already-completed delete has nothing left to read
		// on disk (the asset is genuinely gone), so replay must not depend
		// on a not_found gate the way the fresh-attempt path below does.
		idemHash := ""
		if strings.TrimSpace(in.IdempotencyKey) != "" {
			h, hashErr := requestHash(struct {
				Slug             string `json:"slug"`
				Filename         string `json:"filename"`
				ExpectedSha256   string `json:"expected_sha256,omitempty"`
				ExpectedRevision string `json:"expected_revision,omitempty"`
				Scope            string `json:"scope,omitempty"`
				Force            bool   `json:"force,omitempty"`
			}{Slug: slug, Filename: filename, ExpectedSha256: in.ExpectedSha256, ExpectedRevision: in.ExpectedRevision, Scope: target.scope, Force: in.Force})
			if hashErr != nil {
				return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = h
			var cached deletePageAssetOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "delete_page_asset", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, deletePageAssetOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("not_found: asset %q not found in bundle %q", filename, slug))
			}
			slog.Error("delete_page_asset: read failed", "slug", slug, "filename", filename, "error", readErr)
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read asset"))
		}
		actualHash := contentmodel.SourceRevisionBytes(data)
		actualBundleRevision := ""
		if resolvedSource := inspectDeleteSource(dir); resolvedSource.SourcePath != "" {
			if rev, revErr := contentmodel.SourceRevision(resolvedSource.SourcePath); revErr == nil {
				actualBundleRevision = rev
			}
		}
		referencedIn, refErr := findAssetReferences(dir, filename, target.referenceID)
		if refErr != nil {
			slog.Warn("delete_page_asset: reference scan failed", "slug", slug, "filename", filename, "error", refErr)
		}

		if in.ExpectedSha256 != "" && in.ExpectedSha256 != actualHash {
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: asset changed since it was read; call list_page_assets to get the current hash and retry"))
		}
		if in.ExpectedRevision != "" && in.ExpectedRevision != actualBundleRevision {
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: asset changed since it was read; call list_page_assets to get the current hash and retry"))
		}
		if len(referencedIn) > 0 && !in.Force {
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("asset_referenced: %q is referenced in %v; pass force=true to delete anyway", filename, referencedIn))
		}

		if err := os.Remove(filePath); err != nil {
			slog.Error("delete_page_asset: remove failed", "slug", slug, "filename", filename, "error", err)
			return nil, deletePageAssetOutput{}, wrapErrWithLimiter(fmt.Errorf("delete_error: failed to delete asset"))
		}

		var warning string
		auditLog := filepath.Join(cfg.ContentRoot, ".mcp-audit.log")
		entry := fmt.Sprintf("%s DELETE_ASSET %s/%s\n", time.Now().UTC().Format(time.RFC3339), slug, filename)
		if auditErr := appendAuditLog(auditLog, entry); auditErr != nil {
			slog.Warn("delete_page_asset: audit log write failed (delete already committed)", "slug", slug, "filename", filename, "error", auditErr)
			warning = "audit_error: " + auditErr.Error()
		}

		out := newDeletePageAssetOutput(deletePageAssetData{
			Status:       "ok",
			Slug:         canonicalPublicSlug(slug),
			SourceKey:    slug,
			Scope:        target.scope,
			Path:         target.logicalPath,
			Kind:         target.kind,
			Filename:     filename,
			Sha256:       actualHash,
			Referenced:   fileutil.BoolPtr(len(referencedIn) > 0),
			ReferencedIn: referencedIn,
			Warning:      warning,
			RateLimit:    ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.DestructivePerMin, rateLimitScopeDestructive, time.Now().UTC())),
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "delete_page_asset", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("delete_page_asset: could not persist idempotency result", "slug", slug, "filename", filename, "error", err)
			}
		}
		return nil, out, nil
	}))
}
