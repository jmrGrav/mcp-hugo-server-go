package admin

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeHashForTemplateAlgorithms(t *testing.T) {
	payload := []byte("hello world")

	got256, err := computeHashForTemplate(payload, "sha256-anything")
	if err != nil {
		t.Fatalf("computeHashForTemplate(sha256) error = %v", err)
	}
	sum256 := sha256.Sum256(payload)
	want256 := "sha256-" + base64.StdEncoding.EncodeToString(sum256[:])
	if got256 != want256 {
		t.Fatalf("sha256 hash = %q want %q", got256, want256)
	}

	got384, err := computeHashForTemplate(payload, "sha384-anything")
	if err != nil {
		t.Fatalf("computeHashForTemplate(sha384) error = %v", err)
	}
	if got384 != computeSHA384(payload) {
		t.Fatalf("sha384 hash = %q want computeSHA384()", got384)
	}

	got512, err := computeHashForTemplate(payload, "sha512-anything")
	if err != nil {
		t.Fatalf("computeHashForTemplate(sha512) error = %v", err)
	}
	sum512 := sha512.Sum512(payload)
	want512 := "sha512-" + base64.StdEncoding.EncodeToString(sum512[:])
	if got512 != want512 {
		t.Fatalf("sha512 hash = %q want %q", got512, want512)
	}

	if _, err := computeHashForTemplate(payload, "md5-nope"); err == nil || !strings.Contains(err.Error(), "unsupported SRI algorithm") {
		t.Fatalf("unsupported algorithm error = %v", err)
	}
}

func TestLoadSRIDataAndExtractHashes(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "sri.yaml")
	raw := `
entries:
  vendor:
    url: https://cdn.example/app.js
    integrity: sha384-abc
  other:
    https://cdn.example/site.css:
      hash: sha256-def
  nested:
    - url: https://cdn.example/image.js
      sha: sha512-ghi
    - "sha384-jkl"
`
	if err := os.WriteFile(dataPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(sri.yaml) error = %v", err)
	}

	entries, err := loadSRIDataFile(dataPath)
	if err != nil {
		t.Fatalf("loadSRIDataFile() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("loadSRIDataFile() count = %d want 3 (%#v)", len(entries), entries)
	}

	if hashes := extractHashes("sha256-one sha384-two"); len(hashes) != 2 {
		t.Fatalf("extractHashes(string) = %#v", hashes)
	}
	if hashes := extractHashes(map[string]any{"integrity": "sha512-three"}); len(hashes) != 1 || hashes[0] != "sha512-three" {
		t.Fatalf("extractHashes(map) = %#v", hashes)
	}
	if hashes := extractHashes(12); hashes != nil {
		t.Fatalf("extractHashes(non-supported) = %#v want nil", hashes)
	}
}

func TestVerifySRIEntryUnsupportedAlgorithm(t *testing.T) {
	entry := verifySRIEntry(context.Background(), http.DefaultClient, "data:text/plain,hello", "md5-nope")
	if entry.Error == "" {
		t.Fatal("verifySRIEntry() should surface unsupported hash errors")
	}
}
