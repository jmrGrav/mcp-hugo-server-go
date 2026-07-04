package admin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_/-]*$`)

// maxImageBytes caps the response body from the image generation API (10 MiB).
const maxImageBytes = 10 << 20

type generateFeaturedImageInput struct {
	Slug   string `json:"slug"`
	Prompt string `json:"prompt"`
}

type generateFeaturedImageOutput struct {
	Path string `json:"path"`
}

// Register wires all admin tools (site.admin + system.admin scopes).
func Register(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}
	RegisterSiteAdmin(s, cfg)
	RegisterSRI(s, cfg)
}

// RegisterSiteAdmin wires the site.admin-scoped tools only.
func RegisterSiteAdmin(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}
	RegisterBuild(s, cfg)
	RegisterPreviewBuild(s, cfg)
	RegisterHooks(s, cfg)
	registerGenerateFeaturedImage(s, cfg)
}

// Defs returns tool definitions for all admin tools (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "build_site", RequiredScope: "site.admin"},
		{Name: "preview_build", RequiredScope: "site.admin"},
		{Name: "run_post_build_hooks", RequiredScope: "site.admin"},
		{Name: "generate_featured_image", RequiredScope: "site.admin"},
		{Name: "check_sri_versions", RequiredScope: "system.admin"},
	}
}

func registerGenerateFeaturedImage(s *mcp.Server, cfg config.Config) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "generate_featured_image",
		Title:       "Generate featured image",
		Description: "[RequiredScope: site.admin] Generate a featured image for a page using the configured image generation API and save it to {SiteRoot}/images/featured/{slug}.jpg.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in generateFeaturedImageInput) (*mcp.CallToolResult, generateFeaturedImageOutput, error) {
		if in.Slug == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		if !validSlug.MatchString(in.Slug) {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: slug contains invalid characters")
		}
		if in.Prompt == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: prompt must not be empty")
		}
		if cfg.ImageGenURL == "" {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: image_gen_url is not configured")
		}

		pg, err := security.New(cfg.SiteRoot, false)
		if err != nil {
			slog.Error("generate_featured_image: could not initialize path guard", "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: could not initialize path guard")
		}

		relPath := filepath.Join("images", "featured", in.Slug+".jpg")
		destPath, err := pg.SafeJoin(relPath)
		if err != nil {
			slog.Warn("generate_featured_image: path validation failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		imgBytes, err := fetchImage(ctx, cfg.ImageGenURL, cfg.ImageGenKey, in.Prompt)
		if err != nil {
			return nil, generateFeaturedImageOutput{}, err
		}

		if err := atomicWriteBytes(destPath, imgBytes); err != nil {
			slog.Error("generate_featured_image: write failed", "slug", in.Slug, "error", err)
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("write_error: failed to write image")
		}

		return nil, generateFeaturedImageOutput{Path: destPath}, nil
	})
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

// atomicWriteBytes writes data to path atomically using a unique temp file.
// On failure the temp file is removed.
func atomicWriteBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func boolPtr(v bool) *bool { return &v }
