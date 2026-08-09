package admin

import (
	"context"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
)

func previewCallerKey(ctx context.Context) string {
	if key := caller.Key(ctx); key != "" {
		return key
	}
	return currentUserForLog()
}
