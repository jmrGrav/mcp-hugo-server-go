package write_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestRateLimitScalarMirrorsStructuredRemaining is the #852 invariant test:
// wherever a response carries BOTH the legacy scalar root-level
// `rate_limit_remaining` AND the structured `data.rate_limit` bucket, the
// scalar MUST equal `data.rate_limit.remaining`. The structured
// `data.rate_limit.remaining` is the canonical source of truth (see
// docs/mcp-contract.md §6.4); the scalar is a deprecated back-compat mirror.
//
// The scalar is populated only on the error path (on success the scalar is
// deliberately omitted, #520/#605, leaving the bucket as the sole surface),
// so the invariant is exercised by forcing a rate_limit_exceeded error: with
// CreateUpdatePerMin=1 the first create_page consumes the only token and the
// second is rejected, producing a response that carries both fields at
// remaining=0. An earlier bug class (#690/#725) had one copy correct while
// the other silently fell back to 0; this locks the two together so a future
// refactor that repopulates them from divergent sources fails CI.
func TestRateLimitScalarMirrorsStructuredRemaining(t *testing.T) {
	contentRoot := t.TempDir()
	rl := config.Default().RateLimit
	rl.CreateUpdatePerMin = 1
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RateLimit: &rl})
	defer done()

	// Consume the only create/update/upload token.
	first := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/rl-first", "title": "First", "body": "Body.",
		"tags": []any{}, "categories": []any{},
	})
	if first.IsError {
		t.Fatalf("first create_page unexpectedly failed: %s", marshalContent(t, first))
	}

	// Second call must be rate_limit_exceeded and carry both representations.
	second := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/rl-second", "title": "Second", "body": "Body.",
		"tags": []any{}, "categories": []any{},
	})
	if !second.IsError {
		t.Fatalf("second create_page should have been rate limited: %s", marshalContent(t, second))
	}

	env := decodeWriteErrorEnvelope(t, second)
	assertRateLimitConvergence(t, "create_page (rate_limit_exceeded)", env)
}

// assertRateLimitConvergence fails unless, when both the scalar
// `rate_limit_remaining` and the structured `data.rate_limit` bucket are
// present on a response envelope, the scalar equals the bucket's `remaining`.
// If only one (or neither) is present there is nothing to converge and the
// check passes — the invariant is specifically about them not diverging
// wherever both appear.
func assertRateLimitConvergence(t *testing.T, label string, env map[string]any) {
	t.Helper()
	scalar, hasScalar := env["rate_limit_remaining"]
	data, _ := env["data"].(map[string]any)
	bucket, hasBucket := data["rate_limit"].(map[string]any)
	if !hasScalar || !hasBucket {
		t.Fatalf("%s: expected BOTH rate_limit_remaining (present=%v) and data.rate_limit (present=%v) to exercise the invariant", label, hasScalar, hasBucket)
	}
	scalarF, ok := scalar.(float64)
	if !ok {
		t.Fatalf("%s: rate_limit_remaining type = %T, want number", label, scalar)
	}
	bucketRemaining, ok := bucket["remaining"].(float64)
	if !ok {
		t.Fatalf("%s: data.rate_limit.remaining type = %T, want number", label, bucket["remaining"])
	}
	if scalarF != bucketRemaining {
		t.Errorf("%s: scalar rate_limit_remaining=%v diverges from canonical data.rate_limit.remaining=%v (#852)", label, scalarF, bucketRemaining)
	}
	if scalarF != 0 {
		t.Errorf("%s: expected remaining=0 after exhausting a 1-token budget, got %v", label, scalarF)
	}
}
