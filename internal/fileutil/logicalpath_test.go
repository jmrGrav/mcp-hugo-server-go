package fileutil

import "testing"

func TestLogicalContentPath(t *testing.T) {
	t.Run("absolute under content root", func(t *testing.T) {
		got := LogicalContentPath("/srv/site/content", "/srv/site/content/posts/hello/index.fr.md")
		if got != "content/posts/hello/index.fr.md" {
			t.Fatalf("LogicalContentPath() = %q, want content/posts/hello/index.fr.md", got)
		}
	})

	t.Run("empty source path", func(t *testing.T) {
		if got := LogicalContentPath("/srv/site/content", ""); got != "" {
			t.Fatalf("LogicalContentPath() = %q, want empty string", got)
		}
	})

	t.Run("path outside content root falls back to slash-normalized path", func(t *testing.T) {
		got := LogicalContentPath("/srv/site/content", "/tmp/other/index.md")
		if got != "" {
			t.Fatalf("LogicalContentPath() = %q, want empty string", got)
		}
	})

	t.Run("relative path outside content root remains relative", func(t *testing.T) {
		got := LogicalContentPath("/srv/site/content", "posts/hello/index.fr.md")
		if got != "posts/hello/index.fr.md" {
			t.Fatalf("LogicalContentPath() = %q, want posts/hello/index.fr.md", got)
		}
	})
}
