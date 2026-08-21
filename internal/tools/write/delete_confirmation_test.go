package write_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDeleteConfirmationRequiredForRealPageWhenConfigured is the gate's core
// behavior: on a deployment with require_delete_confirmation:true, a
// non-dry-run delete_page against a real (non-test_content) page must be
// rejected unless confirm_delete_of_published_page:true is also set —
// checked on top of, not instead of, the existing expected_revision
// requirement.
func TestDeleteConfirmationRequiredForRealPageWhenConfigured(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RequireDeleteConfirmation: true})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/real-page", "title": "Real Page", "body": "Real content.",
		"tags": []any{}, "categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	rev := currentRevision(t, filepath.Join(contentRoot, "posts", "real-page", "index.md"))

	unconfirmed := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/real-page", "expected_revision": rev,
	})
	if !unconfirmed.IsError {
		t.Fatalf("delete_page without confirm_delete_of_published_page: want rejection, got success")
	}
	if raw := marshalContent(t, unconfirmed); !strings.Contains(raw, "invalid_params") || !strings.Contains(raw, "confirm_delete_of_published_page") {
		t.Fatalf("delete_page rejection = %s, want invalid_params naming confirm_delete_of_published_page", raw)
	}

	confirmed := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/real-page", "expected_revision": rev, "confirm_delete_of_published_page": true,
	})
	if confirmed.IsError {
		t.Fatalf("delete_page with confirm_delete_of_published_page:true failed: %s", marshalContent(t, confirmed))
	}
}

// TestDeleteConfirmationExemptsTestContentPages proves the exemption: a page
// carrying the test_content frontmatter marker (#661) deletes without
// confirm_delete_of_published_page even when require_delete_confirmation is
// on — disposable-by-design content needs no ceremony.
func TestDeleteConfirmationExemptsTestContentPages(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RequireDeleteConfirmation: true})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/disposable", "title": "Disposable", "body": "Throwaway.",
		"tags": []any{}, "categories": []any{},
		"test_content": map[string]any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	rev := currentRevision(t, filepath.Join(contentRoot, "posts", "disposable", "index.md"))
	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/disposable", "expected_revision": rev,
	})
	if res.IsError {
		t.Fatalf("delete_page on a test_content page without confirm: want success, got %s", marshalContent(t, res))
	}
}

// TestDeleteConfirmationNotRequiredByDefault confirms the config-gated
// design's whole point: an ordinary deployment (require_delete_confirmation
// unset, the default) deletes a real page exactly as before — this flag
// exists specifically so no existing integration or test fixture breaks
// just by this feature shipping.
func TestDeleteConfirmationNotRequiredByDefault(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/unconfirmed-ok", "title": "Unconfirmed OK", "body": "Body.",
		"tags": []any{}, "categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	rev := currentRevision(t, filepath.Join(contentRoot, "posts", "unconfirmed-ok", "index.md"))
	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/unconfirmed-ok", "expected_revision": rev,
	})
	if res.IsError {
		t.Fatalf("delete_page without confirm on a default-config deployment: want success, got %s", marshalContent(t, res))
	}
}

// TestDeleteConfirmationDryRunNeverGated confirms dry_run stays a pure
// preview regardless of require_delete_confirmation: it never touches disk,
// so gating it would add friction without protecting anything.
func TestDeleteConfirmationDryRunNeverGated(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot, testServerOpts{RequireDeleteConfirmation: true})
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/preview-only", "title": "Preview Only", "body": "Body.",
		"tags": []any{}, "categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	res := callTool(t, session, "delete_page", map[string]any{
		"slug": "posts/preview-only", "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("dry_run delete_page without confirm: want success (preview only), got %s", marshalContent(t, res))
	}
}
