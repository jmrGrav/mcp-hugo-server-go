package write

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
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

func TestPersistentIdempotencyOperationsStayOffGlobalPruneHotPath(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.RememberMutation(db.MutationJournalEntry{CallerKey: "caller", Tool: "create_page", Key: "expired", RequestHash: "old", ResultJSON: []byte(`{}`), CreatedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	store := newIdempotencyStore(time.Hour, 8, d)
	hash, err := requestHash(map[string]string{"title": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.remember("caller", "create_page", "live", hash, map[string]string{"title": "new"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.lookup("caller", "create_page", "expired"); err != nil || found {
		t.Fatalf("expired lookup = found %v, err %v", found, err)
	}
	var replay map[string]string
	if hit, err := store.replay("caller", "create_page", "live", hash, &replay); err != nil || !hit || replay["title"] != "new" {
		t.Fatalf("live replay = hit %v payload %#v err %v", hit, replay, err)
	}
	stats, err := d.MutationJournalStats()
	if err != nil || stats.ActiveEntries != 1 || !stats.LastPrunedAt.IsZero() {
		t.Fatalf("hot-path retention stats = %+v, %v; replay/remember/lookup must not run global maintenance", stats, err)
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
	if err := store.remember("caller-a", "create_page", "ttl-key", hash, in); err != nil {
		t.Fatalf("remember: %v", err)
	}

	// Immediately after remember, the entry must still be present.
	if _, found, err := store.lookup("caller-a", "create_page", "ttl-key"); err != nil || !found {
		t.Fatal("lookup immediately after remember: expected entry to be present")
	}

	// After the configured 1-second TTL elapses, the entry must be gone —
	// with the hardcoded 15-minute default this assertion would fail.
	time.Sleep(1200 * time.Millisecond)
	if _, found, err := store.lookup("caller-a", "create_page", "ttl-key"); err != nil || found {
		t.Fatal("lookup after configured TTL elapsed: expected entry to have expired")
	}
}

// TestIdempotencyStoreIsolatesByCallerKey is the discriminating regression
// test for #627: two different callers (distinct bearer-token hashes, see
// oauth.CtxTokenID) using the exact same tool+idempotency_key must not see
// each other's remembered result. Before this fix, cacheKey only ever
// combined tool+key, so any caller who knew (or guessed) another caller's
// idempotency_key for a given tool could replay or look up that caller's
// mutation result — this test fails under the pre-fix two-argument cacheKey
// and passes once callerKey is part of it.
func TestIdempotencyStoreIsolatesByCallerKey(t *testing.T) {
	store := newIdempotencyStore(time.Hour, 256)

	type payload struct {
		Value string `json:"value"`
	}
	in := payload{Value: "caller-a-secret-result"}
	hash, err := requestHash(in)
	if err != nil {
		t.Fatalf("requestHash: %v", err)
	}

	const sharedTool = "create_page"
	const sharedKey = "shared-idempotency-key"

	if err := store.remember("caller-a", sharedTool, sharedKey, hash, in); err != nil {
		t.Fatalf("remember (caller-a): %v", err)
	}

	// caller-a can look up its own result.
	if _, found, err := store.lookup("caller-a", sharedTool, sharedKey); err != nil || !found {
		t.Fatal("caller-a lookup: expected its own entry to be present")
	}

	// caller-b, using the identical tool+key, must NOT see caller-a's result.
	if _, found, err := store.lookup("caller-b", sharedTool, sharedKey); err != nil || found {
		t.Fatal("caller-b lookup: leaked caller-a's mutation result across the caller boundary (#627)")
	}

	// replay must behave the same way: caller-b gets no hit, not caller-a's
	// cached response, even though the tool+key match exactly.
	var cached payload
	hit, replayErr := store.replay("caller-b", sharedTool, sharedKey, hash, &cached)
	if replayErr != nil {
		t.Fatalf("caller-b replay: unexpected error %v", replayErr)
	}
	if hit {
		t.Fatalf("caller-b replay: leaked caller-a's cached result across the caller boundary (#627): got %+v", cached)
	}

	// Two callers can independently use the identical tool+key without
	// conflicting with each other (this is the isolation actually being
	// bought, not just "caller-b sees nothing").
	otherIn := payload{Value: "caller-b-own-result"}
	otherHash, err := requestHash(otherIn)
	if err != nil {
		t.Fatalf("requestHash (caller-b): %v", err)
	}
	if err := store.remember("caller-b", sharedTool, sharedKey, otherHash, otherIn); err != nil {
		t.Fatalf("remember (caller-b): %v", err)
	}
	var cachedA payload
	if _, err := store.replay("caller-a", sharedTool, sharedKey, hash, &cachedA); err != nil {
		t.Fatalf("caller-a replay after caller-b remember: unexpected error %v", err)
	}
	if cachedA.Value != "caller-a-secret-result" {
		t.Fatalf("caller-a's own entry was overwritten by caller-b's remember: got %+v", cachedA)
	}
}

func TestFormatTTLDescription(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "0s"},
		{name: "single minute", in: time.Minute, want: "1 minute"},
		{name: "whole minutes plural", in: 15 * time.Minute, want: "15 minutes"},
		{name: "non whole minute falls back to duration string", in: 90 * time.Second, want: "1m30s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatTTLDescription(tc.in); got != tc.want {
				t.Fatalf("formatTTLDescription(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIdempotencyStorePruneAndTrimLocked(t *testing.T) {
	now := time.Date(2026, 7, 25, 23, 30, 0, 0, time.UTC)
	store := &idempotencyStore{
		ttl:        time.Minute,
		maxEntries: 2,
		entries: map[string]idempotencyEntry{
			"expired": {CreatedAt: now.Add(-2 * time.Minute)},
			"oldest":  {CreatedAt: now.Add(-30 * time.Second)},
			"middle":  {CreatedAt: now.Add(-20 * time.Second)},
			"newest":  {CreatedAt: now.Add(-10 * time.Second)},
		},
	}

	store.pruneLocked(now)
	if _, ok := store.entries["expired"]; ok {
		t.Fatal("pruneLocked() kept expired entry")
	}

	store.trimLocked()
	if len(store.entries) != 2 {
		t.Fatalf("trimLocked() kept %d entries, want 2", len(store.entries))
	}
	if _, ok := store.entries["oldest"]; ok {
		t.Fatal("trimLocked() kept the oldest entry beyond maxEntries")
	}
	if _, ok := store.entries["middle"]; !ok {
		t.Fatal("trimLocked() dropped middle entry, want it retained")
	}
	if _, ok := store.entries["newest"]; !ok {
		t.Fatal("trimLocked() dropped newest entry, want it retained")
	}
}

// TestValidateIdempotencyKey is a regression test for #888: a caller-supplied
// idempotency_key must be bounded to a safe charset (alphanumeric, '-', '_')
// and length. Path-traversal shapes, embedded whitespace, Unicode, and emoji
// are rejected; a blank key (idempotency not requested) and a valid
// boundary-length key are accepted.
func TestValidateIdempotencyKey(t *testing.T) {
	valid := []string{
		"",          // absent: idempotency not requested
		"   ",       // all-whitespace: treated as absent
		"abc123",    // plain
		"a-b_c-123", // with allowed separators
		strings.Repeat("k", maxIdempotencyKeyLen), // boundary length
	}
	for _, k := range valid {
		if err := validateIdempotencyKey(k); err != nil {
			t.Errorf("validateIdempotencyKey(%q) = %v, want nil", k, err)
		}
	}

	invalid := []string{
		"../../etc/passwd", // path-traversal shape
		"..\\..\\windows",  // backslash traversal
		"abc def",          // embedded whitespace
		"key/with/slash",   // slash
		"café",             // unicode
		"key\U0001F389",    // emoji
		"key.with.dots",    // dots
		strings.Repeat("k", maxIdempotencyKeyLen+1), // over length
	}
	for _, k := range invalid {
		if err := validateIdempotencyKey(k); err == nil {
			t.Errorf("validateIdempotencyKey(%q) = nil, want error", k)
		}
	}
}
