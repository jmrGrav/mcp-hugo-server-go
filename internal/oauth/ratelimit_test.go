package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func smallCfg() config.RateLimitConfig {
	return config.RateLimitConfig{
		AnonymousPerMin:    5,
		ContentReadPerMin:  5,
		ContentWritePerMin: 5,
		SiteAdminPerMin:    5,
		DestructivePerMin:  5,
	}
}

func TestRateLimiterAllows(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	for i := 0; i < 5; i++ {
		if !rl.Allow("") {
			t.Fatalf("expected Allow on call %d to be true", i+1)
		}
	}
}

func TestRateLimiterBlocks(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	for i := 0; i < 5; i++ {
		rl.Allow("")
	}
	if rl.Allow("") {
		t.Fatal("expected 6th Allow to return false")
	}
}

func TestRateLimiter429Response(t *testing.T) {
	rl := NewRateLimiter(smallCfg())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.Middleware(inner)

	exhaust := func() {
		for i := 0; i < 5; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			h.ServeHTTP(rec, req)
		}
	}
	exhaust()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", got)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "rate_limit_exceeded" {
		t.Fatalf("unexpected error body: %v", body)
	}
}
