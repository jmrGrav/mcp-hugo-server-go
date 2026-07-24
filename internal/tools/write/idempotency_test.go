package write

import (
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestIdempotencyTTLFromConfig is a regression test for #616: the
// idempotency-key retention window must come from config.Config, not the
// previously-hardcoded 15*time.Minute constant, and must fall back safely
// for non-positive configured values rather than constructing a
// zero/negative-TTL store (which would defeat replay protection entirely).
func TestIdempotencyTTLFromConfig(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"configured value is honored", 60, 60 * time.Second},
		{"large configured value is honored", 3600, time.Hour},
		{"zero falls back to default", 0, defaultIdempotencyTTL},
		{"negative falls back to default", -5, defaultIdempotencyTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{IdempotencyTTLSeconds: tc.seconds}
			got := idempotencyTTLFromConfig(cfg)
			if got != tc.want {
				t.Fatalf("idempotencyTTLFromConfig(%d) = %v, want %v", tc.seconds, got, tc.want)
			}
		})
	}
}

// TestIdempotencyStoreExpiresByConfiguredTTL confirms a short
// server-configured TTL actually shortens the replay/lookup window,
// end-to-end through newIdempotencyStore(idempotencyTTLFromConfig(cfg), ...)
// exactly as Register() constructs it — not just that the duration value is
// plumbed through, but that a shorter TTL causes an idempotency key to
// expire faster than the 15-minute default would (#616).
func TestIdempotencyStoreExpiresByConfiguredTTL(t *testing.T) {
	cfg := config.Config{IdempotencyTTLSeconds: 1} // 1 second, far shorter than the 15-minute default
	ttl := idempotencyTTLFromConfig(cfg)
	if ttl != time.Second {
		t.Fatalf("idempotencyTTLFromConfig = %v, want 1s", ttl)
	}
	store := newIdempotencyStore(ttl, 256)

	type payload struct {
		Value string `json:"value"`
	}
	in := payload{Value: "hello"}
	hash, err := requestHash(in)
	if err != nil {
		t.Fatalf("requestHash: %v", err)
	}
	if err := store.remember("create_page", "ttl-key", hash, in); err != nil {
		t.Fatalf("remember: %v", err)
	}

	// Immediately after remember, the entry must still be present.
	if _, found := store.lookup("create_page", "ttl-key"); !found {
		t.Fatal("lookup immediately after remember: expected entry to be present")
	}

	// After the configured 1-second TTL elapses, the entry must be gone —
	// with the hardcoded 15-minute default this assertion would fail.
	time.Sleep(1200 * time.Millisecond)
	if _, found := store.lookup("create_page", "ttl-key"); found {
		t.Fatal("lookup after configured TTL elapsed: expected entry to have expired")
	}
}
