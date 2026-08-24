package write_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteToolsRejectRootSlugExceptUpdatePage is the cross-handler
// regression coverage for #1261. #1259's review found that widening the
// shared slug resolver (normalizeInputSlug preserving "/" instead of
// collapsing it to "", needed only for update_page's guarded section-index
// support) silently reopened two independent catastrophic bugs:
// delete_page("/") and delete_bundle("/") could each reach
// os.RemoveAll(cfg.ContentRoot) — the site's entire content tree — because
// each handler had relied on the old "/" -> "" collapse to implicitly
// reject the root slug, and neither had an explicit guard of its own. Both
// gaps were found and fixed before either ever shipped, then consolidated
// into the single shared rejectRootSlug helper this test exercises
// indirectly (delete_page/delete_bundle) and directly (every other
// mutating write tool, via its own existing validateSlugFormat/
// validateBundleSlug guard).
//
// This test asserts the invariant structurally, once, for every mutating
// write tool: slug "/" must be rejected by every one of them except
// update_page (#1254) and list_page_snapshots. list_page_snapshots is
// ReadOnlyHint:true/DestructiveHint:false — it only lists rollback
// snapshots for whatever path the slug resolves to, so resolving "/"
// carries none of the risk this test otherwise guards against; forcing it
// to reject "/" would invent an inconsistency rather than fix a real gap.
// A future change to the shared resolver that reopens this bug class for
// any handler — new or existing — fails this test instead of depending on
// a human catching it in review again.
func TestWriteToolsRejectRootSlugExceptUpdatePage(t *testing.T) {
	contentRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(contentRoot, "_index.en.md"), []byte("---\ntitle: Home\ndraft: false\n---\n\nunique home body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	survivor := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(survivor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(survivor, []byte("---\ntitle: Hello\ndraft: false\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_page", map[string]any{"slug": "/", "title": "Root", "body": "body", "tags": []any{}, "categories": []any{}, "dry_run": true}},
		{"delete_page", map[string]any{"slug": "/", "dry_run": true}},
		{"delete_bundle", map[string]any{"slug": "/", "languages": []any{"en"}, "expected_revisions": map[string]any{"en": "sha256:0000000000000000000000000000000000000000000000000000000000000000"}, "dry_run": true}},
		{"create_bundle", map[string]any{"slug": "/", "pages": []any{map[string]any{"lang": "en", "title": "Root", "body": "body"}}, "dry_run": true}},
		{"plan_content_change", map[string]any{"slug": "/", "operations": []any{}}},
		{"rollback_change", map[string]any{"slug": "/", "to_revision": "sha256:0000000000000000000000000000000000000000000000000000000000000000", "dry_run": true}},
		{"plan_bundle_change", map[string]any{"slug": "/", "translations": []any{}}},
		{"rollback_bundle", map[string]any{"slug": "/", "to_bundle_revision": "sha256:0000000000000000000000000000000000000000000000000000000000000000", "dry_run": true}},
		{"begin_asset_upload", map[string]any{"slug": "/", "filename": "test.png", "size_bytes": 10}},
		{"upload_page_asset", map[string]any{"slug": "/", "filename": "test.png", "content_base64": "AAAA", "dry_run": true}},
		{"delete_page_asset", map[string]any{"slug": "/", "filename": "test.png", "dry_run": true}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			session, _, done := newTestServer(t, contentRoot)
			defer done()
			res := callTool(t, session, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s(slug=\"/\") succeeded, want rejection: %s", tc.tool, marshalContent(t, res))
			}
		})
	}

	// The sole intentional exception: update_page must accept slug "/" when
	// the caller opts in correctly, proving this is a deliberate carve-out
	// and not just an oversight elsewhere in the table above.
	t.Run("update_page", func(t *testing.T) {
		session, _, done := newTestServer(t, contentRoot)
		defer done()
		res := callTool(t, session, "update_page", map[string]any{
			"slug": "/", "lang": "en", "confirm_section_index": true,
			"old_str": "unique home body", "new_str": "changed home body", "dry_run": true,
		})
		if res.IsError {
			t.Fatalf("update_page(slug=\"/\") with confirm_section_index unexpectedly failed: %s", marshalContent(t, res))
		}
	})

	t.Run("list_page_snapshots", func(t *testing.T) {
		session, _, done := newTestServer(t, contentRoot)
		defer done()
		res := callTool(t, session, "list_page_snapshots", map[string]any{"slug": "/"})
		if res.IsError {
			t.Fatalf("list_page_snapshots(slug=\"/\") unexpectedly failed: %s", marshalContent(t, res))
		}
	})

	if _, err := os.Stat(survivor); err != nil {
		t.Fatalf("unrelated page was removed or became unreadable: %v", err)
	}
	if entries, err := os.ReadDir(contentRoot); err != nil || len(entries) == 0 {
		t.Fatalf("content root itself was removed: entries=%v err=%v", entries, err)
	}
}
