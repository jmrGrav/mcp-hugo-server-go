package fileutil_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func swapDirToSymlinkOnTempWrite(t *testing.T, dir, symlinkTarget string) <-chan struct{} {
	t.Helper()
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		moved := dir + "-moved"
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			matches, err := filepath.Glob(filepath.Join(dir, ".mcp-write-*.tmp"))
			if err != nil {
				t.Errorf("Glob() error = %v", err)
				return
			}
			if len(matches) == 0 {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			if err := os.Rename(dir, moved); err != nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			if err := os.Symlink(symlinkTarget, dir); err != nil {
				t.Errorf("os.Symlink() error = %v", err)
			}
			return
		}
		t.Errorf("timed out waiting for atomic temp file in %s", dir)
	}()
	return swapped
}

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

func TestAtomicWriteRenameFailureCleansTemp(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "existing-dir")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	err := fileutil.AtomicWrite(targetDir, "hello")
	if err == nil {
		t.Fatal("expected AtomicWrite() to fail when target path is an existing directory")
	}
	matches, globErr := filepath.Glob(filepath.Join(root, ".mcp-write-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob() error = %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("AtomicWrite() left temp files behind: %v", matches)
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

func TestAtomicWriteCheckedAllowsSymlinkWhenGuardConfiguredToAllowIt(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	symlinkDir := filepath.Join(base, "subdir")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, false)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "file.txt")
	if err := fileutil.AtomicWriteChecked(filePath, "hello", pg); err != nil {
		t.Fatalf("AtomicWriteChecked() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile(target/file.txt): %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", data)
	}
}

func TestAtomicWriteCheckedMkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "nested")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	if err := fileutil.AtomicWriteChecked(filepath.Join(blocker, "file.txt"), "hello", pg); err == nil {
		t.Fatal("expected AtomicWriteChecked() to fail when parent path is a file")
	}
}

func TestAtomicWriteCheckedRenameFailureCleansTemp(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "existing-dir")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	err = fileutil.AtomicWriteChecked(targetDir, "hello", pg)
	if err == nil {
		t.Fatal("expected AtomicWriteChecked() to fail when target path is an existing directory")
	}
	matches, globErr := filepath.Glob(filepath.Join(base, ".mcp-write-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob() error = %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("AtomicWriteChecked() left temp files behind: %v", matches)
	}
}

func TestAtomicWriteCheckedRejectsSymlinkSwapBeforeRename(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	dir := filepath.Join(base, "sub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	swapped := swapDirToSymlinkOnTempWrite(t, dir, target)
	err = fileutil.AtomicWriteChecked(filepath.Join(dir, "file.txt"), strings.Repeat("x", 8<<20), pg)
	<-swapped
	if err == nil {
		t.Fatal("expected AtomicWriteChecked() to fail after dir was swapped to a symlink before rename")
	}
	if _, statErr := os.Stat(filepath.Join(target, "file.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicWriteChecked() wrote through swapped symlink target: %v", statErr)
	}
}

func TestAtomicCreateCheckedRejectsExistingFile(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(base, "sub", "file.txt")
	if err := fileutil.AtomicCreateChecked(filePath, "first", pg); err != nil {
		t.Fatalf("AtomicCreateChecked(first): %v", err)
	}
	if err := fileutil.AtomicCreateChecked(filePath, "second", pg); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("AtomicCreateChecked(second) error = %v, want fs.ErrExist", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "first" {
		t.Fatalf("content = %q, want first", string(data))
	}
}

func TestAtomicCreateCheckedAllowsSymlinkWhenGuardConfiguredToAllowIt(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	symlinkDir := filepath.Join(base, "subdir")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, false)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "file.txt")
	if err := fileutil.AtomicCreateChecked(filePath, "hello", pg); err != nil {
		t.Fatalf("AtomicCreateChecked() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile(target/file.txt): %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", data)
	}
}

func TestAtomicCreateCheckedRejectsSymlinkSwapBeforeLink(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	dir := filepath.Join(base, "bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	swapped := swapDirToSymlinkOnTempWrite(t, dir, target)
	err = fileutil.AtomicCreateChecked(filepath.Join(dir, "index.md"), strings.Repeat("y", 8<<20), pg)
	<-swapped
	if err == nil {
		t.Fatal("expected AtomicCreateChecked() to fail after dir was swapped to a symlink before link")
	}
	if _, statErr := os.Stat(filepath.Join(target, "index.md")); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicCreateChecked() created file through swapped symlink target: %v", statErr)
	}
}

func TestAtomicCreateCheckedMkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "bundle")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	if err := fileutil.AtomicCreateChecked(filepath.Join(blocker, "index.md"), "hello", pg); err == nil {
		t.Fatal("expected AtomicCreateChecked() to fail when parent path is a file")
	}
}

func TestAtomicCreateCheckedRejectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()

	symlinkDir := filepath.Join(base, "subdir")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "file.txt")
	if err := fileutil.AtomicCreateChecked(filePath, "should not write", pg); err == nil {
		t.Fatal("expected AtomicCreateChecked to fail when parent dir is a symlink")
	}

	if _, statErr := os.Stat(filepath.Join(target, "file.txt")); !os.IsNotExist(statErr) {
		t.Error("file was created under symlink target — escape not prevented")
	}
}

func TestAtomicCreateCheckedBytesSucceedsAndRejectsExistingFile(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(base, "bundle", "cover.png")
	first := []byte{0x89, 'P', 'N', 'G'}
	if err := fileutil.AtomicCreateCheckedBytes(filePath, first, pg); err != nil {
		t.Fatalf("AtomicCreateCheckedBytes(first): %v", err)
	}
	if err := fileutil.AtomicCreateCheckedBytes(filePath, []byte("other"), pg); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("AtomicCreateCheckedBytes(second) error = %v, want fs.ErrExist", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(first) {
		t.Fatalf("content = %q, want %q", string(data), string(first))
	}
}

func TestAtomicCreateCheckedBytesMkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "bundle")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	if err := fileutil.AtomicCreateCheckedBytes(filepath.Join(blocker, "cover.png"), []byte{1, 2, 3}, pg); err == nil {
		t.Fatal("expected AtomicCreateCheckedBytes() to fail when parent path is a file")
	}
}

func TestAtomicCreateCheckedBytesRejectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()

	symlinkDir := filepath.Join(base, "bundle")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "cover.png")
	if err := fileutil.AtomicCreateCheckedBytes(filePath, []byte{0x89, 'P', 'N', 'G'}, pg); err == nil {
		t.Fatal("expected AtomicCreateCheckedBytes to fail when parent dir is a symlink")
	}

	if _, statErr := os.Stat(filepath.Join(target, "cover.png")); !os.IsNotExist(statErr) {
		t.Error("binary payload was created under symlink target — escape not prevented")
	}
}

func TestAtomicCreateCheckedBytesAllowsSymlinkWhenGuardConfiguredToAllowIt(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	symlinkDir := filepath.Join(base, "bundle")
	if err := os.Symlink(target, symlinkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	pg, err := security.New(base, false)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	filePath := filepath.Join(symlinkDir, "cover.png")
	payload := []byte{0x89, 'P', 'N', 'G'}
	if err := fileutil.AtomicCreateCheckedBytes(filePath, payload, pg); err != nil {
		t.Fatalf("AtomicCreateCheckedBytes() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "cover.png"))
	if err != nil {
		t.Fatalf("ReadFile(target/cover.png): %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("content = %v, want %v", data, payload)
	}
}

func TestAtomicCreateCheckedBytesRejectsSymlinkSwapBeforeLink(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	dir := filepath.Join(base, "bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	swapped := swapDirToSymlinkOnTempWrite(t, dir, target)
	err = fileutil.AtomicCreateCheckedBytes(filepath.Join(dir, "cover.png"), []byte(strings.Repeat("z", 8<<20)), pg)
	<-swapped
	if err == nil {
		t.Fatal("expected AtomicCreateCheckedBytes() to fail after dir was swapped to a symlink before link")
	}
	if _, statErr := os.Stat(filepath.Join(target, "cover.png")); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicCreateCheckedBytes() created file through swapped symlink target: %v", statErr)
	}
}

func TestAtomicWriteBytesRenameFailureCleansTemp(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "existing-dir")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	err := fileutil.AtomicWriteBytes(targetDir, []byte("hello"))
	if err == nil {
		t.Fatal("expected AtomicWriteBytes() to fail when target path is an existing directory")
	}
	matches, globErr := filepath.Glob(filepath.Join(root, ".mcp-write-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob() error = %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("AtomicWriteBytes() left temp files behind: %v", matches)
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
