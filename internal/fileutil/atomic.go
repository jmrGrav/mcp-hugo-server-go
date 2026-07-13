package fileutil

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

// AtomicWrite writes content to path atomically using a unique temp file in the
// same directory. On failure the temp file is removed; partial writes are never
// promoted to the target path.
func AtomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AtomicWriteChecked writes content to path atomically, calling
// pg.RevalidateForWrite on the parent directory both immediately before
// os.CreateTemp and again immediately before os.Rename. This closes the
// TOCTOU window between an earlier SafeJoin call (T1) and the actual write
// (T2/T3), detecting a directory that was swapped to a symlink in between.
// When pg has rejectSymlinks disabled the extra checks are no-ops and this
// behaves identically to AtomicWrite.
func AtomicWriteChecked(path, content string, pg *security.PathGuard) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Pre-CreateTemp check: fail if dir became a symlink after MkdirAll.
	if err := pg.RevalidateForWrite(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Pre-Rename check: fail if dir was swapped after CreateTemp succeeded.
	if err := pg.RevalidateForWrite(dir); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AtomicCreateChecked creates a brand-new file and writes content to it using
// exclusive create semantics. If the target already exists, the original file
// is left untouched and fs.ErrExist is returned.
func AtomicCreateChecked(path, content string, pg *security.PathGuard) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := pg.RevalidateForWrite(dir); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fsErrExist(path)
		}
		return err
	}
	writeOK := false
	defer func() {
		_ = f.Close()
		if !writeOK {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	writeOK = true
	return nil
}

func fsErrExist(path string) error {
	return &os.PathError{Op: "open", Path: path, Err: fs.ErrExist}
}

// AtomicWriteBytes writes data to path atomically using a unique temp file.
// On failure the temp file is removed.
func AtomicWriteBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func BoolPtr(v bool) *bool { return &v }
