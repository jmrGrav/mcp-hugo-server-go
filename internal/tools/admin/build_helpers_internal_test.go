package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestBuildHelperBranches(t *testing.T) {
	if got := commandString("hugo", nil); got != "hugo" {
		t.Fatalf("commandString(nil) = %q", got)
	}
	if got := commandString("hugo", []string{"--renderToMemory"}); got != "hugo --renderToMemory" {
		t.Fatalf("commandString(args) = %q", got)
	}

	cfg := config.Default()
	cfg.OAuth.StoragePath = filepath.Join(t.TempDir(), "state", "oauth.sqlite")
	if got := hugoCacheDir(cfg); !strings.HasSuffix(got, filepath.Join("state", "hugo-cache")) {
		t.Fatalf("hugoCacheDir(storage) = %q", got)
	}
	cfg.OAuth.StoragePath = ""
	if got := hugoCacheDir(cfg); !strings.Contains(got, "hugo-cache") {
		t.Fatalf("hugoCacheDir(default) = %q", got)
	}

	userName := currentUserForLog()
	if strings.TrimSpace(userName) == "" {
		t.Fatal("currentUserForLog() should not return empty string")
	}

	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	err := checkBuildWritable(file)
	if err == nil || !strings.Contains(err.Error(), "build_precondition_failed") {
		t.Fatalf("checkBuildWritable(file path) error = %v", err)
	}

	if got := buildPreflightChownError("/tmp/site"); !strings.Contains(got.Error(), "ownership") && !strings.Contains(got.Error(), "build_precondition_failed") {
		t.Fatalf("buildPreflightChownError() = %v", got)
	}
}

// TestCheckDirWritableIgnoresOwnershipMismatch guards the #981 production
// incident: SiteRoot's parent only needs to be writable (this process
// creates its own siblings there, so it always owns what it creates) — it
// must not be rejected merely because a *different* uid happens to own the
// directory itself. checkBuildWritable enforces ownership (correct for
// directories whose pre-existing files get chtimes'd, like the Hugo
// resources cache); checkDirWritable deliberately does not.
func TestCheckDirWritableIgnoresOwnershipMismatch(t *testing.T) {
	orig := geteuid
	geteuid = func() int { return -1 } // guaranteed not to match any real owner uid
	defer func() { geteuid = orig }()

	dir := t.TempDir() // owned by the real test-process uid, not -1
	if err := checkDirWritable(dir); err != nil {
		t.Fatalf("checkDirWritable() = %v, want nil (ownership must not be checked)", err)
	}
}

// TestCheckBuildWritableRejectsOwnershipMismatch is the converse of the
// above: checkBuildWritable's ownership enforcement must still fire for
// directories that do need it (e.g. the Hugo resources cache), or the
// chtimes-EPERM class of failure this preflight exists to catch would go
// undetected.
func TestCheckBuildWritableRejectsOwnershipMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownershipMismatch is a no-op on windows (build_ownership_windows.go)")
	}
	orig := geteuid
	geteuid = func() int { return -1 }
	defer func() { geteuid = orig }()

	dir := t.TempDir()
	err := checkBuildWritable(dir)
	if err == nil {
		t.Fatal("checkBuildWritable() = nil, want ownership-mismatch error")
	}
	if !strings.Contains(err.Error(), "build_precondition_failed") {
		t.Fatalf("checkBuildWritable() = %v, want build_precondition_failed", err)
	}
}
