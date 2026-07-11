package indexnow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

const defaultEndpoint = "https://api.indexnow.org/indexnow"

var httpClient = &http.Client{Timeout: 10 * time.Second}

type submitRequest struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

// Submit sends a list of URLs to IndexNow. No-op when cfg is not enabled or
// the URL list is empty. Taxonomy and search URLs are filtered out automatically.
func Submit(cfg config.IndexNowConfig, urls []string) error {
	if !cfg.Enabled() || len(urls) == 0 {
		return nil
	}
	filtered := filterIndexable(urls)
	if len(filtered) == 0 {
		return nil
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	keyLocation := cfg.KeyLocation
	if keyLocation == "" && cfg.Host != "" && cfg.Key != "" {
		keyLocation = "https://" + strings.TrimPrefix(cfg.Host, "https://") + "/" + cfg.Key + ".txt"
	}

	payload := submitRequest{
		Host:        cfg.Host,
		Key:         cfg.Key,
		KeyLocation: keyLocation,
		URLList:     filtered,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("indexnow: marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("indexnow: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("indexnow: HTTP error: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		return fmt.Errorf("indexnow: unexpected status %d", resp.StatusCode)
	}
	slog.Info("indexnow: submitted", "urls", len(filtered), "status", resp.StatusCode)
	return nil
}

// SubmitURL is a convenience wrapper for a single URL.
func SubmitURL(cfg config.IndexNowConfig, url string) error {
	return Submit(cfg, []string{url})
}

var taxonomyPrefixes = []string{"/tags/", "/categories/", "/authors/", "/search/", "/en/tags/", "/en/categories/"}

func filterIndexable(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		skip := false
		for _, pfx := range taxonomyPrefixes {
			if strings.Contains(u, pfx) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, u)
		}
	}
	return out
}
