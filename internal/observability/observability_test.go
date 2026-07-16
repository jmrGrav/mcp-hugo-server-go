package observability

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMetricsRenderPrometheus(t *testing.T) {
	m := NewMetrics()
	m.RecordLegacyScope("mcp")
	got := m.RenderPrometheus()
	if !strings.Contains(got, "mcp_legacy_scope_requests_total") {
		t.Fatalf("RenderPrometheus() missing metric: %q", got)
	}
	if !strings.Contains(got, `scope="mcp"`) {
		t.Fatalf("RenderPrometheus() missing scope label: %q", got)
	}
}

func TestToolCallMetrics(t *testing.T) {
	m := NewMetrics()
	m.RecordToolCall("get_page", "content.read", "success", 42)
	m.RecordToolCall("get_page", "content.read", "success", 10)
	m.RecordToolCall("get_page", "content.read", "tool_error", 5)

	prom := m.RenderPrometheus()
	if !strings.Contains(prom, "mcp_tool_calls_total") {
		t.Fatalf("missing mcp_tool_calls_total: %s", prom)
	}
	if !strings.Contains(prom, `tool="get_page"`) {
		t.Fatalf("missing tool label: %s", prom)
	}
	if !strings.Contains(prom, `result="success"`) {
		t.Fatalf("missing result label: %s", prom)
	}
	if !strings.Contains(prom, `result="tool_error"`) {
		t.Fatalf("missing tool_error result: %s", prom)
	}
	if !strings.Contains(prom, "mcp_tool_call_duration_ms_total") {
		t.Fatalf("missing duration metric: %s", prom)
	}
}

func TestToolCallMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	m := NewMetrics()
	knownTools := map[string]bool{"get_page": true}
	mw := NewToolCallMiddleware(log, m, "content.read", knownTools)

	// Wrap a simple handler that returns a successful tool result.
	inner := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	wrapped := mw(inner)

	params := &mcp.CallToolParamsRaw{Name: "get_page"}
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: params}
	result, err := wrapped(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("wrapped handler error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	logLine := buf.String()
	if !strings.Contains(logLine, `"tool_call"`) {
		t.Fatalf("missing tool_call log: %s", logLine)
	}
	if !strings.Contains(logLine, `"get_page"`) {
		t.Fatalf("missing tool name in log: %s", logLine)
	}
	if !strings.Contains(logLine, `"success"`) {
		t.Fatalf("missing result_class in log: %s", logLine)
	}

	// Non-tools/call method should NOT emit a log line.
	buf.Reset()
	_, _ = wrapped(context.Background(), "tools/list", req)
	if strings.Contains(buf.String(), "tool_call") {
		t.Fatal("should not log for non-tools/call method")
	}

	// tool_error classification.
	buf.Reset()
	errInner := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		r := &mcp.CallToolResult{}
		r.IsError = true
		return r, nil
	})
	wrapped2 := mw(errInner)
	_, _ = wrapped2(context.Background(), "tools/call", req)
	if !strings.Contains(buf.String(), `"tool_error"`) {
		t.Fatalf("expected tool_error class: %s", buf.String())
	}

	// E1: scope is logged, not derived from context.
	if !strings.Contains(logLine, `"content.read"`) {
		t.Fatalf("scope not present in log: %s", logLine)
	}

	// E3: unknown tool names are bucketed as "unknown".
	buf.Reset()
	unknownParams := &mcp.CallToolParamsRaw{Name: "evil_tool_xyz"}
	unknownReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: unknownParams}
	_, _ = wrapped(context.Background(), "tools/call", unknownReq)
	unknownLog := buf.String()
	if !strings.Contains(unknownLog, `"unknown"`) {
		t.Fatalf("unregistered tool not bucketed as unknown: %s", unknownLog)
	}
	if strings.Contains(unknownLog, "evil_tool_xyz") {
		t.Fatalf("raw unregistered tool name leaked into log: %s", unknownLog)
	}

	// Prometheus counters updated.
	prom := m.RenderPrometheus()
	if !strings.Contains(prom, "mcp_tool_calls_total") {
		t.Fatalf("prometheus not updated: %s", prom)
	}
	if !strings.Contains(prom, `tool="unknown"`) {
		t.Fatalf("unknown tool not recorded in prometheus: %s", prom)
	}
}

func TestClassifyToolResult(t *testing.T) {
	if got := classifyToolResult(&mcp.CallToolResult{}, nil); got != "success" {
		t.Fatalf("success case: %q", got)
	}
	errResult := &mcp.CallToolResult{}
	errResult.IsError = true
	if got := classifyToolResult(errResult, nil); got != "tool_error" {
		t.Fatalf("tool_error case: %q", got)
	}
	if got := classifyToolResult(nil, context.DeadlineExceeded); got != "protocol_error" {
		t.Fatalf("protocol_error case: %q", got)
	}
}

// TestToolCallMiddlewareTagsMutationAndAdminEventTypes proves the #371
// security-audit-trail wiring: content.write/site.admin tool calls carry an
// event_type field distinguishing them as mutation/admin_operation, while
// content.read calls are left untagged (avoiding audit-volume noise for
// ordinary reads).
func TestToolCallMiddlewareTagsMutationAndAdminEventTypes(t *testing.T) {
	knownTools := map[string]bool{"create_page": true, "build_site": true, "get_page": true}
	inner := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "create_page"}}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := NewToolCallMiddleware(log, NewMetrics(), "content.write", knownTools)
	_, _ = mw(inner)(context.Background(), "tools/call", req)
	if !strings.Contains(buf.String(), `"event_type":"mutation"`) {
		t.Fatalf("content.write call missing event_type=mutation: %s", buf.String())
	}

	buf.Reset()
	adminReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "build_site"}}
	mwAdmin := NewToolCallMiddleware(log, NewMetrics(), "site.admin", knownTools)
	_, _ = mwAdmin(inner)(context.Background(), "tools/call", adminReq)
	if !strings.Contains(buf.String(), `"event_type":"admin_operation"`) {
		t.Fatalf("site.admin call missing event_type=admin_operation: %s", buf.String())
	}

	buf.Reset()
	readReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "get_page"}}
	mwRead := NewToolCallMiddleware(log, NewMetrics(), "content.read", knownTools)
	_, _ = mwRead(inner)(context.Background(), "tools/call", readReq)
	if strings.Contains(buf.String(), "event_type") {
		t.Fatalf("content.read call must not be tagged with an audit event_type: %s", buf.String())
	}
}

// TestToolCallMiddlewareFlagsDegradedResults proves that a successful
// (non-IsError) result whose StructuredContent carries status:
// "partial_success" (e.g. build_site) is flagged degraded:true in the audit
// trail, distinguishing "succeeded cleanly" from "succeeded with a warning".
func TestToolCallMiddlewareFlagsDegradedResults(t *testing.T) {
	knownTools := map[string]bool{"build_site": true}
	degradedInner := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"status": "partial_success"}}, nil
	})
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "build_site"}}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := NewToolCallMiddleware(log, NewMetrics(), "site.admin", knownTools)
	_, _ = mw(degradedInner)(context.Background(), "tools/call", req)
	if !strings.Contains(buf.String(), `"degraded":true`) {
		t.Fatalf("partial_success result must be flagged degraded: %s", buf.String())
	}

	buf.Reset()
	okInner := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"status": "ok"}}, nil
	})
	_, _ = mw(okInner)(context.Background(), "tools/call", req)
	if strings.Contains(buf.String(), "degraded") {
		t.Fatalf("clean success must not be flagged degraded: %s", buf.String())
	}
}

// TestAuditStreamUnifiedByEventType proves the #371 acceptance criterion
// that operators can filter the entire audit stream on a single field.
// mutation/admin_operation events are tagged onto the existing tool_call
// line (msg differs from the audit package's own "msg":"audit" lines), so
// event_type — not msg — must be the documented, working discriminator,
// and both paths must carry the same result field.
func TestAuditStreamUnifiedByEventType(t *testing.T) {
	knownTools := map[string]bool{"create_page": true}
	inner := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "create_page"}}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := NewToolCallMiddleware(log, NewMetrics(), "content.write", knownTools)
	_, _ = mw(inner)(context.Background(), "tools/call", req)

	line := buf.String()
	if !strings.Contains(line, `"event_type":"mutation"`) {
		t.Fatalf("mutation event missing event_type: %s", line)
	}
	if !strings.Contains(line, `"result":"success"`) {
		t.Fatalf("mutation event missing result field alongside event_type: %s", line)
	}
}

func TestNewLoggerAndRequestMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}), log)
	req := httptest.NewRequest(http.MethodGet, "https://example.test/mcp", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("middleware status = %d", rec.Code)
	}
	if !strings.Contains(buf.String(), `"method":"GET"`) {
		t.Fatalf("middleware log missing request fields: %s", buf.String())
	}
	if NewLogger() == nil {
		t.Fatal("NewLogger() returned nil")
	}
}

// TestRequestMiddlewareRedactsPreviewToken proves the #345 preview token
// (the sole confidentiality boundary for a draft preview build) never
// appears in cleartext in the request log — logging it would let anyone
// with log-read access lift a preview URL and view unpublished drafts for
// the rest of its TTL.
func TestRequestMiddlewareRedactsPreviewToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	const secretToken = "super-secret-preview-token-abc123"
	req := httptest.NewRequest(http.MethodGet, "https://example.test/preview/abc123/"+secretToken+"/index.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, secretToken) {
		t.Fatalf("preview token leaked into request log: %s", logged)
	}
	if !strings.Contains(logged, `"path":"/preview/abc123/[redacted]/index.html"`) {
		t.Fatalf("expected redacted path in log, got: %s", logged)
	}
}

func TestRedactRequestPathLeavesNonPreviewPathsUnchanged(t *testing.T) {
	for _, p := range []string{"/mcp", "/.well-known/agent.json", "/preview/", "/preview/onlyid"} {
		if got := redactRequestPath(p); got != p {
			t.Fatalf("redactRequestPath(%q) = %q, want unchanged", p, got)
		}
	}
}
