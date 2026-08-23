package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdatePageBodyRejectsUnsafeText is the regression coverage for #1257.
// The ordinary full-body path must enforce the same input safety policy as
// the surgical old_str/new_str path, including on dry_run before any write.
func TestUpdatePageBodyRejectsUnsafeText(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "null", body: "safe\x00hidden", want: "null bytes"},
		{name: "bidi override", body: "safe\u202Ehidden", want: "bidirectional control"},
		{name: "malformed tag sequence", body: "safe\U000E0001hidden", want: "TAG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			session, _, done := newTestServer(t, contentRoot)
			defer done()

			created := callTool(t, session, "create_page", map[string]any{
				"slug": "body-safety-update", "title": "Body safety",
				"body": "unchanged", "tags": []any{}, "categories": []any{},
			})
			if created.IsError {
				t.Fatalf("create_page setup failed: %s", marshalContent(t, created))
			}
			path := filepath.Join(contentRoot, "body-safety-update", "index.md")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			res := callTool(t, session, "update_page", map[string]any{
				"slug": "body-safety-update", "body": tc.body, "dry_run": true,
			})
			if !res.IsError || !strings.Contains(marshalContent(t, res), tc.want) {
				t.Fatalf("error = %s, want rejection mentioning %q", marshalContent(t, res), tc.want)
			}
			if code, _ := firstStructuredErrorCodeAndField(t, res); code != "invalid_params" {
				t.Fatalf("error code = %q, want invalid_params", code)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("rejected update_page body mutated the page")
			}
		})
	}
}

// TestCreatePageBodyRejectsUnsafeText ensures create_page retains the same
// safety bar as update_page for all write entry points (#1257).
func TestCreatePageBodyRejectsUnsafeText(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "null", body: "safe\x00hidden", want: "null bytes"},
		{name: "bidi override", body: "safe\u202Ehidden", want: "bidirectional control"},
		{name: "malformed tag sequence", body: "safe\U000E0001hidden", want: "TAG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			session, _, done := newTestServer(t, contentRoot)
			defer done()

			res := callTool(t, session, "create_page", map[string]any{
				"slug": "body-safety-create", "title": "Body safety",
				"body": tc.body, "tags": []any{}, "categories": []any{},
			})
			if !res.IsError || !strings.Contains(marshalContent(t, res), tc.want) {
				t.Fatalf("error = %s, want rejection mentioning %q", marshalContent(t, res), tc.want)
			}
			if code, _ := firstStructuredErrorCodeAndField(t, res); code != "invalid_params" {
				t.Fatalf("error code = %q, want invalid_params", code)
			}
			if _, err := os.Stat(filepath.Join(contentRoot, "body-safety-create")); !os.IsNotExist(err) {
				t.Fatalf("rejected create_page body wrote content: %v", err)
			}
		})
	}
}
