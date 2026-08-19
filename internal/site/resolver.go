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
	for _, c := range sourceSlugCandidatesForRequest(sourceSlug, lang) {
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
	for _, c := range sourceSlugCandidatesForRequest(sourceSlug, "") {
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
	if sourceSlug == "" {
		// Genuinely empty/invalid input (never a real source slug at all,
		// e.g. "///") — distinct from a root "_index"/"_index.<lang>"
		// slug, which also trims to "" below but *does* still get a
		// language prefix, since it legitimately represents the homepage.
		return ""
	}
	sourceSlug = trimIndexSlugSegment(sourceSlug)
	lang = strings.TrimSpace(lang)
	defaultLang = strings.TrimSpace(defaultLang)
	if lang == "" || lang == defaultLang {
		return normalizeSlug(sourceSlug)
	}
	if sourceSlug == "" {
		return normalizeSlug("/" + lang)
	}
	return normalizeSlug("/" + lang + "/" + sourceSlug)
}

// trimIndexSlugSegment strips a trailing "_index" (or "_index.<lang>")
// path segment from a source slug, mapping it to the public URL of the
// section/root it indexes rather than a literal "_index"-shaped path
// segment that Hugo never actually renders (#1174).
//
// hugosite.SlugFromRel deliberately keeps "_index"/"_index.<lang>" as a
// literal slug segment for "_index.md"/"_index.<lang>.md" source files —
// this is load-bearing elsewhere (e.g. #457's section-listing exclusion,
// read-tool addressing) and must not change. But that same literal slug
// was, until this fix, passed straight through to the public-URL mapping
// here, which is wrong: Hugo renders "_index.en.md" at content root as the
// homepage ("/", not "/_index.en/"), and "posts/_index.en.md" as the
// section list page ("/posts/", not "/posts/_index.en/"). Without this
// trim, canonicalPublicSlugForSourceLang could never produce the correct
// public slug for any "_index" source file, which meant
// content_shadow.go's resolvePublicSource never matched an "_index"
// source to its real public page — every "_index"/section-index page's
// public representation silently fell into the unresolved "@public:..."
// bucket, permanently reporting as missing_public in
// build_reconciliation.public_drift_count (visible as
// get_runtime_status.publication_safety.safe_to_publish staying false
// forever, even on a fully published, zero-drift site — see #1174).
func trimIndexSlugSegment(slug string) string {
	parts := strings.Split(slug, "/")
	last := parts[len(parts)-1]
	if base, _, _ := strings.Cut(last, "."); base != "_index" {
		return slug
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// PublicSlugForSourceLang returns the rendered URL slug Hugo uses for a
// source translation. Non-default languages receive their language prefix;
// the default language keeps the bare source slug.
func PublicSlugForSourceLang(sourceSlug, lang, defaultLang string) string {
	return canonicalPublicSlugForSourceLang(sourceSlug, lang, defaultLang)
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
	candidates := sourceSlugCandidatesForRequest(sourceSlug, r.cfg.DefaultLanguage)
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

// sourceSlugCandidatesForRequest is SourceSlugCandidates, plus explicit
// handling for the site root (sourceSlug == "" after trimming), which
// SourceSlugCandidates itself always turns into zero candidates.
//
// hugosite.SlugFromRel keeps "_index"/"_index.<lang>" as a literal slug
// segment for root "_index.md"/"_index.<lang>.md" source files — the same
// fact trimIndexSlugSegment's doc comment relies on (#1174). But that
// literal segment only carries the language suffix when the file *is*
// explicitly labelled: an unlabelled "_index.md" gets the bare slug
// "_index" (found via GetDefaultBySlug/GetBySlug), while an explicitly
// labelled "_index.en.md" gets slug "_index.en" — NOT "_index" — with
// Lang "en" parsed separately (found via GetBySlugLang("_index.en", "en")).
// A bare "_index" candidate alone therefore resolves an unlabelled default
// root page but silently misses every explicitly-labelled translation,
// which is what left get_page_for_edit/check_ai_readiness unable to
// resolve the site root for a labelled language while validate_site/
// inspect_rendered (which don't route through PageResolver) kept working
// (#1184, the public→source-direction counterpart to #1174's fix).
//
// A trimmed sourceSlug that exactly equals the requested language code
// (e.g. sourceSlug "fr" for a "/fr/" request) is the same root case in
// disguise: languagePrefixFromSlug/stripLanguagePrefix require at least two
// path segments before they'll treat a leading segment as a language
// prefix (a single segment is ambiguous with an ordinary content slug that
// happens to match a configured language code), so normalizeResolverSlugs
// never strips it down to "" the way it does for e.g. "/fr/posts/". The
// homepage candidates are appended as a fallback in that case, after any
// real page literally slugged that way, rather than replacing the normal
// candidate list outright.
func sourceSlugCandidatesForRequest(sourceSlug, lang string) []string {
	trimmed := strings.Trim(sourceSlug, "/")
	lang = strings.TrimSpace(lang)
	if trimmed == "" {
		return rootIndexCandidates(lang)
	}
	candidates := SourceSlugCandidates(sourceSlug)
	if lang != "" && trimmed == lang {
		candidates = append(candidates, rootIndexCandidates(lang)...)
	}
	return candidates
}

func rootIndexCandidates(lang string) []string {
	if lang == "" {
		return []string{"_index"}
	}
	return []string{"_index." + lang, "_index"}
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
