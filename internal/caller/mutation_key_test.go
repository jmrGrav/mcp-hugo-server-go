package caller

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// MutationKey is the identity every mutation rate-limit bucket,
// idempotency-store lookup, and change-set ownership check in
// internal/tools/write/internal/changeset is keyed by (see its own doc
// comment) — a caller-isolation boundary, not just a logging convenience.
// Previously untested directly (only exercised indirectly through those
// higher-level packages' own tests).
func TestMutationKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), oauth.CtxPrincipal, "principal-a")
	if got := MutationKey(ctx); got != "principal-a" {
		t.Fatalf("MutationKey (principal present) = %q, want principal-a", got)
	}

	ctx = context.WithValue(context.Background(), oauth.CtxTokenID, "tok-abc")
	if got := MutationKey(ctx); got != "tok-abc" {
		t.Fatalf("MutationKey (token only) = %q, want tok-abc", got)
	}

	// No principal, no token, no caller IP: Key() itself returns "", and
	// MutationKey's whole reason to exist over bare Key() is substituting
	// "unknown" instead of leaving quota/idempotency keyed on an empty
	// string.
	if got := MutationKey(context.Background()); got != "unknown" {
		t.Fatalf("MutationKey (empty context) = %q, want the \"unknown\" fallback", got)
	}
}
