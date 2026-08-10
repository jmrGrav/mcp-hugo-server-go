package caller

import (
	"context"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// Key returns the strongest stable caller identity currently present in ctx.
// For OAuth requests this prefers the stable principal identity (client_id,
// agent registration id, etc.), then falls back to the per-bearer token hash;
// without OAuth it falls back to caller IP, and finally to "" when neither
// exists.
func Key(ctx context.Context) string {
	if principal, _ := ctx.Value(oauth.CtxPrincipal).(string); strings.TrimSpace(principal) != "" {
		return principal
	}
	return TokenKey(ctx)
}

// TokenKey returns the caller identity scoped to the exact bearer token in
// use, never the broader OAuth principal — for the ephemeral-state isolation
// boundary (plans, rollback snapshots, previews) that #932/#934 bound to the
// specific bearer that created them. Widening that boundary to Key()'s
// principal-preferring identity would let any token sharing a principal
// (client_id / agent registration id) — e.g. after a token refresh, or a
// second concurrent session for the same registered client — consume,
// apply, or revoke another token's plan/snapshot/preview, reopening the
// exact cross-caller leak #932/#934 fixed. Key() itself should only be used
// where that principal-level sharing is the deliberate goal, such as
// mutation rate-limit quotas (#950), where an agent renewing its token
// mid-session is meant to keep the same shared bucket.
func TokenKey(ctx context.Context) string {
	if id, _ := ctx.Value(oauth.CtxTokenID).(string); strings.TrimSpace(id) != "" {
		return id
	}
	if ip, _ := ctx.Value(oauth.CtxCallerIP).(string); strings.TrimSpace(ip) != "" {
		return ip
	}
	return ""
}
