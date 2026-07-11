package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"golang.org/x/time/rate"
)

func TestRateLimiterHelperBranches(t *testing.T) {
	cfg := config.RateLimitConfig{
		AnonymousPerMin:    11,
		ContentReadPerMin:  22,
		ContentWritePerMin: 33,
		SiteAdminPerMin:    44,
	}
	rl := &RateLimiter{
		limiters: map[string]*rate.Limiter{},
		lastSeen: map[string]time.Time{},
		cfg:      cfg,
	}

	if got := rl.perMinFor("content.read"); got != 22 {
		t.Fatalf("perMinFor(content.read) = %d", got)
	}
	if got := rl.perMinFor("content.write"); got != 33 {
		t.Fatalf("perMinFor(content.write) = %d", got)
	}
	if got := rl.perMinFor("site.admin"); got != 44 {
		t.Fatalf("perMinFor(site.admin) = %d", got)
	}
	if got := rl.perMinFor("unknown"); got != 11 {
		t.Fatalf("perMinFor(unknown) = %d", got)
	}

	rl.limiters["old"] = rate.NewLimiter(rate.Every(time.Minute), 1)
	rl.limiters["new"] = rate.NewLimiter(rate.Every(time.Minute), 1)
	rl.lastSeen["old"] = time.Now().Add(-time.Hour)
	rl.lastSeen["new"] = time.Now()
	rl.evictOldest()
	if _, ok := rl.limiters["old"]; ok {
		t.Fatal("evictOldest() should remove oldest bucket")
	}
	if _, ok := rl.limiters["new"]; !ok {
		t.Fatal("evictOldest() removed wrong bucket")
	}
}

func TestMCPToolCallRequestShapes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	if ok, id := mcpToolCallRequest(req); ok || id != nil {
		t.Fatalf("GET request = %v %s", ok, string(id))
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if ok, _ := mcpToolCallRequest(req); ok {
		t.Fatal("nil body should not be a tool call")
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if ok, id := mcpToolCallRequest(req); !ok || string(id) != "1" {
		t.Fatalf("single tools/call = %v %s", ok, string(id))
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`[{"jsonrpc":"2.0","id":2,"method":"tools/list"},{"jsonrpc":"2.0","id":3,"method":"tools/call"}]`))
	if ok, id := mcpToolCallRequest(req); !ok || string(id) != "3" {
		t.Fatalf("batch tools/call = %v %s", ok, string(id))
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if ok, _ := mcpToolCallRequest(req); ok {
		t.Fatal("tools/list should not count as tools/call")
	}
}
