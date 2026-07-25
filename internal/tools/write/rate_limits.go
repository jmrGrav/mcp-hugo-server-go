package write

import (
	"context"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// rateLimitBucket reports one of the two per-caller mutation quotas (#614):
// remaining is the caller's current available quota, rounded down to a
// whole call (matching rate_limit_remaining's existing semantics on every
// mutation tool's own response, #466); retry_after_seconds is how long the
// caller must wait before its next call to that quota would succeed, 0 if
// it would succeed right now.
type rateLimitBucket struct {
	Remaining         int     `json:"remaining"`
	Limit             int     `json:"limit"`
	WindowSeconds     int     `json:"window_seconds"`
	Scope             string  `json:"scope"`
	ResetAt           string  `json:"reset_at"`
	RetryAfterSeconds float64 `json:"retry_after_seconds"`
}

const (
	rateLimitScopeCreateUpdateUpload = "create_update_upload"
	rateLimitScopeDestructive        = "destructive"
	rateLimitWindowSeconds           = 60
)

func newRateLimitBucket(l *rate.Limiter, limit int, scope string, now time.Time) rateLimitBucket {
	retryAfter := rateLimitRetryAfterSeconds(l)
	resetAt := now.UTC()
	if retryAfter > 0 {
		resetAt = resetAt.Add(time.Duration(retryAfter * float64(time.Second)))
	}
	return rateLimitBucket{
		Remaining:         rateLimitRemaining(l),
		Limit:             limit,
		WindowSeconds:     rateLimitWindowSeconds,
		Scope:             scope,
		ResetAt:           resetAt.Format(time.RFC3339),
		RetryAfterSeconds: retryAfter,
	}
}

func rateLimitRootFields(l *rate.Limiter) map[string]any {
	return map[string]any{
		"rate_limit_remaining": rateLimitRemaining(l),
	}
}

func rateLimitDataFields(l *rate.Limiter, limit int, scope string, now time.Time) map[string]any {
	bucket := newRateLimitBucket(l, limit, scope, now)
	return map[string]any{
		"rate_limit_remaining": bucket.Remaining,
		"rate_limit":           bucket,
	}
}

func ptrRateLimitBucket(bucket rateLimitBucket) *rateLimitBucket { return &bucket }

// getRateLimitsInput is empty — get_rate_limits takes no parameters; the
// caller identity it reports on comes entirely from context (the same
// mutationCallerKey every mutation tool already derives its own quota from).
type getRateLimitsInput struct{}

type getRateLimitsData struct {
	CreateUpdateUpload rateLimitBucket `json:"create_update_upload"`
	Destructive        rateLimitBucket `json:"destructive"`
}

type getRateLimitsOutput struct {
	toolcontract.ToolResponse[getRateLimitsData]
}

func newGetRateLimitsOutput(data getRateLimitsData) getRateLimitsOutput {
	return getRateLimitsOutput{ToolResponse: writeSuccessEnvelope(data)}
}

// registerGetRateLimits wires get_rate_limits (#614): a read-only way to
// check the caller's remaining budget on both per-caller mutation quotas
// before acting, instead of only ever discovering it after the fact via a
// mutation call's own rate_limit_remaining field. Reuses the exact
// callerLimiter/mutationCallerKey/rateLimitRemaining/rateLimitRetryAfterSeconds
// machinery every mutation tool already shares (#378, #466) rather than
// duplicating rate-limit logic — this never calls Allow(), so checking
// quota here never consumes it.
func registerGetRateLimits(s *mcp.Server, cfg config.Config, mutationMu *sync.Mutex, mutationLimiters map[string]*rate.Limiter, deleteMu *sync.Mutex, deleteLimiters map[string]*rate.Limiter) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "get_rate_limits",
		Title:        "Get rate limits",
		Description:  "Check your remaining per-caller mutation quota before acting, instead of only discovering it after a create_page/update_page/delete_page/... call already reports rate_limit_remaining. Reports both independent quotas: `create_update_upload` (shared by create_page/update_page/upload_page_asset/apply_content_plan/rollback_change) and `destructive` (shared by delete_page/delete_page_asset). Each bucket's `remaining`/`limit` are call counts; `retry_after_seconds` is 0 if a call would succeed right now, otherwise how long to wait. Calling this tool never itself consumes any quota. Requires content.write (this reports the same per-caller identity and budget the mutation tools themselves use, which isn't meaningful for a pure reader).",
		InputSchema:  tools.MustSchema[getRateLimitsInput](),
		OutputSchema: tools.MustSchema[getRateLimitsOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ getRateLimitsInput) (*mcp.CallToolResult, getRateLimitsOutput, error) {
		callerKey := mutationCallerKey(ctx)

		createLimiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		deleteLimiter := callerLimiter(deleteMu, deleteLimiters, callerKey, cfg.RateLimit.DestructivePerMin)

		return nil, newGetRateLimitsOutput(getRateLimitsData{
			CreateUpdateUpload: newRateLimitBucket(createLimiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			Destructive:        newRateLimitBucket(deleteLimiter, cfg.RateLimit.DestructivePerMin, rateLimitScopeDestructive, time.Now().UTC()),
		}), nil
	}))
}
