package write_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBundleIsAtomicAcrossTranslations(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()
	res := callTool(t, session, "create_bundle", map[string]any{
		"slug": "posts/atomic-bundle",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "{{< script >}}unsafe{{< /script >}}"},
		},
	})
	if !res.IsError {
		t.Fatal("create_bundle with invalid EN page must fail")
	}
	if _, err := os.Stat(filepath.Join(root, "posts/atomic-bundle")); !os.IsNotExist(err) {
		t.Fatalf("failed bundle left source files behind: %v", err)
	}
	res = callTool(t, session, "create_bundle", map[string]any{
		"slug": "posts/atomic-bundle",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
	})
	if res.IsError {
		t.Fatalf("create_bundle failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if data["status"] != "created" {
		t.Fatalf("status = %v", data["status"])
	}
	for _, lang := range []string{"fr", "en"} {
		if _, err := os.Stat(filepath.Join(root, "posts/atomic-bundle", "index."+lang+".md")); err != nil {
			t.Fatalf("missing %s translation: %v", lang, err)
		}
	}
}

func TestDeleteBundleChecksEveryRevisionBeforeUnlinking(t *testing.T) {
	root := t.TempDir()
	writeBilingualBundle(t, root, "posts/delete-atomic")
	session, _, done := newTestServer(t, root)
	defer done()
	res := callTool(t, session, "delete_bundle", map[string]any{
		"slug": "posts/delete-atomic", "languages": []any{"fr", "en"},
		"expected_revisions": map[string]any{"fr": "stale", "en": "stale"},
	})
	if !res.IsError {
		t.Fatal("stale revision must fail")
	}
	for _, lang := range []string{"fr", "en"} {
		if _, err := os.Stat(filepath.Join(root, "posts/delete-atomic", "index."+lang+".md")); err != nil {
			t.Fatalf("preflight failure removed %s: %v", lang, err)
		}
	}
}
