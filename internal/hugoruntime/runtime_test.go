package hugoruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func writeMockHugo(t *testing.T, version, configJSON string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '%s\\n' '" + version + "'; else printf '%s\\n' '" + configJSON + "'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "hugo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func writeRawMockHugo(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hugo"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestProbeAndVersionString(t *testing.T) {
	writeMockHugo(t, "hugo v0.150.0+extended linux/amd64", `{}`)
	status := Probe(context.Background(), config.Config{})
	if !status.Available || !status.Extended || status.Version != "0.150.0" || status.Error != "" {
		t.Fatalf("Probe() = %+v", status)
	}
	if got := VersionString(context.Background(), config.Config{}); got != "0.150.0" {
		t.Fatalf("VersionString() = %q", got)
	}
}

func TestProbeUnavailableAndUnparseable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	status := Probe(context.Background(), config.Config{})
	if status.Available || status.Error == "" {
		t.Fatalf("unavailable Probe() = %+v", status)
	}
	writeMockHugo(t, "unexpected output", `{}`)
	status = Probe(context.Background(), config.Config{})
	if !status.Available || status.Version != "" || status.Extended {
		t.Fatalf("unparseable Probe() = %+v", status)
	}
}

func TestResolvedThemePrimitives(t *testing.T) {
	root := t.TempDir()
	layouts := filepath.Join(root, "themes", "demo", "layouts")
	cssDir := filepath.Join(root, "themes", "demo", "assets")
	if err := os.MkdirAll(layouts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cssDir, "style.css"), []byte(".article table { overflow-x: auto; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMockHugo(t, "hugo v0.150.0", `{"theme":"demo"}`)
	cfg := config.Config{HugoRoot: root}
	dirs := ResolvedThemeLayoutDirs(context.Background(), cfg)
	if len(dirs) != 1 || dirs[0] != layouts {
		t.Fatalf("ResolvedThemeLayoutDirs() = %v", dirs)
	}
	if got := TableOverflowProtection(context.Background(), cfg); got == nil || !*got {
		t.Fatalf("TableOverflowProtection() = %v", got)
	}
}

func TestResolveThemeNamesExcludesModuleLayouts(t *testing.T) {
	root := t.TempDir()
	writeMockHugo(t, "hugo v0.150.0", `{"module":{"imports":[{"path":"example.com/theme"}]}}`)
	names, source, errText := ResolveThemeNames(context.Background(), config.Config{HugoRoot: root})
	if errText != "" || source != "hugo_module" || len(names) != 1 || names[0] != "example.com/theme" {
		t.Fatalf("ResolveThemeNames() = %v, %q, %q", names, source, errText)
	}
	if dirs := ResolvedThemeLayoutDirs(context.Background(), config.Config{HugoRoot: root}); dirs != nil {
		t.Fatalf("ResolvedThemeLayoutDirs() = %v, want nil", dirs)
	}
}

func TestResolveThemeNamesVariantsAndErrors(t *testing.T) {
	if _, _, got := ResolveThemeNames(context.Background(), config.Config{}); got != "hugo_root is not configured" {
		t.Fatalf("missing root error = %q", got)
	}

	root := t.TempDir()
	writeRawMockHugo(t, "printf '%s\\n' '"+root+"/secret failure' >&2\nexit 1\n")
	if _, _, got := ResolveThemeNames(context.Background(), config.Config{HugoRoot: root}); got != "<site_root>/secret failure" {
		t.Fatalf("redacted command error = %q", got)
	}

	writeMockHugo(t, "hugo v0.150.0", `{not-json}`)
	if _, _, got := ResolveThemeNames(context.Background(), config.Config{HugoRoot: root}); got != "hugo config output was not valid JSON" {
		t.Fatalf("malformed config error = %q", got)
	}

	writeMockHugo(t, "hugo v0.150.0", `{"theme":[null,"one","two"]}`)
	names, source, got := ResolveThemeNames(context.Background(), config.Config{HugoRoot: root})
	if got != "" || source != "themes_dir" || len(names) != 2 {
		t.Fatalf("theme array = %v, %q, %q", names, source, got)
	}

	writeMockHugo(t, "hugo v0.150.0", `{"module":{"imports":[null,{"disabled":true},{"path":"module/theme"}]}}`)
	names, source, got = ResolveThemeNames(context.Background(), config.Config{HugoRoot: root})
	if got != "" || source != "hugo_module" || len(names) != 1 {
		t.Fatalf("module array = %v, %q, %q", names, source, got)
	}

	writeMockHugo(t, "hugo v0.150.0", `{}`)
	names, source, got = ResolveThemeNames(context.Background(), config.Config{HugoRoot: root})
	if got != "" || source != "" || names != nil {
		t.Fatalf("themeless config = %v, %q, %q", names, source, got)
	}
}

func TestThemeLayoutAndOverflowUnknownOrFalse(t *testing.T) {
	root := t.TempDir()
	writeMockHugo(t, "hugo v0.150.0", `{"theme":"missing"}`)
	cfg := config.Config{HugoRoot: root}
	if dirs := ResolvedThemeLayoutDirs(context.Background(), cfg); len(dirs) != 0 {
		t.Fatalf("missing layout dirs = %v", dirs)
	}
	if got := TableOverflowProtection(context.Background(), cfg); got != nil {
		t.Fatalf("missing theme overflow = %v", got)
	}

	themeDir := filepath.Join(root, "themes", "missing")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "style.css"), []byte(".wide { overflow-x: auto; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := TableOverflowProtectionForThemes(cfg, []string{"missing"}, "themes_dir"); got == nil || *got {
		t.Fatalf("inspected false overflow = %v", got)
	}
	if got := TableOverflowProtectionForThemes(cfg, []string{"module/theme"}, "hugo_module"); got != nil {
		t.Fatalf("module overflow = %v", got)
	}
}

func TestSanitiseBoundsAndEnvironment(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("LANG", "C")
	if got := boundedCommandEnv(); len(got) < 2 {
		t.Fatalf("boundedCommandEnv() = %v", got)
	}
	long := "/site/" + string(make([]byte, 600))
	if got := sanitise(long, "/site", ""); len(got) > 500 || !strings.HasPrefix(got, "<site_root>") {
		t.Fatalf("sanitise() length=%d prefix=%q", len(got), got[:min(len(got), 16)])
	}
}
