package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_/-]*$`)
var validAccent = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxImageBytes caps the response body from the image generation API (10 MiB).
const maxImageBytes = 10 << 20

// HeroImageSuffix is the filename suffix generate_hero_image appends to a
// slug to produce {HugoRoot}/static/images/{slug}HeroImageSuffix. Exported so
// delete_page (internal/tools/write) can rederive the exact same path to
// clean up an orphaned hero image on delete (#606) without duplicating the
// literal — a single source of truth means a future rename here can't
// silently desync the cleanup logic from the write path that produced the
// file in the first place.
const HeroImageSuffix = "-featured.jpg"

type generateFeaturedImageInput struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title,omitempty"`
	Subtitle string   `json:"subtitle,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Accent   string   `json:"accent,omitempty"`
	Style    string   `json:"style,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
}

// generateFeaturedImageOutput's payload lives only under data.* as of
// v1.5.9 (#573) — see createPreviewOutput's comment in create_preview.go
// for why. BREAKING: callers reading path at the root must switch to
// data.path.
type generateFeaturedImageOutput struct {
	toolcontract.ToolResponse[generateFeaturedImageData]
}

type generateFeaturedImageData struct {
	Path string `json:"path"`
	// SourceKey is the canonical slug form shared by write tools and by
	// generated-asset cleanup logic, regardless of whether the caller used a
	// public slug (/posts/example/) or a source-key slug (posts/example).
	SourceKey string `json:"source_key"`
	// PublicPath (#812 follow-up) is Path rewritten from the hugo_root-
	// relative filesystem form ("static/images/...") to the public URL form
	// ("/images/...") a page's featuredImage frontmatter field actually
	// expects — ready to paste into update_page's featured_image parameter
	// without the caller having to strip the "static" prefix by hand.
	PublicPath string `json:"public_path"`
	// DeleteSlug/DeleteScope/DeleteFilename close the generate_hero_image →
	// delete_page_asset contract gap (#845, #846): callers can feed these
	// straight back into delete_page_asset (with SourceKey as the canonical
	// slug identity) without re-deriving a second filename/scope contract
	// from data.path.
	DeleteSlug     string `json:"delete_slug"`
	DeleteScope    string `json:"delete_scope"`
	DeleteFilename string `json:"delete_filename"`
}

type imageWriteErrorPayload struct {
	Error           string `json:"error"`
	TargetDirectory string `json:"target_directory"`
	TargetPath      string `json:"target_path"`
	OperatorHint    string `json:"operator_hint"`
	Retryable       bool   `json:"retryable"`
	Docs            string `json:"docs"`
}

// HeroImageLocation is the canonical generated-hero-image location for one
// source slug under HugoRoot's static/images tree.
type HeroImageLocation struct {
	AbsPath     string
	LogicalPath string
	Name        string
}

func imageSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

// logicalHugoRootPath projects an absolute file path under hugoRoot into a
// hugo_root-relative logical path (e.g. "static/images/hello-featured.jpg"),
// so responses don't leak the host's absolute filesystem layout (#551).
func logicalHugoRootPath(hugoRoot, absPath string) string {
	hugoRoot = strings.TrimSpace(hugoRoot)
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return ""
	}
	if hugoRoot != "" {
		if rel, err := filepath.Rel(hugoRoot, absPath); err == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if filepath.IsAbs(absPath) {
		return ""
	}
	return filepath.ToSlash(absPath)
}

// ResolveHeroImageLocation derives the exact generated hero-image path for a
// slug using the same static/images root and suffix generate_hero_image
// itself writes to. Exported so read/write tools can report or explicitly
// operate on the generated file without re-deriving a second path contract.
func ResolveHeroImageLocation(hugoRoot, slug string) (HeroImageLocation, error) {
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	guard, err := security.New(imagesRoot, true)
	if err != nil {
		return HeroImageLocation{}, err
	}
	target, err := guard.SafeJoin(slug + HeroImageSuffix)
	if err != nil {
		return HeroImageLocation{}, err
	}
	return HeroImageLocation{
		AbsPath:     target,
		LogicalPath: logicalHugoRootPath(hugoRoot, target),
		Name:        filepath.Base(target),
	}, nil
}

// NormalizeHeroImageSlug returns the canonical source-key form accepted by
// generate_hero_image and related generated-asset cleanup code.
func NormalizeHeroImageSlug(raw string) (string, error) {
	return normalizeHeroImageSlug(raw)
}

// publicImagePathFromLogical rewrites a hugo_root-relative logical path
// ("static/images/posts/slug-featured.jpg") into the public URL form
// ("/images/posts/slug-featured.jpg") that a page's featuredImage
// frontmatter field expects. Returns "" if logicalPath isn't rooted at
// "static/" (defensive; every path this package produces always is).
func publicImagePathFromLogical(logicalPath string) string {
	const prefix = "static/"
	if !strings.HasPrefix(logicalPath, prefix) {
		return ""
	}
	return "/" + strings.TrimPrefix(logicalPath, prefix)
}

// newGenerateFeaturedImageOutput builds the success envelope and attaches an
// advisory warning (#812 follow-up): generate_hero_image only ever writes
// the image file itself, never a page's frontmatter, so a caller that stops
// after this call ends up with a generated image no page actually
// references — the exact gap that shipped a broken homepage card earlier.
// Deliberately a non-blocking warning rather than an automatic frontmatter
// write: this tool has no language awareness (a bundle's translations each
// need their own featured_image set via update_page) and no access to
// update_page's optimistic-locking (expected_revision) machinery, so
// writing frontmatter from here would mean either duplicating that locking
// or silently bypassing it.
func newGenerateFeaturedImageOutput(data generateFeaturedImageData) generateFeaturedImageOutput {
	data.PublicPath = publicImagePathFromLogical(data.Path)
	if data.SourceKey == "" && strings.HasPrefix(data.Path, "static/images/") {
		data.SourceKey = strings.TrimSuffix(strings.TrimPrefix(data.Path, "static/images/"), HeroImageSuffix)
	}
	if data.DeleteScope == "" {
		data.DeleteScope = "generated"
	}
	if data.DeleteSlug == "" {
		data.DeleteSlug = data.SourceKey
	}
	if data.DeleteFilename == "" {
		data.DeleteFilename = filepath.Base(data.Path)
	}
	out := generateFeaturedImageOutput{
		ToolResponse: imageSuccessEnvelope(data),
	}
	if data.PublicPath != "" {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"image generated but not attached to any page yet — call update_page with featured_image=%q to use it in card/list views (set it again per language if the page has translations)",
			data.PublicPath,
		))
	}
	return out
}

// Register wires all admin tools (site.admin scope).
// siteReload is an optional callback called after a successful build_site to
// refresh the in-memory site index (resolves #212).
func Register(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteReload ...PostBuildCallback) {
	if s == nil {
		return
	}
	RegisterBuild(s, cfg, srcIdx, siteReload...)
	RegisterPreviewBuild(s, cfg)
	RegisterHooks(s, cfg)
	registerGenerateFeaturedImage(s, cfg)
	RegisterSRI(s, cfg)
	RegisterRuntimeStatus(s, cfg, srcIdx)
	RegisterThemeStatus(s, cfg)
}

// RegisterSiteAdmin is an alias for Register kept for compatibility.
func RegisterSiteAdmin(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteReload ...PostBuildCallback) {
	Register(s, cfg, srcIdx, siteReload...)
}

// Defs returns tool definitions for all admin tools (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "build_site", RequiredScope: "write"},
		{Name: "preview_build", RequiredScope: "write"},
		{Name: "run_post_build_hooks", RequiredScope: "write"},
		{Name: "generate_hero_image", RequiredScope: "write"},
		{Name: "check_sri_versions", RequiredScope: "write"},
		{Name: "get_runtime_status", RequiredScope: "write"},
		{Name: "get_theme_status", RequiredScope: "write"},
		{Name: "verify_publication", RequiredScope: "write"},
		{Name: "create_preview", RequiredScope: "write"},
		{Name: "list_previews", RequiredScope: "write"},
		{Name: "revoke_preview", RequiredScope: "write"},
		{Name: "revoke_all_previews", RequiredScope: "write"},
		{Name: "inspect_preview", RequiredScope: "write"},
		{Name: "publish_changes", RequiredScope: "write"},
		{Name: "get_storage_health", RequiredScope: "write"},
	}
}

func registerGenerateFeaturedImage(s *mcp.Server, cfg config.Config) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "generate_hero_image",
		Title: "Generate hero image",
		Description: "Generate a hero/featured image for a page and save it to {HugoRoot}/static/images/{slug}-featured.jpg. " +
			"`slug` accepts either the canonical public form (`/posts/example/`) or the source-key form (`posts/example`); " +
			"language-prefixed public slugs are normalized to the same source key before writing. " +
			"Uses local Go rendering (1200×675 JPEG, Unsplash photo background selected by title hash, dark gradient overlay, title, tags). " +
			"Required: slug, title. Optional: subtitle, tags (max 6), accent (hex colour like #7aa2f7), style (tech|geo). " +
			"This tool only writes the image file — it never touches page frontmatter. `data.public_path` is the ready-to-use " +
			"featuredImage value; call update_page with featured_image=data.public_path afterwards to attach it (per language, " +
			"for a bundle with translations), or the image will exist but never appear on the site's card/list views. " +
			"`data.source_key` is the canonical page identifier after slug normalization, and `data.delete_slug` + `data.delete_scope` + `data.delete_filename` " +
			"can be passed straight to delete_page_asset later to remove this generated file without re-deriving the cleanup contract.",
		InputSchema: tools.WithEnum(
			tools.MustSchema[generateFeaturedImageInput](),
			"style",
			"",
			"tech",
			"geo",
		),
		OutputSchema: tools.MustSchema[generateFeaturedImageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in generateFeaturedImageInput) (*mcp.CallToolResult, generateFeaturedImageOutput, error) {
		slug, err := NormalizeHeroImageSlug(in.Slug)
		if err != nil {
			return nil, generateFeaturedImageOutput{}, err
		}
		if slug == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		if !validSlug.MatchString(slug) {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug contains invalid characters")
		}
		in.Slug = slug

		// External API mode: when image_gen_url is configured and prompt is provided.
		if cfg.ImageGenURL != "" && in.Prompt != "" {
			return generateViaAPI(ctx, cfg, in)
		}

		// Local rendering mode.
		if cfg.HugoRoot == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
		}
		if in.Title == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: title must not be empty")
		}
		style := strings.TrimSpace(in.Style)
		if style == "" {
			style = "tech"
		}
		if style != "tech" && style != "geo" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: style must be 'tech' or 'geo'")
		}
		accent := strings.TrimSpace(in.Accent)
		if accent != "" && !validAccent.MatchString(accent) {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: accent must be a 6-digit hex colour like #7aa2f7")
		}
		if accent == "" {
			if style == "geo" {
				accent = "#bb9af7"
			} else {
				accent = "#7aa2f7"
			}
		}
		if len(in.Tags) > 6 {
			in.Tags = in.Tags[:6]
		}

		// Use a guard anchored at HugoRoot with symlink rejection always on,
		// regardless of the operator's RejectSymlinks config setting. This detects
		// a symlinked static/images directory (component visible from HugoRoot).
		outerPg, err := security.New(cfg.HugoRoot, true)
		if err != nil {
			slog.Error("generate_hero_image: could not initialize path guard", "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
		}
		// Validate BEFORE MkdirAll so we don't follow a symlink before detecting it.
		relPath := filepath.Join("static", "images", in.Slug+HeroImageSuffix)
		if _, err := outerPg.SafeJoin(relPath); err != nil {
			slog.Warn("generate_hero_image: path validation failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		// Create images directory and narrow-scoped guard for the actual write.
		imagesRoot := filepath.Join(cfg.HugoRoot, "static", "images")
		if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
			errPath := filepath.Join(imagesRoot, in.Slug+HeroImageSuffix)
			slog.Error("generate_hero_image: could not create images directory", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, imageWriteError(errPath)
		}
		imagesGuard, err := security.New(imagesRoot, true)
		if err != nil {
			slog.Error("generate_hero_image: could not initialize images guard", "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
		}
		destPath, err := imagesGuard.SafeJoin(in.Slug + HeroImageSuffix)
		if err != nil {
			slog.Warn("generate_hero_image: scoped path validation failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		if err := ensureImageTargetWritable(destPath); err != nil {
			slog.Error("generate_hero_image: destination not writable", "slug", in.Slug, "path", destPath, "error", err)
			return nil, generateFeaturedImageOutput{}, err
		}

		bgDir := filepath.Join(cfg.HugoRoot, "static", "images", "featured-backgrounds")
		if err := imagesGuard.RevalidateForWrite(destPath); err != nil {
			slog.Warn("generate_hero_image: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("security_error: symlink detected in image write path")
		}
		if err := renderFeaturedImage(bgDir, destPath, style, in.Title, in.Subtitle, in.Tags, accent, cfg.SiteName); err != nil {
			slog.Error("generate_hero_image: render failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, imageWriteError(destPath)
		}

		return nil, newGenerateFeaturedImageOutput(generateFeaturedImageData{
			Path:           logicalHugoRootPath(cfg.HugoRoot, destPath),
			SourceKey:      slug,
			DeleteFilename: filepath.Base(destPath),
		}), nil
	}))
}

func normalizeHeroImageSlug(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "/") {
		if !strings.HasSuffix(raw, "/") {
			return "", fmt.Errorf("invalid_params: slug must be either a source key like %q or a canonical public slug like %q", "posts/example", "/posts/example/")
		}
		raw = strings.Trim(raw, "/")
	}
	candidates := site.SourceSlugCandidates(raw)
	if len(candidates) == 0 {
		return "", fmt.Errorf("invalid_params: slug must not be empty")
	}
	return candidates[len(candidates)-1], nil
}

func generateViaAPI(ctx context.Context, cfg config.Config, in generateFeaturedImageInput) (*mcp.CallToolResult, generateFeaturedImageOutput, error) {
	if cfg.HugoRoot == "" {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
	}
	// Use a guard anchored at HugoRoot with symlink rejection always on,
	// regardless of the operator's RejectSymlinks config setting. This detects
	// a symlinked static/images directory (component visible from HugoRoot).
	outerPg, err := security.New(cfg.HugoRoot, true)
	if err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
	}
	// Validate BEFORE MkdirAll so we don't follow a symlink before detecting it.
	relPath := filepath.Join("static", "images", in.Slug+HeroImageSuffix)
	if _, err := outerPg.SafeJoin(relPath); err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
	}
	// Create images directory and narrow-scoped guard for the actual write.
	imagesRoot := filepath.Join(cfg.HugoRoot, "static", "images")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		return nil, generateFeaturedImageOutput{}, imageWriteError(filepath.Join(imagesRoot, in.Slug+HeroImageSuffix))
	}
	imagesGuard, err := security.New(imagesRoot, true)
	if err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
	}
	destPath, err := imagesGuard.SafeJoin(in.Slug + HeroImageSuffix)
	if err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
	}
	if err := ensureImageTargetWritable(destPath); err != nil {
		return nil, generateFeaturedImageOutput{}, err
	}

	imgBytes, err := fetchImage(ctx, cfg.ImageGenURL, cfg.ImageGenKey, in.Prompt)
	if err != nil {
		return nil, generateFeaturedImageOutput{}, err
	}

	if err := imagesGuard.RevalidateForWrite(destPath); err != nil {
		slog.Warn("generate_hero_image: symlink-swap detected before write (api path)", "slug", in.Slug, "error", err)
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("security_error: symlink detected in image write path")
	}
	if err := fileutil.AtomicWriteBytes(destPath, imgBytes); err != nil {
		slog.Error("generate_hero_image: write failed", "slug", in.Slug, "error", err)
		return nil, generateFeaturedImageOutput{}, imageWriteError(destPath)
	}

	return nil, newGenerateFeaturedImageOutput(generateFeaturedImageData{
		Path:           logicalHugoRootPath(cfg.HugoRoot, destPath),
		SourceKey:      in.Slug,
		DeleteFilename: filepath.Base(destPath),
	}), nil
}

func imageWriteError(destPath string) error {
	payload := imageWriteErrorPayload{
		Error:           "write_error",
		TargetDirectory: filepath.Dir(destPath),
		TargetPath:      destPath,
		OperatorHint:    "Ensure hugo_root/static/images is writable by the MCP service user and hugo_root is included in systemd ReadWritePaths before using generate_hero_image.",
		Retryable:       false,
		Docs:            "docs/operator-guide.md#image-generation-configuration",
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("write_error: %s", b)
}

func ensureImageTargetWritable(destPath string) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return imageWriteError(destPath)
	}
	f, err := os.CreateTemp(dir, ".mcp-image-*.tmp")
	if err != nil {
		return imageWriteError(destPath)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// fetchImage calls the image generation API and returns the image bytes.
// It enforces: 2xx status, image/* content-type, and a maximum body size.
func fetchImage(ctx context.Context, url, key, prompt string) ([]byte, error) {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := &http.Client{}

	req, err := http.NewRequestWithContext(tctx, http.MethodPost, url, strings.NewReader(prompt))
	if err != nil {
		return nil, fmt.Errorf("request_error: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch_error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("image_api_error: server returned HTTP %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("unexpected content-type: %q (expected image/*)", ct)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("read_error: %w", err)
	}
	return data, nil
}
