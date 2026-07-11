package googleindex

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestSubmitPartialQuotaAndAllFailures(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota.json")
	cfg := config.GoogleIndexConfig{
		ServiceAccountPath: filepath.Join(t.TempDir(), "service-account.json"),
		QuotaStatePath:     statePath,
		DailyQuotaLimit:    2,
	}
	if err := os.WriteFile(cfg.ServiceAccountPath, []byte(`{`), 0o644); err != nil {
		t.Fatalf("WriteFile(service account) error = %v", err)
	}
	tokenMu.Lock()
	cachedToken = "cached-token"
	tokenExpiry = time.Now().Add(5 * time.Minute)
	tokenMu.Unlock()
	t.Cleanup(func() {
		tokenMu.Lock()
		cachedToken = ""
		tokenExpiry = time.Time{}
		tokenMu.Unlock()
	})

	calls := 0
	withGoogleClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return gResp(500, `bad`), nil
	})
	err := Submit(cfg, []string{
		"https://example.test/a",
		"https://example.test/b",
		"https://example.test/c",
	}, TypeUpdated)
	if err == nil || !strings.Contains(err.Error(), "all 2 notifications failed") {
		t.Fatalf("Submit(all fail) error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("Submit() calls = %d want 2 due to quota cap", calls)
	}
}

func TestFetchTokenParseError(t *testing.T) {
	tokenMu.Lock()
	cachedToken = ""
	tokenExpiry = time.Time{}
	tokenMu.Unlock()

	saPath := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(saPath, []byte(`not-json`), 0o644); err != nil {
		t.Fatalf("WriteFile(service account) error = %v", err)
	}
	_, err := fetchToken(saPath)
	if err == nil || (!strings.Contains(err.Error(), "parse service account") && !strings.Contains(err.Error(), "fetch token")) {
		t.Fatalf("fetchToken(parse error) = %v", err)
	}
}

func TestSubmitQuotaCheckErrorStillProceeds(t *testing.T) {
	cfg := config.GoogleIndexConfig{
		ServiceAccountPath: filepath.Join(t.TempDir(), "sa.json"),
		QuotaStatePath:     filepath.Join(t.TempDir(), "quota-dir"),
	}
	if err := os.WriteFile(cfg.ServiceAccountPath, []byte(`{`), 0o644); err != nil {
		t.Fatalf("WriteFile(service account) error = %v", err)
	}
	if err := os.WriteFile(cfg.QuotaStatePath, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("WriteFile(quota blocker) error = %v", err)
	}
	tokenMu.Lock()
	cachedToken = "cached-token"
	tokenExpiry = time.Now().Add(5 * time.Minute)
	tokenMu.Unlock()
	t.Cleanup(func() {
		tokenMu.Lock()
		cachedToken = ""
		tokenExpiry = time.Time{}
		tokenMu.Unlock()
	})

	calls := 0
	withGoogleClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network down")
	})
	err := Submit(cfg, []string{"https://example.test/a"}, TypeUpdated)
	if err == nil || !strings.Contains(err.Error(), "all 1 notifications failed") {
		t.Fatalf("Submit(quota error proceeds) error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("Submit(quota error proceeds) calls = %d want 1", calls)
	}
}
