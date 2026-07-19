package admin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGetRuntimeStatusReportsHugoAndGitAvailability(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	origChannel := buildinfo.BuildChannel
	buildinfo.BuildChannel = "main"
	t.Cleanup(func() {
		buildinfo.BuildChannel = origChannel
	})

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0+extended linux/amd64 BuildDate=2026-07-01T00:00:00Z VendorInfo=gohugoio'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(contentRoot, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{"include_revisions": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", out["data"])
	}

	hugo, ok := data["hugo"].(map[string]any)
	if !ok {
		t.Fatalf("hugo field type = %T", data["hugo"])
	}
	if hugo["available"] != true {
		t.Fatalf("hugo.available = %v, want true", hugo["available"])
	}
	if hugo["version"] != "0.150.0" {
		t.Fatalf("hugo.version = %v, want 0.150.0", hugo["version"])
	}
	if hugo["extended"] != true {
		t.Fatalf("hugo.extended = %v, want true", hugo["extended"])
	}

	git, ok := data["git"].(map[string]any)
	if !ok {
		t.Fatalf("git field type = %T", data["git"])
	}
	if git["available"] != true {
		t.Fatalf("git.available = %v, want true", git["available"])
	}
	if git["baseline_mode"] != "auto" {
		t.Fatalf("git.baseline_mode = %v, want auto", git["baseline_mode"])
	}
	if got, ok := git["head_commit"].(string); !ok || got == "" {
		t.Fatalf("git.head_commit = %v, want non-empty", git["head_commit"])
	}
	if git["dirty"] != false {
		t.Fatalf("git.dirty = %v, want false", git["dirty"])
	}
	// Absolute host paths must never be exposed.
	if _, present := git["root"]; present {
		t.Fatal("git.root must not be exposed (would leak host filesystem layout)")
	}

	site, ok := data["site"].(map[string]any)
	if !ok {
		t.Fatalf("site field type = %T", data["site"])
	}
	if site["content_root_configured"] != true {
		t.Fatalf("site.content_root_configured = %v, want true", site["content_root_configured"])
	}
	if got, ok := site["source_revision"].(string); !ok || got == "" {
		t.Fatalf("site.source_revision = %v, want non-empty", site["source_revision"])
	}

	if degraded, present := out["data"].(map[string]any)["degraded"]; present {
		t.Fatalf("expected no degraded surfaces when hugo+git are both available, got %v", degraded)
	}
	if _, present := data["release_version"]; present {
		t.Fatalf("release_version = %v, want removed (#560) — server_version + build_channel already carry this", data["release_version"])
	}
	if got := data["build_channel"]; got != "main" {
		t.Fatalf("build_channel = %v, want main", got)
	}
}

func TestGetRuntimeStatusOmitsRevisionsByDefault(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "page.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	// No include_revisions arg at all: hashing the full content/public
	// trees on every poll would make this "compact status" tool expensive,
	// so it must be opt-in.
	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	site := data["site"].(map[string]any)
	if _, present := site["source_revision"]; present {
		t.Fatalf("source_revision must be omitted unless include_revisions is set, got %v", site["source_revision"])
	}
	if _, present := site["public_revision"]; present {
		t.Fatalf("public_revision must be omitted unless include_revisions is set, got %v", site["public_revision"])
	}
}

func TestGetRuntimeStatusReportsDegradedSurfacesWhenHugoAndGitUnavailable(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	emptyPathDir := t.TempDir() // no hugo binary here
	t.Setenv("PATH", emptyPathDir)

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content") // no .git anywhere
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	hugo := data["hugo"].(map[string]any)
	if hugo["available"] != false {
		t.Fatalf("hugo.available = %v, want false", hugo["available"])
	}
	if got, _ := hugo["error"].(string); got == "" {
		t.Fatal("expected hugo.error to explain why hugo is unavailable")
	}

	git := data["git"].(map[string]any)
	if git["available"] != false {
		t.Fatalf("git.available = %v, want false", git["available"])
	}

	degraded, ok := data["degraded"].([]any)
	if !ok || len(degraded) != 2 {
		t.Fatalf("degraded = %#v, want two explanatory entries", data["degraded"])
	}
}

func TestGetRuntimeStatusRespectsGitBaselineDisabledMode(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "--allow-empty", "-m", "initial")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	git := data["git"].(map[string]any)
	if git["available"] != false {
		t.Fatalf("git.available = %v, want false when baseline disabled", git["available"])
	}
	if git["baseline_mode"] != "disabled" {
		t.Fatalf("git.baseline_mode = %v, want disabled", git["baseline_mode"])
	}
	errText, _ := git["error"].(string)
	if errText == "" {
		t.Fatal("expected git.error explaining baseline is disabled")
	}
}

// #467: get_runtime_status surfaces the outcome of the most recent
// build_site attempt via last_build, without requiring the caller to invoke
// build_site itself to discover a broken publish pipeline.
func TestGetRuntimeStatusOmitsLastBuildBeforeAnyBuildAttempt(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	cfg := config.Default()
	cfg.ContentRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	if _, present := data["last_build"]; present {
		t.Fatalf("last_build = %v, want omitted before any build_site attempt", data["last_build"])
	}
}

func TestGetRuntimeStatusReportsLastBuildFailure(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	at := time.Now().UTC()
	buildstatus.RecordFailure("permission_denied", at)

	cfg := config.Default()
	cfg.ContentRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	lastBuild, ok := data["last_build"].(map[string]any)
	if !ok {
		t.Fatalf("last_build type = %T, want present after a recorded failure", data["last_build"])
	}
	if lastBuild["status"] != "failed" {
		t.Fatalf("last_build.status = %v, want failed", lastBuild["status"])
	}
	if lastBuild["error_class"] != "permission_denied" {
		t.Fatalf("last_build.error_class = %v, want permission_denied", lastBuild["error_class"])
	}
	degraded, _ := data["degraded"].([]any)
	found := false
	for _, d := range degraded {
		if s, _ := d.(string); s != "" && s == "build_site: last attempt failed (permission_denied) at "+lastBuild["at"].(string) {
			found = true
		}
	}
	if !found {
		t.Fatalf("degraded = %v, want an entry about the failed last build attempt", degraded)
	}
}
