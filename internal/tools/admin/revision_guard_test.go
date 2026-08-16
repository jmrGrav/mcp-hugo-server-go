package admin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// TestExpectedSourceRevisionConflictRejectsBuildAndPreservesOutput is #1141's
// "mutation after capture of revision" case: a caller reads the current
// source revision, the source changes (here: a direct filesystem write,
// standing in for any change made after the caller's read — an MCP mutation
// would work identically since it also changes the on-disk bytes), then the
// caller's stale expected_source_revision must cause build_site to refuse
// and the previous public output must remain exactly as it was.
func TestExpectedSourceRevisionConflictRejectsBuildAndPreservesOutput(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "page.md"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "old.txt"), []byte("last-known-good"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'new-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = filepath.Join(root, "hugo")
	if err := os.MkdirAll(cfg.HugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.ContentRoot = contentRoot
	session, done := newTestServer(t, cfg)
	defer done()

	statusRes, err := callTool(t, session, "get_runtime_status", map[string]any{"include_revisions": true})
	if err != nil || statusRes.IsError {
		t.Fatalf("get_runtime_status failed: err=%v result=%s", err, resultText(statusRes))
	}
	var statusBody struct {
		Data struct {
			Site struct {
				SourceRevision string `json:"source_revision"`
			} `json:"site"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resultText(statusRes)), &statusBody); err != nil {
		t.Fatalf("decode get_runtime_status: %v (%s)", err, resultText(statusRes))
	}
	staleRevision := statusBody.Data.Site.SourceRevision
	if staleRevision == "" {
		t.Fatal("get_runtime_status returned an empty source_revision")
	}

	// Source changes after the caller's read — standing in for any mutation
	// (MCP tool or direct write) that lands after the revision was captured.
	if err := os.WriteFile(filepath.Join(contentRoot, "page.md"), []byte("changed after read"), 0o644); err != nil {
		t.Fatal(err)
	}

	buildRes, err := callTool(t, session, "build_site", map[string]any{"expected_source_revision": staleRevision})
	if err != nil {
		t.Fatalf("build_site transport error: %v", err)
	}
	if !buildRes.IsError {
		t.Fatalf("build_site with a stale expected_source_revision unexpectedly succeeded: %s", resultText(buildRes))
	}
	if got := resultText(buildRes); !strings.Contains(got, "source_revision_conflict") {
		t.Fatalf("build_site error = %q, want source_revision_conflict", got)
	}

	// The previous public output must be completely untouched — no build
	// ever ran against the stale expectation.
	content, readErr := os.ReadFile(filepath.Join(siteRoot, "old.txt"))
	if readErr != nil || string(content) != "last-known-good" {
		t.Fatalf("previous output changed after a rejected build: %q, err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(siteRoot, "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("a new build leaked into the public tree despite the revision conflict: %v", statErr)
	}
}

// TestExpectedPublicRevisionConflictRejectsBuildAndPreservesOutput is
// #1141's "public modified before promotion" case: the caller read the
// current public revision, someone else published in the meantime (a
// direct filesystem write here, standing in for any publish), and the
// caller's stale expected_public_revision must cause the build to refuse
// before ever touching the public tree again.
func TestExpectedPublicRevisionConflictRejectsBuildAndPreservesOutput(t *testing.T) {
	root := t.TempDir()
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "old.txt"), []byte("last-known-good"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'new-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = filepath.Join(root, "hugo")
	if err := os.MkdirAll(cfg.HugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	session, done := newTestServer(t, cfg)
	defer done()

	statusRes, err := callTool(t, session, "get_runtime_status", map[string]any{"include_revisions": true})
	if err != nil || statusRes.IsError {
		t.Fatalf("get_runtime_status failed: err=%v result=%s", err, resultText(statusRes))
	}
	var statusBody struct {
		Data struct {
			Site struct {
				PublicRevision string `json:"public_revision"`
			} `json:"site"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resultText(statusRes)), &statusBody); err != nil {
		t.Fatalf("decode get_runtime_status: %v (%s)", err, resultText(statusRes))
	}
	staleRevision := statusBody.Data.Site.PublicRevision
	if staleRevision == "" {
		t.Fatal("get_runtime_status returned an empty public_revision")
	}

	// Someone else publishes after the caller's read.
	if err := os.WriteFile(filepath.Join(siteRoot, "old.txt"), []byte("someone-else-published"), 0o644); err != nil {
		t.Fatal(err)
	}

	buildRes, err := callTool(t, session, "build_site", map[string]any{"expected_public_revision": staleRevision})
	if err != nil {
		t.Fatalf("build_site transport error: %v", err)
	}
	if !buildRes.IsError {
		t.Fatalf("build_site with a stale expected_public_revision unexpectedly succeeded: %s", resultText(buildRes))
	}
	if got := resultText(buildRes); !strings.Contains(got, "public_revision_conflict") {
		t.Fatalf("build_site error = %q, want public_revision_conflict", got)
	}

	content, readErr := os.ReadFile(filepath.Join(siteRoot, "old.txt"))
	if readErr != nil || string(content) != "someone-else-published" {
		t.Fatalf("public output changed after a rejected build (should still be the other publisher's content): %q, err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(siteRoot, "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("a new build leaked into the public tree despite the revision conflict: %v", statErr)
	}
}

// TestSourceChangedDuringBuildNeverPromotes is #1141's "mutation during
// build" case, specifically the channel ContentMu cannot see: a direct
// filesystem write to content_root while Hugo is running, standing in for
// an SSH session editing source concurrently with a build. The mock hugo
// sleeps briefly so the test goroutine has a real window to write during
// the build; runBuild's own before/after fingerprint (always on, no input
// required) must catch the mismatch and never promote the freshly built
// output.
func TestSourceChangedDuringBuildNeverPromotes(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(contentRoot, "page.md")
	if err := os.WriteFile(pagePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	siteRoot := filepath.Join(root, "public")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "old.txt"), []byte("last-known-good"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeMockHugo(t, "#!/bin/sh\nsleep 0.3\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then\n    shift\n    printf 'new-build' > \"$1/index.html\"\n  fi\n  shift\ndone\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = filepath.Join(root, "hugo")
	if err := os.MkdirAll(cfg.HugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.ContentRoot = contentRoot
	session, done := newTestServer(t, cfg)
	defer done()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		// A direct filesystem write, deliberately not going through any MCP
		// tool (and so never touching hugosite.ContentMu) — the exact
		// channel this guard exists to catch.
		_ = os.WriteFile(pagePath, []byte("changed mid-build by an external writer"), 0o644)
	}()

	buildRes, err := callTool(t, session, "build_site", map[string]any{})
	wg.Wait()
	if err != nil {
		t.Fatalf("build_site transport error: %v", err)
	}
	if !buildRes.IsError {
		t.Fatalf("build_site unexpectedly succeeded despite a mid-build external write: %s", resultText(buildRes))
	}
	if got := resultText(buildRes); !strings.Contains(got, "source_changed_during_build") {
		t.Fatalf("build_site error = %q, want source_changed_during_build", got)
	}

	content, readErr := os.ReadFile(filepath.Join(siteRoot, "old.txt"))
	if readErr != nil || string(content) != "last-known-good" {
		t.Fatalf("previous output changed after a build that should never have promoted: %q, err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(siteRoot, "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("a build with mid-build source drift leaked into the public tree: %v", statErr)
	}
}
