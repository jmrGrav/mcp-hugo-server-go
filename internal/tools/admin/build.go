package admin

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	buildMu         sync.Mutex
	buildInProgress bool
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
		Description: "[RequiredScope: site.admin] Build the Hugo site. Returns duration in milliseconds. Returns build_in_progress error if a build is already running.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ buildSiteInput) (*mcp.CallToolResult, buildSiteOutput, error) {
		buildMu.Lock()
		if buildInProgress {
			buildMu.Unlock()
			return nil, buildSiteOutput{}, fmt.Errorf("build_in_progress")
		}
		buildInProgress = true
		buildMu.Unlock()

		defer func() {
			buildMu.Lock()
			buildInProgress = false
			buildMu.Unlock()
		}()

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
			return nil, buildSiteOutput{}, fmt.Errorf("build_error: %w", err)
		}

		slog.Info("build_site completed", "duration_ms", durationMs, "exit_code", exitCode)
		return nil, buildSiteOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
