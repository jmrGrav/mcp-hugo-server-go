package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePageBodyPatchReplacesUniqueSnippetOnly(t *testing.T) {
	contentRoot := t.TempDir()
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "patch-me", "title": "Patch me",
		"body": "before\n\nunique old text\n\nafter", "tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}
	path := filepath.Join(contentRoot, "patch-me", "index.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug": "patch-me", "old_str": "unique old text", "new_str": "small new text",
		"expected_revision": currentRevision(t, path),
	})
	if res.IsError {
		t.Fatalf("update_page patch failed: %s", marshalContent(t, res))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(before), "unique old text", "small new text", 1)
	if string(after) != want {
		t.Fatalf("body patch changed bytes outside the unique snippet:\nwant %q\n got %q", want, string(after))
	}
	page, ok := idx.GetBySlug("patch-me")
	if !ok || page.Body != "before\n\nsmall new text\n\nafter" {
		t.Fatalf("source index body not reconciled after patch: %#v", page)
	}
}

func TestUpdatePageBodyPatchRejectsUnsafeOrAmbiguousRequests(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "patch-guards", "title": "Patch guards",
		"body": "repeat repeat\n\nanchor", "tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}
	path := filepath.Join(contentRoot, "patch-guards", "index.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing new_str", map[string]any{"old_str": "anchor"}, "must be supplied together"},
		{"missing old_str", map[string]any{"new_str": "x"}, "must be supplied together"},
		{"empty old_str", map[string]any{"old_str": "", "new_str": "x"}, "must not be empty"},
		{"full body conflict", map[string]any{"body": "rewrite", "old_str": "anchor", "new_str": "x"}, "mutually exclusive"},
		{"explicit empty body conflict", map[string]any{"body": "", "old_str": "anchor", "new_str": "x"}, "mutually exclusive"},
		{"no match", map[string]any{"old_str": "absent", "new_str": "x"}, "does not match"},
		{"ambiguous", map[string]any{"old_str": "repeat", "new_str": "x"}, "exactly once"},
		{"blocked shortcode", map[string]any{"old_str": "anchor", "new_str": "{{< raw >}}<script>x</script>{{< /raw >}}"}, "blocked shortcode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"slug": "patch-guards", "dry_run": true}
			for k, v := range tc.args {
				args[k] = v
			}
			res := callTool(t, session, "update_page", args)
			if !res.IsError || !strings.Contains(marshalContent(t, res), tc.want) {
				t.Fatalf("error = %s, want substring %q", marshalContent(t, res), tc.want)
			}
			env := decodeWriteErrorEnvelope(t, res)
			errors, ok := env["errors"].([]any)
			if !ok || len(errors) != 1 || errors[0].(map[string]any)["code"] != "invalid_params" {
				t.Fatalf("structured error = %#v, want one invalid_params error", env["errors"])
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("rejected/dry-run body patch mutated the page")
	}
}

func TestUpdatePageBodyPatchAllowsDeletionAndDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "patch-delete", "title": "Patch delete",
		"body": "keep\nREMOVE ME\nkeep too", "tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}
	path := filepath.Join(contentRoot, "patch-delete", "index.md")
	before, _ := os.ReadFile(path)
	res := callTool(t, session, "update_page", map[string]any{
		"slug": "patch-delete", "old_str": "REMOVE ME\n", "new_str": "", "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("dry-run deletion patch failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if data["status"] != "would_update" || !strings.Contains(data["diff"].(string), "REMOVE ME") {
		t.Fatalf("unexpected dry-run output: %#v", data)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("dry-run deletion patch wrote to disk")
	}

	real := callTool(t, session, "update_page", map[string]any{
		"slug": "patch-delete", "old_str": "REMOVE ME\n", "new_str": "",
		"expected_revision": currentRevision(t, path),
	})
	if real.IsError {
		t.Fatalf("real deletion patch failed: %s", marshalContent(t, real))
	}
	after, _ = os.ReadFile(path)
	if strings.Contains(string(after), "REMOVE ME") || !strings.Contains(string(after), "keep\nkeep too") {
		t.Fatalf("real deletion patch produced unexpected content: %q", string(after))
	}
}

func TestUpdatePageBodyPatchSupportsFrontmatterAndIdempotentReplay(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug": "patch-replay", "title": "Before", "body": "old body anchor",
		"tags": []any{}, "categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page setup failed: %s", marshalContent(t, create))
	}
	path := filepath.Join(contentRoot, "patch-replay", "index.md")
	args := map[string]any{
		"slug": "patch-replay", "title": "After", "old_str": "old body", "new_str": "new body",
		"expected_revision": currentRevision(t, path), "idempotency_key": "patch-replay-key",
	}
	first := callTool(t, session, "update_page", args)
	if first.IsError {
		t.Fatalf("first patch failed: %s", marshalContent(t, first))
	}
	contentAfterFirst, _ := os.ReadFile(path)
	if !strings.Contains(string(contentAfterFirst), "title: After") || !strings.Contains(string(contentAfterFirst), "new body anchor") {
		t.Fatalf("combined body/frontmatter patch failed: %q", string(contentAfterFirst))
	}
	second := callTool(t, session, "update_page", args)
	if second.IsError {
		t.Fatalf("idempotent replay failed: %s", marshalContent(t, second))
	}
	if got, want := decodeWriteData(t, second)["new_revision"], decodeWriteData(t, first)["new_revision"]; got != want {
		t.Fatalf("replay new_revision = %v, want original %v", got, want)
	}
	contentAfterReplay, _ := os.ReadFile(path)
	if string(contentAfterReplay) != string(contentAfterFirst) {
		t.Fatal("idempotent replay rewrote content")
	}

	conflictArgs := map[string]any{}
	for k, v := range args {
		conflictArgs[k] = v
	}
	conflictArgs["new_str"] = "different body"
	conflict := callTool(t, session, "update_page", conflictArgs)
	if !conflict.IsError || !strings.Contains(marshalContent(t, conflict), "idempotency_conflict") {
		t.Fatalf("divergent patch replay = %s, want idempotency_conflict", marshalContent(t, conflict))
	}
}
