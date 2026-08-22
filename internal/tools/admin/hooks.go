package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxHookResponseBytes = 1 << 20

func newHookHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("redirects are not allowed for post-build hooks")
		},
	}
}

var hookClient = newHookHTTPClient(10 * time.Second)

type runPostBuildHooksInput struct {
	DryRun bool `json:"dry_run,omitempty"`
	// Status reports the outcome of the build/deploy this call follows.
	// "success" (default) contacts heartbeat_hooks URLs unsuffixed; "fail"
	// appends "/fail" per the BetterStack heartbeat-failure convention.
	// post_build_hooks URLs are never suffixed regardless of Status — they
	// are generic webhook targets, not heartbeat monitors.
	Status string `json:"status,omitempty"`
}

type hookResult struct {
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// runPostBuildHooksData is the canonical data.* payload (#552).
type runPostBuildHooksData struct {
	Status                   string       `json:"status"`
	Results                  []hookResult `json:"results"`
	DryRun                   bool         `json:"dry_run,omitempty"`
	ConfiguredCount          int          `json:"configured_count"`
	HeartbeatResults         []hookResult `json:"heartbeat_results"`
	HeartbeatConfiguredCount int          `json:"heartbeat_configured_count"`
}

// runPostBuildHooksOutput's payload lives only under data.* (#1118 finishes
// #520/#573's root/data convergence for this tool — the root aliases #552
// originally added, and #1060 deprecated, are removed).
type runPostBuildHooksOutput struct {
	toolcontract.ToolResponse[runPostBuildHooksData]
}

func hooksSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func newRunPostBuildHooksOutput(data runPostBuildHooksData) runPostBuildHooksOutput {
	return runPostBuildHooksOutput{
		ToolResponse: hooksSuccessEnvelope(data),
	}
}

func RegisterHooks(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:         "run_post_build_hooks",
		Title:        "Run post-build hooks",
		Description:  "Fire all configured post-build webhook URLs, plus any configured heartbeat monitor URLs. Sends {\"event\":\"post_build\"} to each operator-configured post_build_hooks URL and returns per-hook status or error in `results[]`. Separately, contacts each operator-configured heartbeat_hooks URL (e.g. a BetterStack heartbeat) and returns per-hook status or error in `heartbeat_results[]`; pass `status:\"fail\"` to report a failed build/deploy, which appends `/fail` to each heartbeat URL per the BetterStack heartbeat-failure convention (omit or pass `status:\"success\"` for the default unsuffixed ping). post_build_hooks URLs are never suffixed by `status` — only heartbeat_hooks are. Set `dry_run:true` to inspect the configured hook/heartbeat targets without contacting them; this returns the same URL lists (heartbeat URLs shown with the effective `/fail` suffix already applied when `status:\"fail\"`) plus `configured_count`/`heartbeat_configured_count`, making `no hooks configured` distinguishable from `hooks configured but intentionally not executed`. `data.status` is `no_hooks_configured` when zero post_build_hooks are configured, `dry_run` when hooks are configured but intentionally not contacted, and `completed` when one or more hooks were actually attempted. Only configured URLs (with the documented `/fail` suffix, when applicable) are ever reported or contacted.",
		InputSchema:  tools.MustSchema[runPostBuildHooksInput](),
		OutputSchema: tools.MustSchema[runPostBuildHooksOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in runPostBuildHooksInput) (*mcp.CallToolResult, runPostBuildHooksOutput, error) {
		failed, err := parseHeartbeatStatus(in.Status)
		if err != nil {
			return nil, runPostBuildHooksOutput{}, err
		}

		if in.DryRun {
			results := previewHooks(cfg)
			heartbeatResults := previewHeartbeats(cfg, failed)
			status := "dry_run"
			if len(results) == 0 {
				status = "no_hooks_configured"
			}
			return nil, newRunPostBuildHooksOutput(runPostBuildHooksData{
				Status:                   status,
				Results:                  results,
				DryRun:                   true,
				ConfiguredCount:          len(results),
				HeartbeatResults:         heartbeatResults,
				HeartbeatConfiguredCount: len(heartbeatResults),
			}), nil
		}
		results := fireHooks(ctx, cfg, hookClient)
		heartbeatResults := fireHeartbeats(ctx, cfg, hookClient, failed)
		status := "completed"
		if len(cfg.PostBuildHooks) == 0 {
			status = "no_hooks_configured"
		}
		return nil, newRunPostBuildHooksOutput(runPostBuildHooksData{
			Status:                   status,
			Results:                  results,
			ConfiguredCount:          len(cfg.PostBuildHooks),
			HeartbeatResults:         heartbeatResults,
			HeartbeatConfiguredCount: len(cfg.HeartbeatHooks),
		}), nil
	}))
}

// parseHeartbeatStatus validates the input Status field, returning whether
// the caller is reporting a failed build/deploy. Only the fixed "success"/
// "fail" vocabulary is accepted (not an arbitrary exit code) so the value
// can never be attacker- or caller-controlled path input.
func parseHeartbeatStatus(status string) (failed bool, err error) {
	switch status {
	case "", "success":
		return false, nil
	case "fail":
		return true, nil
	default:
		return false, fmt.Errorf("invalid_params: status must be \"success\" or \"fail\", got %q", status)
	}
}

// heartbeatURL appends the BetterStack heartbeat-failure suffix to url when
// failed is true, leaving url unchanged otherwise.
func heartbeatURL(url string, failed bool) string {
	if !failed {
		return url
	}
	if strings.HasSuffix(url, "/") {
		return url + "fail"
	}
	return url + "/fail"
}

func previewHeartbeats(cfg config.Config, failed bool) []hookResult {
	results := make([]hookResult, 0, len(cfg.HeartbeatHooks))
	for _, url := range cfg.HeartbeatHooks {
		results = append(results, hookResult{URL: heartbeatURL(url, failed)})
	}
	return results
}

func fireHeartbeats(ctx context.Context, cfg config.Config, client *http.Client, failed bool) []hookResult {
	results := make([]hookResult, 0, len(cfg.HeartbeatHooks))
	for _, url := range cfg.HeartbeatHooks {
		r := fireHook(ctx, client, heartbeatURL(url, failed), nil)
		results = append(results, r)
	}
	return results
}

// PingHeartbeats fires every configured heartbeat_hooks URL, best-effort,
// signalling build success (unsuffixed) or failure (`/fail` suffix). Wired
// as a build_site PostBuildCallback (see internal/server postBuildCallbacks)
// so a configured heartbeat monitor (e.g. BetterStack) actually reflects
// real build/deploy outcomes, rather than only firing when an operator
// remembers to call run_post_build_hooks by hand.
func PingHeartbeats(ctx context.Context, cfg config.Config, failed bool) []hookResult {
	return fireHeartbeats(ctx, cfg, hookClient, failed)
}

func previewHooks(cfg config.Config) []hookResult {
	results := make([]hookResult, 0, len(cfg.PostBuildHooks))
	for _, url := range cfg.PostBuildHooks {
		results = append(results, hookResult{URL: url})
	}
	return results
}

func fireHooks(ctx context.Context, cfg config.Config, client *http.Client) []hookResult {
	results := make([]hookResult, 0, len(cfg.PostBuildHooks))
	body := []byte(`{"event":"post_build"}`)

	for _, url := range cfg.PostBuildHooks {
		r := fireHook(ctx, client, url, body)
		results = append(results, r)
	}
	return results
}

func fireHook(ctx context.Context, client *http.Client, url string, body []byte) hookResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return hookResult{URL: url, Error: err.Error()}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return hookResult{URL: url, Error: err.Error()}
	}
	defer resp.Body.Close()
	n, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxHookResponseBytes+1))
	if copyErr != nil {
		return hookResult{URL: url, Error: copyErr.Error()}
	}
	if n > maxHookResponseBytes {
		return hookResult{URL: url, Error: fmt.Sprintf("response_too_large: response body exceeded %d bytes", maxHookResponseBytes)}
	}

	return hookResult{URL: url, Status: resp.StatusCode}
}
