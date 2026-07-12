package admin

import (
	"os"
	"path/filepath"
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
