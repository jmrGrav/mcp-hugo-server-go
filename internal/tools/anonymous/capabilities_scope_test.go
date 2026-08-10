package anonymous

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// TestEffectiveCapabilityScopesFallsBackToServerTierWithoutOAuth is a
// regression test: on a no-OAuth deployment (OAuth disabled, or the stdio
// transport, neither of which ever populates oauth.CtxScope), the write
// server tier grants full write access with no further per-call scope
// check — reporting "anonymous" there would falsely tell the caller their
// own tools are masked. The fallback must reflect the physical tier
// (scopeName) the tool was registered on, not a hardcoded "anonymous".
func TestEffectiveCapabilityScopesFallsBackToServerTierWithoutOAuth(t *testing.T) {
	ctx := context.Background()

	if got := effectiveCapabilityScopes(ctx, "write"); len(got) != 1 || got[0] != "write" {
		t.Errorf("effectiveCapabilityScopes(ctx, %q) = %v, want [write]", "write", got)
	}
	if got := effectiveCapabilityScopes(ctx, ""); len(got) != 1 || got[0] != "read" {
		t.Errorf("effectiveCapabilityScopes(ctx, %q) = %v, want [read] (public tier registers read tools unconditionally)", "", got)
	}

	if masked := maskedCapabilityTools(ctx, "write", true); masked != nil {
		t.Errorf("maskedCapabilityTools(ctx, write, writeEnabled=true) = %+v, want nil (JSON omits the field entirely; a zero-value struct would not)", masked)
	}
	masked := maskedCapabilityTools(ctx, "", true)
	if masked == nil || masked.Count == 0 || masked.Reason != "missing_scope" {
		t.Errorf("maskedCapabilityTools(ctx, \"\", true) = %+v, want non-zero count with reason missing_scope (write tools masked on the public tier)", masked)
	}
}

// TestEffectiveCapabilityScopesPrefersVerifiedOAuthScope confirms an
// OAuth-verified scope in context always wins over the server-tier
// fallback, regardless of which tier the tool happens to be registered on.
func TestEffectiveCapabilityScopesPrefersVerifiedOAuthScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), oauth.CtxScope, "read")

	if got := effectiveCapabilityScopes(ctx, "write"); len(got) != 1 || got[0] != "read" {
		t.Errorf("effectiveCapabilityScopes with verified scope=read on write tier = %v, want [read]", got)
	}
	masked := maskedCapabilityTools(ctx, "write", true)
	if masked == nil || masked.Reason != "missing_scope" || len(masked.Scopes) != 1 || masked.Scopes[0] != "write" {
		t.Errorf("maskedCapabilityTools with verified scope=read on write tier = %+v, want missing_scope/[write]", masked)
	}
}

// TestMaskedCapabilityToolsReportsNotConfiguredWhenWriteScopeHasNoContentRoot
// is a regression test: a write-scoped caller (real OAuth "write" token, or
// the no-OAuth write-tier fallback) whose deployment has no content_root
// configured still can't see write tools — newScopedServer only registers
// them when scopeName == "write" && writeEnabled. Reporting them as
// unmasked, or masking them under "missing_scope" (which would tell the
// caller to get a scope it already has), would both be wrong; the reason
// must be "not_configured".
func TestMaskedCapabilityToolsReportsNotConfiguredWhenWriteScopeHasNoContentRoot(t *testing.T) {
	ctx := context.WithValue(context.Background(), oauth.CtxScope, "write")

	masked := maskedCapabilityTools(ctx, "write", false)
	if masked == nil || masked.Reason != "not_configured" || len(masked.Scopes) != 1 || masked.Scopes[0] != "write" || masked.Count == 0 {
		t.Errorf("maskedCapabilityTools(write scope, writeEnabled=false) = %+v, want not_configured/[write] with non-zero count", masked)
	}

	// Same result via the no-OAuth fallback path (scopeName carries the tier).
	fallbackMasked := maskedCapabilityTools(context.Background(), "write", false)
	if fallbackMasked == nil || fallbackMasked.Reason != "not_configured" {
		t.Errorf("maskedCapabilityTools(fallback write tier, writeEnabled=false) = %+v, want not_configured", fallbackMasked)
	}
}
