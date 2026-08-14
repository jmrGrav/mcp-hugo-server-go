package read

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestImplicitMultilingualResolutionWarning(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "posts", "bilingual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, title := range map[string]string{"index.fr.md": "Bonjour", "index.en.md": "Hello"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\ntitle: "+title+"\n---\nBody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcIdx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	fr, ok := srcIdx.GetBySlugLang("posts/bilingual", "fr")
	if !ok {
		t.Fatal("French source page missing")
	}
	resolved := site.ResolvedPage{Source: fr}
	cfg := config.Config{DefaultLanguage: "fr"}

	warning := implicitMultilingualResolutionWarning("/posts/bilingual/", "", resolved, srcIdx, cfg)
	for _, want := range []string{`resolved to "fr"`, "[en, fr]", "Pass lang explicitly"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning = %q, want %q", warning, want)
		}
	}
	if got := implicitMultilingualResolutionWarning("/posts/bilingual/", "en", resolved, srcIdx, cfg); got != "" {
		t.Fatalf("explicit lang warning = %q, want empty", got)
	}
	if got := implicitMultilingualResolutionWarning("/en/posts/bilingual/", "", site.ResolvedPage{Source: fr, RequestedLang: "en"}, srcIdx, cfg); got != "" {
		t.Fatalf("language-prefixed slug warning = %q, want empty", got)
	}
}
