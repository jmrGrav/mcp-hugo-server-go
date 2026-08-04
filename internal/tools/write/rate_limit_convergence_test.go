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
// This test exercises the error path specifically: forcing a
// rate_limit_exceeded response with CreateUpdatePerMin=1 (the first create_page
// consumes the only token; the second is rejected) yields both fields at
// remaining=0. The root-level scalar is present on success too (see
// TestRateLimitScalarMirrorsStructuredRemainingOnSuccess for that half of the
// invariant); only the DATA-level scalar copy is deliberately omitted on
// success (#520/#605). An earlier bug class (#690/#725) had one copy correct
// while the other silently fell back to 0; this locks the two together so a
// future refactor that repopulates them from divergent sources fails CI.
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

// TestRateLimitScalarMirrorsStructuredRemainingOnSuccess closes the other half
// of the #852 AC3 invariant ("both representations stay identical wherever both
// are present"). The sibling error-path test above only exercises the
// rate_limit_exceeded branch, but a SUCCESSFUL write also carries both surfaces:
// the root-level `rate_limit_remaining` scalar (createPageOutput.RateLimitRemaining,
// no omitempty, "mirrored at the root on both success and error" per its own
// contract comment) AND the structured `data.rate_limit` bucket populated on the
// success path. They are filled from two separate rateLimitRemaining(limiter)
// call sites, so this is precisely the #690/#725 divergence class — and it was
// previously untested on the far more common success path. A fresh limiter with
// ample budget is used so remaining is a non-zero value, proving the convergence
// holds for real quota readings, not only the exhausted remaining=0 case.
func TestRateLimitScalarMirrorsStructuredRemainingOnSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/rl-success", "title": "Success", "body": "Body.",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		t.Fatalf("create_page unexpectedly failed: %s", marshalContent(t, res))
	}

	env := decodeWriteContent(t, res)
	scalar, hasScalar := env["rate_limit_remaining"]
	data, _ := env["data"].(map[string]any)
	bucket, hasBucket := data["rate_limit"].(map[string]any)
	if !hasScalar || !hasBucket {
		t.Fatalf("success create_page: expected BOTH rate_limit_remaining (present=%v) and data.rate_limit (present=%v) so the #852 invariant is exercised on the success path", hasScalar, hasBucket)
	}
	scalarF, ok := scalar.(float64)
	if !ok {
		t.Fatalf("success create_page: rate_limit_remaining type = %T, want number", scalar)
	}
	bucketRemaining, ok := bucket["remaining"].(float64)
	if !ok {
		t.Fatalf("success create_page: data.rate_limit.remaining type = %T, want number", bucket["remaining"])
	}
	if scalarF != bucketRemaining {
		t.Errorf("success create_page: scalar rate_limit_remaining=%v diverges from canonical data.rate_limit.remaining=%v (#852)", scalarF, bucketRemaining)
	}
	if scalarF <= 0 {
		t.Errorf("success create_page: expected a positive remaining budget after a single successful write, got %v", scalarF)
	}
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
