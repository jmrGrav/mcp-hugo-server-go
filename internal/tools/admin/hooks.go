package admin

import (
	"bytes"
	"context"
	"fmt"
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
		Description: "[RequiredScope: site.admin] Fire all configured post-build webhook URLs. POSTs {\"event\":\"post_build\"} to each URL in post_build_hooks. Returns per-hook status or error. Only operator-configured URLs are fired (SSRF protected).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ runPostBuildHooksInput) (*mcp.CallToolResult, runPostBuildHooksOutput, error) {
		results, err := fireHooks(cfg, hookClient)
		if err != nil {
			return nil, runPostBuildHooksOutput{}, fmt.Errorf("hook_error: %w", err)
		}
		return nil, runPostBuildHooksOutput{Results: results}, nil
	})
}

func fireHooks(cfg config.Config, client *http.Client) ([]hookResult, error) {
	results := make([]hookResult, 0, len(cfg.PostBuildHooks))
	body := []byte(`{"event":"post_build"}`)

	for _, url := range cfg.PostBuildHooks {
		r := fireHook(client, url, body)
		results = append(results, r)
	}
	return results, nil
}

func fireHook(client *http.Client, url string, body []byte) hookResult {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return hookResult{URL: url, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return hookResult{URL: url, Error: err.Error()}
	}
	defer resp.Body.Close()

	return hookResult{URL: url, Status: resp.StatusCode}
}
