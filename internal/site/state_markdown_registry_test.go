package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

func TestIndexStateMutations(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	originalCount := len(idx.Sitemap())
	idx.RemoveBySlug("/posts/hello/")
	if _, ok := idx.GetBySlug("/posts/hello/"); ok {
		t.Fatal("RemoveBySlug() should remove the page")
	}
	if got := len(idx.Sitemap()); got != originalCount-1 {
		t.Fatalf("Sitemap() len after remove = %d want %d", got, originalCount-1)
	}

	idx.UpsertPage(Page{
		Slug:    "/posts/hello/",
		Title:   "Hello restored",
		URL:     "https://example.test/posts/hello/",
		RawHTML: "<article><p>Restored</p></article>",
		Lang:    "en",
	})
	p, ok := idx.GetBySlug("/posts/hello/")
	if !ok || p.Title != "Hello restored" {
		t.Fatalf("UpsertPage(insert) = %#v, %v", p, ok)
	}
	idx.UpsertPage(Page{
		Slug:    "/posts/hello/",
		Title:   "Hello updated",
		URL:     "https://example.test/posts/hello/",
		RawHTML: "<article><p>Updated</p></article>",
		Lang:    "en",
	})
	p, ok = idx.GetBySlug("/posts/hello/")
	if !ok || p.Title != "Hello updated" {
		t.Fatalf("UpsertPage(update) = %#v, %v", p, ok)
	}
}

func TestIndexReloadAndNilMutations(t *testing.T) {
	var nilIdx *Index
	nilIdx.RemoveBySlug("/posts/missing/")
	nilIdx.UpsertPage(Page{Slug: "/posts/missing/"})

	root := t.TempDir()
	writeHTML := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	pageOne := "<!doctype html><html><head><title>One</title><link rel=\"canonical\" href=\"https://example.test/posts/one/\"></head><body><article><p>One</p></article></body></html>"
	pageTwo := "<!doctype html><html><head><title>Two</title><link rel=\"canonical\" href=\"https://example.test/posts/two/\"></head><body><article><p>Two</p></article></body></html>"
	writeHTML("posts/one/index.html", pageOne)

	cfg := minimalCfg(root)
	idx, err := NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	if _, ok := idx.GetBySlug("/posts/one/"); !ok {
		t.Fatal("expected /posts/one/ before reload")
	}
	os.Remove(filepath.Join(root, "posts", "one", "index.html"))
	writeHTML("posts/two/index.html", pageTwo)
	if err := idx.Reload(cfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if _, ok := idx.GetBySlug("/posts/one/"); ok {
		t.Fatal("Reload() should replace stale page state")
	}
	if _, ok := idx.GetBySlug("/posts/two/"); !ok {
		t.Fatal("Reload() should load new page state")
	}
}

func TestExtractArticleHTMLPaths(t *testing.T) {
	articleHTML := "<body><header>nav</header><article><h1>Title</h1><p>Body</p></article><footer>foot</footer></body>"
	if got := ExtractArticleHTML(articleHTML); !strings.Contains(got, "<h1>Title</h1>") || strings.Contains(got, "nav") {
		t.Fatalf("ExtractArticleHTML(article) = %q", got)
	}

	mainHTML := "<body><main><p>Main body</p></main></body>"
	if got := ExtractArticleHTML(mainHTML); !strings.Contains(got, "<p>Main body</p>") {
		t.Fatalf("ExtractArticleHTML(main) = %q", got)
	}

	bodyHTML := "<body><nav>menu</nav><div><p>Keep me</p></div><script>x</script><footer>foot</footer></body>"
	got := ExtractArticleHTML(bodyHTML)
	if strings.Contains(got, "menu") || strings.Contains(got, "foot") || strings.Contains(got, "<script") {
		t.Fatalf("ExtractArticleHTML(body fallback) = %q", got)
	}
	if !strings.Contains(got, "<p>Keep me</p>") {
		t.Fatalf("ExtractArticleHTML(body fallback) lost content: %q", got)
	}

	if got := ExtractArticleHTML("<not html"); got != "" {
		t.Fatalf("ExtractArticleHTML(malformed html) = %q", got)
	}
	if got := renderChildrenHTML(nil); got != "" {
		t.Fatalf("renderChildrenHTML(nil) = %q", got)
	}
}

func TestToolsRegistryHelpers(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.ToolDef{Name: "public_tool"})
	r.Register(tools.ToolDef{Name: "read_tool", RequiredScope: "content.read"})
	r.Register(tools.ToolDef{Name: "admin_tool", RequiredScope: "site.admin"})

	all := r.All()
	if len(all) != 3 || all[0].Name != "public_tool" || all[2].Name != "admin_tool" {
		t.Fatalf("All() = %#v", all)
	}
	if got := tools.IsAdminScope("site.admin"); !got {
		t.Fatal("IsAdminScope(site.admin) = false")
	}
	if got := tools.IsAdminScope("content.write"); got {
		t.Fatal("IsAdminScope(content.write) = true")
	}
}

func TestConfigEnabledHelpers(t *testing.T) {
	if (config.CloudflareConfig{}).Enabled() {
		t.Fatal("CloudflareConfig{}.Enabled() should be false")
	}
	if !(config.CloudflareConfig{ZoneID: "zone", APIToken: "token"}).Enabled() {
		t.Fatal("CloudflareConfig.Enabled() should require both fields")
	}
	if (config.IndexNowConfig{}).Enabled() {
		t.Fatal("IndexNowConfig{}.Enabled() should be false")
	}
	if !(config.IndexNowConfig{Key: "k"}).Enabled() {
		t.Fatal("IndexNowConfig.Enabled() should require key")
	}
	if (config.GoogleIndexConfig{}).Enabled() {
		t.Fatal("GoogleIndexConfig{}.Enabled() should be false")
	}
	if !(config.GoogleIndexConfig{ServiceAccountPath: "/tmp/sa.json"}).Enabled() {
		t.Fatal("GoogleIndexConfig.Enabled() should require service account path")
	}
}
