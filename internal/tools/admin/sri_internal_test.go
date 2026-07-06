package admin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestSRIHelperBranches(t *testing.T) {
	if _, err := runSRICheck(context.Background(), config.Config{}); err == nil {
		t.Fatal("runSRICheck() should fail without hugo_root")
	}

	if pairs, _, err := scanDirForSRI(filepath.Join(t.TempDir(), "missing")); err != nil || len(pairs) != 0 {
		t.Fatalf("scanDirForSRI(missing) = %#v, %v", pairs, err)
	}

	pairs := extractSRIPairs(`<script src="https://cdn.example/test.js" integrity="sha384-abc"></script>`)
	if len(pairs) != 1 || !strings.Contains(pairs[0].URL, "cdn.example") {
		t.Fatalf("extractSRIPairs() = %#v", pairs)
	}

	escapedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(escapedDir, "index.html"), []byte(`<script src="https://cdn.example/escaped.js" integrity="sha256-FCivwg/IXcwt&#43;HIkbvbFqRh6By8rh8u2qRrw4imaZNY="></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	escapedPairs, _, err := scanDirForSRI(escapedDir)
	if err != nil {
		t.Fatalf("scanDirForSRI(escaped) error = %v", err)
	}
	if len(escapedPairs) != 1 || escapedPairs[0].Hash != "sha256-FCivwg/IXcwt+HIkbvbFqRh6By8rh8u2qRrw4imaZNY=" {
		t.Fatalf("scanDirForSRI(escaped) = %#v", escapedPairs)
	}

	mixedPairs := extractSRIPairs(`<script src="/js/local.js" integrity="sha256-localhash"></script><script src="https://cdn.example/remote.js" integrity="sha256-remotehash"></script>`)
	if len(mixedPairs) != 1 {
		t.Fatalf("extractSRIPairs(mixed) count = %d want 1 (%#v)", len(mixedPairs), mixedPairs)
	}
	if mixedPairs[0].URL != "https://cdn.example/remote.js" || mixedPairs[0].Hash != "sha256-remotehash" {
		t.Fatalf("extractSRIPairs(mixed) unexpected pair: %#v", mixedPairs)
	}

	entry := verifySRIEntry(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "sha384-abc")
	if entry.Error == "" {
		t.Fatal("verifySRIEntry() should surface request errors")
	}

	entries, err := loadSRIDataFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("loadSRIDataFile(missing) = %#v, %v", entries, err)
	}

	if !sriScannableFile("layouts/baseof.html") || !sriScannableFile("public/index.xml") {
		t.Fatal("sriScannableFile should allow html/xml")
	}
	if sriScannableFile("themes/package-lock.json") || sriScannableFile("assets/app.js") {
		t.Fatal("sriScannableFile should skip json/js assets")
	}
}
