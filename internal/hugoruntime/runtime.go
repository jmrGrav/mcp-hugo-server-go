// Package hugoruntime owns scope-neutral Hugo binary and theme resolution.
package hugoruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

const probeTimeout = 5 * time.Second

var versionPattern = regexp.MustCompile(`v(\d+\.\d+\.\d+(?:-\S+)?)`)

const (
	cssScanMaxFiles     = 500
	cssScanMaxTotalSize = 4 << 20
	cssScanMaxFileSize  = 1 << 20
)

var (
	cssBlockRe      = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	cssOverflowXRe  = regexp.MustCompile(`overflow-x\s*:\s*(auto|scroll)`)
	cssStyleFileExt = map[string]bool{".css": true, ".scss": true, ".sass": true}
)

// Status describes the locally resolved Hugo binary.
type Status struct {
	Available bool
	Version   string
	Extended  bool
	Error     string
}

// Probe runs a bounded `hugo version` command using a minimal environment.
func Probe(ctx context.Context, cfg config.Config) Status {
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, "hugo", "version")
	cmd.Env = boundedCommandEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = err.Error()
		}
		return Status{Error: sanitise(reason, cfg.HugoRoot, cfg.SiteRoot)}
	}
	text := strings.TrimSpace(string(out))
	status := Status{Available: true, Extended: strings.Contains(text, "+extended")}
	if match := versionPattern.FindStringSubmatch(text); len(match) == 2 {
		status.Version = match[1]
	}
	return status
}

// VersionString reports the resolved Hugo semantic version, or an empty
// string when Hugo is unavailable or its output cannot be parsed.
func VersionString(ctx context.Context, cfg config.Config) string {
	return Probe(ctx, cfg).Version
}

// ResolveThemeNames resolves active classic or module themes from Hugo's
// effective JSON configuration.
func ResolveThemeNames(ctx context.Context, cfg config.Config) (names []string, source string, errText string) {
	if strings.TrimSpace(cfg.HugoRoot) == "" {
		return nil, "", "hugo_root is not configured"
	}
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, "hugo", "config", "--format", "json")
	cmd.Dir = cfg.HugoRoot
	cmd.Env = boundedCommandEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, "", sanitise(reason, cfg.HugoRoot, cfg.SiteRoot)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, "", "hugo config output was not valid JSON"
	}
	if theme, ok := parsed["theme"]; ok {
		switch value := theme.(type) {
		case string:
			if value != "" {
				return []string{value}, "themes_dir", ""
			}
		case []any:
			for _, item := range value {
				if name, ok := item.(string); ok && name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				return names, "themes_dir", ""
			}
		}
	}
	if module, ok := parsed["module"].(map[string]any); ok {
		if imports, ok := module["imports"].([]any); ok {
			for _, item := range imports {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if name, ok := entry["path"].(string); ok && name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				return names, "hugo_module", ""
			}
		}
	}
	return nil, "", ""
}

// ResolvedThemeLayoutDirs returns existing layout roots for active classic
// themes. Hugo module themes are intentionally excluded.
func ResolvedThemeLayoutDirs(ctx context.Context, cfg config.Config) []string {
	names, source, _ := ResolveThemeNames(ctx, cfg)
	if source != "themes_dir" {
		return nil
	}
	dirs := make([]string, 0, len(names))
	for _, name := range names {
		dir := filepath.Join(cfg.HugoRoot, "themes", name, "layouts")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// TableOverflowProtection reports whether an inspectable active classic
// theme contains a table-scoped horizontal-overflow rule. Nil means no
// classic theme could be inspected.
func TableOverflowProtection(ctx context.Context, cfg config.Config) *bool {
	names, source, _ := ResolveThemeNames(ctx, cfg)
	return TableOverflowProtectionForThemes(cfg, names, source)
}

// TableOverflowProtectionForThemes applies the overflow scan to a previously
// resolved theme set, avoiding a second Hugo config probe for callers that
// already need the active theme metadata.
func TableOverflowProtectionForThemes(cfg config.Config, names []string, source string) *bool {
	if source != "themes_dir" {
		return nil
	}
	inspected := false
	for _, name := range names {
		found, ok := scanThemeCSS(filepath.Join(cfg.HugoRoot, "themes", name))
		if !ok {
			continue
		}
		inspected = true
		if found {
			value := true
			return &value
		}
	}
	if !inspected {
		return nil
	}
	value := false
	return &value
}

func scanThemeCSS(themeDir string) (found bool, ok bool) {
	filesSeen, bytesSeen := 0, 0
	_ = filepath.Walk(themeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if found || filesSeen >= cssScanMaxFiles || bytesSeen >= cssScanMaxTotalSize {
			if info.IsDir() && found {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() || !cssStyleFileExt[strings.ToLower(filepath.Ext(path))] || info.Size() > cssScanMaxFileSize {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, cssScanMaxFileSize))
		if err != nil {
			return nil
		}
		filesSeen++
		bytesSeen += len(content)
		ok = true
		for _, match := range cssBlockRe.FindAllStringSubmatch(string(content), -1) {
			if strings.Contains(strings.ToLower(match[1]), "table") && cssOverflowXRe.MatchString(match[2]) {
				found = true
				break
			}
		}
		return nil
	})
	return found, ok
}

func boundedCommandEnv() []string {
	env := make([]string, 0, 5)
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func sanitise(text, hugoRoot, siteRoot string) string {
	if hugoRoot != "" {
		text = strings.ReplaceAll(text, hugoRoot, "<site_root>")
	}
	if siteRoot != "" && siteRoot != hugoRoot {
		text = strings.ReplaceAll(text, siteRoot, "<site_root>")
	}
	raw := []byte(text)
	if len(raw) > 500 {
		raw = raw[:500]
		for len(raw) > 0 && raw[len(raw)-1]&0xC0 == 0x80 {
			raw = raw[:len(raw)-1]
		}
		if len(raw) > 0 && raw[len(raw)-1]&0xC0 == 0xC0 {
			raw = raw[:len(raw)-1]
		}
	}
	return strings.TrimSpace(string(raw))
}
