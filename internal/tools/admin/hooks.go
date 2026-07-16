package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
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
		Name:         "run_post_build_hooks",
		Title:        "Run post-build hooks",
		Description:  "Fire all configured post-build webhook URLs. Sends {\"event\":\"post_build\"} to each operator-configured hook and returns per-hook status or error. Only configured URLs are contacted.",
		InputSchema:  tools.MustSchema[runPostBuildHooksInput](),
		OutputSchema: tools.MustSchema[runPostBuildHooksOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
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
