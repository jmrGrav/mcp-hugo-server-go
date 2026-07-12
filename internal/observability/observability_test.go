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
