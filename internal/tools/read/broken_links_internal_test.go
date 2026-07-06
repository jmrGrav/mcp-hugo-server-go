package read

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestResolveInternalLinkSkipsMdSuffix(t *testing.T) {
	base, _ := url.Parse("https://example.test/posts/hello/")
	cases := []struct {
		href string
		want bool
	}{
		{"./index.md", false},
		{"../other.md", false},
		{"/posts/world/", true},
		{"#section", false}, // already filtered
	}
	for _, tc := range cases {
		_, ok := resolveInternalLink(base, tc.href)
		if ok != tc.want {
			t.Errorf("resolveInternalLink(%q) ok=%v, want %v", tc.href, ok, tc.want)
		}
	}
}

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
	write("index.html", `<html><body><a href="/posts/hello/">ok</a><a href="/missing-home/">ignored home link</a></body></html>`)
	write("posts/hello/index.html", `<html><body><a href="/missing/">bad</a></body></html>`)

	idx, err := site.NewIndex(config.Config{
		SiteRoot:         root,
		SiteURL:          "https://example.test",
		SiteName:         "example",
		DefaultLanguage:  "en",
		RejectSymlinks:   true,
		RejectHiddenPath: true,
	})
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	issues := collectBrokenLinks(idx)
	if len(issues) != 1 {
		t.Fatalf("collectBrokenLinks() = %#v", issues)
	}
	if issues[0].PageSlug != "/posts/hello/" || issues[0].Link != "/missing/" {
		t.Fatalf("collectBrokenLinks() issue = %#v", issues[0])
	}

	if got := sliceBrokenLinks(issues, 0, 1); len(got) != 1 {
		t.Fatalf("sliceBrokenLinks() = %#v", got)
	}
	if got := sliceBrokenLinks(issues, 10, 1); len(got) != 0 {
		t.Fatalf("sliceBrokenLinks(offset overflow) = %#v", got)
	}
}

func TestCollectBrokenLinksIgnoresGeneratedAndNonHTTPLinks(t *testing.T) {
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
	write("posts/hello/index.html", `<html><head>
<link rel="canonical" href="https://example.test/posts/hello/">
</head><body>
<a href="#local">fragment</a>
<a href="javascript:void(0)">javascript</a>
<a href="/page/2/">pagination</a>
<a href="/posts/page/2/">section pagination</a>
<a href="/robots.txt">robots</a>
<a href="/security.txt">security</a>
<a href="/llms.txt">llms</a>
<a href="/.well-known/security.txt">well-known</a>
<a href="/missing/">real missing page</a>
</body></html>`)

	idx, err := site.NewIndex(config.Config{
		SiteRoot:         root,
		SiteURL:          "https://example.test",
		SiteName:         "example",
		DefaultLanguage:  "en",
		RejectSymlinks:   true,
		RejectHiddenPath: false,
	})
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	issues := collectBrokenLinks(idx)
	if len(issues) != 1 {
		t.Fatalf("collectBrokenLinks() = %#v, want only /missing/", issues)
	}
	if issues[0].Link != "/missing/" {
		t.Fatalf("collectBrokenLinks() issue = %#v, want /missing/", issues[0])
	}
}
