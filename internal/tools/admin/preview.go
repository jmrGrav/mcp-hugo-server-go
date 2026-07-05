package admin

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type previewBuildInput struct{}

type previewBuildOutput struct {
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

func RegisterPreviewBuild(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "preview_build",
		Title:       "Preview build",
		Description: "Run a non-destructive Hugo preview build with render-to-memory semantics. Use this to validate the site without writing build artifacts.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ previewBuildInput) (*mcp.CallToolResult, previewBuildOutput, error) {
		if cfg.SiteRoot == "" {
			return nil, previewBuildOutput{}, fmt.Errorf("config_error: site_root is not configured")
		}
		hugosite.ContentMu.RLock()
		defer hugosite.ContentMu.RUnlock()

		tctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		start := time.Now()
		cmd := exec.CommandContext(tctx, "hugo", "--renderToMemory")
		cmd.Dir = cfg.SiteRoot
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()
		if err != nil {
			if tctx.Err() != nil {
				return nil, previewBuildOutput{}, fmt.Errorf("build_error: preview build timeout exceeded")
			}
			slog.Error("preview_build failed", "duration_ms", durationMs, "error", err)
			return nil, previewBuildOutput{}, fmt.Errorf("build_error: hugo exited with error")
		}
		return nil, previewBuildOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
