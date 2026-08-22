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

// TestExtractSRIPairsUnquotedAttributes guards #1252: Hugo's own --minify
// flag (tdewolff/minify's HTML minifier) strips quotes from attribute
// values that don't need them by default — href=https://... rather than
// href="https://..." — which is legal HTML5, observed live in production
// (the exact tag shape below is copied from a real minified page). This
// must still be recognized, not silently dropped from the scan.
func TestExtractSRIPairsUnquotedAttributes(t *testing.T) {
	pairs := extractSRIPairs(`<link rel=preload href=https://cdn.jsdelivr.net/npm/@fortawesome/fontawesome-free@7.3.1/css/all.min.css crossorigin=anonymous integrity="sha256-4Lad8m4ZWW1Lgb9+sMVLYEfnIh7BjV1NQMEe79Pviks=" as=style>`)
	if len(pairs) != 1 {
		t.Fatalf("extractSRIPairs(unquoted href) count = %d, want 1 (%#v)", len(pairs), pairs)
	}
	if pairs[0].URL != "https://cdn.jsdelivr.net/npm/@fortawesome/fontawesome-free@7.3.1/css/all.min.css" {
		t.Fatalf("extractSRIPairs(unquoted href) URL = %q", pairs[0].URL)
	}
	if pairs[0].Hash != "sha256-4Lad8m4ZWW1Lgb9+sMVLYEfnIh7BjV1NQMEe79Pviks=" {
		t.Fatalf("extractSRIPairs(unquoted href) Hash = %q", pairs[0].Hash)
	}

	// integrity itself can also appear unquoted; verified tolerant even
	// though real tdewolff output has not been observed to strip quotes
	// from integrity's own value (it contains '/' and '=', which forces
	// quoting) — defense in depth against a future minifier config change.
	unquotedIntegrity := extractSRIPairs(`<script src=https://cdn.example/test.js integrity=sha384-abc></script>`)
	if len(unquotedIntegrity) != 1 || unquotedIntegrity[0].Hash != "sha384-abc" {
		t.Fatalf("extractSRIPairs(unquoted integrity) = %#v", unquotedIntegrity)
	}

	// Quoted attributes (the pre-existing, still-common case) keep working.
	quoted := extractSRIPairs(`<script src="https://cdn.example/test.js" integrity="sha384-abc"></script>`)
	if len(quoted) != 1 || quoted[0].URL != "https://cdn.example/test.js" {
		t.Fatalf("extractSRIPairs(quoted) = %#v", quoted)
	}
}
