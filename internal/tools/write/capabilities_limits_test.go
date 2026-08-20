package write_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

// capabilities_limits.go exists so get_capabilities (internal/tools/anonymous)
// reports the exact enforcement values these tools actually use, from one
// source of truth (#859) — a security-relevant disclosure surface: a caller
// deciding how to shape a request relies on these numbers being accurate,
// not stale or forked from the real constants. Previously exercised only
// indirectly through internal/tools/anonymous's own tests, which — under
// Go's default per-package coverage mode (no -coverpkg, matching ci.yml's
// coverage gate) — attributes nothing back to this package's own number.
func TestCapabilitiesLimitAccessorsReportPositiveValues(t *testing.T) {
	if got := write.BodyMaxBytes(); got <= 0 {
		t.Fatalf("BodyMaxBytes() = %d, want > 0", got)
	}
	if got := write.TitleMaxRunes(); got <= 0 {
		t.Fatalf("TitleMaxRunes() = %d, want > 0", got)
	}
	if got := write.AssetMaxBytes(); got != 10<<20 {
		t.Fatalf("AssetMaxBytes() = %d, want %d (10MiB, matching upload_page_asset/begin_asset_upload's documented cap)", got, 10<<20)
	}
	if got := write.TestContentMaxTTLHours(); got <= 0 {
		t.Fatalf("TestContentMaxTTLHours() = %d, want > 0", got)
	}
}

func TestAllowedAssetExtensionsSortedAndComplete(t *testing.T) {
	got := write.AllowedAssetExtensions()
	want := []string{"gif", "jpeg", "jpg", "png", "svg", "webp"}
	if len(got) != len(want) {
		t.Fatalf("AllowedAssetExtensions() = %v, want %v", got, want)
	}
	for i, ext := range want {
		if got[i] != ext {
			t.Fatalf("AllowedAssetExtensions()[%d] = %q, want %q (sorted order)", i, got[i], ext)
		}
	}
	// No leading dot — the point of this accessor is a clean list for
	// get_capabilities' response, not upload_page_asset's internal
	// map-keyed-by-extension-with-dot representation.
	for _, ext := range got {
		if ext[0] == '.' {
			t.Fatalf("AllowedAssetExtensions() returned %q with a leading dot, want it stripped", ext)
		}
	}
}
