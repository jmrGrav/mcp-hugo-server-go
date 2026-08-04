package admin

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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
	TTLSeconds    *int `json:"ttl_seconds,omitempty"`
}

// createPreviewData is the canonical data.* payload (#552).
type createPreviewData struct {
	PreviewID           string `json:"preview_id"`
	URL                 string `json:"url"`
	ExpiresAt           string `json:"expires_at"`
	Build               string `json:"build"`
	EffectiveTTLSeconds int    `json:"effective_ttl_seconds"`
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

func resolvedPreviewTTL(input *int) (time.Duration, bool) {
	if input == nil {
		return previewDefaultTTL, false
	}
	ttl := time.Duration(*input) * time.Second
	clamped := false
	if ttl < previewMinTTL {
		ttl = previewMinTTL
		clamped = true
	}
	if ttl > previewMaxTTL {
		ttl = previewMaxTTL
		clamped = true
	}
	return ttl, clamped
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
			"expose it at a temporary preview entry URL for visual inspection. The entry URL is opaque (not a raw process ID), " +
			"non-indexable (X-Robots-Tag: noindex), isolated from the public site (a dedicated build, never cfg.SiteRoot), " +
			"and expires after ttl_seconds (default 900s, min 60s, max 3600s). On first open, the entry URL exchanges its token for an HttpOnly preview session cookie and redirects the browser to a clean `/preview/{id}/...` URL so internal asset and link requests no longer carry the bearer secret in every path. Preview builds run Hugo with `--environment preview` so templates can suppress preview-unsafe features such as share links. The response always echoes the actual applied TTL as `data.effective_ttl_seconds`; values outside the allowed range are clamped with a warning. Requires site.admin.",
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
		ttl, clamped := resolvedPreviewTTL(in.TTLSeconds)

		// Opportunistic cleanup of expired previews before adding a new one,
		// so disk usage doesn't grow unbounded even if nobody ever revisits
		// an expired preview URL (which is what triggers cleanup in Get).
		store.Sweep()

		// Enforce the active-preview and preview-disk caps (#871) before doing
		// any build work. These are soft caps: create_preview runs under only
		// ContentMu.RLock, so two concurrent creates can both pass these checks
		// and both Put — acceptable for a low-frequency admin operation, and
		// not worth reservation plumbing. A breach is surfaced explicitly as a
		// tool error (never a silent no-op), matching this repo's "clamped-with-
		// signal, never silent" convention. list_previews reports current usage
		// against these caps so an agent can self-regulate before hitting them.
		maxPerCaller := cfg.PreviewMaxPerCaller
		if maxPerCaller <= 0 {
			maxPerCaller = config.DefaultPreviewMaxPerCaller
		}
		maxDiskBytes := cfg.PreviewMaxDiskBytes
		if maxDiskBytes <= 0 {
			maxDiskBytes = config.DefaultPreviewMaxDiskBytes
		}
		owner := currentUserForLog()
		if n := store.CountByOwner(owner); n >= maxPerCaller {
			return nil, createPreviewOutput{}, fmt.Errorf("preview_limit_exceeded: caller already has %d active preview(s), at the configured limit of %d — revoke one with revoke_preview or wait for expiry before creating another", n, maxPerCaller)
		}
		if used := store.DiskUsageBytes(); used >= maxDiskBytes {
			return nil, createPreviewOutput{}, fmt.Errorf("preview_disk_limit_exceeded: active previews already use %d bytes of the configured %d-byte preview-disk budget — revoke previews or wait for expiry before creating another", used, maxDiskBytes)
		}

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
		previewAccessURL := strings.TrimRight(baseURL, "/") + "/preview/" + previewID + "/" + token + "/"
		previewBrowseURL := strings.TrimRight(baseURL, "/") + previewstore.CleanPath(previewID, "")

		tctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		cacheDir := hugoCacheDir(cfg)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			_ = os.RemoveAll(destDir)
			return nil, createPreviewOutput{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
		}

		args := []string{"--noBuildLock", "--cacheDir", cacheDir, "--destination", destDir, "--baseURL", previewBrowseURL, "--environment", "preview"}
		if in.IncludeDrafts {
			args = append(args, "--buildDrafts")
		}
		// #nosec G204 -- executable is fixed to hugo; args are derived from
		// server-controlled preview settings and validated config.
		cmd := exec.CommandContext(tctx, "hugo", args...)
		cmd.Dir = cfg.HugoRoot
		cmd.Env = boundedCommandEnv()
		setNewProcessGroup(cmd)
		cmd.Cancel = func() error {
			killProcessGroup(cmd)
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
			CreatedAt:   time.Now().UTC(),
			Owner:       currentUserForLog(),
		})

		slog.Info("create_preview completed",
			"tool", "create_preview",
			"user", currentUserForLog(),
			"preview_id", previewID,
			"include_drafts", in.IncludeDrafts,
			"ttl_seconds", int(ttl.Seconds()),
		)

		out := newCreatePreviewOutput(createPreviewData{
			PreviewID:           previewID,
			URL:                 previewAccessURL,
			ExpiresAt:           expiresAt.UTC().Format(time.RFC3339),
			Build:               "passed",
			EffectiveTTLSeconds: int(ttl.Seconds()),
		})
		if clamped {
			out.Warnings = append(out.Warnings, fmt.Sprintf("ttl_seconds was clamped to %d", int(ttl.Seconds())))
		}
		return nil, out, nil
	}))
}
