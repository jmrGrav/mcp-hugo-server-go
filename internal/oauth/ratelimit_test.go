package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func makeReq(remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func TestRateLimiterAllows(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, makeReq("1.2.3.4:1234"))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on call %d, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimiterBlocks(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, makeReq("1.2.3.4:1234"))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeReq("1.2.3.4:1234"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th call, got %d", rec.Code)
	}
}

func TestRateLimiterIndependentPerIP(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Exhaust the limit for IP1.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, makeReq("10.0.0.1:1234"))
	}
	// A different IP must still be allowed.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeReq("10.0.0.2:5678"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected IP2 to be allowed (separate bucket), got %d", rec.Code)
	}
}

func TestRateLimiter429Response(t *testing.T) {
	rl := NewRateLimiter(smallCfg())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.Middleware(inner)

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, makeReq("9.9.9.9:9999"))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeReq("9.9.9.9:9999"))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	retryAfterHeader := rec.Header().Get("Retry-After")
	retryAfterVal, err := strconv.Atoi(retryAfterHeader)
	if err != nil || retryAfterVal < 1 {
		t.Fatalf("expected Retry-After >= 1, got %q", retryAfterHeader)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "rate_limit_exceeded" {
		t.Fatalf("unexpected error body: %v", body)
	}
	if got := body["retry_after_seconds"]; got != float64(retryAfterVal) {
		t.Fatalf("retry_after_seconds %v does not match Retry-After header %s", got, retryAfterHeader)
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Fatalf("missing message in 429 body: %v", body)
	}
}
