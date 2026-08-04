package contentmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundleRevisionCoversWholeBundle is the #857 regression test: the bundle
// revision must change whenever ANY part of the bundle changes — a sibling
// translation OR a shared asset — not only the one file a per-file revision
// protects.
func TestBundleRevisionCoversWholeBundle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.en.md"), "---\ntitle: Hello\n---\nEN body.\n")
	writeFile(t, filepath.Join(dir, "index.fr.md"), "---\ntitle: Bonjour\n---\nFR body.\n")
	writeFile(t, filepath.Join(dir, "diagram.svg"), "<svg/>")

	base, err := BundleRevision(dir)
	if err != nil {
		t.Fatalf("BundleRevision: %v", err)
	}
	if !strings.HasPrefix(base, "sha256:") {
		t.Fatalf("bundle revision = %q, want sha256: prefix", base)
	}
	// Stable: recomputing over unchanged files yields the same value.
	if again, _ := BundleRevision(dir); again != base {
		t.Fatalf("bundle revision not stable: %q != %q", again, base)
	}

	// EN changes after a caller read FR → bundle revision must change.
	writeFile(t, filepath.Join(dir, "index.en.md"), "---\ntitle: Hello\n---\nEN body EDITED.\n")
	afterEN, _ := BundleRevision(dir)
	if afterEN == base {
		t.Fatalf("bundle revision unchanged after EN translation edit; want changed")
	}

	// Shared asset changes → bundle revision must change again.
	writeFile(t, filepath.Join(dir, "diagram.svg"), "<svg><rect/></svg>")
	afterAsset, _ := BundleRevision(dir)
	if afterAsset == afterEN {
		t.Fatalf("bundle revision unchanged after shared asset edit; want changed")
	}

	// Adding a new translation file → bundle revision must change (path folded in).
	writeFile(t, filepath.Join(dir, "index.de.md"), "---\ntitle: Hallo\n---\nDE body.\n")
	afterAdd, _ := BundleRevision(dir)
	if afterAdd == afterAsset {
		t.Fatalf("bundle revision unchanged after adding a translation; want changed")
	}

	// The revision string never contains a filesystem path fragment.
	for _, frag := range []string{dir, "index.en.md", "diagram.svg"} {
		if strings.Contains(afterAdd, frag) {
			t.Fatalf("bundle revision leaked path fragment %q: %s", frag, afterAdd)
		}
	}
}

// TestBundleRevisionDiffersFromSingleFileRevision confirms the bundle token is
// genuinely distinct from any one file's revision — the whole point of #857.
func TestBundleRevisionDiffersFromSingleFileRevision(t *testing.T) {
	dir := t.TempDir()
	enPath := filepath.Join(dir, "index.en.md")
	writeFile(t, enPath, "---\ntitle: Hello\n---\nEN.\n")
	writeFile(t, filepath.Join(dir, "index.fr.md"), "---\ntitle: Bonjour\n---\nFR.\n")

	fileRev, err := SourceRevision(enPath)
	if err != nil {
		t.Fatalf("SourceRevision: %v", err)
	}
	bundleRev, err := BundleRevision(dir)
	if err != nil {
		t.Fatalf("BundleRevision: %v", err)
	}
	if fileRev == bundleRev {
		t.Fatalf("bundle revision equals single-file revision; they must differ")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
