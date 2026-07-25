package write_test

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func countFilesUnder(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q): %v", root, err)
	}
	return count
}

func TestCreatePageRejectsHostileSlugCorpus(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, slug := range []string{
		"/tmp/escape",
		"%2e%2e/escape",
		"%252e%252e/escape",
		`..\\escape`,
		"posts/evil\nname",
		"posts/\x00evil",
	} {
		t.Run(slug, func(t *testing.T) {
			before := countFilesUnder(t, contentRoot)
			res := callTool(t, session, "create_page", map[string]any{
				"slug":       slug,
				"title":      "Blocked",
				"body":       "Body.",
				"tags":       []any{},
				"categories": []any{},
			})
			if !res.IsError {
				t.Fatalf("create_page(%q): want error, got success", slug)
			}
			raw, _ := json.Marshal(res.Content)
			if !strings.Contains(string(raw), "invalid_params") && !strings.Contains(string(raw), "validation failed") {
				t.Fatalf("create_page(%q): raw error = %s, want invalid_params or schema validation failure", slug, raw)
			}
			after := countFilesUnder(t, contentRoot)
			if after != before {
				t.Fatalf("create_page(%q) wrote files: before=%d after=%d", slug, before, after)
			}
		})
	}
}

func TestUploadPageAssetRejectsHostileFilenameWithStructuredField(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, filename := range []string{
		"../evil.png",
		"sub/dir.png",
		".hidden.png",
		"evil\nname.png",
	} {
		t.Run(filename, func(t *testing.T) {
			before := countFilesUnder(t, filepath.Join(contentRoot, "posts", "article"))
			res := callTool(t, session, "upload_page_asset", map[string]any{
				"slug":           "posts/article",
				"filename":       filename,
				"content_base64": b64(minimalPNG),
			})
			if !res.IsError {
				t.Fatalf("upload_page_asset(%q): want error, got success", filename)
			}
			envelope := decodeWriteErrorEnvelope(t, res)
			errors, ok := envelope["errors"].([]any)
			if !ok || len(errors) == 0 {
				t.Fatalf("upload_page_asset(%q): errors = %#v, want structured error", filename, envelope["errors"])
			}
			err0 := errors[0].(map[string]any)
			if got := err0["code"]; got != "invalid_params" {
				t.Fatalf("upload_page_asset(%q): code = %v, want invalid_params", filename, got)
			}
			if got := err0["field"]; got != "filename" {
				t.Fatalf("upload_page_asset(%q): field = %v, want filename", filename, got)
			}
			after := countFilesUnder(t, filepath.Join(contentRoot, "posts", "article"))
			if after != before {
				t.Fatalf("upload_page_asset(%q) wrote files: before=%d after=%d", filename, before, after)
			}
		})
	}
}
