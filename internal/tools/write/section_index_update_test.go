package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePageSectionIndexRequiresConfirmationAndSurgicalPatch(t *testing.T) {
	contentRoot := t.TempDir()
	for _, rel := range []string{"_index.en.md", "posts/_index.en.md"} {
		path := filepath.Join(contentRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\ntitle: Section\n---\nkeep\nreplace me\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	for _, tc := range []struct {
		name, slug, path string
	}{
		{"homepage", "/", filepath.Join(contentRoot, "_index.en.md")},
		{"nested section", "posts", filepath.Join(contentRoot, "posts", "_index.en.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			missing := callTool(t, session, "update_page", map[string]any{
				"slug": tc.slug, "lang": "en", "old_str": "replace me", "new_str": "changed", "dry_run": true,
			})
			if !missing.IsError || !strings.Contains(marshalContent(t, missing), "confirm_section_index") {
				t.Fatalf("missing confirmation = %s", marshalContent(t, missing))
			}

			res := callTool(t, session, "update_page", map[string]any{
				"slug": tc.slug, "lang": "en", "confirm_section_index": true,
				"old_str": "replace me", "new_str": "changed", "dry_run": true,
			})
			if res.IsError {
				t.Fatalf("section index dry-run failed: %s", marshalContent(t, res))
			}
			data := decodeWriteData(t, res)
			if data["slug"] != "/" && tc.slug == "/" {
				t.Fatalf("homepage response slug = %v, want /", data["slug"])
			}
			after, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("dry-run section index update mutated source")
			}

			real := callTool(t, session, "update_page", map[string]any{
				"slug": tc.slug, "lang": "en", "confirm_section_index": true,
				"old_str": "replace me", "new_str": "changed", "expected_revision": currentRevision(t, tc.path),
			})
			if real.IsError {
				t.Fatalf("section index real update failed: %s", marshalContent(t, real))
			}
			after, err = os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(after), "changed") || strings.Contains(string(after), "replace me") {
				t.Fatalf("section index real update produced unexpected content: %q", string(after))
			}

			fullBody := "rewrite"
			full := callTool(t, session, "update_page", map[string]any{
				"slug": tc.slug, "lang": "en", "confirm_section_index": true,
				"body": fullBody, "dry_run": true,
			})
			if !full.IsError || !strings.Contains(marshalContent(t, full), "old_str/new_str") {
				t.Fatalf("full-body section update = %s", marshalContent(t, full))
			}

			if page, ok := idx.GetByFilePath(tc.path); !ok || page.Body != "keep\nchanged" {
				t.Fatalf("source index section page = %#v, ok=%v", page, ok)
			}
		})
	}
}
