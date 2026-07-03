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

func (pg *PathGuard) SafeJoin(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("pathguard: empty relative path")
	}

	clean := filepath.Clean(rel)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("pathguard: hidden path component %q not allowed", part)
		}
	}

	joined := filepath.Join(pg.root, clean)

	if pg.rejectSymlinks {
		info, err := os.Lstat(joined)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("pathguard: symlinks not allowed: %q", joined)
		}
		real, err := filepath.EvalSymlinks(joined)
		if err == nil && real != joined {
			return "", fmt.Errorf("pathguard: symlinks not allowed: %q resolves to %q", joined, real)
		}
	}

	if !pg.WithinRoot(joined) {
		return "", fmt.Errorf("pathguard: path %q escapes root %q", joined, pg.root)
	}

	return joined, nil
}

func (pg *PathGuard) WithinRoot(abs string) bool {
	return abs == pg.root || strings.HasPrefix(abs, pg.root+string(filepath.Separator))
}
