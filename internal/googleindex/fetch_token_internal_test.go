package googleindex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fetchToken guards Google Indexing API calls behind a real service-account
// JWT exchange; its error branches (bad path, malformed service-account
// JSON) and its cache-hit fast path were previously untested. The genuine
// network token exchange is deliberately left uncovered here — it would
// require a live call to Google's OAuth2 token endpoint.

func resetTokenCache(t *testing.T) {
	t.Helper()
	tokenMu.Lock()
	prevToken, prevExpiry := cachedToken, tokenExpiry
	tokenMu.Unlock()
	t.Cleanup(func() {
		tokenMu.Lock()
		cachedToken, tokenExpiry = prevToken, prevExpiry
		tokenMu.Unlock()
	})
	tokenMu.Lock()
	cachedToken, tokenExpiry = "", time.Time{}
	tokenMu.Unlock()
}

func TestFetchTokenReturnsCachedTokenWithoutTouchingDisk(t *testing.T) {
	resetTokenCache(t)
	tokenMu.Lock()
	cachedToken = "cached-value"
	tokenExpiry = time.Now().Add(time.Hour)
	tokenMu.Unlock()

	got, err := fetchToken("/nonexistent/path/that/would/error/if/read.json")
	if err != nil {
		t.Fatalf("fetchToken() error = %v, want the cache hit to skip disk entirely", err)
	}
	if got != "cached-value" {
		t.Fatalf("fetchToken() = %q, want the cached value", got)
	}
}

func TestFetchTokenRejectsMissingServiceAccountFile(t *testing.T) {
	resetTokenCache(t)
	if _, err := fetchToken(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("fetchToken() error = nil, want an error for a missing service account file")
	}
}

func TestFetchTokenRejectsMalformedServiceAccountJSON(t *testing.T) {
	resetTokenCache(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-sa.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchToken(path); err == nil {
		t.Fatal("fetchToken() error = nil, want an error for malformed service account JSON")
	}
}

func TestFetchTokenExpiredCacheFallsThroughToDisk(t *testing.T) {
	resetTokenCache(t)
	tokenMu.Lock()
	cachedToken = "stale-value"
	tokenExpiry = time.Now().Add(-time.Hour) // already expired
	tokenMu.Unlock()

	// An expired cache must fall through to reading saPath — which fails
	// here, proving the cache-hit branch was correctly skipped.
	if _, err := fetchToken(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("fetchToken() error = nil, want the expired cache to fall through to a (failing) disk read")
	}
}
