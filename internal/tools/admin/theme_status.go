package admin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
			"(for classic themes/ directory installs) the pinned Git commit and dirty/local-override state. Read-only — " +
			"never installs, updates, or fetches theme code from a URL.",
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
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
