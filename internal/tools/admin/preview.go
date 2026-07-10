package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
		if err := checkBuildWritable(filepath.Join(cfg.HugoRoot, "resources")); err != nil {
			return nil, previewBuildOutput{}, err
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

		cacheDir := hugoCacheDir(cfg)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, previewBuildOutput{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
		}
		start := time.Now()
		runID := newBuildID(start)
		args := buildCommandArgs(cacheDir, true)
		cmd := exec.CommandContext(tctx, "hugo", args...)
		cmd.Dir = cfg.HugoRoot
		var stderrBuf bytes.Buffer
		var stdoutBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		cmd.Stdout = &stdoutBuf
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()

		if err != nil {
			exitCode := 0
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			summary := buildOutputSummary(stderrBuf.Bytes(), stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot)
			errClass := classifyBuildFailure(tctx, summary)

			slog.Error("preview_build failed",
				"build_id", runID,
				"tool", "preview_build",
				"user", currentUserForLog(),
				"command", commandString("hugo", args),
				"cwd", cfg.HugoRoot,
				"cache_dir", cacheDir,
				"duration_ms", durationMs,
				"exit_code", exitCode,
				"error_class", errClass,
				"stdout_tail", outputTail(stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
				"stderr_tail", outputTail(stderrBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
				"output_summary", summary,
				"error", err,
			)

			errKind := "build_error"
			if tctx.Err() != nil {
				errKind = "build_timeout"
			}

			payload := buildErrorPayload{
				Error:            errKind,
				ErrorClass:       errClass,
				ExitCode:         exitCode,
				Command:          commandString("hugo", args),
				WorkingDirectory: cfg.HugoRoot,
				CacheDirectory:   cacheDir,
				DurationMs:       durationMs,
				StderrSummary:    summary,
				StdoutSummary:    outputTail(stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
				BuildID:          runID,
				LogHint:          "Search server logs for build_id=" + runID,
			}
			if errClass == "permission_denied" {
				payload.Suggestion = "Verify that hugo_root/resources is listed in ReadWritePaths in the systemd service override. Run: systemctl cat mcp-hugo-server-go"
				payload.DocsURL = buildDocsURL
			}
			jsonPayload, _ := json.Marshal(payload)
			return nil, previewBuildOutput{}, fmt.Errorf("build_error: %s", jsonPayload)
		}

		slog.Info("preview_build completed",
			"build_id", runID,
			"tool", "preview_build",
			"user", currentUserForLog(),
			"command", commandString("hugo", args),
			"cwd", cfg.HugoRoot,
			"cache_dir", cacheDir,
			"duration_ms", durationMs,
			"exit_code", 0,
		)
		return nil, previewBuildOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
