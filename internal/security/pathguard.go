// Package security provides write-side path security for the MCP Hugo server.
package security

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathGuard enforces that all file operations stay within a designated root
// directory, with optional symlink rejection and hidden-path rejection.
type PathGuard struct {
	root           string
	rejectSymlinks bool
}

// New creates a PathGuard rooted at root. If rejectSymlinks is true, SafeJoin
// will reject any path component that resolves to a symlink. New resolves root
// to its real absolute path so that subsequent WithinRoot checks are reliable.
func New(root string, rejectSymlinks bool) (*PathGuard, error) {
	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		// root may not yet exist; fall back to Abs
		abs, err = filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("pathguard: resolving root %q: %w", root, err)
		}
	}
	return &PathGuard{root: abs, rejectSymlinks: rejectSymlinks}, nil
}

// SafeJoin joins rel to the guard root, rejecting:
//   - empty rel
//   - any component starting with "." (hidden files/dirs)
//   - path traversal (resolved path escapes root)
//   - symlinks (when rejectSymlinks is true)
func (pg *PathGuard) SafeJoin(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("pathguard: empty relative path")
	}

	// Check each component for hidden-path markers before joining.
	clean := filepath.Clean(rel)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("pathguard: hidden path component %q not allowed", part)
		}
	}

	joined := filepath.Join(pg.root, clean)

	// Reject symlinks before resolving — check whether the joined path itself
	// is a symlink or any component up the tree resolves differently.
	if pg.rejectSymlinks {
		info, err := os.Lstat(joined)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("pathguard: symlinks not allowed: %q", joined)
		}
		// Also detect symlinks introduced by intermediate components.
		real, err := filepath.EvalSymlinks(joined)
		if err == nil && real != joined {
			return "", fmt.Errorf("pathguard: symlinks not allowed: %q resolves to %q", joined, real)
		}
	}

	// Verify path traversal: the cleaned join must still be within root.
	if !pg.WithinRoot(joined) {
		return "", fmt.Errorf("pathguard: path %q escapes root %q", joined, pg.root)
	}

	return joined, nil
}

// WithinRoot reports whether abs is the root or a descendant of it.
func (pg *PathGuard) WithinRoot(abs string) bool {
	return abs == pg.root || strings.HasPrefix(abs, pg.root+string(filepath.Separator))
}
