package write

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
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
	if !*fileutil.BoolPtr(true) {
		t.Fatal("fileutil.BoolPtr() returned false")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "page.md")
	if err := fileutil.AtomicWrite(path, "content"); err != nil {
		t.Fatalf("fileutil.AtomicWrite() error = %v", err)
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

func TestWriteHelperBranches(t *testing.T) {
	fm := buildFrontmatter("Title", nil, nil, "")
	if !strings.Contains(fm, "tags: []") || !strings.Contains(fm, "categories: []") {
		t.Fatalf("buildFrontmatter(nil slices) = %q", fm)
	}
	m := map[string]any{"title": "Title"}
	fm2 := buildFrontmatterFromMap(m, "")
	if !strings.Contains(fm2, "title: Title") {
		t.Fatalf("buildFrontmatterFromMap() = %q", fm2)
	}

	dir := t.TempDir()
	blocker := filepath.Join(dir, "audit.log")
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := appendAuditLog(blocker, "entry\n"); err == nil {
		t.Fatal("appendAuditLog() should fail when target path is a directory")
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, nil, nil, config.Default())
}

func TestApplyPageUpdatesPreservesOrder(t *testing.T) {
	input := "---\ntitle: Old Title\ndate: 2026-01-01T00:00:00Z\ntags:\n  - go\n---\n\nOriginal body."

	got, err := applyPageUpdates(input, "New Title", "")
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "New Title") {
		t.Errorf("title not updated: %s", got)
	}
	titleIdx := strings.Index(got, "title:")
	dateIdx := strings.Index(got, "date:")
	if dateIdx < titleIdx {
		t.Errorf("field order not preserved: date appears before title in:\n%s", got)
	}
	if !strings.Contains(got, "Original body.") {
		t.Errorf("body changed unexpectedly: %s", got)
	}
}

func TestApplyPageUpdatesBody(t *testing.T) {
	input := "---\ntitle: Page\n---\n\nOld body."
	got, err := applyPageUpdates(input, "", "New body.")
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "New body.") {
		t.Errorf("body not updated: %s", got)
	}
	if strings.Contains(got, "Old body.") {
		t.Errorf("old body still present: %s", got)
	}
}
