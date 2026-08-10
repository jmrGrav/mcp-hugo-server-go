package admin

import (
	"context"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
)

// previewCallerKey scopes preview ownership to the exact bearer token (see
// caller.TokenKey), not the broader OAuth principal that caller.Key() now
// prefers for rate-limit quotas (#950) — otherwise a token refresh or a
// second session under the same principal could list, inspect, or revoke
// another session's preview, reopening #932/#934.
func previewCallerKey(ctx context.Context) string {
	if key := caller.TokenKey(ctx); key != "" {
		return key
	}
	return currentUserForLog()
}
