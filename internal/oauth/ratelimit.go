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
	cfg      config.RateLimitConfig
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
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

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, requestID := mcpToolCallRequest(r)
		if !limit {
			next.ServeHTTP(w, r)
			return
		}
		scope, _ := r.Context().Value(CtxScope).(string)
		key := callerKey(r.RemoteAddr, scope)

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

		next.ServeHTTP(w, r)
	})
}
