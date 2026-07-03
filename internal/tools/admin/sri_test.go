package admin_test

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func computeTestSHA384(data []byte) string {
	h := sha512.New384()
	h.Write(data)
	return "sha384-" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type sriResult struct {
	URL          string `json:"url"`
	TemplateHash string `json:"template_hash"`
	CurrentHash  string `json:"current_hash"`
	Match        bool   `json:"match"`
	Error        string `json:"error"`
}

type sriOutput struct {
	Results []sriResult `json:"results"`
}

func setupSRILayout(t *testing.T, url, hash string) string {
	t.Helper()
	hugoRoot := t.TempDir()
	layoutsDir := filepath.Join(hugoRoot, "layouts")
	if err := os.MkdirAll(layoutsDir, 0o755); err != nil {
		t.Fatalf("mkdir layouts: %v", err)
	}
	content := `<script src="` + url + `" integrity="` + hash + `"></script>`
	if err := os.WriteFile(filepath.Join(layoutsDir, "base.html"), []byte(content), 0o644); err != nil {
		t.Fatalf("write base.html: %v", err)
	}
	return hugoRoot
}

func TestSRIMatchingHash(t *testing.T) {
	body := []byte("console.log('hello');")
	correctHash := computeTestSHA384(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	hugoRoot := setupSRILayout(t, srv.URL+"/lib.js", correctHash)

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "check_sri_versions", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	text := resultText(res)
	var out sriOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, text)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	r := out.Results[0]
	if r.Error != "" {
		t.Fatalf("unexpected error in result: %s", r.Error)
	}
	if !r.Match {
		t.Errorf("expected match=true, got false (template=%s current=%s)", r.TemplateHash, r.CurrentHash)
	}
}

func TestSRIMismatchHash(t *testing.T) {
	realBody := []byte("console.log('hello');")
	wrongHash := "sha384-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(realBody)
	}))
	defer srv.Close()

	hugoRoot := setupSRILayout(t, srv.URL+"/lib.js", wrongHash)

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "check_sri_versions", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	text := resultText(res)
	var out sriOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, text)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	r := out.Results[0]
	if r.Error != "" {
		t.Fatalf("unexpected error in result: %s", r.Error)
	}
	if r.Match {
		t.Errorf("expected match=false, got true (template=%s current=%s)", r.TemplateHash, r.CurrentHash)
	}
}
