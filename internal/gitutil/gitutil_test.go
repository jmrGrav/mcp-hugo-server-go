package gitutil_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
)

func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "config", "user.email", "test@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "config", "user.name", "Test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v: %s", err, out)
	}
}

func TestDiscoverRootFindsRepoFromNestedSubdir(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	nested := filepath.Join(root, "content", "posts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := gitutil.DiscoverRoot(nested)
	if err != nil {
		t.Fatalf("DiscoverRoot() error = %v", err)
	}
	wantAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		wantAbs = root
	}
	if got != wantAbs {
		t.Fatalf("DiscoverRoot() = %q, want %q", got, wantAbs)
	}
}

func TestDiscoverRootNoRepoFound(t *testing.T) {
	dir := t.TempDir()
	_, err := gitutil.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot() want error for a directory with no .git ancestor")
	}
	// Must never leak the absolute path being searched — this error can
	// reach a tool response (diff_page's git_unavailable fallback).
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("DiscoverRoot() error leaks the searched path: %v", err)
	}
}

func TestCommandInjectsSafeDirectoryForDiscoveredRoot(t *testing.T) {
	// Regression test for the dubious-ownership fix: every git invocation
	// this package builds must pin -c safe.directory=<repoRoot> so a
	// content checkout owned by a different Unix user than the service
	// process doesn't fail with "detected dubious ownership".
	cmd := gitutil.Command(context.Background(), "/some/repo/root", "status")
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "-c safe.directory=/some/repo/root") {
		t.Fatalf("Command() args = %q, want -c safe.directory=/some/repo/root", joined)
	}
	if !strings.Contains(joined, "-C /some/repo/root") {
		t.Fatalf("Command() args = %q, want -C /some/repo/root", joined)
	}
}

func TestCommandNeverUsesWildcardSafeDirectory(t *testing.T) {
	// safe.directory=* trusts every repository on the host, not just the
	// one this call needs — must never be produced regardless of input.
	cmd := gitutil.Command(context.Background(), "*", "status")
	for _, arg := range cmd.Args {
		if arg == "safe.directory=*" {
			t.Fatal("Command() must never emit a literal safe.directory=* even if repoRoot itself is \"*\"")
		}
	}
}

func TestOutputAndBytesWorkAgainstRealRepo(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "-C", root, "add", "a.txt")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	commit := exec.Command("git", "-C", root, "commit", "-q", "-m", "initial")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	ctx := context.Background()
	out, err := gitutil.Output(ctx, root, "rev-parse", "--short", "HEAD")
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if out == "" {
		t.Fatal("Output() returned empty HEAD commit")
	}

	raw, err := gitutil.Bytes(ctx, root, "show", "HEAD:a.txt")
	if err != nil {
		t.Fatalf("Bytes() error = %v", err)
	}
	if string(raw) != "hello\n" {
		t.Fatalf("Bytes() = %q, want %q", raw, "hello\n")
	}
}

func TestOutputReturnsCombinedOutputOnError(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	_, err := gitutil.Output(context.Background(), root, "show", "HEAD:does-not-exist.txt")
	if err == nil {
		t.Fatal("Output() want error for a nonexistent path with no commits yet")
	}
}
