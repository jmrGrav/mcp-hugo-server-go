package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func writeCSSFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestScanThemeCSSForTableOverflowProtectionFindsTableScopedRule(t *testing.T) {
	dir := t.TempDir()
	writeCSSFile(t, filepath.Join(dir, "assets", "css"), "main.css", `
.nav { display: flex; }
table.data { overflow-x: auto; width: 100%; }
`)

	found, ok := scanThemeCSSForTableOverflowProtection(dir)
	if !ok {
		t.Fatalf("expected ok=true (readable CSS found)")
	}
	if !found {
		t.Fatalf("expected found=true for a table-scoped overflow-x:auto rule")
	}
}

func TestScanThemeCSSForTableOverflowProtectionRejectsUnrelatedOverflowRule(t *testing.T) {
	dir := t.TempDir()
	writeCSSFile(t, filepath.Join(dir, "static", "css"), "style.css", `
.sidebar-scroll { overflow-x: auto; }
table.data { width: 100%; }
`)

	found, ok := scanThemeCSSForTableOverflowProtection(dir)
	if !ok {
		t.Fatalf("expected ok=true (readable CSS found)")
	}
	if found {
		t.Fatalf("overflow-x:auto on a non-table selector must not count as table protection")
	}
}

func TestScanThemeCSSForTableOverflowProtectionNoCSSFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "layouts"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, ok := scanThemeCSSForTableOverflowProtection(dir)
	if ok {
		t.Fatalf("expected ok=false when the theme has no stylesheet to inspect")
	}
}

func TestDetectTableOverflowProtectionUnknownForModuleThemes(t *testing.T) {
	cfg := config.Config{HugoRoot: t.TempDir()}
	themes := []themeInfo{{Name: "github.com/example/theme", Source: "hugo_module", Present: true}}

	got := detectTableOverflowProtection(cfg, themes)
	if got != nil {
		t.Fatalf("expected nil (unknown) for a Hugo Module theme with no local checkout, got %v", *got)
	}
}

func TestDetectTableOverflowProtectionTrueWhenAnyThemeProtects(t *testing.T) {
	root := t.TempDir()
	unprotected := filepath.Join(root, "themes", "a")
	protected := filepath.Join(root, "themes", "b")
	writeCSSFile(t, unprotected, "style.css", `table { width: 100%; }`)
	writeCSSFile(t, protected, "style.css", `table { overflow-x: auto; }`)

	cfg := config.Config{HugoRoot: root}
	themes := []themeInfo{
		{Name: "a", Source: "themes_dir", Present: true},
		{Name: "b", Source: "themes_dir", Present: true},
	}

	got := detectTableOverflowProtection(cfg, themes)
	if got == nil || !*got {
		t.Fatalf("expected true when at least one theme protects tables, got %v", got)
	}
}
