package write

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHelpers(t *testing.T) {
	fm := buildFrontmatter("Title", []string{"go"}, []string{"docs"}, "Body")
	if !strings.Contains(fm, "Title") || !strings.Contains(fm, "draft: false") || !strings.Contains(fm, "Body") {
		t.Fatalf("buildFrontmatter() = %q", fm)
	}
	m := map[string]any{"title": "Title", "tags": []string{"go"}}
	fm2 := buildFrontmatterFromMap(m, "Body")
	if !strings.Contains(fm2, "Title") || !strings.Contains(fm2, "Body") {
		t.Fatalf("buildFrontmatterFromMap() = %q", fm2)
	}
	if !*boolPtr(true) {
		t.Fatal("boolPtr() returned false")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "page.md")
	if err := atomicWrite(path, "content"); err != nil {
		t.Fatalf("atomicWrite() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("atomicWrite() content = %q", string(data))
	}

	audit := filepath.Join(dir, "audit.log")
	if err := appendAuditLog(audit, "entry\n"); err != nil {
		t.Fatalf("appendAuditLog() error = %v", err)
	}
	raw, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("ReadFile(audit) error = %v", err)
	}
	if string(raw) != "entry\n" {
		t.Fatalf("appendAuditLog() content = %q", string(raw))
	}

	defs := Defs()
	if len(defs) != 3 || defs[0].RequiredScope != "content.write" {
		t.Fatalf("Defs() = %#v", defs)
	}
}
