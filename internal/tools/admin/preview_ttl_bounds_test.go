package admin_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

// PreviewTTLBoundsSeconds feeds create_preview's advertised ttl_seconds
// bounds (get_capabilities and the tool's own description rely on these
// being accurate) — previously untested directly.
func TestPreviewTTLBoundsSecondsOrdering(t *testing.T) {
	min, def, max := admin.PreviewTTLBoundsSeconds()
	if min <= 0 {
		t.Fatalf("min = %d, want > 0", min)
	}
	if !(min <= def && def <= max) {
		t.Fatalf("bounds out of order: min=%d default=%d max=%d, want min <= default <= max", min, def, max)
	}
}
