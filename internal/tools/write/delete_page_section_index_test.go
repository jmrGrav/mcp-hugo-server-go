package write_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
)

func mustRevision(t *testing.T, path string) string {
	t.Helper()
	rev, err := contentmodel.SourceRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

// TestDeletePageRejectsSectionIndex is the regression coverage for a gap
// #1259 introduced: teaching contentmodel.ResolvePageSource to resolve
// _index.md/_index.<lang>.md (including the root homepage via slug "/") for
// update_page's guarded patch support also made that same resolver, shared
// by delete_page, resolve a section index for deletion — with no
// confirmation gate beyond the ordinary confirm_delete_of_published_page any
// leaf page already accepts. delete_page has no lifecycle/confirmation
// design for structural pages, so this must be a hard reject, on both
// dry_run and a real delete, regardless of RequireDeleteConfirmation.
func TestDeletePageRejectsSectionIndex(t *testing.T) {
	for _, tc := range []struct {
		name string
		slug string
		rel  string
	}{
		{name: "homepage", slug: "/", rel: "_index.md"},
		{name: "nested section index", slug: "posts", rel: filepath.Join("posts", "_index.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			target := filepath.Join(contentRoot, tc.rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "---\ntitle: Section\ndraft: false\n---\n\nSection body.\n"
			if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}

			for _, requireConfirmation := range []bool{false, true} {
				session, _, done := newTestServer(t, contentRoot, testServerOpts{RequireDeleteConfirmation: requireConfirmation})

				dry := callTool(t, session, "delete_page", map[string]any{"slug": tc.slug, "dry_run": true})
				if !dry.IsError {
					t.Fatalf("RequireDeleteConfirmation=%v: dry-run delete_page(%q) succeeded, want invalid_params rejection: %s", requireConfirmation, tc.slug, marshalContent(t, dry))
				}
				if code, _ := firstStructuredErrorCodeAndField(t, dry); code != "invalid_params" {
					t.Fatalf("RequireDeleteConfirmation=%v: dry-run error code = %q, want invalid_params", requireConfirmation, code)
				}

				real := callTool(t, session, "delete_page", map[string]any{
					"slug": tc.slug, "expected_revision": mustRevision(t, target),
					"confirm_delete_of_published_page": true,
				})
				if !real.IsError {
					t.Fatalf("RequireDeleteConfirmation=%v: delete_page(%q) succeeded, want invalid_params rejection: %s", requireConfirmation, tc.slug, marshalContent(t, real))
				}
				if code, _ := firstStructuredErrorCodeAndField(t, real); code != "invalid_params" {
					t.Fatalf("RequireDeleteConfirmation=%v: error code = %q, want invalid_params", requireConfirmation, code)
				}
				if _, err := os.Stat(target); err != nil {
					t.Fatalf("RequireDeleteConfirmation=%v: section index file was removed or became unreadable: %v", requireConfirmation, err)
				}
				done()
			}
		})
	}
}
