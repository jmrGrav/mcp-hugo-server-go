package cloudflare

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func withHTTPClient(t *testing.T, rt roundTripFunc) {
	t.Helper()
	prev := httpClient
	httpClient = &http.Client{Transport: rt}
	t.Cleanup(func() { httpClient = prev })
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestPurgeNoopWhenDisabledOrEmpty(t *testing.T) {
	called := false
	withHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		called = true
		return response(http.StatusOK, `{"success":true}`), nil
	})

	if err := PurgeAll(config.CloudflareConfig{}); err != nil {
		t.Fatalf("PurgeAll(disabled) error = %v", err)
	}
	if err := PurgeURLs(config.CloudflareConfig{}, []string{"https://example.test/a"}); err != nil {
		t.Fatalf("PurgeURLs(disabled) error = %v", err)
	}
	cfg := config.CloudflareConfig{ZoneID: "zone", APIToken: "token"}
	if err := PurgeURLs(cfg, nil); err != nil {
		t.Fatalf("PurgeURLs(empty) error = %v", err)
	}
	if called {
		t.Fatal("HTTP client should not be called for disabled/empty purge")
	}
}

func TestPurgeAllBuildsAuthenticatedRequest(t *testing.T) {
	cfg := config.CloudflareConfig{ZoneID: "zone-123", APIToken: "secret"}
	withHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s want POST", r.Method)
		}
		if got := r.URL.String(); got != apiBase+"/zone-123/purge_cache" {
			t.Fatalf("url = %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll(body) error = %v", err)
		}
		if !strings.Contains(string(raw), `"purge_everything":true`) {
			t.Fatalf("body = %s", raw)
		}
		return response(http.StatusOK, `{"success":true}`), nil
	})

	if err := PurgeAll(cfg); err != nil {
		t.Fatalf("PurgeAll() error = %v", err)
	}
}

func TestPurgeURLsErrorPaths(t *testing.T) {
	cfg := config.CloudflareConfig{ZoneID: "zone-123", APIToken: "secret"}

	t.Run("HTTP failure", func(t *testing.T) {
		withHTTPClient(t, func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})
		err := PurgeURLs(cfg, []string{"https://example.test/a"})
		if err == nil || !strings.Contains(err.Error(), "HTTP request failed") {
			t.Fatalf("PurgeURLs() error = %v", err)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		withHTTPClient(t, func(r *http.Request) (*http.Response, error) {
			return response(http.StatusBadGateway, `not-json`), nil
		})
		err := PurgeURLs(cfg, []string{"https://example.test/a"})
		if err == nil || !strings.Contains(err.Error(), "parse response") {
			t.Fatalf("PurgeURLs() error = %v", err)
		}
	})

	t.Run("Cloudflare API error", func(t *testing.T) {
		withHTTPClient(t, func(r *http.Request) (*http.Response, error) {
			return response(http.StatusBadRequest, `{"success":false,"errors":[{"code":1003,"message":"bad"}]}`), nil
		})
		err := PurgeURLs(cfg, []string{"https://example.test/a"})
		if err == nil || !strings.Contains(err.Error(), "purge failed") || !strings.Contains(err.Error(), "[1003] bad") {
			t.Fatalf("PurgeURLs() error = %v", err)
		}
	})
}
