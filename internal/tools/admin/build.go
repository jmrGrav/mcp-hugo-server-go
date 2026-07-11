package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
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
	ErrorClass       string `json:"error_class,omitempty"`
	ExitCode         int    `json:"exit_code"`
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
	CacheDirectory   string `json:"cache_directory,omitempty"`
	DurationMs       int64  `json:"duration_ms"`
	StderrSummary    string `json:"stderr_summary"`
	StdoutSummary    string `json:"stdout_summary,omitempty"`
	BuildID          string `json:"build_id"`
	LogHint          string `json:"log_hint"`
	Suggestion       string `json:"suggestion,omitempty"`
	DocsURL          string `json:"docs_url,omitempty"`
}

// buildPreflightPayload is the structured JSON returned when a pre-flight check fails.
type buildPreflightPayload struct {
	Error        string `json:"error"`
	ErrorClass   string `json:"error_class"`
	Path         string `json:"path"`
	OperatorHint string `json:"operator_hint"`
	Suggestion   string `json:"suggestion"`
	DocsURL      string `json:"docs_url"`
	Retryable    bool   `json:"retryable"`
}

const buildDocsURL = "docs/operator-guide.md#build-permissions"

// checkBuildWritable verifies that the directories Hugo must write to are
// accessible before invoking the build. Returns a structured JSON error on
// the first problematic path found.
//
// Two checks per directory:
//  1. os.CreateTemp — confirms write permission (ReadWritePaths configured)
//  2. directory uid == euid — Hugo calls chtimes on pre-existing files it
//     copies into the output directory; POSIX requires ownership (not just
//     write) for non-NULL timestamps. A dir owned by a different user means
//     its existing files will trigger EPERM on chtimes.
func checkBuildWritable(paths ...string) error {
	euid := os.Geteuid()
	for _, dir := range paths {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return buildPreflightError(dir)
		}
		f, err := os.CreateTemp(dir, ".mcp-preflight-*.tmp")
		if err != nil {
			return buildPreflightError(dir)
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
		// Check ownership: chtimes on pre-existing files requires the process
		// to own them. If the directory itself belongs to a different uid,
		// its pre-existing files are almost certainly owned by that uid too.
		fi, statErr := os.Stat(dir)
		if statErr != nil {
			return buildPreflightError(dir)
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != euid {
			return buildPreflightChownError(dir)
		}
	}
	return nil
}

func buildPreflightError(dir string) error {
	payload := buildPreflightPayload{
		Error:        "build_precondition_failed",
		ErrorClass:   "permission_denied",
		Path:         dir,
		OperatorHint: "Add this path to ReadWritePaths in the systemd service override and reload: sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go",
		Suggestion:   "Check that the MCP service user owns or has write access to this directory, and that it is listed in ReadWritePaths in the systemd service.",
		DocsURL:      buildDocsURL,
		Retryable:    false,
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("build_precondition_failed: %s", b)
}

func buildPreflightChownError(dir string) error {
	payload := buildPreflightPayload{
		Error:        "build_precondition_failed",
		ErrorClass:   "permission_denied",
		Path:         dir,
		OperatorHint: "Run: sudo chown -R $(systemctl show mcp-hugo-server-go -p User --value) " + dir + " && sudo systemctl restart mcp-hugo-server-go",
		Suggestion:   "The MCP service user can write to this directory but does not own it. Hugo requires ownership to set file timestamps (chtimes). Change ownership to the service user.",
		DocsURL:      buildDocsURL,
		Retryable:    false,
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("build_precondition_failed: %s", b)
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

func buildOutputSummary(stderr, stdout []byte, hugoRoot, siteRoot string) string {
	if strings.TrimSpace(string(stderr)) != "" {
		return sanitiseStderr(stderr, hugoRoot, siteRoot)
	}
	return sanitiseStderr(stdout, hugoRoot, siteRoot)
}

func outputTail(raw []byte, hugoRoot, siteRoot string) string {
	return sanitiseStderr(raw, hugoRoot, siteRoot)
}

func hugoCacheDir(cfg config.Config) string {
	if p := strings.TrimSpace(cfg.OAuth.StoragePath); p != "" {
		return filepath.Join(filepath.Dir(p), "hugo-cache")
	}
	return filepath.Join(os.TempDir(), "mcp-hugo-server-go", "hugo-cache")
}

func buildCommandArgs(cacheDir string, preview bool) []string {
	args := []string{"--noBuildLock", "--cacheDir", cacheDir}
	if preview {
		args = append(args, "--renderToMemory")
	}
	return args
}

func commandString(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

func currentUserForLog() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}

func classifyBuildFailure(ctx context.Context, summary string) string {
	switch {
	case ctx.Err() != nil:
		return "timeout"
	case strings.Contains(strings.ToLower(summary), "permission denied"),
		strings.Contains(strings.ToLower(summary), "read-only file system"),
		strings.Contains(strings.ToLower(summary), "operation not permitted"):
		return "permission_denied"
	default:
		return "build_error"
	}
}

func RegisterBuild(s *mcp.Server, cfg config.Config, siteReload ...func() error) {
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

		if err := checkBuildWritable(cfg.SiteRoot, filepath.Join(cfg.HugoRoot, "resources")); err != nil {
			return nil, buildSiteOutput{}, err
		}

		timeout := cfg.BuildTimeoutSeconds
		if timeout <= 0 {
			timeout = 120
		}
		cacheDir := hugoCacheDir(cfg)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, buildSiteOutput{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
		}
		tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		start := time.Now()
		runID := newBuildID(start)
		args := buildCommandArgs(cacheDir, false)
		cmd := exec.CommandContext(tctx, "hugo", args...)
		cmd.Dir = cfg.HugoRoot
		var stderrBuf bytes.Buffer
		var stdoutBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		cmd.Stdout = &stdoutBuf
		err := cmd.Run()
		durationMs := time.Since(start).Milliseconds()

		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		if err != nil {
			summary := buildOutputSummary(stderrBuf.Bytes(), stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot)
			errClass := classifyBuildFailure(tctx, summary)
			slog.Error("build_site failed",
				"build_id", runID,
				"tool", "build_site",
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
				payload.Suggestion = "Verify that site_root and hugo_root/resources are listed in ReadWritePaths in the systemd service override. Run: systemctl cat mcp-hugo-server-go"
				payload.DocsURL = buildDocsURL
			}
			jsonPayload, _ := json.Marshal(payload)
			return nil, buildSiteOutput{}, fmt.Errorf("build_error: %s", jsonPayload)
		}

		slog.Info("build_site completed",
			"build_id", runID,
			"tool", "build_site",
			"user", currentUserForLog(),
			"command", commandString("hugo", args),
			"cwd", cfg.HugoRoot,
			"cache_dir", cacheDir,
			"duration_ms", durationMs,
			"exit_code", exitCode,
		)
		if len(siteReload) > 0 && siteReload[0] != nil {
			if err := siteReload[0](); err != nil {
				slog.Warn("build_site: site index reload failed", "error", err)
			} else {
				slog.Info("build_site: site index reloaded")
			}
		}
		return nil, buildSiteOutput{Status: "ok", DurationMs: durationMs}, nil
	})
}
