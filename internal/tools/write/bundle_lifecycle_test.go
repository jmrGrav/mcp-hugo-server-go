package write_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreateBundleTestContentForcesDraftForEveryTranslation(t *testing.T) {
	root := t.TempDir()
	session, idx, done := newTestServer(t, root)
	defer done()

	args := map[string]any{
		"slug": "posts/test-bundle-safety",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Test FR", "body": "FR", "draft": false, "test_content": map[string]any{"ttl_hours": 2, "owner": "bundle-audit"}},
			map[string]any{"lang": "en", "title": "Test EN", "body": "EN", "draft": false, "test_content": map[string]any{"ttl_hours": 2, "owner": "bundle-audit"}},
		},
	}
	dryRun := callTool(t, session, "create_bundle", mergeArgs(args, map[string]any{"dry_run": true}))
	if dryRun.IsError {
		t.Fatalf("create_bundle dry-run failed: %s", marshalContent(t, dryRun))
	}
	if _, err := os.Stat(filepath.Join(root, "posts/test-bundle-safety")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created bundle files: %v", err)
	}

	created := callTool(t, session, "create_bundle", args)
	if created.IsError {
		t.Fatalf("create_bundle failed: %s", marshalContent(t, created))
	}
	for _, lang := range []string{"fr", "en"} {
		raw, err := os.ReadFile(filepath.Join(root, "posts/test-bundle-safety", "index."+lang+".md"))
		if err != nil {
			t.Fatalf("read %s translation: %v", lang, err)
		}
		content := string(raw)
		for _, want := range []string{"draft: true", "test_content: true", "test_content_owner: bundle-audit", "test_content_expires_at:"} {
			if !strings.Contains(content, want) {
				t.Errorf("%s frontmatter missing %q: %s", lang, want, content)
			}
		}
	}
	data := decodeWriteData(t, created)
	expires, ok := data["test_content_expires_at"].(map[string]any)
	if !ok || expires["fr"] == nil || expires["en"] == nil {
		t.Fatalf("test_content_expires_at must report every translation, got %#v", data["test_content_expires_at"])
	}

	// The caller passed draft:false explicitly, but test_content must still
	// force draft:true in the in-memory SourceIndex — not just the on-disk
	// file — since get_site_health's DraftPages count and get_page's
	// FrontmatterRaw-derived draft state both read the index directly
	// (internal/tools/read), and it isn't rebuilt from disk until the next
	// full rescan.
	for _, lang := range []string{"fr", "en"} {
		page, ok := idx.GetBySlugLang("posts/test-bundle-safety", lang)
		if !ok {
			t.Fatalf("%s translation missing from SourceIndex after create_bundle", lang)
		}
		if !page.Draft {
			t.Errorf("%s SourcePage.Draft = false, want true (test_content must force it even though draft:false was passed)", lang)
		}
		if fmDraft, _ := page.FrontmatterRaw["draft"].(bool); !fmDraft {
			t.Errorf("%s FrontmatterRaw[\"draft\"] = %v, want true (test_content must force it even though draft:false was passed)", lang, page.FrontmatterRaw["draft"])
		}
	}
}

func mergeArgs(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

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

// TestCreateBundleRejectsOverlongTaxonomyBeforeWriting is the direct
// create_bundle counterpart to the plan/update taxonomy guards from #886.
// A bundle is a newer mutation entry point, so keep an explicit regression
// test here rather than relying on its shared validation helper indirectly.
func TestCreateBundleRejectsOverlongTaxonomyBeforeWriting(t *testing.T) {
	root := t.TempDir()
	session, _, done := newTestServer(t, root)
	defer done()

	res := callTool(t, session, "create_bundle", map[string]any{
		"slug": "posts/overlong-bundle-taxonomy",
		"pages": []any{
			map[string]any{"lang": "fr", "title": "Bonjour", "body": "Corps FR", "tags": []any{strings.Repeat("x", 101)}},
			map[string]any{"lang": "en", "title": "Hello", "body": "Body EN"},
		},
	})
	if !res.IsError {
		t.Fatal("create_bundle with an overlong tag must fail")
	}
	if got := marshalContent(t, res); !strings.Contains(got, "invalid_params") || !strings.Contains(got, "tag value exceeds 100 characters") {
		t.Fatalf("create_bundle overlong-tag error = %s, want structured invalid_params tag-length error", got)
	}
	if _, err := os.Stat(filepath.Join(root, "posts/overlong-bundle-taxonomy")); !os.IsNotExist(err) {
		t.Fatalf("pre-write validation failure left bundle files behind: %v", err)
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
