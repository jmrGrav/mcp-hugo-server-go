package admin

import (
	"reflect"
	"strings"
	"testing"
)

// TestClassifyDirtyPorcelainResourceClasses is the #864 regression test: git
// porcelain lines must be classified into safe coarse resource classes, and
// the classifier must never surface a path fragment — only the stable class
// labels. This proves an orphaned generated asset (and other residue) is
// diagnosable by class without exposing raw paths (#775 invariant preserved).
func TestClassifyDirtyPorcelainResourceClasses(t *testing.T) {
	lines := []string{
		" M content/posts/hello/index.md",                // content_source
		"?? static/images/posts/hello" + HeroImageSuffix, // generated_asset (orphaned hero)
		" M layouts/partials/head.html",                  // external_unknown
		"R  content/a/index.md -> content/b/index.md",    // rename → content_source (dest)
		"?? somewhere/mcp-preview-abc/index.html",        // preview_residue
	}
	got := classifyDirtyPorcelain(lines)
	want := []string{
		dirtyClassContentSource,
		dirtyClassExternalUnknown,
		dirtyClassGeneratedAsset,
		dirtyClassPreviewResidue,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty_classes = %v, want %v (sorted, de-duplicated)", got, want)
	}

	// Path-leak guard: no returned label may contain a path fragment.
	for _, c := range got {
		for _, frag := range []string{"content/", "static/", "hello", "layouts", "index.md", "mcp-preview"} {
			if strings.Contains(c, frag) {
				t.Fatalf("dirty class %q leaked path fragment %q", c, frag)
			}
		}
	}
}

func TestClassifyDirtyPathShapes(t *testing.T) {
	cases := map[string]string{
		"content/posts/x/index.md":                 dirtyClassContentSource,
		"index.fr.md":                              dirtyClassContentSource,
		"static/images/x" + HeroImageSuffix:        dirtyClassGeneratedAsset,
		"static/images/nested/y" + HeroImageSuffix: dirtyClassGeneratedAsset,
		"static/css/site.css":                      dirtyClassExternalUnknown,
		"config.toml":                              dirtyClassExternalUnknown,
		"tmp/mcp-preview-xyz/page.html":            dirtyClassPreviewResidue,
	}
	for path, want := range cases {
		if got := classifyDirtyPath(path); got != want {
			t.Errorf("classifyDirtyPath(%q) = %q, want %q", path, got, want)
		}
	}
}
