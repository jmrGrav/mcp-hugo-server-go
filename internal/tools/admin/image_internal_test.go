package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 10 {
		t.Fatalf("Defs() = %d, want 10", len(defs))
	}
	if defs[0].RequiredScope != "write" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestFetchImageErrorBranches(t *testing.T) {
	if _, err := fetchImage(context.Background(), "://bad-url", "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on malformed URL")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := fetchImage(context.Background(), srv.URL, "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on non-2xx response")
	}
}

func TestLogicalHugoRootPath(t *testing.T) {
	tests := []struct {
		name     string
		hugoRoot string
		absPath  string
		want     string
	}{
		{"under hugo root", "/srv/hugo-site", "/srv/hugo-site/static/images/hello-featured.jpg", "static/images/hello-featured.jpg"},
		{"empty hugo root falls back to input", "", "/srv/hugo-site/static/images/hello-featured.jpg", ""},
		{"empty path", "/srv/hugo-site", "", ""},
		{"outside hugo root", "/srv/hugo-site", "/etc/passwd", ""},
		{"already relative", "/srv/hugo-site", "static/images/hello-featured.jpg", "static/images/hello-featured.jpg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := logicalHugoRootPath(tc.hugoRoot, tc.absPath); got != tc.want {
				t.Fatalf("logicalHugoRootPath(%q, %q) = %q, want %q", tc.hugoRoot, tc.absPath, got, tc.want)
			}
		})
	}
}

// TestRenderFeaturedImageSiteNameNotHardcoded is a regression test for a
// deployment-portability bug: renderFeaturedImage used to draw the literal
// string "arleo.eu" (the original production deployment's own domain) as a
// brand mark on every generated hero image, regardless of who was running
// this server. It's now the caller-supplied siteName (config.Config.SiteName),
// and is skipped entirely when unset rather than falling back to any
// hardcoded brand string.
func TestRenderFeaturedImageSiteNameNotHardcoded(t *testing.T) {
	for _, siteName := range []string{"My Blog", ""} {
		t.Run("siteName="+siteName, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.jpg")
			if err := renderFeaturedImage(dir, path, "", "A Title", "", nil, "#4c8bf5", siteName); err != nil {
				t.Fatalf("renderFeaturedImage() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("output file not found: %v", err)
			}
			if info.Size() < 1000 {
				t.Fatalf("rendered JPEG suspiciously small: %d bytes", info.Size())
			}
		})
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, config.Default(), nil)
	RegisterSiteAdmin(nil, config.Default(), nil)
	RegisterBuild(nil, config.Default())
	RegisterPreviewBuild(nil, config.Default())
	RegisterHooks(nil, config.Default())
	RegisterSRI(nil, config.Default())
}
