package caller

import (
	"context"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// Key returns the strongest stable caller identity currently present in ctx.
// For OAuth requests this is the per-bearer token hash; without OAuth it
// falls back to caller IP, and finally to "" when neither exists.
func Key(ctx context.Context) string {
	if id, _ := ctx.Value(oauth.CtxTokenID).(string); strings.TrimSpace(id) != "" {
		return id
	}
	if ip, _ := ctx.Value(oauth.CtxCallerIP).(string); strings.TrimSpace(ip) != "" {
		return ip
	}
	return ""
}
