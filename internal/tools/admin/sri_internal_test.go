package admin

import (
	"context"
	"net/http"
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

	entry := verifySRIEntry(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "sha384-abc")
	if entry.Error == "" {
		t.Fatal("verifySRIEntry() should surface request errors")
	}

	entries, err := loadSRIDataFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("loadSRIDataFile(missing) = %#v, %v", entries, err)
	}
}
