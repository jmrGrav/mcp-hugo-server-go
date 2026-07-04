package admin

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type buildSiteInput struct{}

type buildSiteOutput struct {
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

func RegisterBuild(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "build_site",
		Title:       "Build site",
		Description: "[RequiredScope: site.admin] Build the Hugo site. Returns duration in milliseconds. Returns build_in_progress error if a build is already running. Serialized with content mutations via ContentMu.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ buildSiteInput) (*mcp.CallToolResult, buildSiteOutput, error) {
		if !hugosite.ContentMu.TryLock() {
			return nil, buildSiteOutput{}, fmt.Errorf("build_in_progress: a content mutation or build is already running")
		}
		defer hugosite.ContentMu.Unlock()

		timeout := cfg.BuildTimeoutSeconds
		if timeout <= 0 {
			timeout = 120
		}
		tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		start := time.Now()
		cmd := exec.CommandContext(tctx, "hugo")
		cmd.Dir = cfg.SiteRoot
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()

		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		if err != nil {
			slog.Error("build_site failed", "duration_ms", durationMs, "exit_code", exitCode, "error", err)
			if tctx.Err() != nil {
				return nil, buildSiteOutput{}, fmt.Errorf("build_error: build timeout exceeded")
			}
			return nil, buildSiteOutput{}, fmt.Errorf("build_error: hugo exited with error")
		}

		slog.Info("build_site completed", "duration_ms", durationMs, "exit_code", exitCode)
		return nil, buildSiteOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
