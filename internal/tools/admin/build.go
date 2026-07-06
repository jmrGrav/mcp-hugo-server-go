package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type buildSiteInput struct{}

type buildSiteOutput struct {
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

// buildErrorPayload is the structured JSON returned on Hugo failure.
type buildErrorPayload struct {
	Error            string `json:"error"`
	ExitCode         int    `json:"exit_code"`
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
	DurationMs       int64  `json:"duration_ms"`
	StderrSummary    string `json:"stderr_summary"`
	BuildID          string `json:"build_id"`
	LogHint          string `json:"log_hint"`
}

// newBuildID generates a build identifier of the form YYYYMMDD-HHMMSS-<4 random lowercase hex chars>.
func newBuildID(t time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return t.UTC().Format("20060102-150405") + "-" + fmt.Sprintf("%04x", b)
}

// truncateUTF8 returns a string from b that is at most maxBytes bytes and ends
// on a valid UTF-8 boundary.
func truncateUTF8(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	b = b[:maxBytes]
	// Walk back continuation bytes (0x80–0xBF).
	for len(b) > 0 && b[len(b)-1]&0xC0 == 0x80 {
		b = b[:len(b)-1]
	}
	// Remove a stranded leading byte (0xC0–0xFF) left by the walk-back.
	if len(b) > 0 && b[len(b)-1]&0xC0 == 0xC0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// sanitiseStderr redacts absolute paths in raw stderr, then truncates to 500
// bytes on a valid UTF-8 boundary. Sanitisation happens before truncation so
// that paths near the 500-byte limit are always redacted.
func sanitiseStderr(raw []byte, hugoRoot, siteRoot string) string {
	s := string(raw)
	if hugoRoot != "" {
		s = strings.ReplaceAll(s, hugoRoot, "<site_root>")
	}
	if siteRoot != "" && siteRoot != hugoRoot {
		s = strings.ReplaceAll(s, siteRoot, "<site_root>")
	}
	return strings.TrimSpace(truncateUTF8([]byte(s), 500))
}

func RegisterBuild(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "build_site",
		Title:       "Build website",
		Description: "Build the Hugo site and return the build duration in milliseconds. Use this after content changes or before publishing. Returns build_in_progress if another build or content mutation is active.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ buildSiteInput) (*mcp.CallToolResult, buildSiteOutput, error) {
		if cfg.HugoRoot == "" {
			return nil, buildSiteOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
		}
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
		cmd.Dir = cfg.HugoRoot
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()

		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		if err != nil {
			buildID := newBuildID(start)
			slog.Error("build_site failed",
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
				Command:          "hugo",
				WorkingDirectory: cfg.HugoRoot,
				DurationMs:       durationMs,
				StderrSummary:    sanitiseStderr(stderrBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
				BuildID:          buildID,
				LogHint:          "Search server logs for build_id=" + buildID,
			}
			jsonPayload, _ := json.Marshal(payload)
			return nil, buildSiteOutput{}, fmt.Errorf("build_error: %s", jsonPayload)
		}

		slog.Info("build_site completed", "duration_ms", durationMs, "exit_code", exitCode)
		return nil, buildSiteOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
