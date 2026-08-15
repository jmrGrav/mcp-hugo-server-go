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
