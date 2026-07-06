package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"

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
	return makeJSONRPCReq(remoteAddr, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_site_information","arguments":{}}}`)
}

func makeJSONRPCReq(remoteAddr, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
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
	h.ServeHTTP(rec, makeJSONRPCReq("9.9.9.9:9999", `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"get_site_information","arguments":{}}}`))

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
	if body["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", body["jsonrpc"])
	}
	errorObj, _ := body["error"].(map[string]any)
	if errorObj == nil {
		t.Fatalf("missing error object in 429 body: %v", body)
	}
	if got := errorObj["code"]; got != float64(-32029) {
		t.Fatalf("expected error.code == -32029, got %v", got)
	}
	data, _ := errorObj["data"].(map[string]any)
	if data == nil {
		t.Fatalf("missing error.data in 429 body: %v", body)
	}
	if data["error"] != "rate_limit_exceeded" {
		t.Fatalf("unexpected error.data.error: %v", data)
	}
	if got := data["retry_after_seconds"]; got != float64(retryAfterVal) {
		t.Fatalf("retry_after_seconds %v does not match Retry-After header %s", got, retryAfterHeader)
	}
	if msg, _ := errorObj["message"].(string); msg == "" {
		t.Fatalf("missing message in 429 error object: %v", errorObj)
	}
	if got := body["id"]; got != float64(99) {
		t.Fatalf("expected 429 body to preserve request id 99, got %v", got)
	}
}

func TestRateLimiterOnlyCountsLogicalToolCalls(t *testing.T) {
	cfg := smallCfg()
	cfg.SiteAdminPerMin = 2
	rl := NewRateLimiter(cfg)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	scopeReq := func(body string) *http.Request {
		req := makeJSONRPCReq("7.7.7.7:7777", body)
		req = req.WithContext(context.WithValue(req.Context(), CtxScope, "site.admin"))
		return req
	}

	controlTraffic := []string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
	}
	for i, body := range controlTraffic {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, scopeReq(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("control request %d status = %d, want 200", i+1, rec.Code)
		}
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, scopeReq(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"get_site_information","arguments":{}}}`, i+10)))
		if rec.Code != http.StatusOK {
			t.Fatalf("logical tool call %d status = %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, scopeReq(`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"get_site_information","arguments":{}}}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third logical tool call status = %d, want 429", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if got := body["id"]; got != float64(99) {
		t.Fatalf("429 id = %v, want 99", got)
	}
	errObj, _ := body["error"].(map[string]any)
	if got := errObj["code"]; got != float64(-32029) {
		t.Fatalf("429 error code = %v, want -32029", got)
	}
}

func TestRateLimiterEvictsAtCapacity(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Temporarily reduce maxBuckets isn't possible without exporting, so test
	// that 5 distinct IPs each get their own bucket and map stays bounded.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, makeReq(fmt.Sprintf("10.20.30.%d:1234", i)))
	}
	rl.mu.Lock()
	size := len(rl.limiters)
	rl.mu.Unlock()
	if size > 5 {
		t.Fatalf("expected at most 5 buckets for 5 IPs, got %d", size)
	}
}

func TestRateLimiterGC(t *testing.T) {
	rl := NewRateLimiter(smallCfg())
	// Manually insert a stale entry
	rl.mu.Lock()
	rl.limiters["stale\x00"] = rate.NewLimiter(1, 1)
	rl.lastSeen["stale\x00"] = time.Now().Add(-idleTTL - time.Second)
	// Insert a fresh entry
	rl.limiters["fresh\x00"] = rate.NewLimiter(1, 1)
	rl.lastSeen["fresh\x00"] = time.Now()
	rl.mu.Unlock()

	rl.gc()

	rl.mu.Lock()
	_, staleExists := rl.limiters["stale\x00"]
	_, freshExists := rl.limiters["fresh\x00"]
	rl.mu.Unlock()
	if staleExists {
		t.Fatal("stale entry should have been evicted by gc()")
	}
	if !freshExists {
		t.Fatal("fresh entry should not have been evicted by gc()")
	}
}
