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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_/-]*$`)
var validAccent = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxImageBytes caps the response body from the image generation API (10 MiB).
const maxImageBytes = 10 << 20

type generateFeaturedImageInput struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title,omitempty"`
	Subtitle string   `json:"subtitle,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Accent   string   `json:"accent,omitempty"`
	Style    string   `json:"style,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
}

type generateFeaturedImageOutput struct {
	toolcontract.ToolResponse[map[string]any]
	Path string `json:"path"`
}

type imageWriteErrorPayload struct {
	Error           string `json:"error"`
	TargetDirectory string `json:"target_directory"`
	TargetPath      string `json:"target_path"`
	OperatorHint    string `json:"operator_hint"`
	Retryable       bool   `json:"retryable"`
	Docs            string `json:"docs"`
}

func imageSuccessEnvelope() toolcontract.ToolResponse[map[string]any] {
	return toolcontract.Success(map[string]any{}, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

// Register wires all admin tools (site.admin scope).
// siteReload is an optional callback called after a successful build_site to
// refresh the in-memory site index (resolves #212).
func Register(s *mcp.Server, cfg config.Config, siteReload ...func() error) {
	if s == nil {
		return
	}
	RegisterBuild(s, cfg, siteReload...)
	RegisterPreviewBuild(s, cfg)
	RegisterHooks(s, cfg)
	registerGenerateFeaturedImage(s, cfg)
	RegisterSRI(s, cfg)
	RegisterRuntimeStatus(s, cfg)
	RegisterThemeStatus(s, cfg)
}

// RegisterSiteAdmin is an alias for Register kept for compatibility.
func RegisterSiteAdmin(s *mcp.Server, cfg config.Config, siteReload ...func() error) {
	Register(s, cfg, siteReload...)
}

// Defs returns tool definitions for all admin tools (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "build_site", RequiredScope: "site.admin"},
		{Name: "preview_build", RequiredScope: "site.admin"},
		{Name: "run_post_build_hooks", RequiredScope: "site.admin"},
		{Name: "generate_hero_image", RequiredScope: "site.admin"},
		{Name: "check_sri_versions", RequiredScope: "site.admin"},
		{Name: "get_runtime_status", RequiredScope: "site.admin"},
		{Name: "get_theme_status", RequiredScope: "site.admin"},
		{Name: "verify_publication", RequiredScope: "site.admin"},
		{Name: "create_preview", RequiredScope: "site.admin"},
	}
}

func registerGenerateFeaturedImage(s *mcp.Server, cfg config.Config) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "generate_hero_image",
		Title: "Generate hero image",
		Description: "Generate a hero/featured image for a page and save it to {HugoRoot}/static/images/{slug}-featured.jpg. " +
			"Uses local Go rendering (1200×675 JPEG, Unsplash photo background selected by title hash, dark gradient overlay, title, tags). " +
			"Required: slug, title. Optional: subtitle, tags (max 6), accent (hex colour like #7aa2f7), style (tech|geo).",
		InputSchema:  tools.MustSchema[generateFeaturedImageInput](),
		OutputSchema: tools.MustSchema[generateFeaturedImageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in generateFeaturedImageInput) (*mcp.CallToolResult, generateFeaturedImageOutput, error) {
		if in.Slug == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		if !validSlug.MatchString(in.Slug) {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug contains invalid characters")
		}

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
		relPath := filepath.Join("static", "images", in.Slug+"-featured.jpg")
		if _, err := outerPg.SafeJoin(relPath); err != nil {
			slog.Warn("generate_hero_image: path validation failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		// Create images directory and narrow-scoped guard for the actual write.
		imagesRoot := filepath.Join(cfg.HugoRoot, "static", "images")
		if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
			errPath := filepath.Join(imagesRoot, in.Slug+"-featured.jpg")
			slog.Error("generate_hero_image: could not create images directory", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, imageWriteError(errPath)
		}
		imagesGuard, err := security.New(imagesRoot, true)
		if err != nil {
			slog.Error("generate_hero_image: could not initialize images guard", "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
		}
		destPath, err := imagesGuard.SafeJoin(in.Slug + "-featured.jpg")
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
		if err := renderFeaturedImage(bgDir, destPath, style, in.Title, in.Subtitle, in.Tags, accent); err != nil {
			slog.Error("generate_hero_image: render failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, imageWriteError(destPath)
		}

		return nil, generateFeaturedImageOutput{ToolResponse: imageSuccessEnvelope(), Path: destPath}, nil
	}))
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
	relPath := filepath.Join("static", "images", in.Slug+"-featured.jpg")
	if _, err := outerPg.SafeJoin(relPath); err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
	}
	// Create images directory and narrow-scoped guard for the actual write.
	imagesRoot := filepath.Join(cfg.HugoRoot, "static", "images")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		return nil, generateFeaturedImageOutput{}, imageWriteError(filepath.Join(imagesRoot, in.Slug+"-featured.jpg"))
	}
	imagesGuard, err := security.New(imagesRoot, true)
	if err != nil {
		return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
	}
	destPath, err := imagesGuard.SafeJoin(in.Slug + "-featured.jpg")
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

	return nil, generateFeaturedImageOutput{ToolResponse: imageSuccessEnvelope(), Path: destPath}, nil
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
