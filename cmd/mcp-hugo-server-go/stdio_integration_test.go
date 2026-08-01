package main

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// copyFixtureTree copies src (a testdata fixture directory) into a fresh
// t.TempDir() and returns the copy's path. The real create_page/build_site
// tools this test exercises WRITE to site_root/content_root — running
// directly against the checked-out testdata/fixtures tree would mutate
// shared, git-tracked fixtures and break every other test that counts pages
// in them (confirmed: an earlier version of this test did exactly that).
func copyFixtureTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("copyFixtureTree(%q): %v", src, err)
	}
	return dst
}

// buildStdioTestBinary compiles the real cmd/mcp-hugo-server-go binary to a
// temp path once per test run. This exercises the actual built artifact —
// the same one MCPB would ship — rather than calling internal functions
// in-process, so it proves the real stdin/stdout JSON-RPC framing works,
// not just that the right *mcp.Server gets constructed.
func buildStdioTestBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binName := "mcp-hugo-server-go-stdio-test"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(dir, binName)
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build stdio test binary: %v\n%s", err, out)
	}
	return binPath
}

// TestStdioTransportRealRoundTrip pipes a real initialize -> tools/list ->
// tools/call sequence over stdin/stdout to the actual compiled binary
// running with transport: stdio, and asserts a write tool (create_page) is
// both listed and actually executes. This is the verifiable core of #782
// Phase 2: the .mcpb bundle itself can't be tested from Linux (Claude
// Desktop doesn't run here), but the stdio transport underneath it is fully
// testable, and this is that test.
func TestStdioTransportRealRoundTrip(t *testing.T) {
	binPath := buildStdioTestBinary(t)

	siteRoot := copyFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	hugoRoot := t.TempDir()
	contentRoot := copyFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "content"))

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := "site_root: " + absPath(t, siteRoot) + "\n" +
		"hugo_root: " + absPath(t, hugoRoot) + "\n" +
		"content_root: " + absPath(t, contentRoot) + "\n" +
		"site_url: https://example.test\n" +
		"site_name: stdio-test\n" +
		"language_default: en\n" +
		"transport: stdio\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "MCP_HUGO_SERVER_CONFIG="+configPath)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()

	toolsResp, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var sawReadTool, sawWriteTool bool
	for _, tool := range toolsResp.Tools {
		if tool.Name == "get_site_health" {
			sawReadTool = true
		}
		if tool.Name == "create_page" {
			sawWriteTool = true
		}
	}
	if !sawReadTool {
		t.Fatal("stdio tools/list did not include a read tool (get_site_health) — fixture/registration problem")
	}
	if !sawWriteTool {
		t.Fatal("stdio tools/list did not include create_page — stdio transport must grant write access unconditionally (#782 Phase 2 design decision)")
	}

	callResp, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_page",
		Arguments: map[string]any{
			"slug":         "posts/stdio-roundtrip-test",
			"title":        "Stdio Roundtrip Test",
			"body":         "Created via the real stdio transport integration test.",
			"tags":         []string{},
			"categories":   []string{},
			"test_content": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(create_page) error = %v", err)
	}
	if callResp.IsError {
		t.Fatalf("create_page over stdio returned an error result: %+v", callResp.Content)
	}
}

func absPath(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}
	return abs
}
