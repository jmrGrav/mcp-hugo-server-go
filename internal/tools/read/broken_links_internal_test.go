package read

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestCollectBrokenLinks(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", `<html><body><a href="/posts/hello/">ok</a><a href="/missing/">bad</a></body></html>`)
	write("posts/hello/index.html", `<html><body>hello</body></html>`)

	idx, err := site.NewIndex(config.Config{
		SiteRoot:        root,
		SiteURL:         "https://example.test",
		SiteName:        "example",
		DefaultLanguage: "en",
		RejectSymlinks:  true,
		RejectHiddenPath: true,
	})
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	issues := collectBrokenLinks(idx)
	if len(issues) != 1 {
		t.Fatalf("collectBrokenLinks() = %#v", issues)
	}
	if issues[0].PageSlug != "/" || issues[0].Link != "/missing/" {
		t.Fatalf("collectBrokenLinks() issue = %#v", issues[0])
	}

	if got := sliceBrokenLinks(issues, 0, 1); len(got) != 1 {
		t.Fatalf("sliceBrokenLinks() = %#v", got)
	}
	if got := sliceBrokenLinks(issues, 10, 1); len(got) != 0 {
		t.Fatalf("sliceBrokenLinks(offset overflow) = %#v", got)
	}
}
