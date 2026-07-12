package security_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func FuzzPathGuardSafeJoin(f *testing.F) {
	seeds := []string{
		"page.md",
		"posts/demo/index.md",
		"",
		".hidden/file",
		"../etc/passwd",
		"..%2fetc%2fpasswd",
		"/absolute/path",
		"double//slash",
		`back\slash\path`,
		"posts/../escape",
		"posts/.git/config",
		"posts/demo/index.fr.md",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rel string) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "posts", "demo"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "posts", "demo", "index.md"), []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		pg, err := security.New(root, true)
		if err != nil {
			t.Fatalf("security.New: %v", err)
		}

		got, err := pg.SafeJoin(rel)
		if err != nil {
			return
		}
		if !pg.WithinRoot(got) {
			t.Fatalf("SafeJoin returned path outside root: rel=%q got=%q root=%q", rel, got, root)
		}
		if err := pg.RevalidateForWrite(got); err != nil {
			t.Fatalf("RevalidateForWrite rejected SafeJoin output: rel=%q got=%q err=%v", rel, got, err)
		}
	})
}
