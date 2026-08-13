package write_test

import (
	"path/filepath"
	"testing"
)

// TestUpdatePageChangedFlagDistinguishesNoOpFromRealEdit is the #860 no-op
// contract test: update_page must return data.changed=false when the write
// would produce byte-identical content, and data.changed=true when it
// actually modifies the page — on both dry_run and real writes — so an agent
// can tell a successful no-op apart from a successful edit without diffing
// revisions.
func TestUpdatePageChangedFlagDistinguishesNoOpFromRealEdit(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	initialRev := currentRevision(t, filepath.Join(contentRoot, "posts/article", "index.md"))

	// A real edit → changed:true.
	realRes := callTool(t, session, "update_page", map[string]any{
		"slug": "posts/article", "title": "A Brand New Title",
		"expected_revision": initialRev,
	})
	if realRes.IsError {
		t.Fatalf("update_page (real edit) failed: %s", marshalContent(t, realRes))
	}
	realData := decodeWriteData(t, realRes)
	if changed, ok := realData["changed"].(bool); !ok || !changed {
		t.Fatalf("real edit: data.changed = %v, want true", realData["changed"])
	}
	rev := realData["new_revision"].(string)

	// A dry-run that re-applies the exact same title → no-op → changed:false.
	dryRes := callTool(t, session, "update_page", map[string]any{
		"slug": "posts/article", "title": "A Brand New Title",
		"expected_revision": rev, "dry_run": true,
	})
	if dryRes.IsError {
		t.Fatalf("update_page (dry-run no-op) failed: %s", marshalContent(t, dryRes))
	}
	dryData := decodeWriteData(t, dryRes)
	if changed, ok := dryData["changed"].(bool); !ok || changed {
		t.Fatalf("dry-run no-op: data.changed = %v, want false", dryData["changed"])
	}
	if diff, _ := dryData["diff"].(string); diff != "" {
		t.Fatalf("dry-run no-op: diff should be empty, got %q", diff)
	}

	// A real re-apply of the same title → real write, still a no-op → changed:false.
	noopRes := callTool(t, session, "update_page", map[string]any{
		"slug": "posts/article", "title": "A Brand New Title",
		"expected_revision": rev,
	})
	if noopRes.IsError {
		t.Fatalf("update_page (real no-op) failed: %s", marshalContent(t, noopRes))
	}
	noopData := decodeWriteData(t, noopRes)
	if changed, ok := noopData["changed"].(bool); !ok || changed {
		t.Fatalf("real no-op: data.changed = %v, want false", noopData["changed"])
	}
	if got := noopData["status"]; got != "unchanged" {
		t.Fatalf("real no-op: data.status = %v, want unchanged", got)
	}
}
