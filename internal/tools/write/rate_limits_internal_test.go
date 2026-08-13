package write

import (
	"math"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimitTokenBucketRefillsContinuously(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	limiter := rate.NewLimiter(rate.Limit(1), 3)
	if !limiter.AllowN(start, 3) {
		t.Fatal("expected the full initial token bucket to be consumable")
	}

	if got := limiter.TokensAt(start); got != 0 {
		t.Fatalf("tokens at depletion time = %v, want 0", got)
	}
	if got := limiter.TokensAt(start.Add(500 * time.Millisecond)); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("tokens after 500ms = %v, want 0.5", got)
	}
	if got := limiter.TokensAt(start.Add(3 * time.Second)); got != 3 {
		t.Fatalf("tokens after three seconds = %v, want bucket capacity 3", got)
	}
}

func TestRateLimitRefillRateMatchesConfiguredWindow(t *testing.T) {
	for _, tc := range []struct {
		limit int
		want  float64
	}{
		{limit: 60, want: 1},
		{limit: 5, want: 5.0 / 60.0},
		{limit: 0, want: 0},
	} {
		if got := rateLimitRefillRatePerSecond(tc.limit); got != tc.want {
			t.Errorf("rateLimitRefillRatePerSecond(%d) = %v, want %v", tc.limit, got, tc.want)
		}
	}
}
