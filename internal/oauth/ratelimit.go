package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

const (
	maxBuckets = 10_000
	idleTTL    = 15 * time.Minute
	gcInterval = 5 * time.Minute
)

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	lastSeen map[string]time.Time
	// credits stores pending phase-1 refunds per key.
	// In MCP Streamable HTTP stateful transport, a tools/call is split into two
	// HTTP requests: phase-1 (no session, server returns 202) and phase-2 (session
	// present, server returns 200 with result). Phase-1 charges a token and stores
	// one credit; phase-2 consumes the credit so it is not charged again.
	credits map[string]int
	cfg     config.RateLimitConfig
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
		credits:  make(map[string]int),
		cfg:      cfg,
	}
	go rl.gcLoop()
	return rl
}

// callerKey returns a rate-limit bucket key combining the caller's IP and scope
// so that two different IPs with the same scope get independent limits.
func callerKey(remoteAddr, scope string) string {
	ip, _, _ := strings.Cut(remoteAddr, ":")
	if ip == "" {
		ip = remoteAddr
	}
	return ip + "\x00" + scope
}

func (rl *RateLimiter) limiterFor(key, scope string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.lastSeen[key] = time.Now()
	if l, ok := rl.limiters[key]; ok {
		return l
	}
	if len(rl.limiters) >= maxBuckets {
		rl.evictOldest()
	}
	perMin := rl.perMinFor(scope)
	if perMin < 1 {
		perMin = 1
	}
	l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMin)), perMin)
	rl.limiters[key] = l
	return l
}

func (rl *RateLimiter) gcLoop() {
	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.gc()
	}
}

func (rl *RateLimiter) gc() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-idleTTL)
	for key, last := range rl.lastSeen {
		if last.Before(cutoff) {
			delete(rl.limiters, key)
			delete(rl.lastSeen, key)
			delete(rl.credits, key)
		}
	}
}

func (rl *RateLimiter) evictOldest() {
	// Called with rl.mu held. Removes the entry with the oldest lastSeen time.
	var oldest string
	var oldestAt time.Time
	for k, t := range rl.lastSeen {
		if oldest == "" || t.Before(oldestAt) {
			oldest = k
			oldestAt = t
		}
	}
	if oldest != "" {
		delete(rl.limiters, oldest)
		delete(rl.lastSeen, oldest)
		delete(rl.credits, oldest)
	}
}

func (rl *RateLimiter) perMinFor(scope string) int {
	switch scope {
	case "content.read":
		return rl.cfg.ContentReadPerMin
	case "content.write":
		return rl.cfg.ContentWritePerMin
	case "site.admin":
		return rl.cfg.SiteAdminPerMin
	default:
		return rl.cfg.AnonymousPerMin
	}
}

type jsonrpcRateLimitRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
}

func mcpToolCallRequest(r *http.Request) (bool, json.RawMessage) {
	if r.Method != http.MethodPost || r.Body == nil {
		return false, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return false, nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return false, nil
	}

	var single jsonrpcRateLimitRequest
	if err := json.Unmarshal(body, &single); err == nil && single.Method != "" {
		return single.Method == "tools/call", single.ID
	}

	var batch []jsonrpcRateLimitRequest
	if err := json.Unmarshal(body, &batch); err != nil {
		return false, nil
	}
	for _, req := range batch {
		if req.Method == "tools/call" {
			return true, req.ID
		}
	}
	return false, nil
}

// statusWriter captures the HTTP response status code so the middleware can
// detect 202 Accepted (MCP stateful phase-1) responses after the handler runs.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, requestID := mcpToolCallRequest(r)
		if !limit {
			next.ServeHTTP(w, r)
			return
		}
		scope, _ := r.Context().Value(CtxScope).(string)
		key := callerKey(r.RemoteAddr, scope)

		// In MCP Streamable HTTP stateful transport, each logical tool call
		// produces two HTTP requests:
		//   Phase 1: tools/call (no Mcp-Session-Id) → server 202 Accepted (session init)
		//   Phase 2: tools/call (Mcp-Session-Id present) → server 200 OK (result)
		// Phase-1 charges one token and stores a credit. Phase-2 consumes the
		// credit, so the two HTTP requests count as one logical tool call.
		rl.mu.Lock()
		hasCredit := rl.credits[key] > 0
		if hasCredit {
			rl.credits[key]--
		}
		rl.mu.Unlock()

		if hasCredit {
			// Phase-2: already paid for by phase-1. Let it through at no cost.
			next.ServeHTTP(w, r)
			return
		}

		res := rl.limiterFor(key, scope).Reserve()
		if delay := res.Delay(); delay > 0 {
			res.Cancel()
			retryAfter := int(math.Ceil(delay.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}
			var id any
			if len(requestID) > 0 {
				id = requestID
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			// Inside an established MCP session (Mcp-Session-Id present) use
			// HTTP 200 so the go-sdk transport forwards the body to the MCP
			// client. The go-sdk discards non-2xx response bodies before the
			// MCP layer can surface the structured JSON-RPC error.
			if r.Header.Get("Mcp-Session-Id") != "" {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusTooManyRequests)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]any{
					"code":    -32029,
					"message": "rate limit exceeded; retry after " + strconv.Itoa(retryAfter) + " second(s)",
					"data": map[string]any{
						"error":               "rate_limit_exceeded",
						"retry_after_seconds": retryAfter,
					},
				},
			})
			return
		}

		// Wrap the response writer to detect phase-1 (202 Accepted).
		// When the server responds 202, a credit is stored so the follow-up
		// phase-2 request does not charge the token again.
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == http.StatusAccepted {
			rl.mu.Lock()
			rl.credits[key]++
			rl.mu.Unlock()
		}
	})
}
