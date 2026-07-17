package fileutil

import (
	"errors"
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

// AtomicCreateChecked writes content to a newly created path atomically. The
// final path must not exist: promotion uses an exclusive create step so
// duplicate create attempts fail with fs.ErrExist instead of replacing the
// existing file.
func AtomicCreateChecked(path, content string, pg *security.PathGuard) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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
	if err := pg.RevalidateForWrite(dir); err != nil {
		return err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fs.ErrExist
		}
		return err
	}
	return nil
}

// AtomicCreateCheckedBytes writes data to a newly created path atomically,
// mirroring AtomicCreateChecked's exclusive-create + TOCTOU-checked semantics
// but for binary payloads (e.g. an uploaded page-bundle asset) instead of
// text content. The final path must not exist: promotion uses an exclusive
// link step so duplicate create attempts fail with fs.ErrExist instead of
// silently overwriting the existing file.
func AtomicCreateCheckedBytes(path string, data []byte, pg *security.PathGuard) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := pg.RevalidateForWrite(dir); err != nil {
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
	if err := pg.RevalidateForWrite(dir); err != nil {
		return err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fs.ErrExist
		}
		return err
	}
	return nil
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
