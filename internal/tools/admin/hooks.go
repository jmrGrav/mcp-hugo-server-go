package admin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var hookClient = &http.Client{Timeout: 10 * time.Second}

type runPostBuildHooksInput struct{}

type hookResult struct {
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

type runPostBuildHooksOutput struct {
	Results []hookResult `json:"results"`
}

func RegisterHooks(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "run_post_build_hooks",
		Title:       "Run post-build hooks",
		Description: "[RequiredScope: site.admin] Fire all configured post-build webhook URLs. Sends {\"event\":\"post_build\"} to each operator-configured hook and returns per-hook status or error. Only configured URLs are contacted.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ runPostBuildHooksInput) (*mcp.CallToolResult, runPostBuildHooksOutput, error) {
		results := fireHooks(ctx, cfg, hookClient)
		return nil, runPostBuildHooksOutput{Results: results}, nil
	})
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
	_, _ = io.Copy(io.Discard, resp.Body)
	defer resp.Body.Close()

	return hookResult{URL: url, Status: resp.StatusCode}
}
