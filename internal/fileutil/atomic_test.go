package fileutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.txt")
	if err := fileutil.AtomicWrite(path, "hello"); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", string(data), "hello")
	}
}

func TestAtomicWriteBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")
	payload := []byte{1, 2, 3}
	if err := fileutil.AtomicWriteBytes(path, payload); err != nil {
		t.Fatalf("AtomicWriteBytes: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("content mismatch")
	}
}

func TestAtomicWriteMkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "nested")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := fileutil.AtomicWrite(filepath.Join(blocker, "file.txt"), "hello"); err == nil {
		t.Fatal("expected AtomicWrite() to fail when parent path is a file")
	}
}

func TestAtomicWriteBytesTempCreateFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.MkdirAll(dir, 0o555); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	if err := fileutil.AtomicWriteBytes(filepath.Join(dir, "file.bin"), []byte("x")); err == nil {
		t.Fatal("expected AtomicWriteBytes() to fail in read-only directory")
	}
}

// TestAtomicWriteCheckedRejectsSymlinkedParent verifies that AtomicWriteChecked
// refuses to write when the parent directory of the target path is a symlink,
// closing the TOCTOU window between SafeJoin (T1) and the write (T2/T3).
func TestAtomicWriteCheckedRejectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()

	// Make "subdir" inside base a symlink pointing outside base.
	symlinkDir := filepath.Join(base, "subdir")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "file.txt")
	if err := fileutil.AtomicWriteChecked(filePath, "should not write", pg); err == nil {
		t.Fatal("expected AtomicWriteChecked to fail when parent dir is a symlink")
	}

	// Verify no file was written to the symlink target.
	if _, statErr := os.Stat(filepath.Join(target, "file.txt")); !os.IsNotExist(statErr) {
		t.Error("file was written to symlink target — escape not prevented")
	}
}

// TestAtomicWriteCheckedSucceedsNormalPath verifies that AtomicWriteChecked
// works correctly for a plain (non-symlinked) path.
func TestAtomicWriteCheckedSucceedsNormalPath(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(base, "sub", "file.txt")
	if err := fileutil.AtomicWriteChecked(filePath, "hello", pg); err != nil {
		t.Fatalf("AtomicWriteChecked: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", string(data), "hello")
	}
}

func TestAtomicCreateCheckedCreatesNewFile(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(base, "sub", "file.txt")
	if err := fileutil.AtomicCreateChecked(filePath, "hello", pg); err != nil {
		t.Fatalf("AtomicCreateChecked: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", string(data), "hello")
	}
}

func TestAtomicCreateCheckedRefusesExistingFile(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(base, "sub", "file.txt")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fileutil.AtomicCreateChecked(filePath, "replacement", pg); err == nil {
		t.Fatal("expected AtomicCreateChecked to fail on existing file")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("content = %q, want original", string(data))
	}
}

func TestBoolPtr(t *testing.T) {
	if !*fileutil.BoolPtr(true) {
		t.Fatal("BoolPtr(true) returned false")
	}
	if *fileutil.BoolPtr(false) {
		t.Fatal("BoolPtr(false) returned true")
	}
}
