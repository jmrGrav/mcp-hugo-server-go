package admin_test

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	FilesScanned           int         `json:"files_scanned"`
	FilesWithSRIAttributes int         `json:"files_with_sri_attributes"`
	SRIEntriesLoaded       int         `json:"sri_entries_loaded"`
	SRIChecked             int         `json:"sri_checked"`
	Status                 string      `json:"status"`
	Summary                string      `json:"summary"`
	Findings               []sriResult `json:"findings"`
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
	if out.SRIChecked < 1 {
		t.Fatal("expected at least 1 SRI check")
	}
	r := out.Findings[0]
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
	if out.SRIChecked < 1 {
		t.Fatal("expected at least 1 SRI check")
	}
	r := out.Findings[0]
	if r.Error != "" {
		t.Fatalf("unexpected error in result: %s", r.Error)
	}
	if r.Match {
		t.Errorf("expected match=false, got true (template=%s current=%s)", r.TemplateHash, r.CurrentHash)
	}
}

func TestSRICheckSummaryFields(t *testing.T) {
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

	if out.FilesScanned < 1 {
		t.Errorf("expected files_scanned >= 1, got %d", out.FilesScanned)
	}
	if out.FilesWithSRIAttributes != 1 {
		t.Errorf("expected files_with_sri_attributes == 1, got %d", out.FilesWithSRIAttributes)
	}
	if out.SRIChecked != 1 {
		t.Errorf("expected sri_checked == 1, got %d", out.SRIChecked)
	}
	if out.SRIEntriesLoaded != 0 {
		t.Errorf("expected sri_entries_loaded == 0 for layouts-only fixture, got %d", out.SRIEntriesLoaded)
	}
	if out.Status != "clean" {
		t.Errorf("expected status clean, got %q", out.Status)
	}
	if out.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(out.Summary, "passed") && !strings.Contains(out.Summary, "mismatch") {
		t.Errorf("summary should contain 'passed' or 'mismatch', got %q", out.Summary)
	}
	if out.Summary != "All 1 SRI integrity check(s) passed." {
		t.Errorf("unexpected summary: %q", out.Summary)
	}
}

func TestSRICheckLoadsDataSRIYAML(t *testing.T) {
	hugoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hugoRoot, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	body := []byte("console.log('from data file');")
	hash := computeTestSHA384(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(hugoRoot, "data", "sri.yaml"), []byte(`
"`+srv.URL+`/fontawesome.css": "`+hash+`"
assets:
  theme:
    url: `+srv.URL+`/theme.js
    integrity: `+hash+`
`), 0o644); err != nil {
		t.Fatalf("write sri.yaml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(hugoRoot, "layouts"), 0o755); err != nil {
		t.Fatalf("mkdir layouts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hugoRoot, "layouts", "plugin.html"), []byte(`{{ with index site.Data.sri $src }}integrity="{{ . }}"{{ end }}`), 0o644); err != nil {
		t.Fatalf("write plugin.html: %v", err)
	}

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

	var out sriOutput
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, resultText(res))
	}
	if out.SRIEntriesLoaded != 2 {
		t.Fatalf("sri_entries_loaded = %d want 2", out.SRIEntriesLoaded)
	}
	if out.SRIChecked != 2 {
		t.Fatalf("sri_checked = %d want 2 when layouts use site.Data.sri", out.SRIChecked)
	}
	if out.Status != "clean" {
		t.Fatalf("status = %q want clean", out.Status)
	}
	if out.FilesWithSRIAttributes != 1 {
		t.Fatalf("files_with_sri_attributes = %d want 1", out.FilesWithSRIAttributes)
	}
	if out.Summary == "" || !strings.Contains(out.Summary, "passed") {
		t.Fatalf("summary = %q want passed summary", out.Summary)
	}
}

func TestSRICheckMissingDataAndNoAttributesIsNotConfigured(t *testing.T) {
	hugoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hugoRoot, "layouts"), 0o755); err != nil {
		t.Fatalf("mkdir layouts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hugoRoot, "layouts", "base.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatalf("write base.html: %v", err)
	}

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

	var out sriOutput
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, resultText(res))
	}
	if out.Status != "not_configured" {
		t.Fatalf("status = %q want not_configured", out.Status)
	}
}
