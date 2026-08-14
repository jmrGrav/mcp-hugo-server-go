package read

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// implicitMultilingualResolutionWarning preserves the historical implicit
// resolution of a bare slug, while making it visible when that slug names a
// bundle with multiple translations (#1063). A language-prefixed slug already
// selects one translation, so it does not need the warning.
func implicitMultilingualResolutionWarning(rawSlug, explicitLang string, resolved site.ResolvedPage, srcIdx *hugosite.SourceIndex, cfg config.Config) string {
	if srcIdx == nil || resolved.Source == nil || strings.TrimSpace(explicitLang) != "" || strings.TrimSpace(resolved.RequestedLang) != "" {
		return ""
	}

	selected := strings.TrimSpace(resolved.Source.Lang)
	if selected == "" {
		selected = strings.TrimSpace(cfg.DefaultLanguage)
	}
	if selected == "" {
		return ""
	}

	languages := map[string]bool{}
	for _, candidate := range srcIdx.ListPages(0, 0) {
		if candidate.Slug != resolved.Source.Slug {
			continue
		}
		lang := strings.TrimSpace(candidate.Lang)
		if lang == "" {
			lang = strings.TrimSpace(cfg.DefaultLanguage)
		}
		if lang != "" {
			languages[lang] = true
		}
	}
	if len(languages) < 2 {
		return ""
	}

	available := make([]string, 0, len(languages))
	for lang := range languages {
		available = append(available, lang)
	}
	sort.Strings(available)
	return fmt.Sprintf("lang was not specified; bare slug resolved to %q while translations [%s] exist. Pass lang explicitly to select a translation.", selected, strings.Join(available, ", "))
}
