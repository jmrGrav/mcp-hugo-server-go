package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestRegisterNilServer(t *testing.T) {
	Register(nil, config.Default())
	RegisterSiteAdmin(nil, config.Default())
	RegisterBuild(nil, config.Default())
	RegisterPreviewBuild(nil, config.Default())
	RegisterHooks(nil, config.Default())
	RegisterSRI(nil, config.Default())
}
