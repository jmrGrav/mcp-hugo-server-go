package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func TestAtomicWriteCreateTempFailure(t *testing.T) {
	origCreateTmp := createTmp
	createTmp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp boom")
	}
	defer func() { createTmp = origCreateTmp }()

	err := AtomicWrite(filepath.Join(t.TempDir(), "file.txt"), "hello")
	if err == nil || err.Error() != "create temp boom" {
		t.Fatalf("AtomicWrite() error = %v, want create temp boom", err)
	}
}

func TestAtomicWriteRenameFailureRemovesTemp(t *testing.T) {
	origRename := rename
	var tmpPath string
	rename = func(oldpath, newpath string) error {
		tmpPath = oldpath
		return errors.New("rename boom")
	}
	defer func() { rename = origRename }()

	err := AtomicWrite(filepath.Join(t.TempDir(), "file.txt"), "hello")
	if err == nil || err.Error() != "rename boom" {
		t.Fatalf("AtomicWrite() error = %v, want rename boom", err)
	}
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicWrite() temp file should be removed after rename failure, stat err = %v", statErr)
	}
}

func TestAtomicWriteWriteFailureRemovesTemp(t *testing.T) {
	origCreateTmp := createTmp
	var tmpPath string
	createTmp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		tmpPath = f.Name()
		if err := f.Close(); err != nil {
			return nil, err
		}
		return f, nil
	}
	defer func() { createTmp = origCreateTmp }()

	err := AtomicWrite(filepath.Join(t.TempDir(), "file.txt"), "hello")
	if err == nil {
		t.Fatal("expected AtomicWrite() to fail when temp file is already closed")
	}
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicWrite() temp file should be removed after write failure, stat err = %v", statErr)
	}
}

func TestAtomicWriteCheckedCreateTempFailure(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origCreateTmp := createTmp
	createTmp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp boom")
	}
	defer func() { createTmp = origCreateTmp }()

	err = AtomicWriteChecked(filepath.Join(base, "file.txt"), "hello", pg)
	if err == nil || err.Error() != "create temp boom" {
		t.Fatalf("AtomicWriteChecked() error = %v, want create temp boom", err)
	}
}

func TestAtomicCreateCheckedLinkFailurePropagatesAndCleansTemp(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origLink := link
	var tmpPath string
	link = func(oldname, newname string) error {
		tmpPath = oldname
		return errors.New("link boom")
	}
	defer func() { link = origLink }()

	err = AtomicCreateChecked(filepath.Join(base, "index.md"), "hello", pg)
	if err == nil || err.Error() != "link boom" {
		t.Fatalf("AtomicCreateChecked() error = %v, want link boom", err)
	}
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicCreateChecked() temp file should be removed after link failure, stat err = %v", statErr)
	}
}

func TestAtomicCreateCheckedCreateTempFailure(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origCreateTmp := createTmp
	createTmp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp boom")
	}
	defer func() { createTmp = origCreateTmp }()

	err = AtomicCreateChecked(filepath.Join(base, "index.md"), "hello", pg)
	if err == nil || err.Error() != "create temp boom" {
		t.Fatalf("AtomicCreateChecked() error = %v, want create temp boom", err)
	}
}

func TestAtomicCreateCheckedBytesLinkFailurePropagatesAndCleansTemp(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origLink := link
	var tmpPath string
	link = func(oldname, newname string) error {
		tmpPath = oldname
		return errors.New("link boom")
	}
	defer func() { link = origLink }()

	err = AtomicCreateCheckedBytes(filepath.Join(base, "cover.png"), []byte{1, 2, 3}, pg)
	if err == nil || err.Error() != "link boom" {
		t.Fatalf("AtomicCreateCheckedBytes() error = %v, want link boom", err)
	}
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicCreateCheckedBytes() temp file should be removed after link failure, stat err = %v", statErr)
	}
}

func TestAtomicCreateCheckedBytesCreateTempFailure(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origCreateTmp := createTmp
	createTmp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp boom")
	}
	defer func() { createTmp = origCreateTmp }()

	err = AtomicCreateCheckedBytes(filepath.Join(base, "cover.png"), []byte{1, 2, 3}, pg)
	if err == nil || err.Error() != "create temp boom" {
		t.Fatalf("AtomicCreateCheckedBytes() error = %v, want create temp boom", err)
	}
}

func TestAtomicCreateCheckedMapsErrExist(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origLink := link
	link = func(oldname, newname string) error {
		return fs.ErrExist
	}
	defer func() { link = origLink }()

	err = AtomicCreateChecked(filepath.Join(base, "index.md"), "hello", pg)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("AtomicCreateChecked() error = %v, want fs.ErrExist", err)
	}
}

func TestAtomicCreateCheckedBytesMapsErrExist(t *testing.T) {
	base := t.TempDir()
	pg, err := security.New(base, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	origLink := link
	link = func(oldname, newname string) error {
		return fs.ErrExist
	}
	defer func() { link = origLink }()

	err = AtomicCreateCheckedBytes(filepath.Join(base, "cover.png"), []byte{1, 2, 3}, pg)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("AtomicCreateCheckedBytes() error = %v, want fs.ErrExist", err)
	}
}

func TestAtomicWriteBytesCreateTempFailure(t *testing.T) {
	origCreateTmp := createTmp
	createTmp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp boom")
	}
	defer func() { createTmp = origCreateTmp }()

	err := AtomicWriteBytes(filepath.Join(t.TempDir(), "file.bin"), []byte("hello"))
	if err == nil || err.Error() != "create temp boom" {
		t.Fatalf("AtomicWriteBytes() error = %v, want create temp boom", err)
	}
}

func TestAtomicWriteBytesWriteFailureRemovesTemp(t *testing.T) {
	origCreateTmp := createTmp
	var tmpPath string
	createTmp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		tmpPath = f.Name()
		if err := f.Close(); err != nil {
			return nil, err
		}
		return f, nil
	}
	defer func() { createTmp = origCreateTmp }()

	err := AtomicWriteBytes(filepath.Join(t.TempDir(), "file.bin"), []byte("hello"))
	if err == nil {
		t.Fatal("expected AtomicWriteBytes() to fail when temp file is already closed")
	}
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("AtomicWriteBytes() temp file should be removed after write failure, stat err = %v", statErr)
	}
}
