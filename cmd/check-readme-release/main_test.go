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
	readme := filepath.Join(tmp, "README.md")
	content := "[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest)\n"
	if err := os.WriteFile(readme, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-readme", readme}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(success) code = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dynamic") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	if err := os.WriteFile(readme, []byte("Release v1.3.4 is here"), 0o644); err != nil {
		t.Fatalf("WriteFile(update) error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-readme", readme}, &stdout, &stderr); code != 1 {
		t.Fatalf("run(static metadata) code = %d stderr=%q", code, stderr.String())
	}
}
