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
