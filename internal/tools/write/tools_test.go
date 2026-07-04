package write_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestServer(t *testing.T, contentRoot string) (*mcp.ClientSession, func()) {
	t.Helper()
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg)

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

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func TestCreatePage(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "my-post",
		"title":      "My Post",
		"body":       "Hello world.",
		"tags":       []any{"go", "hugo"},
		"categories": []any{"tutorials"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}

	path := filepath.Join(contentRoot, "my-post", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not found at %s: %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, "My Post") {
		t.Errorf("frontmatter missing title: %s", content)
	}
	if !strings.Contains(content, "Hello world.") {
		t.Errorf("body missing: %s", content)
	}
	if !strings.Contains(content, "go") {
		t.Errorf("tags missing: %s", content)
	}
	if !strings.Contains(content, "draft") {
		t.Errorf("frontmatter missing draft field: %s", content)
	}
}

func TestCreatePageSymlinkBlocked(t *testing.T) {
	contentRoot := t.TempDir()

	target := t.TempDir()
	symlinkPath := filepath.Join(contentRoot, "bad-slug")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "bad-slug",
		"title": "Bad Slug",
	})
	if !res.IsError {
		t.Fatal("expected error for symlink slug, got success")
	}
}

func TestCreatePageReservedSlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":  "_index",
		"title": "Index",
	})
	if !res.IsError {
		t.Fatal("expected error for reserved slug _index, got success")
	}
}

func TestDeletePageRateLimit(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	for i := 0; i < 5; i++ {
		res := callTool(t, session, "delete_page", map[string]any{"slug": "my-post"})
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("delete %d expected success, got error: %s", i+1, raw)
		}
	}

	res := callTool(t, session, "delete_page", map[string]any{"slug": "my-post"})
	if !res.IsError {
		t.Fatal("expected rate_limit_exceeded on 6th delete, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded error, got: %s", raw)
	}
}

func TestUpdatePageNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":  "nonexistent",
		"title": "New Title",
	})
	if !res.IsError {
		t.Fatal("expected not_found error for nonexistent page, got success")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not_found") {
		t.Errorf("expected not_found error, got: %s", raw)
	}
}

func TestCreatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestCreatePageEmptyTitle(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{"slug": "valid-slug", "title": ""})
	if !res.IsError {
		t.Fatal("expected error for empty title")
	}
}

func TestUpdatePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{"slug": "", "title": "T"})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestDeletePageEmptySlug(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "delete_page", map[string]any{"slug": ""})
	if !res.IsError {
		t.Fatal("expected error for empty slug")
	}
}

func TestUpdatePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	// create first
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "update-me",
		"title":      "Original Title",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// update title only
	res = callTool(t, session, "update_page", map[string]any{
		"slug":  "update-me",
		"title": "New Title",
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("update_page failed: %s", raw)
	}

	data, err := os.ReadFile(filepath.Join(contentRoot, "update-me", "index.md"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if !strings.Contains(string(data), "New Title") {
		t.Errorf("updated file missing new title: %s", data)
	}
}

func TestDeletePageSuccess(t *testing.T) {
	contentRoot := t.TempDir()
	session, done := newTestServer(t, contentRoot)
	defer done()

	// create page first
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "to-delete",
		"title":      "Delete Me",
		"body":       "body",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page failed: %s", raw)
	}

	// delete it
	res = callTool(t, session, "delete_page", map[string]any{"slug": "to-delete"})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("delete_page failed: %s", raw)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "to-delete", "index.md")); !os.IsNotExist(err) {
		t.Error("expected index.md to be deleted")
	}
}
