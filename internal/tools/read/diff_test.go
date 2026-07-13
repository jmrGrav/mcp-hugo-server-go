package read_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newDiffPageClient(t *testing.T, contentRoot string) (*mcp.ClientSession, func()) {
	t.Helper()
	idx := mustTestIndex(t)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	read.Register(s, idx, cfg, srcIdx)
	read.RegisterWithSourceIndex(s, idx, srcIdx, cfg)

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
	return session, func() { _ = session.Close() }
}

func TestDiffPage(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Hello\ndate: 2026-07-03\n---\nHello world.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.test")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Hello\ndate: 2026-07-03\n---\nHello brave new world.\n"), 0o644); err != nil {
		t.Fatalf("rewrite page: %v", err)
	}

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "diff_page", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("diff_page returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("diff_page data type = %T", m["data"])
	}
	if got := data["status"]; got != "modified" {
		t.Fatalf("diff_page status = %v, want modified", got)
	}
	if got := data["diff_available"]; got != true {
		t.Fatalf("diff_page diff_available = %v, want true", got)
	}
	if got := data["path"]; got != "posts/hello/index.md" {
		t.Fatalf("diff_page path = %v, want posts/hello/index.md", got)
	}
	if got := data["resolved_source_path"]; got != pagePath {
		t.Fatalf("diff_page resolved_source_path = %v, want %s", got, pagePath)
	}
	if got := data["resolved_lang"]; got != "" {
		t.Fatalf("diff_page resolved_lang = %v, want empty default lang", got)
	}
	diffText, _ := data["diff"].(string)
	if !strings.Contains(diffText, "Hello brave new world.") {
		t.Fatalf("diff_page diff missing updated text: %s", diffText)
	}
	if strings.TrimSpace(asString(t, data["base_commit"])) == "" {
		t.Fatal("diff_page missing base_commit")
	}
	if strings.TrimSpace(asString(t, data["head_commit"])) == "" {
		t.Fatal("diff_page missing head_commit")
	}
	assertReadPageState(t, data["state"], "present", "built", "available", "fresh")
}

func TestDiffPageResolvesMultilingualBundleFromSourceIndex(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "bonjour", "index.fr.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Bonjour\ndate: 2026-07-03\n---\nBonjour monde.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.test")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Bonjour\ndate: 2026-07-03\n---\nBonjour tout le monde.\n"), 0o644); err != nil {
		t.Fatalf("rewrite page: %v", err)
	}

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "diff_page", map[string]any{"slug": "/posts/bonjour/"})
	if res.IsError {
		t.Fatalf("diff_page multilingual returned error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	if got := data["path"]; got != "posts/bonjour/index.fr.md" {
		t.Fatalf("diff_page multilingual path = %v, want posts/bonjour/index.fr.md", got)
	}
	if got := data["resolved_source_path"]; got != pagePath {
		t.Fatalf("diff_page multilingual resolved_source_path = %v, want %s", got, pagePath)
	}
	if got := data["resolved_lang"]; got != "fr" {
		t.Fatalf("diff_page multilingual resolved_lang = %v, want fr", got)
	}
	assertReadPageState(t, data["state"], "present", "built", "available", "fresh")
}

func TestDiffPageWithoutGitReturnsSourceContent(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "nogit", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: No Git\ndate: 2026-07-03\n---\nNo git source body.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "diff_page", map[string]any{"slug": "/posts/nogit/"})
	if res.IsError {
		t.Fatalf("diff_page without git returned MCP error: %v", res.Content)
	}
	m := decodeContent(t, res)
	data := m["data"].(map[string]any)
	if got := data["status"]; got != "git_not_available" {
		t.Fatalf("diff_page status = %v, want git_not_available", got)
	}
	if got := data["diff_available"]; got != false {
		t.Fatalf("diff_page diff_available = %v, want false", got)
	}
	if got := data["fallback_mode"]; got != "source_content" {
		t.Fatalf("diff_page fallback_mode = %v, want source_content", got)
	}
	if got := data["source_content"]; got != "No git source body." {
		t.Fatalf("source_content = %q, want source body", got)
	}
	if got := data["resolved_source_path"]; got != pagePath {
		t.Fatalf("diff_page no-git resolved_source_path = %v, want %s", got, pagePath)
	}
	assertReadPageState(t, data["state"], "present", "pending", "not_yet_available", "source_only")
	warnings := m["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("expected warning explaining git is unavailable")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("value %T is not string", v)
	}
	return s
}
