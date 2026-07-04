package observability_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/observability"
)

func TestRequestMiddlewareLogsRequest(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := observability.RequestMiddleware(inner, log)

	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	checks := map[string]any{
		"method": "GET",
		"path":   "/test-path",
		"status": float64(http.StatusCreated),
	}
	for k, want := range checks {
		got, ok := entry[k]
		if !ok {
			t.Errorf("missing log field %q", k)
			continue
		}
		if got != want {
			t.Errorf("field %q: got %v, want %v", k, got, want)
		}
	}

	if _, ok := entry["duration_ms"]; !ok {
		t.Error("missing log field \"duration_ms\"")
	}
}

func TestMiddlewarePassthrough(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("hello"))
	})
	handler := observability.RequestMiddleware(inner, log)

	req := httptest.NewRequest(http.MethodPost, "/pass", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler was not called")
	}
	if rr.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", rr.Code)
	}
	if rr.Body.String() != "hello" {
		t.Errorf("expected body \"hello\", got %q", rr.Body.String())
	}
	if rr.Header().Get("X-Custom") != "yes" {
		t.Errorf("expected X-Custom header to be passed through")
	}
}
