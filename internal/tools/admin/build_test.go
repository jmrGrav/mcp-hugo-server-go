package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for file %s", path)
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

func TestBuildSiteCompletionCallbackReceivesDiskFingerprints(t *testing.T) {
	hugoRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'built' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	contentRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(contentRoot, "page.md"), []byte("---\ntitle: manifest\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()

	var got admin.BuildCompletion
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, nil, admin.PostBuildCallback{
		Name: "publication_manifest",
		OnBuildComplete: func(completion admin.BuildCompletion) error {
			got = completion
			return nil
		},
	})
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	done := func() { _ = session.Close() }
	defer done()
	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site error = %v, result = %s", err, resultText(res))
	}
	if !matchesBuildIDPattern(got.BuildID) {
		t.Fatalf("completion build_id = %q, want generated ID", got.BuildID)
	}
	if !strings.HasPrefix(got.SourceRevision, "sha256:") || !strings.HasPrefix(got.OutputRevision, "sha256:") {
		t.Fatalf("completion revisions = source %q output %q, want sha256 fingerprints", got.SourceRevision, got.OutputRevision)
	}
	if got.Status != "ok" || got.ObservedAt.IsZero() {
		t.Fatalf("completion = %+v, want successful observed build", got)
	}
}

func TestBuildSiteRecordsRecoveryLifecycleAroundOutputSwap(t *testing.T) {
	hugoRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'built' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()

	var events []string
	var buildID string
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, nil, admin.PostBuildCallback{
		Name: "recovery_journal",
		OnBuildStart: func(progress admin.BuildProgress) error {
			buildID = progress.BuildID
			events = append(events, "in_progress")
			return nil
		},
		OnOutputSwapped: func(progress admin.BuildProgress) error {
			if progress.BuildID != buildID {
				t.Fatalf("output-swap build ID = %q, want %q", progress.BuildID, buildID)
			}
			if _, err := os.Stat(filepath.Join(cfg.SiteRoot, "index.html")); err != nil {
				t.Fatalf("output was not installed before lifecycle callback: %v", err)
			}
			events = append(events, "file_written")
			return nil
		},
		OnBuildComplete: func(completion admin.BuildCompletion) error {
			if completion.BuildID != buildID {
				t.Fatalf("completion build ID = %q, want %q", completion.BuildID, buildID)
			}
			events = append(events, "committed")
			return nil
		},
	})
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()
	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site error = %v, result = %s", err, resultText(res))
	}
	if got, want := strings.Join(events, ","), "in_progress,file_written,committed"; got != want {
		t.Fatalf("recovery lifecycle = %q, want %q", got, want)
	}
}

func TestBuildSiteRecoveryCommitsInstalledTreeAfterCallbackFailure(t *testing.T) {
	hugoRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'complete' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(cfg.SiteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.SiteRoot, "index.html"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var states []string
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, nil,
		admin.PostBuildCallback{
			Name:            "recovery_journal",
			OnBuildStart:    func(admin.BuildProgress) error { states = append(states, "in_progress"); return nil },
			OnOutputSwapped: func(admin.BuildProgress) error { states = append(states, "file_written"); return nil },
			OnBuildComplete: func(c admin.BuildCompletion) error {
				states = append(states, "committed:"+c.Status)
				return nil
			},
		},
		admin.PostBuildCallback{Name: "index_reload", Fn: func() error { return errors.New("injected callback failure") }},
	)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site = %v, %s", err, resultText(res))
	}
	if got := strings.Join(states, ","); got != "in_progress,file_written,committed:partial_success" {
		t.Fatalf("recovery states = %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.SiteRoot, "index.html"))
	if err != nil || string(raw) != "complete" {
		t.Fatalf("public output = %q, %v, want complete installed tree", raw, err)
	}
}

// TestBuildSiteMarksPartialSuccessWhenOnBuildCompleteCallbackFails is a
// regression test for the callback-outcome bookkeeping at the
// OnBuildComplete call site: a callback that itself returns an error
// (as opposed to failing earlier setup/swap stages) must still be recorded
// as "failed" and downgrade the overall build status to partial_success.
func TestBuildSiteMarksPartialSuccessWhenOnBuildCompleteCallbackFails(t *testing.T) {
	hugoRoot := t.TempDir()
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'complete' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, nil,
		admin.PostBuildCallback{
			Name:            "flaky_completion",
			OnBuildComplete: func(admin.BuildCompletion) error { return errors.New("injected completion failure") },
		},
	)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site = %v, %s", err, resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "partial_success") {
		t.Fatalf("build_site result = %s, want status partial_success", text)
	}
	if !strings.Contains(text, "injected completion failure") {
		t.Fatalf("build_site result = %s, want callback failure surfaced in warnings", text)
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

func TestBuildSiteSwapsOnlyAfterSuccessfulTemporaryBuild(t *testing.T) {
	root := t.TempDir()
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "old.txt"), []byte("last-known-good"), 0o644); err != nil {
		t.Fatal(err)
	}
	hugoRoot := filepath.Join(root, "hugo")
	if err := os.MkdirAll(hugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'new-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = hugoRoot
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site failed: err=%v result=%s", err, resultText(res))
	}
	if _, err := os.Stat(filepath.Join(siteRoot, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old output still present after successful swap: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(siteRoot, "index.html"))
	if err != nil || string(content) != "new-build" {
		t.Fatalf("new output = %q, err=%v", content, err)
	}
}

// TestBuildSiteSwappedOutputIsWorldReadable guards a production incident:
// os.MkdirTemp creates directories at mode 0700, and that mode survives
// unchanged through swapBuildOutput's rename into SiteRoot. Left at 0700,
// the site's own reverse proxy (a different uid/gid than the MCP service)
// gets a blanket 403 on every path after every single build, until an
// operator notices and chmods SiteRoot by hand.
func TestBuildSiteSwappedOutputIsWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	root := t.TempDir()
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	hugoRoot := filepath.Join(root, "hugo")
	if err := os.MkdirAll(hugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'new-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = hugoRoot
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site failed: err=%v result=%s", err, resultText(res))
	}
	fi, statErr := os.Stat(siteRoot)
	if statErr != nil {
		t.Fatalf("Stat(siteRoot) error = %v", statErr)
	}
	if got := fi.Mode().Perm(); got&0o005 == 0 {
		t.Fatalf("SiteRoot mode = %v, want world-readable (e.g. 0755) so the reverse proxy can serve it", got)
	}
}

// TestBuildSiteSelfHealsUnreadableNestedOutput guards the general case
// TestBuildSiteSwappedOutputIsWorldReadable doesn't reach: a file or
// subdirectory somewhere *inside* the tree ending up unreadable (e.g. a
// theme/build step that sets its own restrictive mode on one file), not
// just the top-level directory MkdirTemp controls. fixUnreadableOutputPaths
// must silently correct this — every path here was just written by this
// same build, so there's nothing for an operator to be told about, and no
// warning should appear once it's fixed.
func TestBuildSiteSelfHealsUnreadableNestedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	root := t.TempDir()
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	hugoRoot := filepath.Join(root, "hugo")
	if err := os.MkdirAll(hugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    mkdir -p \"$1/posts/secret\"\n    printf 'restricted' > \"$1/posts/secret/index.html\"\n    chmod 600 \"$1/posts/secret/index.html\"\n    chmod 700 \"$1/posts/secret\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = hugoRoot
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("build_site failed: err=%v result=%s", err, resultText(res))
	}
	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — %q", err, text)
	}
	if warnings, _ := out["warnings"].([]any); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none — the nested permission problem should have been self-healed, not reported", warnings)
	}
	if warning, _ := out["warning"].(string); warning != "" {
		t.Fatalf("warning = %q, want empty — the nested permission problem should have been self-healed, not reported", warning)
	}

	nestedFile := filepath.Join(siteRoot, "posts", "secret", "index.html")
	fi, statErr := os.Stat(nestedFile)
	if statErr != nil {
		t.Fatalf("Stat(nested file) error = %v", statErr)
	}
	if got := fi.Mode().Perm(); got&0o004 == 0 {
		t.Fatalf("nested file mode = %v, want world-readable after self-heal", got)
	}
	dirFi, statErr := os.Stat(filepath.Join(siteRoot, "posts", "secret"))
	if statErr != nil {
		t.Fatalf("Stat(nested dir) error = %v", statErr)
	}
	if got := dirFi.Mode().Perm(); got&0o005 == 0 {
		t.Fatalf("nested dir mode = %v, want world-traversable after self-heal", got)
	}
}

func TestBuildSiteFailurePreservesPreviousOutput(t *testing.T) {
	root := t.TempDir()
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(siteRoot, "old.txt")
	if err := os.WriteFile(oldPath, []byte("last-known-good"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'partial-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 1\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = t.TempDir()
	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "build_site", map[string]any{})
	if err != nil {
		t.Fatalf("build_site transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("build_site unexpectedly succeeded")
	}
	content, readErr := os.ReadFile(oldPath)
	if readErr != nil || string(content) != "last-known-good" {
		t.Fatalf("previous output changed after failed build: %q, err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(siteRoot, "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("partial output leaked into public tree: %v", statErr)
	}
}

func TestBuildSiteConcurrentReject(t *testing.T) {
	startedFile := filepath.Join(t.TempDir(), "hugo-started")
	dir := writeMockHugo(t, "#!/bin/sh\ntouch \""+startedFile+"\"\nsleep 5\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg, nil)

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

	waitForFile(t, startedFile, 2*time.Second)

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

// TestBuildSiteFailureHasEnvelopeShape is a regression test for #572's
// error path: build_site's Content.Text keeps its pre-existing
// JSON-blob-in-message convention (build_error's payload is a marshaled
// buildErrorPayload, not promoted to structured error fields — a separate
// follow-up), but StructuredContent must now carry the standard
// success:false + non-empty errors[] envelope like every other tool.
func TestBuildSiteFailureHasEnvelopeShape(t *testing.T) {
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

	out := decodeStructuredResult(t, res)
	if got := out["success"]; got != false {
		t.Fatalf("success = %v, want false (#572)", got)
	}
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("errors = %v, want non-empty []any (#572)", out["errors"])
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
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("Chmod parent: %v", err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	siteRoot := filepath.Join(parent, "readonly")

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
	admin.RegisterBuild(s, cfg, nil,
		admin.PostBuildCallback{Name: "slow", Fn: slowCallback},
		admin.PostBuildCallback{Name: "sentinel", Fn: sentinelCallback},
	)

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
	// Warning must identify the "slow" callback by name (#644), not the
	// "sentinel" one (which would indicate the loop continued past the
	// timeout and overwrote the first warning).
	if !strings.Contains(warning, `"slow"`) {
		t.Errorf("warning %q must identify the \"slow\" callback by name (first to time out, #644)", warning)
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
	admin.RegisterBuild(s, cfg, nil, admin.PostBuildCallback{Name: "index_reload", Fn: errCallback})

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
