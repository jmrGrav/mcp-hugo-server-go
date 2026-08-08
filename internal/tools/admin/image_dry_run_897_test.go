package admin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// #897: generate_hero_image was the only write tool without dry_run. These
// tests fail red against pre-fix code (dry_run was an unknown input field, so
// the call either errored or wrote a real file) and confirm that a dry run
// returns the full output contract while writing nothing to disk — in both the
// local-render and external-API code paths.

func assertDryRunContract(t *testing.T, data map[string]any) {
	t.Helper()
	want := map[string]string{
		"path":            "static/images/my-post-featured.jpg",
		"public_path":     "/images/my-post-featured.jpg",
		"source_key":      "my-post",
		"delete_slug":     "my-post",
		"delete_scope":    "generated",
		"delete_filename": "my-post-featured.jpg",
	}
	for k, v := range want {
		if got := data[k]; got != v {
			t.Fatalf("dry_run data.%s = %v, want %q; full data=%#v", k, got, v, data)
		}
	}
	if got := data["dry_run"]; got != true {
		t.Fatalf("dry_run data.dry_run = %v, want true", got)
	}
}

func TestGenerateHeroImageDryRunLocalWritesNoFile(t *testing.T) {
	hugoRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":    "my-post",
		"title":   "My Post",
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("dry_run returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	assertDryRunContract(t, data)

	// AC #2: no file (and no images dir side effect) written by the dry run.
	imagePath := filepath.Join(hugoRoot, "static", "images", "my-post-featured.jpg")
	if _, statErr := os.Stat(imagePath); !os.IsNotExist(statErr) {
		t.Fatalf("dry_run must not write %s (stat err = %v)", imagePath, statErr)
	}
}

func TestGenerateHeroImageDryRunAPIMakesNoWriteOrNetworkCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	}))
	defer srv.Close()

	hugoRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoRoot
	cfg.ImageGenURL = srv.URL
	cfg.ImageGenKey = "test-key"

	session, done := newTestServer(t, cfg)
	defer done()

	// prompt + ImageGenURL selects the external-API path.
	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":    "my-post",
		"prompt":  "a scenic mountain landscape",
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("dry_run returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	assertDryRunContract(t, data)

	if called {
		t.Fatal("dry_run must not call the external image API")
	}
	imagePath := filepath.Join(hugoRoot, "static", "images", "my-post-featured.jpg")
	if _, statErr := os.Stat(imagePath); !os.IsNotExist(statErr) {
		t.Fatalf("dry_run must not write %s (stat err = %v)", imagePath, statErr)
	}
}
