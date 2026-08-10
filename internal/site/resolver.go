package site

import (
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

type ResolvedPage struct {
	Public        *Page
	Source        *hugosite.SourcePage
	SourcePath    string
	RequestedSlug string
	RequestedLang string
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
	return r.ResolveWithLang(rawSlug, "")
}

func (r *PageResolver) ResolveWithLang(rawSlug, explicitLang string) (ResolvedPage, bool) {
	publicSlug, sourceSlug := normalizeResolverSlugs(rawSlug)
	if publicSlug == "" && sourceSlug == "" {
		return ResolvedPage{}, false
	}
	explicitLang = strings.TrimSpace(explicitLang)
	if explicitLang == "" {
		return r.resolveImplicit(publicSlug, sourceSlug)
	}
	if prefixedLang := languagePrefixFromSlug(publicSlug); prefixedLang != "" && prefixedLang != explicitLang {
		return ResolvedPage{}, false
	}

	out := ResolvedPage{RequestedSlug: publicSlug, RequestedLang: explicitLang}
	if r != nil && r.srcIdx != nil {
		if p, ok := r.resolveSourceForRequestedLang(sourceSlug, explicitLang); ok {
			out.Source = p
			out.SourcePath = p.FilePath
			sourceSlug = p.Slug
		}
	}
	if r != nil && r.idx != nil {
		if pub, ok := r.resolvePublicForSourceLang(sourceSlug, explicitLang); ok && pageMatchesExplicitLang(pub, explicitLang, r.cfg.DefaultLanguage) {
			out.Public = pub
		} else if p, ok := r.idx.GetBySlug(publicSlug); ok && pageMatchesExplicitLang(p, explicitLang, r.cfg.DefaultLanguage) {
			out.Public = p
			if out.Source == nil {
				_, sourceSlug = normalizeResolverSlugs(p.Slug)
			}
		}
	}
	if out.Source == nil && out.Public != nil && r != nil && r.srcIdx != nil {
		if p, ok := r.resolveSourceForRequestedLang(sourceSlug, explicitLang); ok {
			out.Source = p
			out.SourcePath = p.FilePath
		}
	}
	return out, out.Public != nil || out.Source != nil
}

func (r *PageResolver) resolveImplicit(publicSlug, sourceSlug string) (ResolvedPage, bool) {
	requestedLang := languagePrefixFromSlug(publicSlug)
	out := ResolvedPage{RequestedSlug: publicSlug, RequestedLang: requestedLang}
	resolvedLang := requestedLang
	if r != nil && r.idx != nil {
		if p, ok := r.idx.GetBySlug(publicSlug); ok {
			out.Public = p
			if strings.TrimSpace(p.Lang) != "" {
				resolvedLang = p.Lang
			}
			_, sourceSlug = normalizeResolverSlugs(p.Slug)
		}
	}
	if r != nil && r.srcIdx != nil {
		var p *hugosite.SourcePage
		var ok bool
		if requestedLang != "" {
			p, ok = r.resolveSourceExact(sourceSlug, resolvedLang)
			// Preserve legacy index.md bundles only when an exact public URL
			// proves which language that unlabelled source rendered as. Without
			// that proof, falling back here is the cross-language bug: /en/ can
			// silently borrow the default-language source and public page.
			if !ok && out.Public != nil && (strings.TrimSpace(out.Public.Lang) == "" || strings.TrimSpace(resolvedLang) == strings.TrimSpace(r.cfg.DefaultLanguage)) {
				p, ok = r.resolveUnlabelledSource(sourceSlug)
			}
		} else if resolvedLang != "" {
			p, ok = r.resolveSourceForRequestedLang(sourceSlug, resolvedLang)
		} else {
			p, ok = r.resolveDefaultSource(sourceSlug)
		}
		if ok {
			out.Source = p
			out.SourcePath = p.FilePath
			if out.Public == nil && requestedLang == "" && r.idx != nil {
				if pub, ok := r.idx.GetBySlug("/" + p.Slug + "/"); ok {
					out.Public = pub
				}
			}
		}
	}
	return out, out.Public != nil || out.Source != nil
}

func (r *PageResolver) resolveSourceForRequestedLang(sourceSlug, lang string) (*hugosite.SourcePage, bool) {
	if r == nil {
		return nil, false
	}
	if p, ok := r.resolveSourceExact(sourceSlug, lang); ok {
		return p, true
	}
	if strings.TrimSpace(lang) == strings.TrimSpace(r.cfg.DefaultLanguage) {
		return r.resolveUnlabelledSource(sourceSlug)
	}
	return nil, false
}

func (r *PageResolver) resolveSourceExact(sourceSlug, lang string) (*hugosite.SourcePage, bool) {
	if r == nil || r.srcIdx == nil || strings.TrimSpace(lang) == "" {
		return nil, false
	}
	for _, c := range SourceSlugCandidates(sourceSlug) {
		if p, ok := r.srcIdx.GetBySlugLang(c, lang); ok {
			return p, true
		}
	}
	return nil, false
}

func (r *PageResolver) resolveUnlabelledSource(sourceSlug string) (*hugosite.SourcePage, bool) {
	if r == nil || r.srcIdx == nil {
		return nil, false
	}
	for _, c := range SourceSlugCandidates(sourceSlug) {
		if p, ok := r.srcIdx.GetDefaultBySlug(c); ok {
			return p, true
		}
	}
	return nil, false
}

func (r *PageResolver) resolvePublicForSourceLang(sourceSlug, lang string) (*Page, bool) {
	if r == nil || r.idx == nil {
		return nil, false
	}
	sourceSlug = strings.Trim(sourceSlug, "/")
	lang = strings.TrimSpace(lang)
	if sourceSlug == "" || lang == "" {
		return nil, false
	}
	return r.idx.GetBySlug(canonicalPublicSlugForSourceLang(sourceSlug, lang, r.cfg.DefaultLanguage))
}

func canonicalPublicSlugForSourceLang(sourceSlug, lang, defaultLang string) string {
	sourceSlug = strings.Trim(sourceSlug, "/")
	lang = strings.TrimSpace(lang)
	defaultLang = strings.TrimSpace(defaultLang)
	if sourceSlug == "" {
		return ""
	}
	if lang == "" || lang == defaultLang {
		return normalizeSlug(sourceSlug)
	}
	return normalizeSlug("/" + lang + "/" + sourceSlug)
}

func pageMatchesExplicitLang(p *Page, lang, defaultLang string) bool {
	if p == nil {
		return false
	}
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return true
	}
	pageLang := strings.TrimSpace(p.Lang)
	if pageLang != "" {
		return pageLang == lang
	}
	if prefix := languagePrefixFromSlug(p.Slug); prefix != "" {
		return prefix == lang
	}
	return lang == strings.TrimSpace(defaultLang) && languagePrefixFromSlug(p.Slug) == ""
}

func (r *PageResolver) resolveDefaultSource(sourceSlug string) (*hugosite.SourcePage, bool) {
	if r == nil || r.srcIdx == nil {
		return nil, false
	}
	candidates := SourceSlugCandidates(sourceSlug)
	if r.cfg.DefaultLanguage != "" {
		for _, c := range candidates {
			if p, ok := r.srcIdx.GetBySlugLang(c, r.cfg.DefaultLanguage); ok {
				return p, true
			}
		}
	}
	if p, ok := r.resolveUnlabelledSource(sourceSlug); ok {
		return p, true
	}
	for _, c := range candidates {
		if p, ok := r.srcIdx.GetBySlug(c); ok {
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

func LanguagePrefixFromSlug(raw string) string {
	publicSlug, _ := normalizeResolverSlugs(raw)
	return languagePrefixFromSlug(publicSlug)
}
