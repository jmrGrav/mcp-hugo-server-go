package admin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// writeMockHugoDispatch writes a mock `hugo` binary that dispatches on its
// first argument, so a single test server can exercise both `hugo version`
// (used by get_runtime_status/get_theme_status) and `hugo config --format
// json` (used only by get_theme_status) with different canned output.
func writeMockHugoDispatch(t *testing.T, versionOutput, configOutput string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"version\" ]; then\n  echo '" + versionOutput + "'\n  exit 0\n" +
		"elif [ \"$1\" = \"config\" ]; then\n  echo '" + configOutput + "'\n  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	p := filepath.Join(dir, "hugo")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	return dir
}

func TestGetThemeStatusClassicThemeDirReportsGitState(t *testing.T) {
	hugoRoot := t.TempDir()
	hugoDir := writeMockHugoDispatch(t, "hugo v0.150.0 linux/amd64", `{"theme":"PaperMod"}`)
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	themeDir := filepath.Join(hugoRoot, "themes", "PaperMod")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatalf("mkdir theme dir: %v", err)
	}
	runGitCmd(t, themeDir, "init")
	runGitCmd(t, themeDir, "config", "user.email", "test@example.test")
	runGitCmd(t, themeDir, "config", "user.name", "Test User")
	runGitCmd(t, themeDir, "commit", "--allow-empty", "-m", "vendor PaperMod")

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_theme_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	themes, ok := data["themes"].([]any)
	if !ok || len(themes) != 1 {
		t.Fatalf("themes = %#v, want 1 entry", data["themes"])
	}
	theme := themes[0].(map[string]any)
	if theme["name"] != "PaperMod" {
		t.Fatalf("theme.name = %v, want PaperMod", theme["name"])
	}
	if theme["source"] != "themes_dir" {
		t.Fatalf("theme.source = %v, want themes_dir", theme["source"])
	}
	if theme["present"] != true {
		t.Fatalf("theme.present = %v, want true", theme["present"])
	}
	if got, ok := theme["commit"].(string); !ok || got == "" {
		t.Fatalf("theme.commit = %v, want non-empty", theme["commit"])
	}
	if theme["dirty"] != false {
		t.Fatalf("theme.dirty = %v, want false", theme["dirty"])
	}

	hugo := data["hugo"].(map[string]any)
	if hugo["available"] != true {
		t.Fatalf("hugo.available = %v, want true", hugo["available"])
	}
}

func TestGetThemeStatusHugoModuleReportsPresentWithoutGitState(t *testing.T) {
	hugoDir := writeMockHugoDispatch(t, "hugo v0.150.0 linux/amd64",
		`{"module":{"imports":[{"path":"github.com/example/hugo-theme"}]}}`)
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_theme_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	themes := data["themes"].([]any)
	if len(themes) != 1 {
		t.Fatalf("themes = %#v, want 1 entry", themes)
	}
	theme := themes[0].(map[string]any)
	if theme["name"] != "github.com/example/hugo-theme" {
		t.Fatalf("theme.name = %v", theme["name"])
	}
	if theme["source"] != "hugo_module" {
		t.Fatalf("theme.source = %v, want hugo_module", theme["source"])
	}
	if theme["present"] != true {
		t.Fatalf("theme.present = %v, want true", theme["present"])
	}
	if _, present := theme["commit"]; present {
		t.Fatalf("hugo_module theme must not report a commit, got %v", theme["commit"])
	}
}

func TestGetThemeStatusNoThemeConfiguredReturnsEmptyList(t *testing.T) {
	hugoDir := writeMockHugoDispatch(t, "hugo v0.150.0 linux/amd64", `{}`)
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_theme_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("themeless site must not be an error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	themes, ok := data["themes"].([]any)
	if !ok || len(themes) != 0 {
		t.Fatalf("themes = %#v, want empty list for themeless site", data["themes"])
	}
}

func TestGetThemeStatusSurfacesHugoConfigError(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo 'hugo v0.150.0 linux/amd64'\n  exit 0\nfi\necho 'config error: bad frontmatter' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "hugo"), []byte(script), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_theme_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("a broken hugo config must degrade, not error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	hugo := data["hugo"].(map[string]any)
	if got, _ := hugo["error"].(string); got == "" {
		t.Fatal("expected hugo.error to explain the hugo config failure")
	}
	themes := data["themes"].([]any)
	if len(themes) != 0 {
		t.Fatalf("themes = %#v, want empty list when config probing failed", themes)
	}
}
