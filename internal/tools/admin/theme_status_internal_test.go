package admin

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestTableOverflowProtectionUnknownWithNoResolvableTheme is a regression
// test for the #1138 Part 2 exported wrapper: an empty/unconfigured
// HugoRoot resolves no theme names at all (resolveThemeNames returns an
// error string, not a panic), and TableOverflowProtection must report that
// as "unknown" (nil), not a guessed false.
func TestTableOverflowProtectionUnknownWithNoResolvableTheme(t *testing.T) {
	cfg := config.Config{HugoRoot: t.TempDir()}
	got := TableOverflowProtection(context.Background(), cfg)
	if got != nil {
		t.Fatalf("TableOverflowProtection() = %v, want nil (no resolvable theme)", *got)
	}
}
