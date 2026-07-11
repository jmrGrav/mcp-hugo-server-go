package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
)

func TestRunVersionAndMissingSiteRoot(t *testing.T) {
	origArgs := os.Args
	origStdout := os.Stdout
	origVersion := server.Version
	defer func() {
		os.Args = origArgs
		os.Stdout = origStdout
		server.Version = origVersion
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	server.Version = "test-version"
	os.Args = []string{"mcp-hugo-server-go", "--version"}
	if err := run(); err != nil {
		t.Fatalf("run(--version) error = %v", err)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "test-version") {
		t.Fatalf("version stdout = %q", out)
	}
	os.Args = []string{"mcp-hugo-server-go"}
	t.Setenv("MCP_HUGO_SERVER_CONFIG", "")
	if err := run(); err == nil || !strings.Contains(err.Error(), "site_root not configured") {
		t.Fatalf("run(missing site_root) error = %v", err)
	}
}
