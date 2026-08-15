package admin_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestRunPostBuildHooksMultipleHooksWithRequestIntrospection verifies end-to-end
// execution of multiple configured hooks with detailed assertion of the actual HTTP
// requests received. This documents that the tool sends POST with Content-Type:
// application/json and body {"event":"post_build"} to each configured URL.
func TestRunPostBuildHooksMultipleHooksWithRequestIntrospection(t *testing.T) {
	// Capture first hook's request details.
	var firstReq struct {
		method  string
		headers http.Header
		body    []byte
	}
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstReq.method = r.Method
		firstReq.headers = r.Header.Clone()
		var err error
		firstReq.body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read first hook body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()

	// Capture second hook's request details.
	var secondReq struct {
		method  string
		headers http.Header
		body    []byte
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondReq.method = r.Method
		secondReq.headers = r.Header.Clone()
		var err error
		secondReq.body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read second hook body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv1.URL, srv2.URL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", data["results"])
	}

	// Verify first hook.
	if firstReq.method != http.MethodPost {
		t.Errorf("first hook method = %s, want POST", firstReq.method)
	}
	if ct := firstReq.headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("first hook Content-Type = %q, want application/json", ct)
	}
	if got, want := string(firstReq.body), `{"event":"post_build"}`; got != want {
		t.Errorf("first hook body = %q, want %q", got, want)
	}

	// Verify second hook.
	if secondReq.method != http.MethodPost {
		t.Errorf("second hook method = %s, want POST", secondReq.method)
	}
	if ct := secondReq.headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("second hook Content-Type = %q, want application/json", ct)
	}
	if got, want := string(secondReq.body), `{"event":"post_build"}`; got != want {
		t.Errorf("second hook body = %q, want %q", got, want)
	}

	// Verify result structure.
	firstResult := results[0].(map[string]any)
	if got := firstResult["url"]; got != srv1.URL {
		t.Errorf("first result URL = %v, want %s", got, srv1.URL)
	}
	if got := firstResult["status"]; got != float64(http.StatusOK) {
		t.Errorf("first result status = %v, want %d", got, http.StatusOK)
	}

	secondResult := results[1].(map[string]any)
	if got := secondResult["url"]; got != srv2.URL {
		t.Errorf("second result URL = %v, want %s", got, srv2.URL)
	}
	if got := secondResult["status"]; got != float64(http.StatusOK) {
		t.Errorf("second result status = %v, want %d", got, http.StatusOK)
	}
}

// TestRunPostBuildHooksNonSuccessResponseCaptured verifies that non-2xx HTTP
// responses (e.g., 500) are captured in hookResult.Status but NOT flagged as
// errors in hookResult.Error. This documents the current behavior: HTTP 500 is
// treated as a valid response with Status=500 and Error="" (advisory per-hook
// reporting, not blocking). This differs from transport-level failures which set
// Error instead.
//
// Note on architecture mismatch: The issue's acceptance criterion asked for
// "bounded stdout/stderr, exit status, and duration" — features that only exist
// in subprocess-based webhook execution. This tool fires HTTP POST webhooks, not
// shell subprocesses, so there is no stdout/stderr/exit status/duration to capture.
// Response bodies are discarded after a size limit check, HTTP status codes are
// captured in hookResult.Status, and transport errors are in hookResult.Error.
// This test exercises the real (non-dry_run) execution path; dry_run:true
// coverage already exists in hooks_envelope_test.go (#766).
func TestRunPostBuildHooksNonSuccessResponseCaptured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv.URL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tool call succeeds even when hook returns 500 (advisory, not blocking).
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("expected 1 result, got %#v", data["results"])
	}

	result := results[0].(map[string]any)
	if got := result["status"]; got != float64(http.StatusInternalServerError) {
		t.Errorf("result status = %v, want %d", got, http.StatusInternalServerError)
	}
	if got := result["error"]; got != nil && got != "" {
		t.Errorf("result error = %q, want empty (500 is not a transport error)", got)
	}
}

// TestRunPostBuildHooksMultipleHooksPartialFailure verifies that when one hook
// fails to connect, other configured hooks still execute. This documents that
// hook failures are purely advisory: the overall tool call succeeds (res.IsError==false),
// each hook's status/error is captured individually, and one hook's failure does
// not prevent other hooks from being attempted.
func TestRunPostBuildHooksMultipleHooksPartialFailure(t *testing.T) {
	// First hook succeeds.
	successCalled := false
	srvSuccess := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srvSuccess.Close()

	// Second hook is unreachable (connection refused).
	unreachableURL := "http://127.0.0.1:1"

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srvSuccess.URL, unreachableURL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tool call succeeds even though one hook failed (advisory).
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", data["results"])
	}

	// Verify first hook succeeded and was actually called.
	if !successCalled {
		t.Error("successful hook was not called")
	}
	firstResult := results[0].(map[string]any)
	if got := firstResult["url"]; got != srvSuccess.URL {
		t.Errorf("first result URL = %v, want %s", got, srvSuccess.URL)
	}
	if got := firstResult["status"]; got != float64(http.StatusOK) {
		t.Errorf("first result status = %v, want 200", got)
	}
	if got := firstResult["error"]; got != nil && got != "" {
		t.Errorf("first result error = %q, want empty", got)
	}

	// Verify second hook failed with transport error (no status set, error present).
	secondResult := results[1].(map[string]any)
	if got := secondResult["url"]; got != unreachableURL {
		t.Errorf("second result URL = %v, want %s", got, unreachableURL)
	}
	if got := secondResult["status"]; got != nil && got != float64(0) {
		t.Errorf("second result status = %v, want 0 (no response on transport error)", got)
	}
	if errVal := secondResult["error"]; errVal == nil || errVal == "" {
		t.Error("second result error should be non-empty (connection refused)")
	}
}

// TestRunPostBuildHooksResponseBodyDiscarded verifies that hook response bodies
// are read and discarded (up to maxHookResponseBytes) per the current design.
// The tool does not capture response body content — only status code and transport errors.
func TestRunPostBuildHooksResponseBodyDiscarded(t *testing.T) {
	bodySeen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodySeen = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this response body is discarded"))
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv.URL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	if !bodySeen {
		t.Error("hook was not invoked")
	}

	// Verify no response body content in result (only URL and Status).
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("expected 1 result, got %#v", data["results"])
	}

	result := results[0].(map[string]any)
	// Response body is read but not stored — only presence of URL/Status indicate success.
	if _, hasBody := result["body"]; hasBody {
		t.Error("result should not contain body field (bodies are discarded)")
	}
	if got := result["status"]; got != float64(http.StatusOK) {
		t.Errorf("result status = %v, want 200", got)
	}
}

// TestRunPostBuildHooksEmptyConfigNoHooksExecuted verifies that when no hooks
// are configured, the tool succeeds with an empty results array.
func TestRunPostBuildHooksEmptyConfigNoHooksExecuted(t *testing.T) {
	cfg := config.Default()
	cfg.PostBuildHooks = []string{} // No hooks

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_post_build_hooks returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 0 {
		t.Fatalf("expected 0 results for empty hook config, got %#v", data["results"])
	}
}

// TestRunPostBuildHooksNoRootFieldDuplication is a regression test for #1118:
// finishes #520/#573's convergence for this tool — the payload must live
// only under data.*, with no root-level compatibility aliases (which #552
// originally added and #1060 deprecated).
func TestRunPostBuildHooksNoRootFieldDuplication(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv.URL}

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "run_post_build_hooks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := decodeStructuredResult(t, res)
	if got := out["success"]; got != true {
		t.Errorf("success = %v, want true (#1118)", got)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any (#1118)", out["data"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("meta type = %T, want map[string]any (#1118)", out["meta"])
	}
	for _, field := range []string{"status", "results", "configured_count"} {
		if _, present := data[field]; !present {
			t.Fatalf("data.%s missing (#1118)", field)
		}
		if _, present := out[field]; present {
			t.Fatalf("root %s = %v, want absent — no more root/data duplication (#1118)", field, out[field])
		}
	}
}
