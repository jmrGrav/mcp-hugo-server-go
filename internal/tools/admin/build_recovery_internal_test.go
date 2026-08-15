package admin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwapBuildOutputRestoresPreviousTreeWhenInstallRenameFails(t *testing.T) {
	parent := t.TempDir()
	siteRoot := filepath.Join(parent, "public")
	tempDir := filepath.Join(parent, "rendered")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "index.html"), []byte("old-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("new-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}

	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return errors.New("injected install rename failure")
		}
		return os.Rename(oldPath, newPath)
	}
	if _, err := swapBuildOutputWithOps(tempDir, siteRoot, rename, os.RemoveAll); err == nil || !strings.Contains(err.Error(), "failed to install") {
		t.Fatalf("swap error = %v, want injected install failure", err)
	}
	raw, err := os.ReadFile(filepath.Join(siteRoot, "index.html"))
	if err != nil || string(raw) != "old-complete-tree" {
		t.Fatalf("public tree after interrupted swap = %q, %v, want old complete tree", raw, err)
	}
}

// TestSwapBuildOutputRetryAfterDoubleRenameFailureReconciles proves restart/retry
// determinism (#1068 acceptance criterion): if the install rename fails AND the
// best-effort restore-of-old-output that follows it also fails — the worst-case
// residue, where siteRoot is left absent and the old complete tree sits orphaned
// under a .mcp-public-backup-* directory — a subsequent build's swap call still
// succeeds deterministically and serves the new output, with no separate
// operator cleanup step required before that retry can succeed.
func TestSwapBuildOutputRetryAfterDoubleRenameFailureReconciles(t *testing.T) {
	parent := t.TempDir()
	siteRoot := filepath.Join(parent, "public")
	tempDir := filepath.Join(parent, "rendered")
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "index.html"), []byte("old-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("new-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}

	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 2:
			return errors.New("injected install rename failure")
		case 3:
			return errors.New("injected restore rename failure")
		default:
			return os.Rename(oldPath, newPath)
		}
	}
	_, err := swapBuildOutputWithOps(tempDir, siteRoot, rename, os.RemoveAll)
	if err == nil || !strings.Contains(err.Error(), "failed to install") {
		t.Fatalf("first swap error = %v, want injected install failure", err)
	}
	// Worst case: siteRoot no longer exists (both the install rename and the
	// best-effort restore failed) and the old complete tree is orphaned under
	// an unreferenced .mcp-public-backup-* sibling directory.
	if _, statErr := os.Lstat(siteRoot); !os.IsNotExist(statErr) {
		t.Fatalf("siteRoot Lstat error = %v, want IsNotExist after double rename failure", statErr)
	}

	// Retry: a fresh render lands in a new temp dir, exactly as a subsequent
	// build_site invocation would produce. No cleanup of the orphaned backup
	// directory happens between the failed attempt and this retry.
	retryDir := filepath.Join(parent, "rendered-retry")
	if err := os.MkdirAll(retryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retryDir, "index.html"), []byte("retry-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning, retryErr := swapBuildOutputWithOps(retryDir, siteRoot, os.Rename, os.RemoveAll)
	if retryErr != nil {
		t.Fatalf("retry swap error = %v, want deterministic success", retryErr)
	}
	if warning != "" {
		t.Fatalf("retry swap warning = %q, want none", warning)
	}
	raw, readErr := os.ReadFile(filepath.Join(siteRoot, "index.html"))
	if readErr != nil || string(raw) != "retry-complete-tree" {
		t.Fatalf("public tree after retry = %q, %v, want retry complete tree", raw, readErr)
	}
}

func TestSwapBuildOutputCleanupFailureKeepsNewCompleteTree(t *testing.T) {
	parent := t.TempDir()
	siteRoot := filepath.Join(parent, "public")
	tempDir := filepath.Join(parent, "rendered")
	for _, dir := range []string{siteRoot, tempDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "index.html"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("new-complete-tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	warning, err := swapBuildOutputWithOps(tempDir, siteRoot, os.Rename, func(string) error {
		return errors.New("injected cleanup failure")
	})
	if err != nil || !strings.Contains(warning, "cleanup failed") {
		t.Fatalf("swap = warning %q, error %v, want non-fatal cleanup warning", warning, err)
	}
	raw, readErr := os.ReadFile(filepath.Join(siteRoot, "index.html"))
	if readErr != nil || string(raw) != "new-complete-tree" {
		t.Fatalf("public tree after cleanup failure = %q, %v, want new complete tree", raw, readErr)
	}
}
