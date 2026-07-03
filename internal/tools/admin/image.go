package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_/-]*$`)

type generateFeaturedImageInput struct {
	Slug   string `json:"slug"`
	Prompt string `json:"prompt"`
}

type generateFeaturedImageOutput struct {
	Path string `json:"path"`
}

func Register(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	RegisterBuild(s, cfg)
	RegisterHooks(s, cfg)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "generate_featured_image",
		Title:       "Generate featured image",
		Description: "[RequiredScope: site.admin] Generate a featured image for a page using the configured image generation API. Saves to {SiteRoot}/images/featured/{slug}.jpg.",
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
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("config_error: site root: %w", err)
		}

		relPath := filepath.Join("images", "featured", in.Slug+".jpg")
		destPath, err := pg.SafeJoin(relPath)
		if err != nil {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("invalid_params: %w", err)
		}

		imgBytes, err := fetchImage(ctx, cfg.ImageGenURL, cfg.ImageGenKey, in.Prompt)
		if err != nil {
			return nil, generateFeaturedImageOutput{}, err
		}

		if err := atomicWriteBytes(destPath, imgBytes); err != nil {
			return nil, generateFeaturedImageOutput{}, fmt.Errorf("write_error: %w", err)
		}

		return nil, generateFeaturedImageOutput{Path: destPath}, nil
	})
}

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

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("unexpected content-type: %q (expected image/*)", ct)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read_error: %w", err)
	}
	return data, nil
}

func atomicWriteBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func boolPtr(v bool) *bool { return &v }
