package observability

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
