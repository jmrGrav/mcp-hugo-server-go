package read

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestGitShowFileBranches(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "a", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGitInternal(t, root, "init")
	runGitInternal(t, root, "config", "user.email", "test@example.test")
	runGitInternal(t, root, "config", "user.name", "Test User")
	runGitInternal(t, root, "add", ".")
	runGitInternal(t, root, "commit", "-m", "initial")

	got, exists, err := gitShowFile(context.Background(), root, "content/posts/a/index.md")
	if err != nil || !exists {
		t.Fatalf("gitShowFile(existing) = %q exists=%v err=%v", got, exists, err)
	}
	if strings.TrimSpace(string(got)) != "hello" {
		t.Fatalf("gitShowFile(existing) = %q want hello", got)
	}

	got, exists, err = gitShowFile(context.Background(), root, "content/posts/missing/index.md")
	if err != nil || exists || got != nil {
		t.Fatalf("gitShowFile(missing) = %q exists=%v err=%v", got, exists, err)
	}
}

func TestValidateFrontMatterPageAliasWarnings(t *testing.T) {
	issues := validateFrontMatterPage(
		hugosite.SourcePage{
			Slug:           "posts/a",
			Title:          "Hello",
			Date:           "2026-07-11",
			Tags:           []string{"Ia"},
			Categories:     []string{"Post-mortems"},
			FrontmatterRaw: map[string]any{"title": "Hello", "date": "2026-07-11"},
		},
		map[string]string{"ia": "ai", "post-mortems": "postmortem"},
	)
	if len(issues) != 2 {
		t.Fatalf("validateFrontMatterPage(alias warnings) = %#v", issues)
	}
}

func runGitInternal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
