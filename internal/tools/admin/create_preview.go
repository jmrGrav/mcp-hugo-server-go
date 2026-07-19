package admin

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	previewDefaultTTL = 15 * time.Minute
	previewMaxTTL     = 60 * time.Minute
	previewMinTTL     = 60 * time.Second
	previewIDBytes    = 8  // 64 bits — opaque identifier, not secret
	previewTokenBytes = 24 // 192 bits — the sole confidentiality boundary for preview content
)

type createPreviewInput struct {
	IncludeDrafts bool `json:"include_drafts,omitempty"`
	TTLSeconds    int  `json:"ttl_seconds,omitempty"`
}

// createPreviewData is the canonical data.* payload (#552).
type createPreviewData struct {
	PreviewID string `json:"preview_id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	Build     string `json:"build"`
}

// createPreviewOutput's payload lives only under data.* as of v1.5.9 (#573)
// — #552 originally added root-level compatibility aliases alongside data
// when this tool first gained an envelope, but #520 (v1.5.7) had already
// established data-only as the convention for every other tool that got an
// envelope around the same time; this finishes that convergence for the
// two tools #520 didn't cover. BREAKING: callers reading preview_id/url/
// expires_at/build at the root must switch to data.preview_id/data.url/
// data.expires_at/data.build.
type createPreviewOutput struct {
	toolcontract.ToolResponse[createPreviewData]
}

func createPreviewSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func newCreatePreviewOutput(data createPreviewData) createPreviewOutput {
	return createPreviewOutput{
		ToolResponse: createPreviewSuccessEnvelope(data),
	}
}

// RegisterCreatePreview wires create_preview (site.admin scope). Unlike
// preview_build (render-to-memory, no URL, no drafts), this builds actual
// files into an isolated temp directory — never cfg.SiteRoot — and
// registers them in store under an opaque, token-gated, time-limited URL so
// an agent can visually inspect pending changes (including drafts) without
// a raw PID, a long-lived local server, or exposure on the public site.
func RegisterCreatePreview(s *mcp.Server, cfg config.Config, store *previewstore.Store, baseURL string) {
	if s == nil || store == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_preview",
		Title: "Create preview",
		Description: "Build the current source (optionally including drafts) into an isolated, non-public directory and " +
			"expose it at a temporary, token-gated URL for visual inspection. The URL is opaque (not a raw process ID), " +
			"non-indexable (X-Robots-Tag: noindex), isolated from the public site (a dedicated build, never cfg.SiteRoot), " +
			"and expires after ttl_seconds (default 900s, max 3600s). Requires site.admin.",
		InputSchema:  tools.MustSchema[createPreviewInput](),
		OutputSchema: tools.MustSchema[createPreviewOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createPreviewInput) (*mcp.CallToolResult, createPreviewOutput, error) {
		if cfg.HugoRoot == "" {
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
		}
		ttl := time.Duration(in.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = previewDefaultTTL
		}
		if ttl < previewMinTTL {
			ttl = previewMinTTL
		}
		if ttl > previewMaxTTL {
			ttl = previewMaxTTL
		}

		// Opportunistic cleanup of expired previews before adding a new one,
		// so disk usage doesn't grow unbounded even if nobody ever revisits
		// an expired preview URL (which is what triggers cleanup in Get).
		store.Sweep()

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryRLock() {
				break
			}
			if time.Now().After(deadline) {
				return nil, createPreviewOutput{}, fmt.Errorf("build_in_progress: content mutation in progress, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer hugosite.ContentMu.RUnlock()

		destDir, err := os.MkdirTemp("", "mcp-preview-*")
		if err != nil {
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: failed to create isolated preview directory")
		}

		// Generate the id/token before the build (not after) so the build's
		// --baseURL can point at this preview's own mount path. Without this,
		// Hugo would emit asset/link URLs rooted at the site's configured
		// baseURL (or root-relative "/css/..."), and every asset request from
		// a browser opening the preview would 404 against the real mount at
		// /preview/{id}/{token}/ — the preview would render unstyled/broken.
		previewID, err := previewstore.NewID(previewIDBytes)
		if err != nil {
			_ = os.RemoveAll(destDir)
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: failed to allocate preview identifier")
		}
		token, err := previewstore.NewID(previewTokenBytes)
		if err != nil {
			_ = os.RemoveAll(destDir)
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: failed to allocate preview token")
		}
		previewURLBase := strings.TrimRight(baseURL, "/") + "/preview/" + previewID + "/" + token + "/"

		tctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		cacheDir := hugoCacheDir(cfg)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			_ = os.RemoveAll(destDir)
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
		}

		args := []string{"--noBuildLock", "--cacheDir", cacheDir, "--destination", destDir, "--baseURL", previewURLBase}
		if in.IncludeDrafts {
			args = append(args, "--buildDrafts")
		}
		cmd := exec.CommandContext(tctx, "hugo", args...)
		cmd.Dir = cfg.HugoRoot
		cmd.Env = boundedCommandEnv()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return nil
		}
		var stderrBuf, stdoutBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		cmd.Stdout = &stdoutBuf
		runErr := cmd.Run()

		if runErr != nil {
			_ = os.RemoveAll(destDir)
			summary := buildOutputSummary(stderrBuf.Bytes(), stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot)
			errClass := classifyBuildFailure(tctx, summary)
			slog.Error("create_preview failed",
				"tool", "create_preview",
				"user", currentUserForLog(),
				"command", commandString("hugo", args),
				"cwd", cfg.HugoRoot,
				"error_class", errClass,
				"output_summary", summary,
				"error", runErr,
			)
			return nil, createPreviewOutput{}, fmt.Errorf("build_error: %s", summary)
		}

		expiresAt := time.Now().Add(ttl)
		store.Put(previewID, &previewstore.Entry{
			Dir:         destDir,
			Token:       token,
			ExpiresAt:   expiresAt,
			BuildStatus: "passed",
		})

		slog.Info("create_preview completed",
			"tool", "create_preview",
			"user", currentUserForLog(),
			"preview_id", previewID,
			"include_drafts", in.IncludeDrafts,
			"ttl_seconds", int(ttl.Seconds()),
		)

		return nil, newCreatePreviewOutput(createPreviewData{
			PreviewID: previewID,
			URL:       previewURLBase,
			ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
			Build:     "passed",
		}), nil
	}))
}
