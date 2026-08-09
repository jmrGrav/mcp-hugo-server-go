package write_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreatePageDryRunLargeBodyResponseBoundedAndUnduplicated(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	body := strings.Repeat("body line for dry-run amplification coverage\n", 2048)
	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/large-dry-run",
		"title":      "Large Dry Run",
		"body":       body,
		"tags":       []any{},
		"categories": []any{},
		"dry_run":    true,
	})
	if res.IsError {
		t.Fatalf("create_page dry_run returned error: %s", marshalContent(t, res))
	}

	root := decodeWriteContent(t, res)
	data := decodeWriteData(t, res)
	assertRootOnlyField(t, root, data, "rate_limit_remaining")

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	max := len(body)*2 + 16*1024
	if len(raw) > max {
		t.Fatalf("create_page dry_run response size = %d, want <= %d for body len %d", len(raw), max, len(body))
	}
	if _, err := os.Stat(filepath.Join(contentRoot, "posts", "large-dry-run")); !os.IsNotExist(err) {
		t.Fatalf("create_page dry_run must not write a file: %v", err)
	}
}

func TestUpdatePageDryRunLargeBodyResponseBoundedAndUnduplicated(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	create := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/large-update",
		"title":      "Original",
		"body":       "before",
		"tags":       []any{},
		"categories": []any{},
	})
	if create.IsError {
		t.Fatalf("create_page returned error: %s", marshalContent(t, create))
	}

	path := filepath.Join(contentRoot, "posts", "large-update", "index.md")
	rev := currentRevision(t, path)
	body := strings.Repeat("updated dry-run body for bounded-response coverage\n", 2048)
	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/large-update",
		"body":              body,
		"expected_revision": rev,
		"dry_run":           true,
	})
	if res.IsError {
		t.Fatalf("update_page dry_run returned error: %s", marshalContent(t, res))
	}

	root := decodeWriteContent(t, res)
	data := decodeWriteData(t, res)
	assertRootOnlyField(t, root, data, "rate_limit_remaining")

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	max := len(body)*4 + 32*1024
	if len(raw) > max {
		t.Fatalf("update_page dry_run response size = %d, want <= %d for body len %d", len(raw), max, len(body))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after update dry_run: %v", err)
	}
	if strings.Contains(string(after), "bounded-response coverage") {
		t.Fatalf("update_page dry_run wrote to disk: %q", string(after))
	}
}

func firstStructuredErrorCodeAndField(t *testing.T, res *mcp.CallToolResult) (string, string) {
	t.Helper()
	env := decodeWriteErrorEnvelope(t, res)
	errors, ok := env["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Fatalf("structured errors = %#v, want at least one error", env["errors"])
	}
	err0, ok := errors[0].(map[string]any)
	if !ok {
		t.Fatalf("structured errors[0] type = %T, want map[string]any", errors[0])
	}
	code, _ := err0["code"].(string)
	field, _ := err0["field"].(string)
	return code, field
}

func TestDryRunAndRealWriteValidationParity(t *testing.T) {
	t.Run("create_page unconfigured lang", func(t *testing.T) {
		contentRoot := t.TempDir()
		session, _, done := newTestServer(t, contentRoot, testServerOpts{ConfiguredLanguages: []string{"en", "fr"}})
		defer done()

		base := map[string]any{
			"slug": "posts/parity-lang", "lang": "zz", "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
		}
		real := callTool(t, session, "create_page", base)
		dry := callTool(t, session, "create_page", map[string]any{
			"slug": "posts/parity-lang", "lang": "zz", "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{}, "dry_run": true,
		})
		if !real.IsError || !dry.IsError {
			t.Fatal("create_page invalid lang must fail on both dry_run and real execution")
		}
		realCode, realField := firstStructuredErrorCodeAndField(t, real)
		dryCode, dryField := firstStructuredErrorCodeAndField(t, dry)
		if realCode != dryCode || realField != dryField {
			t.Fatalf("create_page lang parity = (%s,%s) vs (%s,%s)", realCode, realField, dryCode, dryField)
		}
		if !strings.Contains(marshalContent(t, real), "configured_languages") || !strings.Contains(marshalContent(t, dry), "configured_languages") {
			t.Fatalf("create_page invalid lang errors must name configured_languages:\nreal=%s\ndry=%s", marshalContent(t, real), marshalContent(t, dry))
		}
	})

	t.Run("create_page traversal slug", func(t *testing.T) {
		contentRoot := t.TempDir()
		session, _, done := newTestServer(t, contentRoot)
		defer done()

		real := callTool(t, session, "create_page", map[string]any{
			"slug": "../escape", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
		})
		dry := callTool(t, session, "create_page", map[string]any{
			"slug": "../escape", "title": "T", "body": "B", "tags": []any{}, "categories": []any{}, "dry_run": true,
		})
		if !real.IsError || !dry.IsError {
			t.Fatal("create_page traversal slug must fail on both dry_run and real execution")
		}
		realCode, realField := firstStructuredErrorCodeAndField(t, real)
		dryCode, dryField := firstStructuredErrorCodeAndField(t, dry)
		if realCode != dryCode || realField != dryField {
			t.Fatalf("create_page slug parity = (%s,%s) vs (%s,%s)", realCode, realField, dryCode, dryField)
		}
	})

	t.Run("update_page blocked shortcode", func(t *testing.T) {
		contentRoot := t.TempDir()
		session, _, done := newTestServer(t, contentRoot, testServerOpts{ConfiguredLanguages: []string{"en", "fr"}})
		defer done()

		create := callTool(t, session, "create_page", map[string]any{
			"slug": "posts/parity-update", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
		})
		if create.IsError {
			t.Fatalf("create_page returned error: %s", marshalContent(t, create))
		}
		rev := currentRevision(t, filepath.Join(contentRoot, "posts", "parity-update", "index.md"))
		real := callTool(t, session, "update_page", map[string]any{
			"slug": "posts/parity-update", "body": "{{< script >}}alert(1){{< /script >}}", "expected_revision": rev,
		})
		dry := callTool(t, session, "update_page", map[string]any{
			"slug": "posts/parity-update", "body": "{{< script >}}alert(1){{< /script >}}", "expected_revision": rev, "dry_run": true,
		})
		if !real.IsError || !dry.IsError {
			t.Fatal("update_page blocked shortcode must fail on both dry_run and real execution")
		}
		realCode, realField := firstStructuredErrorCodeAndField(t, real)
		dryCode, dryField := firstStructuredErrorCodeAndField(t, dry)
		if realCode != dryCode || realField != dryField {
			t.Fatalf("update_page blocked-shortcode parity = (%s,%s) vs (%s,%s)", realCode, realField, dryCode, dryField)
		}
	})
}
