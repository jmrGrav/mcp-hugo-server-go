package anonymous

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolRegistryDigestCacheRetriesAfterAFailedAttempt exercises
// toolRegistryDigestCache's error path directly: internal/toolregistry's
// own tests already prove FromServer surfaces a Connect error (a canceled
// context fails the in-memory client.Connect handshake); this test proves
// the caller-facing contract on top of that — a failed attempt logs and
// returns empty WITHOUT latching, so the next call (once ctx is no longer
// canceled) gets a fresh, real attempt rather than a permanently-disabled
// field for this server's whole remaining lifetime. Only a SUCCESSFUL
// computation may latch — a sync.Once-based cache would fail this test,
// since it can only ever attempt once regardless of outcome.
func TestToolRegistryDigestCacheRetriesAfterAFailedAttempt(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	var cache toolRegistryDigestCache

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := cache.get(canceledCtx, s); got != "" {
		t.Fatalf("cache.get with a canceled context = %q, want empty", got)
	}

	got := cache.get(context.Background(), s)
	if !strings.HasPrefix(got, "sha256:") || len(got) <= len("sha256:") {
		t.Fatalf("cache.get with a live context after a prior failure = %q, want a real sha256:... digest (the failed attempt must not have latched)", got)
	}

	// A successful result DOES latch: a second live call must return the
	// identical cached value, not recompute.
	if got2 := cache.get(context.Background(), s); got2 != got {
		t.Fatalf("cache.get after a successful computation = %q, want the cached %q", got2, got)
	}
}

// TestRecommendedInlineAssetMaxBytes is a regression test for #1190:
// asset_max_bytes alone overstated what upload_page_asset's inline
// content_base64 param could actually deliver in one call, since base64
// expands the payload ~4/3 and the result still has to fit inside
// max_request_bytes alongside the rest of the tool-call envelope.
func TestRecommendedInlineAssetMaxBytes(t *testing.T) {
	const assetMaxBytes = 10 << 20 // 10 MiB, matches writepkg.AssetMaxBytes()

	tests := []struct {
		name            string
		maxRequestBytes int64
		wantMax         int // upper bound the result must never exceed
	}{
		{name: "default 1 MiB max_request_bytes caps far below asset_max_bytes", maxRequestBytes: 1 << 20, wantMax: assetMaxBytes},
		{name: "operator-raised max_request_bytes still respects assetMaxBytes ceiling", maxRequestBytes: 100 << 20, wantMax: assetMaxBytes},
		{name: "unset (zero) max_request_bytes falls back to the 1 MiB default", maxRequestBytes: 0, wantMax: assetMaxBytes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := recommendedInlineAssetMaxBytes(tc.maxRequestBytes, assetMaxBytes)
			if got <= 0 {
				t.Fatalf("recommendedInlineAssetMaxBytes(%d, %d) = %d, want > 0", tc.maxRequestBytes, assetMaxBytes, got)
			}
			if got > tc.wantMax {
				t.Fatalf("recommendedInlineAssetMaxBytes(%d, %d) = %d, want <= %d", tc.maxRequestBytes, assetMaxBytes, got, tc.wantMax)
			}
			// The 330 KiB WebP from the issue's real-usage report must clear
			// the default 1 MiB max_request_bytes recommendation — that's
			// the one case #1190 confirms already works today.
			if tc.maxRequestBytes == 1<<20 && got < 330*1024 {
				t.Fatalf("recommendedInlineAssetMaxBytes(1 MiB, %d) = %d, want >= 330 KiB (the reported real-usage case)", assetMaxBytes, got)
			}
		})
	}

	// The computed value must always be strictly less than max_request_bytes
	// itself (base64 + envelope overhead), never equal to or exceeding it —
	// that's the exact contract gap #1190 reports.
	if got := recommendedInlineAssetMaxBytes(1<<20, assetMaxBytes); int64(got) >= 1<<20 {
		t.Fatalf("recommendedInlineAssetMaxBytes(1 MiB, ...) = %d, must stay below max_request_bytes itself", got)
	}
}
