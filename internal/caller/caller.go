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
	if id, _ := ctx.Value(oauth.CtxTokenID).(string); strings.TrimSpace(id) != "" {
		return id
	}
	if ip, _ := ctx.Value(oauth.CtxCallerIP).(string); strings.TrimSpace(ip) != "" {
		return ip
	}
	return ""
}
