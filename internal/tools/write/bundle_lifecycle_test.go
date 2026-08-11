package write_test

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestCreateBundleIdempotencyKeyReplaysAfterSuccessWithoutDuplicating is a
// regression test for #1008's "clear idempotency semantics" requirement:
// create_bundle previously had no idempotency_key support at all, unlike
// every sibling mutation tool, so a client that timed out waiting for the
// response had no safe way to retry without risking already_exists on a
// bundle it may or may not have actually created.
func TestCreateBundleIdempotencyKeyReplaysAfterSuccessWithoutDuplicating(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	args := map[string]any{
		"slug": "posts/idem-bundle",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
		"idempotency_key": "create-bundle-key-1",
	}
	first := callTool(t, session, "create_bundle", args)
	if first.IsError {
		t.Fatalf("first create_bundle failed: %s", marshalContent(t, first))
	}
	second := callTool(t, session, "create_bundle", args)
	if second.IsError {
		t.Fatalf("replayed create_bundle failed (should replay the cached success, not re-fail already_exists): %s", marshalContent(t, second))
	}
	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("create_bundle replay envelope drifted:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}

	status := callTool(t, session, "get_mutation_status", map[string]any{
		"tool": "create_bundle", "idempotency_key": "create-bundle-key-1",
	})
	if status.IsError {
		t.Fatalf("get_mutation_status failed: %s", marshalContent(t, status))
	}
	statusData := decodeWriteData(t, status)
	if statusData["status"] != "succeeded" {
		t.Fatalf("get_mutation_status(create_bundle) = %v, want succeeded", statusData["status"])
	}
}

// TestDeleteBundleIdempotencyKeyReplaysAfterSuccessfulDeleteWhenFilesGone is
// the delete-side counterpart, guarding the same class of bug #724 fixed for
// delete_page: idempotency replay must be checked before the per-language
// existence/revision preflight, or an exact retry of an already-succeeded
// delete would see not_found instead of replaying the original success.
func TestDeleteBundleIdempotencyKeyReplaysAfterSuccessfulDeleteWhenFilesGone(t *testing.T) {
	root := t.TempDir()
	writeBilingualBundle(t, root, "posts/idem-delete-bundle")
	session, _, done := newTestServer(t, root)
	defer done()

	dir := filepath.Join(root, "posts/idem-delete-bundle")
	args := map[string]any{
		"slug":      "posts/idem-delete-bundle",
		"languages": []any{"fr", "en"},
		"expected_revisions": map[string]any{
			"fr": currentRevision(t, filepath.Join(dir, "index.fr.md")),
			"en": currentRevision(t, filepath.Join(dir, "index.en.md")),
		},
		"idempotency_key": "delete-bundle-key-1",
	}
	first := callTool(t, session, "delete_bundle", args)
	if first.IsError {
		t.Fatalf("first delete_bundle failed: %s", marshalContent(t, first))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("bundle directory should be gone after full delete: %v", err)
	}

	second := callTool(t, session, "delete_bundle", args)
	if second.IsError {
		t.Fatalf("replayed delete_bundle failed (should replay cached success, not re-fail not_found on already-deleted files): %s", marshalContent(t, second))
	}
	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("delete_bundle replay envelope drifted:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}
}

// TestCreateBundleRevisionsRoundTripIntoDeleteBundle is a regression test:
// create_bundle keyed its returned revisions map and internal index entries
// off the raw, unnormalized `lang` field from each page instead of the
// trimmed value used for the file path and the `languages` list. A caller
// passing a language with incidental whitespace (accepted by
// validateLangParam, which trims before validating) got back a revisions
// map keyed by the untrimmed string, which delete_bundle's normalized
// `expected_revisions[lang]` lookup could never match, breaking the
// documented create -> delete_bundle recovery round trip.
func TestCreateBundleRevisionsRoundTripIntoDeleteBundle(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	created := callTool(t, session, "create_bundle", map[string]any{
		"slug": "posts/roundtrip-bundle",
		"pages": []any{
			map[string]any{"lang": " fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
	})
	if created.IsError {
		t.Fatalf("create_bundle failed: %s", marshalContent(t, created))
	}
	data := decodeWriteData(t, created)
	revisions, _ := data["revisions"].(map[string]any)
	if _, ok := revisions["fr"]; !ok {
		t.Fatalf("revisions keyed off untrimmed lang, want %q, got %#v", "fr", revisions)
	}

	expectedRevisions := map[string]any{}
	for lang, rev := range revisions {
		expectedRevisions[lang] = rev
	}
	deleted := callTool(t, session, "delete_bundle", map[string]any{
		"slug":               "posts/roundtrip-bundle",
		"languages":          []any{"fr", "en"},
		"expected_revisions": expectedRevisions,
	})
	if deleted.IsError {
		t.Fatalf("delete_bundle using create_bundle's own revisions failed: %s", marshalContent(t, deleted))
	}
}

// TestCreateBundleRollsBackFirstFileOnMidWriteFailure is a regression test
// for #1008's "prove no partial bundle remains when one language operation
// fails" acceptance criterion. The existing atomicity test only exercises
// the validation pre-pass, which runs before any file is touched and never
// reaches the create loop's rollback() closure. This test forces a failure
// *during* the write loop, after the first translation is already on disk:
// a dangling symlink at the second translation's path passes the existence
// preflight (os.Stat on a dangling symlink reports IsNotExist) but makes the
// exclusive os.Link promotion inside fileutil.AtomicCreateChecked fail with
// fs.ErrExist, which is exactly the failure rollback() exists to handle.
func TestCreateBundleRollsBackFirstFileOnMidWriteFailure(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	dir := filepath.Join(root, "posts/rollback-bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "index.en.md")); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, session, "create_bundle", map[string]any{
		"slug": "posts/rollback-bundle",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR"},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
	})
	if !res.IsError {
		t.Fatal("create_bundle must fail when the second translation's path collides mid-write")
	}
	if _, err := os.Lstat(filepath.Join(dir, "index.fr.md")); !os.IsNotExist(err) {
		t.Fatalf("mid-write failure left the first translation behind: %v", err)
	}
}

// TestBundleIdempotencyKeyConflictOnDifferentPayload confirms reusing the
// same idempotency_key for a genuinely different create_bundle request
// (different slug) is rejected as idempotency_conflict rather than silently
// replaying an unrelated result.
func TestBundleIdempotencyKeyConflictOnDifferentPayload(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	first := callTool(t, session, "create_bundle", map[string]any{
		"slug":            "posts/idem-conflict-a",
		"pages":           []any{map[string]any{"lang": "fr", "title": "A", "body": "Corps"}},
		"idempotency_key": "shared-key",
	})
	if first.IsError {
		t.Fatalf("first create_bundle failed: %s", marshalContent(t, first))
	}
	second := callTool(t, session, "create_bundle", map[string]any{
		"slug":            "posts/idem-conflict-b",
		"pages":           []any{map[string]any{"lang": "fr", "title": "B", "body": "Corps"}},
		"idempotency_key": "shared-key",
	})
	if !second.IsError {
		t.Fatal("reusing idempotency_key for a different create_bundle payload must fail")
	}
}
