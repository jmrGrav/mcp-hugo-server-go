package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// observedRecoveryRevision and reconcileBundleFiles gate whether a
// mid-mutation crash gets rolled back or a queued cleanup runs — their
// genuine-error branches (as opposed to the well-covered
// not-found/happy-path branches exercised by the openSiteDB reconciliation
// tests above) were previously untested.

func TestObservedRecoveryRevisionNonBundleReadError(t *testing.T) {
	// os.ReadFile on a directory fails with a non-IsNotExist error,
	// exercising the "genuine ReadFile error" branch.
	dir := t.TempDir()
	_, found, err := observedRecoveryRevision(dir, false)
	if err == nil {
		t.Fatal("observedRecoveryRevision() error = nil, want an error reading a directory as a file")
	}
	if found {
		t.Fatal("observedRecoveryRevision() found = true, want false on error")
	}
}

func TestObservedRecoveryRevisionBundleStatError(t *testing.T) {
	// os.Stat on a path through a regular file (not a directory) fails
	// with ENOTDIR, a genuine non-IsNotExist error.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(notADir, "sub", "bundle")
	_, found, err := observedRecoveryRevision(path, true)
	if err == nil {
		t.Fatal("observedRecoveryRevision() error = nil, want an error statting through a non-directory path component")
	}
	if found {
		t.Fatal("observedRecoveryRevision() found = true, want false on error")
	}
}

func TestObservedRecoveryRevisionBundleNotExist(t *testing.T) {
	dir := t.TempDir()
	rev, found, err := observedRecoveryRevision(filepath.Join(dir, "missing-bundle"), true)
	if err != nil {
		t.Fatalf("observedRecoveryRevision() error = %v, want nil for a missing bundle dir", err)
	}
	if found {
		t.Fatal("observedRecoveryRevision() found = true, want false for a missing bundle dir")
	}
	if rev != "" {
		t.Fatalf("observedRecoveryRevision() rev = %q, want empty", rev)
	}
}

func TestReconcileBundleFilesEmptyPayloadIsNoop(t *testing.T) {
	resolved, changed, landed, err := reconcileBundleFiles(config.Config{}, sourceRecoveryPayload{})
	if err != nil {
		t.Fatalf("reconcileBundleFiles() error = %v", err)
	}
	if resolved || changed || landed {
		t.Fatalf("reconcileBundleFiles() = (%v,%v,%v), want all false for an empty payload", resolved, changed, landed)
	}
}

func TestReconcileBundleFilesRejectsPathOutsideContentRoot(t *testing.T) {
	contentRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	payload := sourceRecoveryPayload{
		BundleDir: contentRoot,
		Files:     []bundleRecoveryFilePayload{{Path: outside}},
	}
	resolved, changed, landed, err := reconcileBundleFiles(config.Config{ContentRoot: contentRoot}, payload)
	if err != nil {
		t.Fatalf("reconcileBundleFiles() error = %v, want nil (unresolved, not an error)", err)
	}
	if resolved || changed || landed {
		t.Fatalf("reconcileBundleFiles() = (%v,%v,%v), want all false when a file path escapes the content root", resolved, changed, landed)
	}
}
