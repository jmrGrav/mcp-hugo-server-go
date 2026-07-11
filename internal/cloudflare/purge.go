package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

const apiBase = "https://api.cloudflare.com/client/v4/zones"

var httpClient = &http.Client{Timeout: 15 * time.Second}

type purgeRequest struct {
	PurgeEverything bool     `json:"purge_everything,omitempty"`
	Files           []string `json:"files,omitempty"`
}

type purgeResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// PurgeAll purges the entire Cloudflare zone cache.
// No-op when cfg is not enabled.
func PurgeAll(cfg config.CloudflareConfig) error {
	if !cfg.Enabled() {
		return nil
	}
	return callPurge(cfg, purgeRequest{PurgeEverything: true})
}

// PurgeURLs purges specific URLs from the Cloudflare edge cache.
// No-op when cfg is not enabled or urls is empty.
func PurgeURLs(cfg config.CloudflareConfig, urls []string) error {
	if !cfg.Enabled() || len(urls) == 0 {
		return nil
	}
	return callPurge(cfg, purgeRequest{Files: urls})
}

func callPurge(cfg config.CloudflareConfig, req purgeRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("cloudflare: marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s/purge_cache", apiBase, cfg.ZoneID)
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cloudflare: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cloudflare: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var cfResp purgeResponse
	if err := json.Unmarshal(raw, &cfResp); err != nil {
		return fmt.Errorf("cloudflare: parse response (status %d): %w", resp.StatusCode, err)
	}
	if !cfResp.Success {
		msgs := make([]string, 0, len(cfResp.Errors))
		for _, e := range cfResp.Errors {
			msgs = append(msgs, fmt.Sprintf("[%d] %s", e.Code, e.Message))
		}
		return fmt.Errorf("cloudflare: purge failed: %v", msgs)
	}

	if req.PurgeEverything {
		slog.Info("cloudflare: purged all cache", "zone", cfg.ZoneID)
	} else {
		slog.Info("cloudflare: purged urls", "zone", cfg.ZoneID, "count", len(req.Files))
	}
	return nil
}
