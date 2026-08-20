package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const profileFixture = `mode: set
github.com/jmrGrav/mcp-hugo-server-go/internal/foo/foo.go:10.2,12.3 2 1
github.com/jmrGrav/mcp-hugo-server-go/internal/foo/foo.go:15.2,15.10 1 0
`

func TestRunPassesWhenDiffCoverageMeetsMinimum(t *testing.T) {
	dir := t.TempDir()
	profile := writeTemp(t, dir, "coverage.out", profileFixture)
	diff := writeTemp(t, dir, "diff.txt", "+++ b/internal/foo/foo.go\n@@ -9,0 +10,3 @@\n+a\n+b\n+c\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", profile, "-diff", diff}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("100.0%")) {
		t.Fatalf("stdout = %q, want it to report 100.0%%", stdout.String())
	}
}

func TestRunFailsBelowMinimumAndListsUncovered(t *testing.T) {
	dir := t.TempDir()
	profile := writeTemp(t, dir, "coverage.out", profileFixture)
	diff := writeTemp(t, dir, "diff.txt", "+++ b/internal/foo/foo.go\n@@ -14,0 +15,1 @@\n+x\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", profile, "-diff", diff}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("foo.go:15-15")) {
		t.Fatalf("stdout = %q, want the uncovered block listed", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("below the required")) {
		t.Fatalf("stderr = %q, want a below-threshold message", stderr.String())
	}
}

func TestRunSkipsWhenNothingCoverableChanged(t *testing.T) {
	dir := t.TempDir()
	profile := writeTemp(t, dir, "coverage.out", profileFixture)
	diff := writeTemp(t, dir, "diff.txt", "+++ b/README.md\n@@ -1,0 +1,1 @@\n+hello\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", profile, "-diff", diff}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("skipping")) {
		t.Fatalf("stdout = %q, want a skip message", stdout.String())
	}
}

func TestRunRequiresDiffFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", "coverage.out"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2 (usage error)", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("-diff is required")) {
		t.Fatalf("stderr = %q, want a -diff required message", stderr.String())
	}
}

func TestRunRejectsMissingProfile(t *testing.T) {
	dir := t.TempDir()
	diff := writeTemp(t, dir, "diff.txt", "+++ b/x.go\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", filepath.Join(dir, "missing.out"), "-diff", diff}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}

func TestRunRejectsMissingDiffFile(t *testing.T) {
	dir := t.TempDir()
	profile := writeTemp(t, dir, "coverage.out", profileFixture)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", profile, "-diff", filepath.Join(dir, "missing.txt")}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}

func TestRunRejectsUnparsableProfile(t *testing.T) {
	dir := t.TempDir()
	profile := writeTemp(t, dir, "coverage.out", "mode: set\nnot a valid line\n")
	diff := writeTemp(t, dir, "diff.txt", "+++ b/x.go\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", profile, "-diff", diff}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-not-a-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}
