package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

// #893: revision_conflict / bundle_conflict errors must carry the current
// revision in their structured data so a caller can retry immediately without
// paying an extra read round-trip. These tests fail red against pre-fix
// update_page (which returned only the static message with no current_revision
// / current_bundle_revision field) and confirm the additive data field does not
// clobber the pre-existing rate_limit_remaining data field.

func TestUpdatePageRevisionConflictIncludesCurrentRevision(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "conflict-current-rev",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	wantRev := currentRevision(t, filepath.Join(contentRoot, "conflict-current-rev", "index.md"))

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "conflict-current-rev",
		"title":             "Changed",
		"expected_revision": "sha256:stale",
	})
	if !res.IsError {
		t.Fatalf("update_page with stale expected_revision should fail, got: %s", marshalContent(t, res))
	}
	if raw := marshalContent(t, res); !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("error should be revision_conflict, got: %s", raw)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["current_revision"]; got != wantRev {
		t.Fatalf("data.current_revision = %v, want %v", got, wantRev)
	}
	// The pre-existing rate_limit_remaining data field must still survive the
	// additive merge (dataFieldsFrom walks the whole unwrap chain).
	if _, ok := data["rate_limit_remaining"]; !ok {
		t.Fatalf("data.rate_limit_remaining missing after additive current_revision merge; data=%#v", data)
	}
}

func TestUpdatePageBundleConflictIncludesCurrentBundleRevision(t *testing.T) {
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
	frRevision := currentRevision(t, frPath) // fr file untouched → per-file guard passes

	// Sibling translation changes out-of-band → bundle revision shifts.
	if err := os.WriteFile(filepath.Join(dir, "index.en.md"),
		[]byte("---\ntitle: Article EN\n---\nSibling changed behind our back.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantBundleRev, err := contentmodel.BundleRevision(dir)
	if err != nil {
		t.Fatalf("BundleRevision after sibling change: %v", err)
	}
	if wantBundleRev == staleBundleRev {
		t.Fatalf("sibling change did not shift bundle revision")
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
	if raw := marshalContent(t, res); !strings.Contains(raw, "bundle_conflict") {
		t.Fatalf("error should be bundle_conflict, got: %s", raw)
	}
	data := decodeWriteErrorData(t, res)
	if got := data["current_bundle_revision"]; got != wantBundleRev {
		t.Fatalf("data.current_bundle_revision = %v, want %v", got, wantBundleRev)
	}
}
