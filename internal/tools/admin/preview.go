package admin

import (
	"bytes"
	"context"
	"encoding/json"
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
		if cfg.HugoRoot == "" {
			return nil, previewBuildOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
		}
		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryRLock() {
				slog.Debug("preview_build: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("preview_build: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, previewBuildOutput{}, fmt.Errorf("build_in_progress: content mutation in progress, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.RUnlock()
			slog.Debug("preview_build: lock_released")
		}()

		tctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		start := time.Now()
		cmd := exec.CommandContext(tctx, "hugo", "--renderToMemory")
		cmd.Dir = cfg.HugoRoot
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()

		if err != nil {
			buildID := newBuildID(start)
			exitCode := 0
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}

			slog.Error("preview_build failed",
				"build_id", buildID,
				"duration_ms", durationMs,
				"exit_code", exitCode,
				"stderr", stderrBuf.String(),
				"error", err,
			)

			errKind := "build_error"
			if tctx.Err() != nil {
				errKind = "build_timeout"
			}

			payload := buildErrorPayload{
				Error:            errKind,
				ExitCode:         exitCode,
				Command:          "hugo --renderToMemory",
				WorkingDirectory: cfg.HugoRoot,
				DurationMs:       durationMs,
				StderrSummary:    sanitiseStderr(stderrBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
				BuildID:          buildID,
				LogHint:          "Search server logs for build_id=" + buildID,
			}
			jsonPayload, _ := json.Marshal(payload)
			return nil, previewBuildOutput{}, fmt.Errorf("build_error: %s", jsonPayload)
		}

		return nil, previewBuildOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
