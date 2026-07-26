package security_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func TestSafeJoinNormal(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "page.md"), []byte("hello"), 0644)
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pg.SafeJoin("page.md")
	if err != nil {
		t.Fatal(err)
	}
	if !pg.WithinRoot(got) {
		t.Fatal("expected path within root")
	}
}

func TestSafeJoinTraversal(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestSafeJoinHiddenPath(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin(".hidden/file")
	if err == nil {
		t.Fatal("expected error for hidden path")
	}
}

func TestSafeJoinSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "link")
	os.Symlink(target, link)
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("link")
	if err == nil {
		t.Fatal("expected error for symlink when reject_symlinks=true")
	}
}

func TestSafeJoinEmptySlug(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("")
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}

// TestSafeJoinSymlinkParent verifies that a symlink in a parent directory
// component is rejected when rejectSymlinks is true (issue #33).
func TestSafeJoinSymlinkParent(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()

	// Create a symlink inside root pointing to the real dir.
	link := filepath.Join(root, "sub")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink (may need elevated perms): %v", err)
	}

	pg, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pg.SafeJoin("sub/file.md")
	if err == nil {
		t.Fatal("expected error when parent component is a symlink")
	}
}

// TestSafeJoinSymlinkParentAllowed verifies that parent symlinks pass when
// rejectSymlinks is false.
func TestSafeJoinSymlinkParentAllowed(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	link := filepath.Join(root, "sub")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	pg, err := security.New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pg.SafeJoin("sub/file.md")
	if err != nil {
		t.Fatalf("unexpected error with rejectSymlinks=false: %v", err)
	}
}

func TestNewFallsBackToAbsWhenRootMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-root")
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}
	got, err := pg.SafeJoin("page.md")
	if err != nil {
		t.Fatalf("SafeJoin() error = %v", err)
	}
	if !pg.WithinRoot(got) {
		t.Fatalf("expected %q to remain within root", got)
	}
}

// TestRevalidateForWriteCatchesTOCTOUSymlinkSwap is a regression test for the
// exact race RevalidateForWrite exists to close: SafeJoin validates at T1,
// but if an attacker swaps a path component for a symlink between T1 and the
// actual write at T2, SafeJoin's result alone can no longer be trusted.
// RevalidateForWrite must re-check and reject at T2.
func TestRevalidateForWriteCatchesTOCTOUSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	pg, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	path, err := pg.SafeJoin("sub/file.md")
	if err != nil {
		t.Fatalf("SafeJoin() error = %v, want success before the swap", err)
	}

	// Simulate the T1->T2 race: after SafeJoin approved this path, an
	// attacker replaces the "sub" directory with a symlink pointing
	// somewhere else entirely.
	escapeTarget := t.TempDir()
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeTarget, sub); err != nil {
		t.Skipf("cannot create symlink (may need elevated perms): %v", err)
	}

	if err := pg.RevalidateForWrite(path); err == nil {
		t.Fatal("RevalidateForWrite() should reject a path swapped to a symlink after SafeJoin approved it")
	}
}

// TestRevalidateForWriteNoOpWhenSymlinksAllowed confirms RevalidateForWrite
// is a deliberate no-op when the guard was constructed with
// rejectSymlinks=false, matching SafeJoin's own behavior for that mode.
func TestRevalidateForWriteNoOpWhenSymlinksAllowed(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	target := t.TempDir()
	if err := os.Symlink(target, sub); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	pg, err := security.New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "file.md")
	if err := pg.RevalidateForWrite(path); err != nil {
		t.Fatalf("RevalidateForWrite() error = %v, want nil when rejectSymlinks=false", err)
	}
}

// TestSafeJoinRejectsNonSymlinkStatError exercises rejectSymlinkComponents'
// stat-error branch (distinct from the not-exist branch it also handles):
// treating an existing file as if it were an intermediate directory
// component produces ENOTDIR, not ENOENT, and must still be rejected rather
// than silently skipped like a genuinely missing path.
func TestSafeJoinRejectsNonSymlinkStatError(t *testing.T) {
	root := t.TempDir()
	// "sub" is a regular file, not a directory, so treating it as a path
	// component to descend into ("sub/file.md") makes the OS return
	// ENOTDIR when lstat'ing the deeper path — not ENOENT.
	if err := os.WriteFile(filepath.Join(root, "sub"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	pg, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pg.SafeJoin("sub/file.md")
	if err == nil {
		t.Fatal("SafeJoin() should reject a path through a non-directory component")
	}
}

func TestWithinRootRejectsOutsidePath(t *testing.T) {
	root := t.TempDir()
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}
	if pg.WithinRoot(filepath.Join(filepath.Dir(root), "other", "file.md")) {
		t.Fatal("WithinRoot() should reject path outside root")
	}
}
