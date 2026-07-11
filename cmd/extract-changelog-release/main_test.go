package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tmp := t.TempDir()
	changelog := filepath.Join(tmp, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("## [v1.3.4] - 2026-07-11\n\n- One\n- Two\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version", "v1.3.4", "-changelog", changelog}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(success) code = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "- One") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-changelog", changelog}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "missing required -version") {
		t.Fatalf("run(missing version) code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-version", "v9.9.9", "-changelog", changelog}, &stdout, &stderr); code != 1 {
		t.Fatalf("run(missing release) code=%d stderr=%q", code, stderr.String())
	}
}
