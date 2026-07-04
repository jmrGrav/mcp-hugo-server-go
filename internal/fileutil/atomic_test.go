package fileutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
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

func TestBoolPtr(t *testing.T) {
	if !*fileutil.BoolPtr(true) {
		t.Fatal("BoolPtr(true) returned false")
	}
	if *fileutil.BoolPtr(false) {
		t.Fatal("BoolPtr(false) returned true")
	}
}
