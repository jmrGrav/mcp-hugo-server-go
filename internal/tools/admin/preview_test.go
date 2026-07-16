package admin_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

func TestPreviewBuildSucceeds(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\n[ \"$(pwd)\" = \""+wantRoot+"\" ] || exit 42\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = wantRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "preview_build", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("preview_build returned error: %s", resultText(res))
	}
	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, text)
	}
	if out["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", out["status"])
	}
}

func TestPreviewBuildFailureStructuredError(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\necho 'Error: TOML parse error' >&2\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "preview_build", map[string]any{})
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
	if !strings.Contains(command, "hugo --noBuildLock --cacheDir ") || !strings.Contains(command, "--renderToMemory") {
		t.Errorf("command %q does not include expected Hugo flags", command)
	}
	if wd, _ := out["working_directory"].(string); wd == "" {
		t.Error("working_directory is empty")
	}
	if cacheDir, _ := out["cache_directory"].(string); cacheDir == "" {
		t.Error("cache_directory is empty")
	}
}

func TestPreviewBuildDoesNotInheritArbitraryEnvironment(t *testing.T) {
	wantRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\n[ -z \"$SECRET_TOKEN_FOR_PREVIEW\" ] || exit 97\n[ \"$(pwd)\" = \""+wantRoot+"\" ] || exit 42\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("SECRET_TOKEN_FOR_PREVIEW", "should-not-leak")

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = wantRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "preview_build", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("preview_build leaked process env or failed unexpectedly: %s", resultText(res))
	}
}

func TestPreviewBuildFailureUsesStdoutWhenStderrEmpty(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\necho 'Error: template failed'\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "preview_build", map[string]any{})
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
	if !strings.Contains(summary, "template failed") {
		t.Errorf("stderr_summary %q does not include stdout failure text", summary)
	}
}

func TestPreviewBuildRegisteredInSiteAdmin(t *testing.T) {
	defs := admin.Defs()
	found := false
	for _, d := range defs {
		if d.Name == "preview_build" {
			found = true
			if d.RequiredScope != "site.admin" {
				t.Fatalf("preview_build scope = %q", d.RequiredScope)
			}
		}
	}
	if !found {
		t.Fatal("preview_build not present in Defs()")
	}
}
