// Package gitutil centralizes how this server invokes git, so every caller
// gets the same dubious-ownership fix (see docs/git-baseline-model.md and
// the issue this package was added for): a content checkout is very often
// owned by an interactive Unix user while the MCP service itself runs as a
// dedicated, unprivileged service account. Since Git 2.35.2 (CVE-2022-24765
// mitigation), git refuses to operate in a repository whose owning UID
// doesn't match the calling process's UID unless that repository path is
// explicitly listed in safe.directory — which fails by default in exactly
// this "content owned by one user, service runs as another" deployment
// shape, with a "detected dubious ownership" error and no diff/commit data.
package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DiscoverRoot walks up from start looking for a .git entry (a directory
// for a normal checkout, a file for a worktree/submodule), returning the
// resolved, symlink-evaluated absolute path of the repository root.
//
// This deliberately never invokes git itself for discovery: git's own
// `rev-parse --show-toplevel` is subject to the same dubious-ownership
// check this package exists to work around, so using it to find the root
// would fail before the root is even known to pass to -c safe.directory=.
// A pure-filesystem walk has no such restriction.
func DiscoverRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("gitutil: resolve start path: %w", err)
	}
	dir := abs
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				return real, nil
			}
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Deliberately does not echo start/dir: callers (e.g. diff_page's
			// git_unavailable fallback) surface this error text directly to
			// callers, and it must never leak an absolute host path.
			return "", errors.New("gitutil: no .git repository found above the configured root")
		}
		dir = parent
	}
}

// Command builds a git command scoped to repoRoot, pinning
// `-c safe.directory=<repoRoot>` so Git's dubious-ownership protection does
// not block operation against a repository owned by a different Unix user
// than the calling process.
//
// repoRoot must be a path the caller has already resolved and validated
// (typically the return value of DiscoverRoot, or an operator-configured
// git_baseline.repo_path already resolved the same way) — never pass
// unresolved caller/agent input here, and never use "*" for safe.directory,
// which would trust every repository on the host rather than just the one
// this call actually needs.
func Command(ctx context.Context, repoRoot string, args ...string) *exec.Cmd {
	safeDirValue := repoRoot
	if safeDirValue == "*" {
		// Defense in depth: never let a caller (even accidentally) produce
		// a literal safe.directory=* — that trusts every repository on the
		// host, not just the one this call needs. Fall back to a value
		// that matches nothing rather than silently widening trust.
		safeDirValue = ""
	}
	gitArgs := append([]string{"-c", "safe.directory=" + safeDirValue, "-C", repoRoot}, args...)
	return exec.CommandContext(ctx, "git", gitArgs...)
}

// Output runs a git command scoped to repoRoot and returns its trimmed
// stdout+stderr on success, or a combined-output-annotated error.
func Output(ctx context.Context, repoRoot string, args ...string) (string, error) {
	out, err := Bytes(ctx, repoRoot, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Bytes runs a git command scoped to repoRoot and returns its raw
// stdout+stderr on success, or a combined-output-annotated error.
func Bytes(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := Command(ctx, repoRoot, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
