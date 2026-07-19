package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeMockHugo(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hugo")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	return dir
}

func TestBuildSiteSucceeds(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\n[ \"$(pwd)\" = \""+wantRoot+"\" ] || exit 42\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = wantRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, text)
	}
	if out["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", out["status"])
	}
	if _, ok := out["duration_ms"]; !ok {
		t.Fatal("response missing duration_ms")
	}
	if buildID, _ := out["build_id"].(string); !matchesBuildIDPattern(buildID) {
		t.Fatalf("response build_id = %q, want YYYYMMDD-HHMMSS-xxxx", buildID)
	}
	if outputRevision, _ := out["output_revision"].(string); !strings.HasPrefix(outputRevision, "sha256:") {
		t.Fatalf("response output_revision = %q, want sha256:*", outputRevision)
	}
	if publishReady, ok := out["publish_ready"].(bool); !ok || !publishReady {
		t.Fatalf("response publish_ready = %v, want true", out["publish_ready"])
	}
}

// TestBuildSiteHasEnvelopeMatchingRootFields is a regression test for #572:
// build_site was the last tool with zero envelope (no data/errors/meta/
// success at all). Root fields are kept as compatibility aliases, additive
// only, mirroring #552's treatment of create_preview/generate_hero_image.
func TestBuildSiteHasEnvelopeMatchingRootFields(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\n[ \"$(pwd)\" = \""+wantRoot+"\" ] || exit 42\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = wantRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	if got := out["success"]; got != true {
		t.Fatalf("success = %v, want true (#572)", got)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any (#572)", out["data"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("meta type = %T, want map[string]any (#572)", out["meta"])
	}
	if _, ok := out["errors"].([]any); !ok {
		t.Fatalf("errors type = %T, want []any (#572)", out["errors"])
	}
	for _, field := range []string{"status", "duration_ms", "build_id", "output_revision", "publish_ready"} {
		if data[field] != out[field] {
			t.Fatalf("data.%s = %v, root %s = %v — must match (#572)", field, data[field], field, out[field])
		}
	}
}

func TestBuildSitePassesCleanDestinationDirFlag(t *testing.T) {
	capturedArgsPath := filepath.Join(t.TempDir(), "captured-args.txt")
	dir := writeMockHugo(t, "#!/bin/sh\necho \"$@\" > \""+capturedArgsPath+"\"\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	raw, err := os.ReadFile(capturedArgsPath)
	if err != nil {
		t.Fatalf("reading captured hugo args: %v", err)
	}
	// Without --cleanDestinationDir, output for a page deleted since the
	// last build lingers in site_root forever (#524): the taxonomy/list
	// pages that referenced it never get regenerated without it, since
	// Hugo only writes/updates pages for content that still exists.
	if !strings.Contains(string(raw), "--cleanDestinationDir") {
		t.Fatalf("hugo invocation args = %q, want --cleanDestinationDir present", raw)
	}
}

func TestBuildSiteConcurrentReject(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nsleep 5\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg)

	ctx := context.Background()
	t1a, t2a := mcp.NewInMemoryTransports()
	t1b, t2b := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1a, nil); err != nil {
		t.Fatalf("server connect 1: %v", err)
	}
	if _, err := s.Connect(ctx, t1b, nil); err != nil {
		t.Fatalf("server connect 2: %v", err)
	}

	clientA := mcp.NewClient(&mcp.Implementation{Name: "ca", Version: "0.1"}, nil)
	sessionA, err := clientA.Connect(ctx, t2a, nil)
	if err != nil {
		t.Fatalf("client A connect: %v", err)
	}
	defer sessionA.Close()

	clientB := mcp.NewClient(&mcp.Implementation{Name: "cb", Version: "0.1"}, nil)
	sessionB, err := clientB.Connect(ctx, t2b, nil)
	if err != nil {
		t.Fatalf("client B connect: %v", err)
	}
	defer sessionB.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sessionA.CallTool(ctx, &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	}()

	time.Sleep(100 * time.Millisecond)

	res, err := sessionB.CallTool(ctx, &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected build_in_progress error, got success")
	}
	text := resultText(res)
	if !strings.Contains(text, "build_in_progress") {
		t.Fatalf("error %q does not contain 'build_in_progress'", text)
	}

	wg.Wait()
}

func TestBuildSiteTimeout(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nsleep 10\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.BuildTimeoutSeconds = 1

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected timeout error, got success")
	}
	text := resultText(res)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "timeout") && !strings.Contains(lower, "deadline") && !strings.Contains(lower, "killed") {
		t.Fatalf("error %q does not indicate timeout", text)
	}
}

func TestBuildSiteFailureStructuredError(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\necho 'Error: TOML parse error' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result, got success")
	}

	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON object in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v — got %q", err, text)
	}

	if out["error"] != "build_error" {
		t.Errorf("error field: want %q, got %v", "build_error", out["error"])
	}
	if out["exit_code"] != float64(1) {
		t.Errorf("exit_code: want 1, got %v", out["exit_code"])
	}
	summary, _ := out["stderr_summary"].(string)
	if !strings.Contains(summary, "TOML parse error") {
		t.Errorf("stderr_summary %q does not contain 'TOML parse error'", summary)
	}
	buildID, _ := out["build_id"].(string)
	if !matchesBuildIDPattern(buildID) {
		t.Errorf("build_id %q does not match pattern YYYYMMDD-HHMMSS-xxxx", buildID)
	}
	if _, ok := out["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing or not a number: %v", out["duration_ms"])
	}
	command, _ := out["command"].(string)
	if !strings.Contains(command, "hugo --noBuildLock --cacheDir ") {
		t.Errorf("command %q does not include expected Hugo flags", command)
	}
	if wd, _ := out["working_directory"].(string); wd == "" {
		t.Error("working_directory is empty")
	}
	if cacheDir, _ := out["cache_directory"].(string); cacheDir == "" {
		t.Error("cache_directory is empty")
	}
}

func TestBuildSiteDoesNotInheritArbitraryEnvironment(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\n[ -z \"$SECRET_TOKEN_FOR_BUILD\" ] || exit 97\n[ \"$(pwd)\" = \""+wantRoot+"\" ] || exit 42\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("SECRET_TOKEN_FOR_BUILD", "should-not-leak")

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = wantRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("build_site leaked process env or failed unexpectedly: %s", resultText(res))
	}
}

func TestBuildSiteFailureUsesStdoutWhenStderrEmpty(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\necho 'Error: module not found'\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result, got success")
	}

	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON object in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v — got %q", err, text)
	}

	summary, _ := out["stderr_summary"].(string)
	if !strings.Contains(summary, "module not found") {
		t.Errorf("stderr_summary %q does not include stdout failure text", summary)
	}
}

func TestBuildSiteStderrSanitised(t *testing.T) {
	secretRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\necho '"+secretRoot+": Error occurred' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = secretRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result, got success")
	}

	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON object in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v", err)
	}

	summary, _ := out["stderr_summary"].(string)
	if strings.Contains(summary, secretRoot) {
		t.Errorf("stderr_summary leaks secretRoot %q: %q", secretRoot, summary)
	}
	if !strings.Contains(summary, "<site_root>") {
		t.Errorf("stderr_summary %q does not contain '<site_root>'", summary)
	}
}

func TestBuildSiteStderrTruncated(t *testing.T) {
	// Write 600 'x' bytes to stderr.
	dir := writeMockHugo(t, "#!/bin/sh\nprintf '%0.sx' $(seq 1 600) >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result, got success")
	}

	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON object in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v", err)
	}

	summary, _ := out["stderr_summary"].(string)
	if len(summary) > 500 {
		t.Errorf("stderr_summary length %d exceeds 500", len(summary))
	}
}

func TestBuildSitePreflightFailsWhenNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test as root")
	}
	siteRoot := filepath.Join(t.TempDir(), "readonly")
	if err := os.MkdirAll(siteRoot, 0o555); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer func() { _ = os.Chmod(siteRoot, 0o755) }()

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected preflight error, got success")
	}
	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON object in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v — got %q", err, text)
	}
	if out["error"] != "build_precondition_failed" {
		t.Errorf("error: want %q, got %v", "build_precondition_failed", out["error"])
	}
	if out["error_class"] != "permission_denied" {
		t.Errorf("error_class: want %q, got %v", "permission_denied", out["error_class"])
	}
	if out["path"] == "" {
		t.Error("path field is empty")
	}
	for _, want := range []string{"suggestion", "docs_url", "operator_hint"} {
		if v, ok := out[want]; !ok || v == "" {
			t.Errorf("field %q is missing or empty", want)
		}
	}
}

func TestBuildSitePermissionDeniedErrorIncludesSuggestion(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\necho 'permission denied' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error, got success")
	}
	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v", err)
	}
	if out["error_class"] != "permission_denied" {
		t.Errorf("error_class: want permission_denied, got %v", out["error_class"])
	}
	if v, _ := out["suggestion"].(string); v == "" {
		t.Error("suggestion field is missing or empty for permission_denied error")
	}
	if v, _ := out["docs_url"].(string); v == "" {
		t.Error("docs_url field is missing or empty for permission_denied error")
	}
}

func TestBuildSiteOwnershipDriftErrorUsesOwnershipSuggestion(t *testing.T) {
	siteRoot := t.TempDir()
	stderr := fmt.Sprintf("Error: error copying static files: chtimes %s: operation not permitted", filepath.Join(siteRoot, "public", "auth.md"))
	dir := writeMockHugo(t, "#!/bin/sh\necho '"+stderr+"' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error, got success")
	}
	text := resultText(res)
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in error text: %q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &out); err != nil {
		t.Fatalf("error text not valid JSON: %v", err)
	}
	if out["error_class"] != "permission_denied" {
		t.Fatalf("error_class: want permission_denied, got %v", out["error_class"])
	}
	suggestion, _ := out["suggestion"].(string)
	if !strings.Contains(strings.ToLower(suggestion), "owner") && !strings.Contains(strings.ToLower(suggestion), "ownership") {
		t.Fatalf("suggestion %q does not mention ownership drift", suggestion)
	}
	if strings.Contains(suggestion, "ReadWritePaths") {
		t.Fatalf("suggestion %q incorrectly points only to ReadWritePaths", suggestion)
	}
}

// TestBuildSiteCallbackTimeout verifies that a slow post-build callback does
// not block build_site indefinitely, the response is partial_success with a
// warning naming the first timed-out callback, and subsequent callbacks are
// not started (preventing goroutine leaks and misleading warning messages) (#241).
func TestBuildSiteCallbackTimeout(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	var secondCalled bool
	// First callback: blocks indefinitely.
	slowCallback := func() error {
		time.Sleep(10 * time.Minute)
		return nil
	}
	// Second callback: must NOT be called after the first times out.
	sentinelCallback := func() error {
		secondCalled = true
		return nil
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, slowCallback, sentinelCallback)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	// The call must return in under 35s (callback timeout is 30s).
	doneCh := make(chan struct{})
	var res *mcp.CallToolResult
	var callErr error
	go func() {
		res, callErr = session.CallTool(ctx, &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(35 * time.Second):
		t.Fatal("build_site blocked past callback timeout — #241 regression")
	}

	if callErr != nil {
		t.Fatalf("unexpected transport error: %v", callErr)
	}
	if res.IsError {
		t.Fatalf("build_site must not be an error when only callbacks time out: %s", resultText(res))
	}
	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — %q", err, text)
	}
	if out["status"] != "partial_success" {
		t.Errorf("status: want partial_success, got %v", out["status"])
	}
	if publishReady, ok := out["publish_ready"].(bool); !ok || publishReady {
		t.Fatalf("response publish_ready = %v, want false on partial_success", out["publish_ready"])
	}
	if buildID, _ := out["build_id"].(string); !matchesBuildIDPattern(buildID) {
		t.Fatalf("response build_id = %q, want YYYYMMDD-HHMMSS-xxxx", buildID)
	}
	if outputRevision, _ := out["output_revision"].(string); !strings.HasPrefix(outputRevision, "sha256:") {
		t.Fatalf("response output_revision = %q, want sha256:*", outputRevision)
	}
	warning, _ := out["warning"].(string)
	if warning == "" {
		t.Error("expected non-empty warning when callback times out")
	}
	// Warning must identify callback 0, not a later index (which would indicate
	// the loop continued past the timeout and overwrote the first warning).
	if !strings.Contains(warning, "callback 0") {
		t.Errorf("warning %q must identify callback 0 (first to time out)", warning)
	}
	if secondCalled {
		t.Error("sentinel callback must not be invoked after the deadline fires — loop must break on cbCtx.Done()")
	}
}

// TestBuildSiteCallbackFailurePartialSuccess verifies that a failing callback
// produces partial_success with a warning rather than a hard error (#238/#244).
func TestBuildSiteCallbackFailurePartialSuccess(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	errCallback := func() error { return fmt.Errorf("index reload: connection refused") }

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, errCallback)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	res, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	if callErr != nil {
		t.Fatalf("unexpected transport error: %v", callErr)
	}
	if res.IsError {
		t.Fatalf("build_site must not be a hard error when only a callback fails: %s", resultText(res))
	}
	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — %q", err, text)
	}
	if out["status"] != "partial_success" {
		t.Errorf("status: want partial_success, got %v", out["status"])
	}
	if publishReady, ok := out["publish_ready"].(bool); !ok || publishReady {
		t.Fatalf("response publish_ready = %v, want false on partial_success", out["publish_ready"])
	}
	if warning, _ := out["warning"].(string); !strings.Contains(warning, "connection refused") {
		t.Errorf("warning %q should contain callback error detail", warning)
	}
}

// TestBuildSiteProcessGroupKilled verifies that on timeout, child processes
// spawned by a shell-wrapper "hugo" are also killed (#240).
func TestBuildSiteProcessGroupKilled(t *testing.T) {
	// The mock hugo script spawns a long-running child and then sleeps itself.
	// Without process-group kill, the child would survive cancellation.
	dir := writeMockHugo(t, "#!/bin/sh\nsleep 30 &\nsleep 30\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.BuildTimeoutSeconds = 1

	session, done := newTestServer(t, cfg)
	defer done()

	start := time.Now()
	res, err := callTool(t, session, "build_site", map[string]any{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected timeout error, got success")
	}
	// Without pgid kill, elapsed would be ~30s (child lives on and keeps stdout
	// open, blocking cmd.Wait). With pgid kill it should be close to 1s timeout.
	if elapsed > 5*time.Second {
		t.Errorf("build_site took %v — child process not killed with process group (#240 regression)", elapsed)
	}
}

// matchesBuildIDPattern returns true if s matches YYYYMMDD-HHMMSS-xxxx.
func matchesBuildIDPattern(s string) bool {
	if len(s) != 20 {
		return false
	}
	// YYYYMMDD-HHMMSS-xxxx
	for i, ch := range s {
		switch i {
		case 8, 15:
			if ch != '-' {
				return false
			}
		case 16, 17, 18, 19:
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
				return false
			}
		default:
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}
