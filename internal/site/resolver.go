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
	if r != nil && r.idx != nil {
		if p, ok := r.idx.GetBySlug(publicSlug); ok {
			out.Public = p
			_, sourceSlug = normalizeResolverSlugs(p.Slug)
		}
	}
	if r != nil && r.srcIdx != nil {
		if p, ok := r.resolveSource(sourceSlug); ok {
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

func (r *PageResolver) resolveSource(sourceSlug string) (*hugosite.SourcePage, bool) {
	for _, candidate := range SourceSlugCandidates(sourceSlug) {
		if p, ok := r.srcIdx.GetBySlug(candidate); ok {
			return p, true
		}
	}
	return nil, false
}

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
