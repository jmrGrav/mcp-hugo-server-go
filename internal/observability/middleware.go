package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func RequestMiddleware(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		log.Info("request",
			"method", r.Method,
			"path", redactRequestPath(r.URL.Path),
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// redactRequestPath strips the confidentiality token out of preview URLs
// (/preview/{id}/{token}/...) before logging (#345). A preview token gates
// access to a build that may include unpublished drafts — logging it in
// cleartext on every request would defeat the point of a token-gated,
// time-limited surface, since anyone with log-read access could lift it and
// view the preview for the rest of its TTL.
func redactRequestPath(path string) string {
	if !strings.HasPrefix(path, "/preview/") {
		return path
	}
	rest := strings.TrimPrefix(path, "/preview/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return path
	}
	redacted := "/preview/" + parts[0] + "/[redacted]"
	if len(parts) == 3 {
		redacted += "/" + parts[2]
	}
	return redacted
}

// NewToolCallMiddleware returns an MCP server middleware that emits one structured
// log line per tools/call invocation. Fields emitted:
//
//   - tool_name    — the MCP tool name (e.g. "get_page"); unknown client-supplied
//     names are recorded as "unknown" to prevent cardinality explosion (E3).
//   - scope        — the OAuth scope this server tier serves (server-controlled, E1).
//   - duration_ms  — wall-clock latency in milliseconds.
//   - result_class — "success", "tool_error", or "protocol_error".
//   - response_bytes — approximate byte size of the result payload (estimated from
//     content text lengths, not a second JSON marshal, W1).
//
// knownTools is the set of registered tool names; any other name from the wire is
// replaced with "unknown" to cap Prometheus series cardinality (E3).
// scope is the fixed OAuth scope for this server instance (e.g. "content.read").
// No request arguments, page content, or OAuth tokens are logged.
func NewToolCallMiddleware(log *slog.Logger, m *Metrics, scope string, knownTools map[string]bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			toolName := "unknown"
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p != nil {
				if knownTools[p.Name] {
					toolName = p.Name
				}
			}

			start := time.Now()
			result, err := next(ctx, method, req)
			durationMs := time.Since(start).Milliseconds()

			resultClass := classifyToolResult(result, err)
			responseBytes := estimateResultBytes(result)
			degraded := isDegradedResult(result)

			attrs := []any{
				"tool_name", toolName,
				"scope", scope,
				"duration_ms", durationMs,
				"result_class", resultClass,
				"response_bytes", responseBytes,
			}
			// Security audit trail (#371): content.write/site.admin tool calls are
			// additionally tagged with event_type + result (the same two fields
			// audit.Log uses), so operators can filter mutation/admin outcomes on
			// a single consistent shape across the whole audit stream, without
			// parsing tool_name or result_class separately. Reads are deliberately
			// left untagged — logging every read as an "audit" event would be
			// high-volume noise for no forensic benefit (see docs/
			// security-audit-trail.md).
			if eventType := auditEventTypeForScope(scope); eventType != "" {
				attrs = append(attrs, "event_type", eventType, "result", resultClass)
				if degraded {
					attrs = append(attrs, "degraded", true)
				}
			}
			log.Info("tool_call", attrs...)
			if m != nil {
				m.RecordToolCall(toolName, scope, resultClass, durationMs)
			}
			return result, err
		}
	}
}

func classifyToolResult(result mcp.Result, err error) string {
	if err != nil {
		return "protocol_error"
	}
	if r, ok := result.(*mcp.CallToolResult); ok && r != nil && r.IsError {
		return "tool_error"
	}
	return "success"
}

// estimateResultBytes approximates the wire size of a CallToolResult from
// its text content lengths — avoids a second full JSON marshal (W1).
func estimateResultBytes(result mcp.Result) int {
	r, ok := result.(*mcp.CallToolResult)
	if !ok || r == nil {
		return 0
	}
	n := 0
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			n += len(tc.Text)
		}
	}
	return n
}

// auditEventTypeForScope maps an OAuth scope tier to a security-audit-trail
// (#371) event type. Returns "" for scopes that should not be tagged as
// audit events (anonymous, content.read) — see the comment at the call site.
func auditEventTypeForScope(scope string) string {
	switch scope {
	case "content.write":
		return audit.EventMutation
	case "site.admin", "system.admin":
		return audit.EventAdminOperation
	default:
		return ""
	}
}

// isDegradedResult reports whether a successful (non-IsError) tool result's
// StructuredContent carries a "status" field indicating a degraded outcome
// (e.g. build_site's "partial_success"), so the audit trail can distinguish
// "succeeded cleanly" from "succeeded with a warning" without every caller
// having to know each tool's own status vocabulary.
func isDegradedResult(result mcp.Result) bool {
	r, ok := result.(*mcp.CallToolResult)
	if !ok || r == nil || r.IsError || r.StructuredContent == nil {
		return false
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		return false
	}
	var probe struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Status == "partial_success"
}
