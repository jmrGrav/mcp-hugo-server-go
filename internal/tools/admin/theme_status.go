package admin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugoruntime"
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
		data.TableOverflowProtection = hugoruntime.TableOverflowProtectionForThemes(cfg, names, source)

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, getThemeStatusOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
}

// TableOverflowProtection resolves the active theme(s) the same way
// get_theme_status does and reports the same three-state
// table_overflow_protection result (see themeStatusData's doc comment for
// the full contract). Exported for get_site_health's opt-in
// responsive_summary aggregation (#1138 Part 2), which needs this
// theme-constant signal to decide fix_scope without duplicating the theme
// resolution/CSS-scanning logic.
func TableOverflowProtection(ctx context.Context, cfg config.Config) *bool {
	return hugoruntime.TableOverflowProtection(ctx, cfg)
}

// ResolvedThemeLayoutDirs returns the on-disk `layouts` directory of every
// classic themes_dir theme currently active, resolved the same way
// get_theme_status does. Exported for #1151's template-change fingerprint,
// which needs to hash every input that can change a page's rendered <head>
// output — the resolved theme's own layout tree is a separate resolution
// root from the site's local layouts/ overrides, so a fingerprint over only
// the local tree would miss a theme-side template regression.
//
// Hugo Module themes are intentionally excluded, same limitation as
// TableOverflowProtection/detectTableOverflowProtection above: they resolve
// through Hugo's own module cache, not a local checkout under HugoRoot, so
// there is no local directory here to hash. A fingerprint that can't see a
// module theme's layout changes is a known, accepted scope limit — not
// silently claiming coverage it doesn't have.
func ResolvedThemeLayoutDirs(ctx context.Context, cfg config.Config) []string {
	return hugoruntime.ResolvedThemeLayoutDirs(ctx, cfg)
}

// resolveThemeNames runs `hugo config --format json` (bounded env/timeout)
// and extracts theme names from either the classic `theme` key or Hugo
// Modules `module.imports`. Returns an empty slice (not an error) for a
// themeless site — that is a valid, common configuration.
func resolveThemeNames(ctx context.Context, cfg config.Config) (names []string, source string, errText string) {
	return hugoruntime.ResolveThemeNames(ctx, cfg)
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
