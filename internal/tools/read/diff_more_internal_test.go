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

func TestHasReservedTestSlugPrefix(t *testing.T) {
	cases := []struct {
		name     string
		slug     string
		wantFlag bool
	}{
		{"mcp-audit- prefix", "posts/mcp-audit-v159-20260720", true},
		{"test-audit- prefix", "posts/test-audit-0719", true},
		{"codex- prefix", "posts/codex-boundary-1784381074", true},
		{"case insensitive", "posts/MCP-AUDIT-something", true},
		{"nested section", "drafts/mcp-audit-nested-run", true},
		{"ordinary slug", "posts/hello-world", false},
		{"contains but doesn't start with prefix", "posts/my-test-run", false},
		// Regression: this site publishes a real article about a security
		// audit — a generic bare "audit-"/"test-" prefix would misclassify
		// it as leftover throwaway content (#584 review finding).
		{"real published security-audit article", "posts/audit-securite-modsecurity-crowdsec", false},
		{"bare test- prefix on real content", "posts/test-driven-development-in-go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasReservedTestSlugPrefix(tc.slug)
			if got != tc.wantFlag {
				t.Fatalf("hasReservedTestSlugPrefix(%q) = %v, want %v", tc.slug, got, tc.wantFlag)
			}
		})
	}
}

// TestValidateFrontMatterPageDoesNotIncludeTestPrefixInIssues is a
// regression test for the #584 review finding that a test-prefixed slug
// must not be treated as a frontmatter-invalid issue — that would flip a
// healthy page's status to "invalid" for a hygiene concern, not a defect.
// The test-content signal lives in validateOutputData.TestContentSlugs
// instead (see TestValidatePagesWithIssuesFilteredSeparatesTestContentFromInvalid).
func TestValidateFrontMatterPageDoesNotIncludeTestPrefixInIssues(t *testing.T) {
	issues := validateFrontMatterPage(
		hugosite.SourcePage{
			Slug:           "posts/mcp-audit-v159-20260720",
			Title:          "Hello",
			Date:           "2026-07-11",
			FrontmatterRaw: map[string]any{"title": "Hello", "date": "2026-07-11"},
		},
		nil,
	)
	if len(issues) != 0 {
		t.Fatalf("validateFrontMatterPage(test-prefixed but otherwise valid) = %#v, want no issues", issues)
	}
}

func runGitInternal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
