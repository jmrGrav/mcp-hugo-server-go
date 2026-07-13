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
		if got != "/tmp/other/index.md" {
			t.Fatalf("LogicalContentPath() = %q, want /tmp/other/index.md", got)
		}
	})
}
