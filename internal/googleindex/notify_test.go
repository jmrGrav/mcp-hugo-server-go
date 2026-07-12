package googleindex

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

type roundTripFn func(*http.Request) (*http.Response, error)

func (f roundTripFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withGoogleClient(t *testing.T, rt roundTripFn) {
	t.Helper()
	prev := httpClient
	httpClient = &http.Client{Transport: rt}
	t.Cleanup(func() { httpClient = prev })
}

func gResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestCheckQuotaTracksState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota", "state.json")
	cfg := config.GoogleIndexConfig{QuotaStatePath: statePath}

	allowed, err := checkQuota(cfg, 3, 5)
	if err != nil {
		t.Fatalf("checkQuota() error = %v", err)
	}
	if allowed != 3 {
		t.Fatalf("checkQuota() allowed = %d want 3", allowed)
	}

	allowed, err = checkQuota(cfg, 4, 5)
	if err != nil {
		t.Fatalf("checkQuota() second error = %v", err)
	}
	if allowed != 2 {
		t.Fatalf("checkQuota() second allowed = %d want 2", allowed)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	if !strings.Contains(string(raw), `"Used":5`) && !strings.Contains(string(raw), `"used":5`) {
		t.Fatalf("quota state = %s", raw)
	}
}

func TestFetchTokenCacheAndError(t *testing.T) {
	tokenMu.Lock()
	cachedToken = "cached"
	tokenExpiry = time.Now().Add(5 * time.Minute)
	tokenMu.Unlock()
	t.Cleanup(func() {
		tokenMu.Lock()
		cachedToken = ""
		tokenExpiry = time.Time{}
		tokenMu.Unlock()
	})

	got, err := fetchToken(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("fetchToken(cached) error = %v", err)
	}
	if got != "cached" {
		t.Fatalf("fetchToken(cached) = %q want cached", got)
	}

	tokenMu.Lock()
	cachedToken = ""
	tokenExpiry = time.Time{}
	tokenMu.Unlock()

	_, err = fetchToken(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "read service account") {
		t.Fatalf("fetchToken(missing) error = %v", err)
	}
}

func TestNotifyOneAndSubmitPaths(t *testing.T) {
	t.Run("notifyOne success and auth header", func(t *testing.T) {
		withGoogleClient(t, func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Fatalf("Authorization = %q", got)
			}
			if r.URL.String() != indexingAPIURL {
				t.Fatalf("url = %s", r.URL)
			}
			return gResp(http.StatusOK, `ok`), nil
		})
		if err := notifyOne("tok", "https://example.test/a", string(TypeUpdated)); err != nil {
			t.Fatalf("notifyOne() error = %v", err)
		}
	})

	t.Run("notifyOne errors", func(t *testing.T) {
		withGoogleClient(t, func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("down")
		})
		if err := notifyOne("tok", "https://example.test/a", string(TypeUpdated)); err == nil {
			t.Fatal("notifyOne() should return HTTP error")
		}
		withGoogleClient(t, func(r *http.Request) (*http.Response, error) {
			return gResp(http.StatusBadGateway, `bad`), nil
		})
		if err := notifyOne("tok", "https://example.test/a", string(TypeUpdated)); err == nil || !strings.Contains(err.Error(), "status 502") {
			t.Fatalf("notifyOne() error = %v", err)
		}
	})

	t.Run("Submit disabled empty and exhausted quota are no-op", func(t *testing.T) {
		if err := Submit(config.GoogleIndexConfig{}, []string{"https://example.test/a"}, TypeUpdated); err != nil {
			t.Fatalf("Submit(disabled) error = %v", err)
		}
		if err := Submit(config.GoogleIndexConfig{ServiceAccountPath: "x"}, nil, TypeUpdated); err != nil {
			t.Fatalf("Submit(empty) error = %v", err)
		}
		cfg := config.GoogleIndexConfig{
			ServiceAccountPath: "/tmp/sa.json",
			DailyQuotaLimit:    1,
			QuotaStatePath:     filepath.Join(t.TempDir(), "quota.json"),
		}
		if err := os.WriteFile(cfg.QuotaStatePath, []byte(`{"date":"`+time.Now().UTC().Format("2006-01-02")+`","used":1}`), 0o644); err != nil {
			t.Fatalf("WriteFile(quota) error = %v", err)
		}
		if err := Submit(cfg, []string{"https://example.test/a"}, TypeUpdated); err != nil {
			t.Fatalf("Submit(exhausted quota) error = %v", err)
		}
	})
}
