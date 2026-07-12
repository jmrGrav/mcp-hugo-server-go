package security

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathGuard struct {
	root           string
	rejectSymlinks bool
}

func New(root string, rejectSymlinks bool) (*PathGuard, error) {
	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		abs, err = filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("pathguard: resolving root %q: %w", root, err)
		}
	}
	return &PathGuard{root: abs, rejectSymlinks: rejectSymlinks}, nil
}

// SafeJoin joins rel to the guard root and validates that the result stays
// within the root. When rejectSymlinks is true it also rejects any existing
// path component (including the target itself) that is a symbolic link,
// mitigating TOCTOU attacks where a parent directory is swapped for a symlink
// between path validation and file creation.
func (pg *PathGuard) SafeJoin(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("pathguard: empty relative path")
	}

	clean := filepath.Clean(rel)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("pathguard: hidden path component not allowed")
		}
	}

	joined := filepath.Join(pg.root, clean)

	if pg.rejectSymlinks {
		if err := pg.rejectSymlinkComponents(joined); err != nil {
			return "", err
		}
	}

	if !pg.WithinRoot(joined) {
		return "", fmt.Errorf("pathguard: path escapes root")
	}

	return joined, nil
}

// rejectSymlinkComponents walks each existing ancestor of path (up to and
// including path itself) and returns an error if any component is a symlink.
// Non-existent path tails (e.g. a new directory being created) are skipped.
func (pg *PathGuard) rejectSymlinkComponents(path string) error {
	rel, err := filepath.Rel(pg.root, path)
	if err != nil {
		return fmt.Errorf("pathguard: cannot relativize path")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := pg.root
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("pathguard: stat failed")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pathguard: symlinks not allowed in path")
		}
		if real, err := filepath.EvalSymlinks(current); err == nil && real != current {
			return fmt.Errorf("pathguard: symlinks not allowed in path")
		}
	}
	return nil
}

// RevalidateForWrite re-checks symlink components on the parent directory of
// path immediately before a write, closing the TOCTOU window between SafeJoin
// (validation at T1) and the actual file write (T2). No-op when rejectSymlinks
// is false.
func (pg *PathGuard) RevalidateForWrite(path string) error {
	if !pg.rejectSymlinks {
		return nil
	}
	return pg.rejectSymlinkComponents(filepath.Dir(path))
}

func (pg *PathGuard) WithinRoot(abs string) bool {
	return abs == pg.root || strings.HasPrefix(abs, pg.root+string(filepath.Separator))
}
