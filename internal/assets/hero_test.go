package assets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHeroImageLocation(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveHeroImageLocation(root, "posts/demo")
	if err != nil {
		t.Fatalf("ResolveHeroImageLocation() error = %v", err)
	}
	if want := filepath.Join(root, "static", "images", "posts", "demo"+HeroImageSuffix); got.AbsPath != want {
		t.Fatalf("AbsPath = %q, want %q", got.AbsPath, want)
	}
	if got.LogicalPath != "static/images/posts/demo"+HeroImageSuffix {
		t.Fatalf("LogicalPath = %q", got.LogicalPath)
	}
}

func TestResolveHeroImageLocationRejectsTraversal(t *testing.T) {
	if _, err := ResolveHeroImageLocation(t.TempDir(), "../escape"); err == nil {
		t.Fatal("ResolveHeroImageLocation() error = nil, want traversal rejection")
	}
}

func TestResolveHeroImageLocationRejectsInvalidRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveHeroImageLocation(root, "demo"); err == nil {
		t.Fatal("ResolveHeroImageLocation() error = nil, want invalid root error")
	}
}

func TestNormalizeHeroImageSlug(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"posts/demo", "posts/demo", false},
		{"/posts/demo/", "posts/demo", false},
		{" /posts/demo/ ", "posts/demo", false},
		{"/posts/demo", "", true},
		{"", "", false},
	}
	for _, tt := range tests {
		got, err := NormalizeHeroImageSlug(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("NormalizeHeroImageSlug(%q) = %q, %v; want %q, error=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestLogicalHugoRootPath(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "static", "image.jpg")
	if got := logicalHugoRootPath(root, inside); got != "static/image.jpg" {
		t.Fatalf("inside path = %q", got)
	}
	if got := logicalHugoRootPath(root, ""); got != "" {
		t.Fatalf("empty path = %q", got)
	}
	if got := logicalHugoRootPath(root, filepath.Join(t.TempDir(), "outside.jpg")); got != "" {
		t.Fatalf("outside absolute path = %q", got)
	}
	if got := logicalHugoRootPath("", "static/image.jpg"); got != "static/image.jpg" {
		t.Fatalf("relative path = %q", got)
	}
}
