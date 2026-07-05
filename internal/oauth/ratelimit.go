package oauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	cfg      config.RateLimitConfig
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		cfg:      cfg,
	}
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
	if l, ok := rl.limiters[key]; ok {
		return l
	}
	perMin := rl.perMinFor(scope)
	if perMin < 1 {
		perMin = 1
	}
	l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMin)), perMin)
	rl.limiters[key] = l
	return l
}

func (rl *RateLimiter) perMinFor(scope string) int {
	switch scope {
	case "content.read":
		return rl.cfg.ContentReadPerMin
	case "content.write":
		return rl.cfg.ContentWritePerMin
	case "site.admin":
		return rl.cfg.SiteAdminPerMin
	case "system.admin":
		return rl.cfg.DestructivePerMin
	default:
		return rl.cfg.AnonymousPerMin
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, _ := r.Context().Value(CtxScope).(string)
		key := callerKey(r.RemoteAddr, scope)
		if !rl.limiterFor(key, scope).Allow() {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limit_exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
