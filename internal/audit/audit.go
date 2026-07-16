// Package audit defines the structured security audit event vocabulary for
// #371. It deliberately does not introduce a separate logging stack: events
// are emitted through the process's default slog logger (the same JSON
// pipeline configured in internal/server, durable via whatever log
// collection the operator already has for stderr/journald). See
// docs/security-audit-trail.md for the event shape and retention notes.
package audit

import (
	"context"
	"log/slog"
)

// Event types. Every audit line carries event_type and result so operators
// can filter/alert on them without parsing free-text messages, and so scope
// denials/auth failures are mechanically distinguishable from ordinary tool
// errors (a structured tool_error still logs event_type=mutation/
// admin_operation, never scope_denied/auth_rejected).
const (
	// EventAuthRejected marks a request rejected before any scope was
	// established: missing/malformed/invalid bearer token.
	EventAuthRejected = "auth_rejected"
	// EventScopeDenied marks a request with a validated token whose scope
	// was insufficient for the requested operation.
	EventScopeDenied = "scope_denied"
	// EventOperatorMilestone marks reader self-registration and
	// operator-approval-flow transitions (issued, pending claim, claimed,
	// claim failed/expired).
	EventOperatorMilestone = "operator_milestone"
	// EventMutation marks a content.write tool call outcome.
	EventMutation = "mutation"
	// EventAdminOperation marks a site.admin tool call outcome.
	EventAdminOperation = "admin_operation"
)

// Log emits one structured audit line at the given level. Callers must
// never pass a raw bearer token/secret as an attr, and any path-shaped
// value must already be a logical identifier (slug, tool name) rather than
// an absolute host filesystem path — this function does no redaction of its
// own; it only fixes the event_type/result vocabulary and the message name
// so every audit event is uniformly greppable as `"msg":"audit"`.
func Log(level slog.Level, eventType, result string, attrs ...any) {
	args := make([]any, 0, len(attrs)+4)
	args = append(args, "event_type", eventType, "result", result)
	args = append(args, attrs...)
	slog.Log(context.Background(), level, "audit", args...)
}

// Info is a convenience wrapper for successful/expected audit events.
func Info(eventType, result string, attrs ...any) {
	Log(slog.LevelInfo, eventType, result, attrs...)
}

// Warn is a convenience wrapper for denials/failures worth an operator's
// attention (auth rejections, scope denials, failed operator-approval
// attempts).
func Warn(eventType, result string, attrs ...any) {
	Log(slog.LevelWarn, eventType, result, attrs...)
}
