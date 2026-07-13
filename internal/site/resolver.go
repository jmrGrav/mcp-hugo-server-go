package site

import (
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

type ResolvedPage struct {
	Public     *Page
	Source     *hugosite.SourcePage
	SourcePath string
}

type PageResolver struct {
	idx    *Index
	srcIdx *hugosite.SourceIndex
	cfg    config.Config
}

func NewPageResolver(idx *Index, srcIdx *hugosite.SourceIndex, cfg config.Config) *PageResolver {
	return &PageResolver{idx: idx, srcIdx: srcIdx, cfg: cfg}
}

func (r *PageResolver) Resolve(rawSlug string) (ResolvedPage, bool) {
	publicSlug, sourceSlug := normalizeResolverSlugs(rawSlug)
	if publicSlug == "" && sourceSlug == "" {
		return ResolvedPage{}, false
	}

	var out ResolvedPage
	resolvedLang := languagePrefixFromSlug(publicSlug)
	if r != nil && r.idx != nil {
		if p, ok := r.idx.GetBySlug(publicSlug); ok {
			out.Public = p
			resolvedLang = p.Lang
			_, sourceSlug = normalizeResolverSlugs(p.Slug)
		}
	}
	if r != nil && r.srcIdx != nil {
		if p, ok := r.resolveSource(sourceSlug, resolvedLang); ok {
			out.Source = p
			out.SourcePath = p.FilePath
			if out.Public == nil && r.idx != nil {
				if pub, ok := r.idx.GetBySlug("/" + p.Slug + "/"); ok {
					out.Public = pub
				}
			}
		}
	}
	return out, out.Public != nil || out.Source != nil
}

func (r *PageResolver) resolveSource(sourceSlug, lang string) (*hugosite.SourcePage, bool) {
	if r == nil || r.srcIdx == nil {
		return nil, false
	}
	for _, candidate := range SourceSlugCandidates(sourceSlug) {
		if lang != "" {
			if p, ok := r.srcIdx.GetBySlugLang(candidate, lang); ok {
				return p, true
			}
		}
	}
	for _, candidate := range SourceSlugCandidates(sourceSlug) {
		if lang != "" {
			if p, ok := r.srcIdx.GetDefaultBySlug(candidate); ok {
				return p, true
			}
		}
	}
	for _, candidate := range SourceSlugCandidates(sourceSlug) {
		if p, ok := r.srcIdx.GetBySlug(candidate); ok {
			return p, true
		}
	}
	return nil, false
}

// SourceSlugCandidates returns the slug lookup keys to try against the source
// index for a given public-page slug, in priority order. It always returns the
// bare slug first; if the slug carries a language prefix (e.g. "en/posts/foo"),
// the prefix-stripped form ("posts/foo") is appended as a fallback. Returns nil
// for an empty input. Callers must break on the first match.
func SourceSlugCandidates(sourceSlug string) []string {
	sourceSlug = strings.Trim(sourceSlug, "/")
	if sourceSlug == "" {
		return nil
	}
	out := []string{sourceSlug}
	parts := strings.Split(sourceSlug, "/")
	langless := strings.Join(stripLanguagePrefix(parts), "/")
	if langless != "" && langless != sourceSlug {
		out = append(out, langless)
	}
	return out
}

func normalizeResolverSlugs(raw string) (publicSlug, sourceSlug string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	publicSlug = normalizeSlug(raw)
	sourceSlug = strings.Trim(publicSlug, "/")
	return publicSlug, sourceSlug
}

func languagePrefixFromSlug(slug string) string {
	parts := strings.Split(strings.Trim(slug, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	stripped := stripLanguagePrefix(parts)
	if len(stripped) == len(parts) {
		return ""
	}
	return parts[0]
}
