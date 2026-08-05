package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

// #857 AC3: update_page's optional, additive expected_bundle_revision guard.
// It sits alongside the existing per-file expected_revision and lets a caller
// additionally reject a write when a SIBLING translation or bundle-local asset
// changed since it read the whole bundle — a class of stale state the per-file
// revision cannot see. Omitting the field leaves behavior 100% unchanged.

// TestUpdatePageStaleBundleRevisionRejects — AC3 case (b): a stale
// expected_bundle_revision (a sibling translation edited out-of-band after the
// caller read the bundle, while the targeted file itself is untouched) causes
// update_page to reject with bundle_conflict rather than silently overwrite.
// The per-file expected_revision is kept CURRENT so this isolates the bundle
// guard as the sole reason for rejection. Fails red against pre-fix update_page
// (no bundle check), which would silently succeed.
func TestUpdatePageStaleBundleRevisionRejects(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	dir := filepath.Join(contentRoot, "posts/example")
	frPath := filepath.Join(dir, "index.fr.md")

	staleBundleRev, err := contentmodel.BundleRevision(dir)
	if err != nil {
		t.Fatalf("BundleRevision: %v", err)
	}
	frRevision := currentRevision(t, frPath) // still valid: fr file is untouched
	beforeFR := readFileString(t, contentRoot, "posts/example/index.fr.md")

	// Sibling translation changes out-of-band → bundle revision shifts, but the
	// fr file's own per-file revision does NOT.
	if err := os.WriteFile(filepath.Join(dir, "index.en.md"),
		[]byte("---\ntitle: Article EN\n---\nSibling changed behind our back.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":                     "posts/example",
		"lang":                     "fr",
		"body":                     "Nouveau corps FR.",
		"expected_revision":        frRevision,
		"expected_bundle_revision": staleBundleRev,
	})
	if !res.IsError {
		t.Fatalf("update_page with stale expected_bundle_revision should fail, got: %s", marshalContent(t, res))
	}
	if body := marshalContent(t, res); !strings.Contains(body, "bundle_conflict") {
		t.Fatalf("error should be bundle_conflict, got: %s", body)
	}
	if afterFR := readFileString(t, contentRoot, "posts/example/index.fr.md"); afterFR != beforeFR {
		t.Fatalf("fr file must not be overwritten on bundle_conflict; before=%q after=%q", beforeFR, afterFR)
	}
}

// TestUpdatePageMatchingBundleRevisionSucceeds — AC3 case (c): a current,
// matching expected_bundle_revision succeeds normally and writes the file.
func TestUpdatePageMatchingBundleRevisionSucceeds(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	dir := filepath.Join(contentRoot, "posts/example")
	frPath := filepath.Join(dir, "index.fr.md")

	bundleRev, err := contentmodel.BundleRevision(dir)
	if err != nil {
		t.Fatalf("BundleRevision: %v", err)
	}
	res := callTool(t, session, "update_page", map[string]any{
		"slug":                     "posts/example",
		"lang":                     "fr",
		"body":                     "Nouveau corps FR.",
		"expected_revision":        currentRevision(t, frPath),
		"expected_bundle_revision": bundleRev,
	})
	if res.IsError {
		t.Fatalf("update_page with matching expected_bundle_revision should succeed, got: %s", marshalContent(t, res))
	}
	if got := readFileString(t, contentRoot, "posts/example/index.fr.md"); !strings.Contains(got, "Nouveau corps FR.") {
		t.Fatalf("fr file not updated: %q", got)
	}
}

// TestUpdatePageOmittedBundleRevisionUnchanged — AC3 case (a): omitting
// expected_bundle_revision behaves exactly as before even when a sibling
// changed — the per-file guard alone governs, and the write proceeds.
func TestUpdatePageOmittedBundleRevisionUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeBilingualBundle(t, contentRoot, "posts/example")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	dir := filepath.Join(contentRoot, "posts/example")
	frPath := filepath.Join(dir, "index.fr.md")
	frRevision := currentRevision(t, frPath)

	// Sibling changes out-of-band; without expected_bundle_revision this is
	// invisible to update_page, exactly as before this feature existed.
	if err := os.WriteFile(filepath.Join(dir, "index.en.md"),
		[]byte("---\ntitle: Article EN\n---\nSibling changed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/example",
		"lang":              "fr",
		"body":              "Nouveau corps FR.",
		"expected_revision": frRevision,
	})
	if res.IsError {
		t.Fatalf("update_page without expected_bundle_revision should succeed unchanged, got: %s", marshalContent(t, res))
	}
	if got := readFileString(t, contentRoot, "posts/example/index.fr.md"); !strings.Contains(got, "Nouveau corps FR.") {
		t.Fatalf("fr file not updated: %q", got)
	}
}
