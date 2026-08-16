package admin

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

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getThemeStatusInput struct{}

type themeInfo struct {
	Name    string `json:"name"`
	Source  string `json:"source"` // "themes_dir" | "hugo_module"
	Present bool   `json:"present"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty"`
	Error   string `json:"error,omitempty"`
}

type themeStatusData struct {
	Themes []themeInfo `json:"themes"`
	Hugo   struct {
		Available bool   `json:"available"`
		Version   string `json:"version,omitempty"`
		Error     string `json:"error,omitempty"`
	} `json:"hugo"`
	// TableOverflowProtection reports whether the theme's own (unminified)
	// CSS/Sass source contains a rule scoping a table selector to
	// `overflow-x: auto|scroll` — the standard fix that lets a wide table
	// scroll horizontally on a narrow viewport instead of breaking layout.
	// This is a theme-constant property, computed once here rather than
	// per-page in inspect_rendered (see #1138). Three states, biased toward
	// the false negative over the false positive (a false "true" would hide
	// a real overflow problem from an agent, the class of mistake #1136
	// exists to prevent): true only when a table-scoped rule is found in an
	// introspectable classic themes_dir theme; false when at least one such
	// theme was inspected and none qualified; nil/omitted when no
	// themes_dir theme could be inspected at all (Hugo Module themes are
	// resolved through Hugo's own module cache, not a local checkout under
	// HugoRoot, so their CSS cannot be read here — see themeStatusFor's own
	// doc comment for the same module-introspection limit). Only the
	// theme's own source tree is scanned, not site-level override CSS
	// layered on top of it (e.g. assets/css/custom.css) — a known scope
	// limit, not a bug.
	TableOverflowProtection *bool `json:"table_overflow_protection,omitempty"`
}

type getThemeStatusOutput struct {
	toolcontract.ToolResponse[themeStatusData]
}

// RegisterThemeStatus wires get_theme_status (site.admin scope). Read-only:
// it never installs, updates, or fetches theme code — only reports what is
// already present on disk.
func RegisterThemeStatus(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_theme_status",
		Title: "Get theme status",
		Description: "Report the active Hugo theme(s) or module imports, whether their on-disk source is present, and " +
			"(for classic themes/ directory installs) the pinned Git commit and dirty/local-override state. Also reports " +
			"table_overflow_protection: whether theme CSS scrolls wide tables horizontally on narrow viewports " +
			"(true/false/omitted-if-unknown, e.g. Hugo Module themes). Read-only — never installs, updates, or fetches " +
			"theme code from a URL.",
		InputSchema:  tools.MustSchema[getThemeStatusInput](),
		OutputSchema: tools.MustSchema[getThemeStatusOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ getThemeStatusInput) (*mcp.CallToolResult, getThemeStatusOutput, error) {
		data := themeStatusData{Themes: []themeInfo{}}

		hugoVersion := probeHugo(ctx, cfg)
		data.Hugo.Available = hugoVersion.Available
		data.Hugo.Version = hugoVersion.Version
		data.Hugo.Error = hugoVersion.Error

		names, source, cfgErr := resolveThemeNames(ctx, cfg)
		if cfgErr != "" && data.Hugo.Available {
			data.Hugo.Error = cfgErr
		}
		for _, name := range names {
			data.Themes = append(data.Themes, themeStatusFor(ctx, cfg, name, source))
		}
		data.TableOverflowProtection = detectTableOverflowProtection(cfg, data.Themes)

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, getThemeStatusOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
}

// resolveThemeNames runs `hugo config --format json` (bounded env/timeout)
// and extracts theme names from either the classic `theme` key or Hugo
// Modules `module.imports`. Returns an empty slice (not an error) for a
// themeless site — that is a valid, common configuration.
func resolveThemeNames(ctx context.Context, cfg config.Config) (names []string, source string, errText string) {
	if strings.TrimSpace(cfg.HugoRoot) == "" {
		return nil, "", "hugo_root is not configured"
	}
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, "hugo", "config", "--format", "json")
	cmd.Dir = cfg.HugoRoot
	cmd.Env = boundedCommandEnv()
	// Stdout only: Hugo routinely writes deprecation warnings to stderr on
	// every invocation (e.g. the languageCode/languageName renames as of
	// v0.158.0), completely independent of whether the config itself is
	// valid. CombinedOutput would merge those warnings into the JSON blob
	// this function must parse, breaking it deterministically on any config
	// that trips a warning — which is not a "config is invalid" condition
	// at all. Only stderr goes into the error report, and only on failure.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, "", sanitiseStderr([]byte(reason), cfg.HugoRoot, cfg.SiteRoot)
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		return nil, "", "hugo config output was not valid JSON"
	}

	if themeVal, ok := parsed["theme"]; ok {
		switch v := themeVal.(type) {
		case string:
			if v != "" {
				return []string{v}, "themes_dir", ""
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				return names, "themes_dir", ""
			}
		}
	}

	if moduleVal, ok := parsed["module"].(map[string]any); ok {
		if imports, ok := moduleVal["imports"].([]any); ok {
			for _, imp := range imports {
				impMap, ok := imp.(map[string]any)
				if !ok {
					continue
				}
				if path, ok := impMap["path"].(string); ok && path != "" {
					names = append(names, path)
				}
			}
			if len(names) > 0 {
				return names, "hugo_module", ""
			}
		}
	}

	return nil, "", ""
}

// themeStatusFor inspects an on-disk classic theme directory for a Git
// commit/dirty state. Hugo Module imports are resolved via Hugo's own module
// cache, not a plain checkout under HugoRoot, so no local presence or Git
// state can be reliably reported for them without duplicating Hugo's module
// resolution logic — that is intentionally left as "present: true" (Hugo
// itself already resolved and is using it) with no commit/dirty fields.
func themeStatusFor(ctx context.Context, cfg config.Config, name, source string) themeInfo {
	info := themeInfo{Name: name, Source: source}
	if source != "themes_dir" {
		info.Present = true
		return info
	}

	themeDir := filepath.Join(cfg.HugoRoot, "themes", name)
	if fi, err := os.Stat(themeDir); err != nil || !fi.IsDir() {
		info.Present = false
		return info
	}
	info.Present = true

	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	commit, err := gitStatusOutput(tctx, themeDir, "rev-parse", "--short", "HEAD")
	if err != nil {
		info.Error = sanitiseGitError(err, cfg, themeDir)
		return info
	}
	info.Commit = commit

	porcelain, err := gitStatusOutput(tctx, themeDir, "status", "--porcelain")
	if err == nil {
		info.Dirty = strings.TrimSpace(porcelain) != ""
	}
	return info
}

const (
	cssScanMaxFiles     = 500
	cssScanMaxTotalSize = 4 << 20 // 4 MiB
	cssScanMaxFileSize  = 1 << 20 // 1 MiB per file
)

var (
	cssBlockRe      = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	cssOverflowXRe  = regexp.MustCompile(`overflow-x\s*:\s*(auto|scroll)`)
	cssStyleFileExt = map[string]bool{".css": true, ".scss": true, ".sass": true}
)

// detectTableOverflowProtection scans each classic themes_dir theme's own
// CSS/Sass source for a rule whose selector contains "table" and whose body
// sets overflow-x to auto or scroll — the standard horizontal-scroll fix for
// wide tables on narrow viewports. See themeStatusData.TableOverflowProtection
// for the three-state contract this returns.
func detectTableOverflowProtection(cfg config.Config, themes []themeInfo) *bool {
	inspected := false
	for _, t := range themes {
		if t.Source != "themes_dir" || !t.Present {
			continue
		}
		themeDir := filepath.Join(cfg.HugoRoot, "themes", t.Name)
		found, ok := scanThemeCSSForTableOverflowProtection(themeDir)
		if !ok {
			continue
		}
		inspected = true
		if found {
			return fileutil.BoolPtr(true)
		}
	}
	if !inspected {
		return nil
	}
	return fileutil.BoolPtr(false)
}

// scanThemeCSSForTableOverflowProtection walks themeDir (bounded by file
// count and size) and returns (found, ok). ok is false only if no
// stylesheet under themeDir could be read at all (e.g. the directory does
// not exist) — a genuine "nothing to inspect" case, distinct from
// "inspected, found nothing" (found=false, ok=true).
func scanThemeCSSForTableOverflowProtection(themeDir string) (found bool, ok bool) {
	filesSeen := 0
	bytesSeen := 0
	readAny := false

	_ = filepath.Walk(themeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort scan; skip unreadable entries
		}
		if found || filesSeen >= cssScanMaxFiles || bytesSeen >= cssScanMaxTotalSize {
			if info.IsDir() && found {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !cssStyleFileExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if info.Size() > cssScanMaxFileSize {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()
		content, readErr := io.ReadAll(io.LimitReader(f, cssScanMaxFileSize))
		if readErr != nil {
			return nil
		}
		filesSeen++
		bytesSeen += len(content)
		readAny = true

		for _, m := range cssBlockRe.FindAllStringSubmatch(string(content), -1) {
			selector := strings.ToLower(m[1])
			body := m[2]
			if strings.Contains(selector, "table") && cssOverflowXRe.MatchString(body) {
				found = true
				break
			}
		}
		return nil
	})

	return found, readAny
}
