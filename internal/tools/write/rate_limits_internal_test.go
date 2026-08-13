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

// TestRateLimitBucketAlgebraMatchesProductionQuotas drains and refills, on
// a synthetic timeline, a limiter built exactly the way callerLimiter
// constructs the two real production quotas (create_update_upload's
// default 60/min and destructive's default 5/min, config.go), asserting
// against the raw *rate.Limiter (AllowN/TokensAt, which take an explicit
// time and never touch the wall clock) that a single token refills after
// 60/perMinute seconds and the bucket is back to full capacity only after
// a full minute — for both quotas, not just an arbitrary bucket size.
func TestRateLimitBucketAlgebraMatchesProductionQuotas(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)

	for _, tc := range []struct {
		name      string
		perMinute int
	}{
		{name: "create_update_upload default", perMinute: 60},
		{name: "destructive default", perMinute: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(tc.perMinute)), tc.perMinute)

			if !l.AllowN(start, tc.perMinute) {
				t.Fatalf("expected the full %d/min burst to be consumable at once", tc.perMinute)
			}
			if got := l.TokensAt(start); got != 0 {
				t.Fatalf("tokens immediately after exhausting the burst = %v, want 0", got)
			}

			// A single token refills after 60/perMinute seconds — spaced
			// sequential calls stay near-exhausted rather than snapping
			// back to full, which is exactly the "59 -> 60 on reconnect"
			// symptom #962 reported when it was actually just this
			// continuous refill being misread as a broken counter.
			oneTokenIn := time.Duration(60/float64(tc.perMinute)*float64(time.Second)) + time.Millisecond
			if got := l.TokensAt(start.Add(oneTokenIn)); math.Abs(got-1) > 0.01 {
				t.Fatalf("tokens %v after one refill interval = %v, want 1 (not still 0, and not back to %d)", oneTokenIn, got, tc.perMinute)
			}

			if got := l.TokensAt(start.Add(time.Minute)); got != float64(tc.perMinute) {
				t.Fatalf("tokens after a full minute = %v, want %d (bucket capacity)", got, tc.perMinute)
			}
		})
	}
}

// TestRateLimitBucketRemainingReflectsRealBurstAndRefill exercises the
// JSON-facing path (newRateLimitBucket -> rateLimitRemaining/
// rateLimitRetryAfterSeconds) rather than the raw limiter: unlike
// TokensAt, those helpers read l.Tokens()/l.Allow(), which consult the
// real wall clock internally, so this test drives an actual limiter
// through real elapsed time instead of a synthetic timeline. It uses a
// scaled-up (but production-shaped) rate rather than the literal 5/min
// and 60/min defaults so the refill it waits for takes milliseconds, not
// seconds — the formula under test (rate.Every(time.Minute/perMinute),
// perMinute) is identical to callerLimiter's for both real quotas.
func TestRateLimitBucketRemainingReflectsRealBurstAndRefill(t *testing.T) {
	const perMinute = 1200 // one token every 50ms, same shape as the real limiters
	l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), perMinute)

	if !l.AllowN(time.Now(), perMinute) {
		t.Fatal("expected the full burst to be consumable at once")
	}
	exhausted := newRateLimitBucket(l, perMinute, rateLimitScopeCreateUpdateUpload, time.Now())
	if exhausted.Remaining != 0 {
		t.Fatalf("Remaining immediately after exhausting the burst = %d, want 0", exhausted.Remaining)
	}
	if exhausted.RetryAfterSeconds <= 0 {
		t.Fatalf("RetryAfterSeconds after exhausting the burst = %v, want > 0", exhausted.RetryAfterSeconds)
	}

	time.Sleep(120 * time.Millisecond)
	refilling := newRateLimitBucket(l, perMinute, rateLimitScopeCreateUpdateUpload, time.Now())
	if refilling.Remaining < 1 {
		t.Fatalf("Remaining after 120ms of refill = %d, want at least 1 (bucket must not stay pinned at 0)", refilling.Remaining)
	}
	if refilling.Remaining >= perMinute {
		t.Fatalf("Remaining after 120ms of refill = %d, want less than the full %d-token capacity", refilling.Remaining, perMinute)
	}
}
