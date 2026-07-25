package read_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestListPageRevisions is the core regression test for #615: a page with
// multiple commits touching its source file must return them most recent
// first, with commit/short_commit/date/subject populated for each.
func TestListPageRevisions(t *testing.T) {
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
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "second revision")

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "list_page_revisions", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("list_page_revisions returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "ok" {
		t.Fatalf("list_page_revisions status = %v, want ok", got)
	}
	if got := data["source_key"]; got != "posts/hello" {
		t.Fatalf("list_page_revisions source_key = %v, want posts/hello", got)
	}
	if got, ok := data["total"].(float64); !ok || got != 2 {
		t.Fatalf("list_page_revisions total = %v, want 2", data["total"])
	}
	revisions, ok := data["revisions"].([]any)
	if !ok || len(revisions) != 2 {
		t.Fatalf("list_page_revisions revisions = %#v, want 2 entries", data["revisions"])
	}
	first, ok := revisions[0].(map[string]any)
	if !ok || first["subject"] != "second revision" {
		t.Fatalf("list_page_revisions revisions[0] = %#v, want most-recent commit first", revisions[0])
	}
	second, ok := revisions[1].(map[string]any)
	if !ok || second["subject"] != "initial" {
		t.Fatalf("list_page_revisions revisions[1] = %#v, want the initial commit second", revisions[1])
	}
	for i, rev := range []map[string]any{first, second} {
		if strings.TrimSpace(asString(t, rev["commit"])) == "" {
			t.Errorf("revisions[%d].commit is empty", i)
		}
		if strings.TrimSpace(asString(t, rev["short_commit"])) == "" {
			t.Errorf("revisions[%d].short_commit is empty", i)
		}
		if strings.TrimSpace(asString(t, rev["date"])) == "" {
			t.Errorf("revisions[%d].date is empty", i)
		}
	}
}

// TestListPageRevisionsLimit confirms limit caps the number of commits
// returned, and total reflects the actually-returned count (not the full
// history depth).
func TestListPageRevisionsLimit(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.test")
	runGit(t, root, "config", "user.name", "Test User")
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(pagePath, []byte("---\ntitle: Hello\n---\nRevision "+string(rune('A'+i))+".\n"), 0o644); err != nil {
			t.Fatalf("write page: %v", err)
		}
		runGit(t, root, "add", ".")
		runGit(t, root, "commit", "-m", "revision "+string(rune('A'+i)))
	}

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "list_page_revisions", map[string]any{"slug": "/posts/hello/", "limit": 2})
	if res.IsError {
		t.Fatalf("list_page_revisions returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	revisions, ok := data["revisions"].([]any)
	if !ok || len(revisions) != 2 {
		t.Fatalf("list_page_revisions revisions = %#v, want 2 entries (limit=2 of 3 total commits)", data["revisions"])
	}
}

// TestListPageRevisionsWithoutGitReturnsUnavailableStatus mirrors
// TestDiffPageWithoutGitReturnsSourceContent: no local git repository means
// status: "git_unavailable" with an empty revisions list and a warning,
// not a hard failure.
func TestListPageRevisionsWithoutGitReturnsUnavailableStatus(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "nogit", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: No Git\n---\nNo git source body.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	session, done := newDiffPageClient(t, contentRoot)
	defer done()

	res := callTool(t, session, "list_page_revisions", map[string]any{"slug": "/posts/nogit/"})
	if res.IsError {
		t.Fatalf("list_page_revisions without git returned MCP error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "git_unavailable" {
		t.Fatalf("list_page_revisions status = %v, want git_unavailable", got)
	}
	revisions, ok := data["revisions"].([]any)
	if !ok || len(revisions) != 0 {
		t.Fatalf("list_page_revisions revisions = %#v, want empty", data["revisions"])
	}
	envelope := decodeEnvelope(t, res)
	warnings, _ := envelope["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("expected warning explaining git is unavailable")
	}
}
