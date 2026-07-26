package write

import (
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

func TestResolveDeleteAssetTargetBundleAndGenerated(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()

	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}

	bundle, err := resolveDeleteAssetTarget(pg, config.Config{}, "posts/demo", "cover.png", deleteAssetScopeBundle)
	if err != nil {
		t.Fatalf("resolveDeleteAssetTarget(bundle) error = %v", err)
	}
	if bundle.scope != deleteAssetScopeBundle || bundle.kind != "page_bundle" || bundle.filename != "cover.png" {
		t.Fatalf("bundle target = %#v", bundle)
	}
	if want := filepath.Join(contentRoot, "posts", "demo", "cover.png"); bundle.filePath != want {
		t.Fatalf("bundle.filePath = %q, want %q", bundle.filePath, want)
	}
	if bundle.referenceID != "cover.png" {
		t.Fatalf("bundle.referenceID = %q, want cover.png", bundle.referenceID)
	}

	cfg := config.Config{HugoRoot: hugoRoot}
	generated, err := resolveDeleteAssetTarget(pg, cfg, "posts/demo", "demo-featured.jpg", deleteAssetScopeGenerated)
	if err != nil {
		t.Fatalf("resolveDeleteAssetTarget(generated) error = %v", err)
	}
	if generated.scope != deleteAssetScopeGenerated || generated.kind != "global_static" {
		t.Fatalf("generated target = %#v", generated)
	}
	if want := filepath.Join(hugoRoot, "static", "images", "posts", "demo"+admin.HeroImageSuffix); generated.filePath != want {
		t.Fatalf("generated.filePath = %q, want %q", generated.filePath, want)
	}
	if generated.logicalPath != "static/images/posts/demo-featured.jpg" {
		t.Fatalf("generated.logicalPath = %q, want static/images/posts/demo-featured.jpg", generated.logicalPath)
	}
	if generated.referenceID != "/images/posts/demo-featured.jpg" {
		t.Fatalf("generated.referenceID = %q, want /images/posts/demo-featured.jpg", generated.referenceID)
	}
}

func TestResolveDeleteAssetTargetGeneratedErrors(t *testing.T) {
	contentRoot := t.TempDir()
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}

	if _, err := resolveDeleteAssetTarget(pg, config.Config{}, "posts/demo", "demo-featured.jpg", deleteAssetScopeGenerated); err == nil {
		t.Fatal("resolveDeleteAssetTarget(generated without hugo_root) error = nil, want error")
	}

	cfg := config.Config{HugoRoot: t.TempDir()}
	if _, err := resolveDeleteAssetTarget(pg, cfg, "posts/demo", "wrong.jpg", deleteAssetScopeGenerated); err == nil {
		t.Fatal("resolveDeleteAssetTarget(generated wrong filename) error = nil, want error")
	}
}
