package admin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
}

func TestBuildSiteConcurrentReject(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nsleep 5\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterBuild(s, cfg)

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

	time.Sleep(100 * time.Millisecond)

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
