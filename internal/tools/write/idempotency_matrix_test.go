package write_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreatePageDryRunDoesNotPoisonIdempotentCreate(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	args := map[string]any{
		"slug":            "idem-dry-run-create",
		"title":           "Original",
		"body":            "Body",
		"tags":            []any{},
		"categories":      []any{},
		"idempotency_key": "idem-dry-run-create-1",
	}

	dryRunArgs := map[string]any{}
	for k, v := range args {
		dryRunArgs[k] = v
	}
	dryRunArgs["dry_run"] = true

	dry := callTool(t, session, "create_page", dryRunArgs)
	if dry.IsError {
		t.Fatalf("dry-run create_page failed: %s", marshalContent(t, dry))
	}

	first := callTool(t, session, "create_page", args)
	if first.IsError {
		t.Fatalf("real create_page after dry-run failed: %s", marshalContent(t, first))
	}
	second := callTool(t, session, "create_page", args)
	if second.IsError {
		t.Fatalf("replayed create_page failed: %s", marshalContent(t, second))
	}

	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("create_page replay envelope drifted after dry-run path:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}
}

func TestUpdatePageIdempotencyReplayReturnsOriginalEnvelope(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-update-envelope",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "idem-update-envelope",
		"title":             "Updated",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-update-envelope", "index.md")),
		"idempotency_key":   "idem-update-envelope-1",
	}
	first := callTool(t, session, "update_page", args)
	if first.IsError {
		t.Fatalf("first update_page failed: %s", marshalContent(t, first))
	}

	path := filepath.Join(contentRoot, "idem-update-envelope", "index.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Mutated\n---\nMutated"), 0o644); err != nil {
		t.Fatalf("WriteFile mutated: %v", err)
	}

	second := callTool(t, session, "update_page", args)
	if second.IsError {
		t.Fatalf("replayed update_page failed: %s", marshalContent(t, second))
	}

	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("update_page replay envelope drifted:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}
}

func TestDeletePageIdempotencyReplayReturnsOriginalEnvelopeAfterSuccessfulDelete(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-delete-envelope",
		"title":      "Original",
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "idem-delete-envelope",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-delete-envelope", "index.md")),
		"idempotency_key":   "idem-delete-envelope-1",
	}
	first := callTool(t, session, "delete_page", args)
	if first.IsError {
		t.Fatalf("first delete_page failed: %s", marshalContent(t, first))
	}
	second := callTool(t, session, "delete_page", args)
	if second.IsError {
		t.Fatalf("replayed delete_page failed: %s", marshalContent(t, second))
	}

	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("delete_page replay envelope drifted:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}
}

// TestDeletePageIdempotencyReplayWithLangReturnsOriginalEnvelope guards
// against a regression seen in an earlier draft of the #724/#727 fix: when
// `lang` is explicitly passed, resolving the source file before the
// idempotency replay lookup runs would make an exact replay of an
// already-successful delete fail with `source_file_not_found` instead of
// returning the cached success, since the target language file no longer
// exists after the first delete. The replay lookup must run before source
// resolution for the lang-specified path too, not just the lang-omitted one.
func TestDeletePageIdempotencyReplayWithLangReturnsOriginalEnvelope(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "idem-delete-lang-envelope",
		"title":      "Original",
		"body":       "Body",
		"lang":       "fr",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}

	args := map[string]any{
		"slug":              "idem-delete-lang-envelope",
		"lang":              "fr",
		"expected_revision": currentRevision(t, filepath.Join(contentRoot, "idem-delete-lang-envelope", "index.fr.md")),
		"idempotency_key":   "idem-delete-lang-envelope-1",
	}
	first := callTool(t, session, "delete_page", args)
	if first.IsError {
		t.Fatalf("first delete_page failed: %s", marshalContent(t, first))
	}
	second := callTool(t, session, "delete_page", args)
	if second.IsError {
		t.Fatalf("replayed delete_page (lang=fr) failed: %s", marshalContent(t, second))
	}

	firstOut := decodeWriteContent(t, first)
	secondOut := decodeWriteContent(t, second)
	if !reflect.DeepEqual(firstOut, secondOut) {
		t.Fatalf("delete_page (lang=fr) replay envelope drifted:\nfirst=%#v\nsecond=%#v", firstOut, secondOut)
	}
}

func TestUploadPageAssetIdempotencyKeyRejectsDivergentReuse(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	key := "idem-upload-conflict-1"
	otherPNG := append([]byte(nil), minimalPNG...)
	otherPNG[len(otherPNG)-1] = 1
	first := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"content_base64":  b64(minimalPNG),
		"idempotency_key": key,
	})
	if first.IsError {
		t.Fatalf("first upload_page_asset failed: %s", marshalContent(t, first))
	}

	second := callTool(t, session, "upload_page_asset", map[string]any{
		"slug":            "posts/article",
		"filename":        "cover.png",
		"content_base64":  b64(otherPNG),
		"idempotency_key": key,
	})
	if !second.IsError {
		t.Fatal("reusing upload_page_asset idempotency_key with different payload should fail")
	}
	if raw := marshalContent(t, second); !strings.Contains(raw, "idempotency_conflict") {
		t.Fatalf("divergent upload_page_asset idempotency reuse error = %s", raw)
	}
}
