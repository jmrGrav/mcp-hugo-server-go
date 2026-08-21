package anonymous

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolRegistryDigestForServerReturnsEmptyOnConnectFailure exercises
// toolRegistryDigestForServer's error path directly: internal/toolregistry's
// own tests already prove FromServer surfaces a Connect error (a canceled
// context fails the in-memory client.Connect handshake); this test proves
// the caller-facing contract on top of that — a failed snapshot logs and
// leaves cached empty rather than panicking or caching a zero-value digest
// that would later be reported as if it were real.
func TestToolRegistryDigestForServerReturnsEmptyOnConnectFailure(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var once sync.Once
	var cached string
	got := toolRegistryDigestForServer(ctx, s, &once, &cached)
	if got != "" {
		t.Fatalf("toolRegistryDigestForServer with a canceled context = %q, want empty", got)
	}
	if cached != "" {
		t.Fatalf("cached = %q after a failed snapshot, want empty", cached)
	}

	// once.Do fires exactly once per (once, cached) pair — a second call
	// sharing the same pair, even with a live context, must still return
	// the cached (empty, from the failed attempt) result rather than
	// retrying, proving the cache is per-server-lifetime, not per-success.
	if got2 := toolRegistryDigestForServer(context.Background(), s, &once, &cached); got2 != "" {
		t.Fatalf("second call recomputed instead of honoring the cached (empty) result from sync.Once: %q", got2)
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
