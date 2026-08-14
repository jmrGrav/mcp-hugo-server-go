package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
}

type hookResult struct {
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// runPostBuildHooksData is the canonical data.* payload (#552).
type runPostBuildHooksData struct {
	Status          string       `json:"status"`
	Results         []hookResult `json:"results"`
	DryRun          bool         `json:"dry_run,omitempty"`
	ConfiguredCount int          `json:"configured_count"`
}

// runPostBuildHooksOutput carries legacy root aliases for compatibility with
// clients predating the structured envelope (#552). `data.*` is canonical;
// the root aliases are deprecated and kept only through the v1 window.
type runPostBuildHooksOutput struct {
	toolcontract.ToolResponse[runPostBuildHooksData]
	Status          string       `json:"status"`
	Results         []hookResult `json:"results"`
	DryRun          bool         `json:"dry_run,omitempty"`
	ConfiguredCount int          `json:"configured_count"`
}

func hooksSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	out := toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
	out.Warnings = append(out.Warnings, rootLevelFieldsDeprecationWarning)
	return out
}

func newRunPostBuildHooksOutput(data runPostBuildHooksData) runPostBuildHooksOutput {
	return runPostBuildHooksOutput{
		ToolResponse:    hooksSuccessEnvelope(data),
		Status:          data.Status,
		Results:         data.Results,
		DryRun:          data.DryRun,
		ConfiguredCount: data.ConfiguredCount,
	}
}

func RegisterHooks(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:         "run_post_build_hooks",
		Title:        "Run post-build hooks",
		Description:  "Fire all configured post-build webhook URLs. Sends {\"event\":\"post_build\"} to each operator-configured hook and returns per-hook status or error. Set `dry_run:true` to inspect the configured hook targets without contacting them; this returns the same `results[]` URL list plus `configured_count`, making `no hooks configured` distinguishable from `hooks configured but intentionally not executed`. `data.status` is `no_hooks_configured` when zero hooks are configured, `dry_run` when hooks are configured but intentionally not contacted, and `completed` when one or more hooks were actually attempted. Only configured URLs are ever reported or contacted.",
		InputSchema:  tools.MustSchema[runPostBuildHooksInput](),
		OutputSchema: tools.MustSchema[runPostBuildHooksOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in runPostBuildHooksInput) (*mcp.CallToolResult, runPostBuildHooksOutput, error) {
		if in.DryRun {
			results := previewHooks(cfg)
			status := "dry_run"
			if len(results) == 0 {
				status = "no_hooks_configured"
			}
			return nil, newRunPostBuildHooksOutput(runPostBuildHooksData{
				Status:          status,
				Results:         results,
				DryRun:          true,
				ConfiguredCount: len(results),
			}), nil
		}
		results := fireHooks(ctx, cfg, hookClient)
		status := "completed"
		if len(cfg.PostBuildHooks) == 0 {
			status = "no_hooks_configured"
		}
		return nil, newRunPostBuildHooksOutput(runPostBuildHooksData{
			Status:          status,
			Results:         results,
			ConfiguredCount: len(cfg.PostBuildHooks),
		}), nil
	}))
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
	req.Header.Set("Content-Type", "application/json")

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
