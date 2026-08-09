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

// The unified quota-consumption rule (#887) governs which
// failure classes consume a caller's per-caller mutation quota, applied
// identically across create_page / update_page / delete_page /
// upload_page_asset / delete_page_asset. Before #887 each handler placed its
// limiter.Allow() call ad hoc, so semantically identical failures cost quota
// on one tool but were free on another (a not_found rejection consumed the
// destructive quota on delete_page_asset but not on delete_page; not_a_bundle
// consumed on upload_page_asset but not on delete_page_asset; a missing
// expected_revision consumed on update_page but not on delete_page). That
// penalized the "check before you act" pattern this server's own tools
// (get_rate_limits, dry_run) exist to encourage.
//
// The rule (variant (b) from the issue — "consumed once identity/existence is
// confirmed, before content/state validation"), chosen because delete_page —
// the most carefully audited destructive path — already implemented exactly
// it, and #836 had already moved the limiter *lookup* early for accurate
// reporting without changing consumption:
//
//	FREE (never spends a token): malformed/invalid input (invalid_params,
//	bad slug/lang/title/filename/scope, a blocked shortcode), an absent or
//	ineligible target (not_found, not_a_bundle, ambiguous_language,
//	source_file_not_found), a missing required guard (expected_revision /
//	expected_sha256 not supplied), and a genuine idempotency replay.
//
//	CONSUMES (spends one token): a valid request against a confirmed-eligible
//	target that makes a genuine mutation attempt — including its state-conflict
//	rejections: revision_conflict, already_exists (the create/upload write-time
//	collision), asset_referenced, security/write/delete errors, and success.
//
// Mechanically: limiter.Allow() is invoked only after every free gate above
// has passed, immediately before the state-conflict check / atomic mutation.
// For create/upload there is no cheap pre-write existence gate — eligibility
// IS the atomic write — so Allow() sits just before it and already_exists
// consumes (this also keeps the pre-existing, tested contract that a
// second, colliding upload costs a token). Deliberately NOT loosening
// revision_conflict / already_exists to "free" is intentional: those are real
// state-conflict rejections, and a strict anti-abuse control is never relaxed
// for mere consistency.
//
// Out of scope: build_in_progress (a transient content-lock timeout) reflects
// momentary server state, not a classification of the caller's request, and
// its quota treatment depends on incidental lock/decode ordering per tool
// (upload_page_asset meters its base64 decode behind Allow() as a DoS guard,
// which necessarily places its lock wait after Allow); it is not governed by
// this rule. dry_run never consumes on any tool (#588).
//
// `owner` remains caller-supplied advisory metadata for test content created
// via create_page, but it is intentionally excluded from quota and
// authorization decisions on destructive tools. Rate limiting continues to
// depend only on the request class and current limiter state, never on an
// unbound label supplied by the caller.
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
		Description:  "Check your remaining per-caller mutation quota before acting, instead of only discovering it after a create_page/update_page/delete_page/... call already reports rate_limit_remaining. Reports both independent quotas: `create_update_upload` (shared by create_page/update_page/upload_page_asset/apply_content_plan/rollback_change) and `destructive` (shared by delete_page/delete_page_asset). Each bucket's `remaining`/`limit` are call counts; `retry_after_seconds` is 0 if a call would succeed right now, otherwise how long to wait. Calling this tool never itself consumes any quota. Requires write (this reports the same per-caller identity and budget the mutation tools themselves use, which isn't meaningful for a pure reader).",
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
