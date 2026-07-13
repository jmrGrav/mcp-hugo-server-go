package read

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getFullPageMarkdownInput struct {
	Slug string `json:"slug"`
}

type pageMarkdownDTO struct {
	Slug               string              `json:"slug"`
	Title              string              `json:"title"`
	Date               string              `json:"date"`
	Tags               []string            `json:"tags"`
	Categories         []string            `json:"categories"`
	TagTerms           []site.TaxonomyTerm `json:"tag_terms,omitempty"`
	CategoryTerms      []site.TaxonomyTerm `json:"category_terms,omitempty"`
	URL                string              `json:"url"`
	Lang               string              `json:"lang"`
	ResolvedLang       string              `json:"resolved_lang"`
	ResolvedSourcePath string              `json:"resolved_source_path"`
	Markdown           string              `json:"markdown"`
}

type getFullPageMarkdownOutput struct {
	Page pageMarkdownDTO `json:"page"`
}

type getPageFrontmatterInput struct {
	Slug string `json:"slug"`
}

type frontmatterDTO struct {
	Slug               string                      `json:"slug"`
	Title              string                      `json:"title"`
	Date               string                      `json:"date"`
	Tags               []string                    `json:"tags"`
	Categories         []string                    `json:"categories"`
	TagTerms           []contentmodel.TaxonomyTerm `json:"tag_terms,omitempty"`
	CategoryTerms      []contentmodel.TaxonomyTerm `json:"category_terms,omitempty"`
	URL                string                      `json:"url"`
	Lang               string                      `json:"lang"`
	ResolvedLang       string                      `json:"resolved_lang"`
	ResolvedSourcePath string                      `json:"resolved_source_path"`
	ReadingTimeMin     int                         `json:"reading_time_minutes"`
}

type getPageFrontmatterOutput struct {
	Frontmatter frontmatterDTO `json:"frontmatter"`
}

type getRelatedContentInput struct {
	Slug  string `json:"slug"`
	Limit int    `json:"limit,omitempty"`
}

type relatedPageDTO struct {
	Slug                string                  `json:"slug"`
	Title               string                  `json:"title"`
	URL                 string                  `json:"url"`
	Lang                string                  `json:"lang,omitempty"`
	SharedTags          []string                `json:"shared_tags,omitempty"`
	SharedCategories    []string                `json:"shared_categories,omitempty"`
	SharedTagTerms      []taxonomy.TaxonomyTerm `json:"shared_tag_terms,omitempty"`
	SharedCategoryTerms []taxonomy.TaxonomyTerm `json:"shared_category_terms,omitempty"`
}

type translationPageDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Lang  string `json:"lang,omitempty"`
}

type getRelatedContentOutput struct {
	Translations []translationPageDTO `json:"translations"`
	RelatedPages []relatedPageDTO     `json:"related_pages"`
	Related      []relatedPageDTO     `json:"related"`
}

type buildAgentContextInput struct {
	Slug string `json:"slug"`
}

type agentContextDTO struct {
	Frontmatter  frontmatterDTO       `json:"frontmatter"`
	Markdown     string               `json:"markdown"`
	Translations []translationPageDTO `json:"translations"`
	RelatedPages []relatedPageDTO     `json:"related_pages"`
}

type buildAgentContextOutput struct {
	Context agentContextDTO `json:"context"`
}

type exportAgentContextInput struct {
	Tag      string `json:"tag,omitempty"`
	Category string `json:"category,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

type pageExportDTO struct {
	Frontmatter frontmatterDTO `json:"frontmatter"`
	Markdown    string         `json:"markdown"`
}

type exportAgentContextOutput struct {
	Export exportResultDTO `json:"export"`
}

type exportResultDTO struct {
	Pages         []pageExportDTO `json:"pages"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
	ReturnedCount int             `json:"returned_count"`
	HasMore       bool            `json:"has_more"`
	NextOffset    *int            `json:"next_offset,omitempty"`
}

func Register(s *mcp.Server, idx *site.Index, cfg config.Config, sources ...*hugosite.SourceIndex) {
	if s == nil {
		return
	}
	var srcIdx *hugosite.SourceIndex
	if len(sources) > 0 {
		srcIdx = sources[0]
	}
	resolver := site.NewPageResolver(idx, srcIdx, cfg)
	aliases := taxonomy.NormalizeAliasMap(cfg.TaxonomyAliases)

	addReadOnlyTool(s, "get_full_page_markdown", "Read page Markdown",
		"Read the full Markdown-formatted content of a published page. Use this when you need the raw article body rather than rendered HTML. Input: indexed slug only.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getFullPageMarkdownInput) (*mcp.CallToolResult, getFullPageMarkdownOutput, error) {
			if idx == nil && srcIdx == nil {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			return nil, getFullPageMarkdownOutput{Page: toResolvedPageMarkdownDTO(resolved)}, nil
		})

	addReadOnlyTool(s, "get_page_frontmatter", "Read page metadata",
		"Read structured metadata for a published page, including title, tags, categories, date, URL, and estimated reading time. Input: indexed slug only.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getPageFrontmatterInput) (*mcp.CallToolResult, getPageFrontmatterOutput, error) {
			if idx == nil {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			p := resolvedPublicPage(resolved)
			md := resolvedMarkdown(resolved)
			rt := readingTimeMinutes(md)
			return nil, getPageFrontmatterOutput{Frontmatter: toFrontmatterDTO(p, resolved.SourcePath, resolvedLang(resolved), rt)}, nil
		})

	addReadOnlyTool(s, "get_related_content", "Get related content",
		"Return pages related to a given slug by shared tags or categories. Use this for content recommendations and editorial linking. Input: indexed slug only.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getRelatedContentInput) (*mcp.CallToolResult, getRelatedContentOutput, error) {
			if idx == nil {
				return nil, getRelatedContentOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getRelatedContentOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			limit := clampLimit(in.Limit, 5, 20)
			ref := resolvedPublicPage(resolved)
			if resolved.Public != nil {
				ref = *resolved.Public
			}
			translations := collectTranslations(idx, ref)
			related := computeRelated(idx, ref, limit)
			return nil, getRelatedContentOutput{
				Translations: translations,
				RelatedPages: related,
				Related:      related,
			}, nil
		})

	addReadOnlyTool(s, "build_agent_context", "Build agent context",
		"Build a complete context bundle for a published page: metadata, reading time, full Markdown content, and related pages. Use this before summarizing or editing a page. Input: indexed slug only.",
		func(_ context.Context, _ *mcp.CallToolRequest, in buildAgentContextInput) (*mcp.CallToolResult, buildAgentContextOutput, error) {
			if idx == nil {
				return nil, buildAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, buildAgentContextOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			p := resolvedPublicPage(resolved)
			md := resolvedMarkdown(resolved)
			rt := readingTimeMinutes(md)
			fm := toFrontmatterDTO(p, resolved.SourcePath, resolvedLang(resolved), rt)
			ref := p
			if resolved.Public != nil {
				ref = *resolved.Public
			}
			translations := collectTranslations(idx, ref)
			related := computeRelated(idx, ref, 5)
			ac := agentContextDTO{
				Frontmatter:  fm,
				Markdown:     md,
				Translations: translations,
				RelatedPages: related,
			}
			return nil, buildAgentContextOutput{Context: ac}, nil
		})

	addReadOnlyTool(s, "export_agent_context", "Export agent context",
		"Paginated export of page context bundles filtered by tag or category. Each page includes front matter, reading time, and full Markdown content. Use this for bulk analysis or migration work.",
		func(_ context.Context, _ *mcp.CallToolRequest, in exportAgentContextInput) (*mcp.CallToolResult, exportAgentContextOutput, error) {
			if idx == nil {
				return nil, exportAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 10, 50)
			all := idx.ContentPages()
			var filtered []site.Page
			tagSlug := taxonomy.Slug(in.Tag)
			catSlug := taxonomy.Slug(in.Category)
			for _, pg := range all {
				if in.Tag != "" && !taxonomy.MatchesSlugWithAliases(pg.Tags, tagSlug, aliases) {
					continue
				}
				if in.Category != "" {
					pgCats := pg.Categories
					if len(pgCats) == 0 && srcIdx != nil {
						if src, ok := srcIdx.GetBySlug(strings.Trim(pg.Slug, "/")); ok {
							pgCats = src.Categories
						}
					}
					if !taxonomy.MatchesSlugWithAliases(pgCats, catSlug, aliases) {
						continue
					}
				}
				filtered = append(filtered, pg)
			}
			total := len(filtered)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			if offset >= len(filtered) {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, exportAgentContextOutput{Export: exportResultDTO{
					Pages:         []pageExportDTO{},
					Total:         meta.Total,
					Limit:         meta.Limit,
					Offset:        meta.Offset,
					ReturnedCount: meta.ReturnedCount,
					HasMore:       meta.HasMore,
					NextOffset:    meta.NextOffset,
				}}, nil
			}
			slice := filtered[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(slice))
			pages := make([]pageExportDTO, 0, len(slice))
			for _, pg := range slice {
				resolved, _ := resolver.Resolve(pg.Slug)
				p := resolvedPublicPage(resolved)
				md := resolvedMarkdown(resolved)
				rt := readingTimeMinutes(md)
				pages = append(pages, pageExportDTO{
					Frontmatter: toFrontmatterDTO(p, resolved.SourcePath, resolvedLang(resolved), rt),
					Markdown:    md,
				})
			}
			return nil, exportAgentContextOutput{Export: exportResultDTO{
				Pages:         pages,
				Total:         meta.Total,
				Limit:         meta.Limit,
				Offset:        meta.Offset,
				ReturnedCount: meta.ReturnedCount,
				HasMore:       meta.HasMore,
				NextOffset:    meta.NextOffset,
			}}, nil
		})
}

func toPageMarkdownDTO(p site.Page, md, resolvedSourcePath, resolvedLang string) pageMarkdownDTO {
	return pageMarkdownDTO{
		Slug:               p.Slug,
		Title:              p.Title,
		Date:               p.Date,
		Tags:               nullsafeStrings(p.Tags),
		Categories:         nullsafeStrings(p.Categories),
		TagTerms:           site.NormalizeTaxonomyTerms(p.Tags),
		CategoryTerms:      site.NormalizeTaxonomyTerms(p.Categories),
		URL:                p.URL,
		Lang:               p.Lang,
		ResolvedLang:       resolvedLang,
		ResolvedSourcePath: resolvedSourcePath,
		Markdown:           md,
	}
}

func toResolvedPageMarkdownDTO(resolved site.ResolvedPage) pageMarkdownDTO {
	p := resolvedPublicPage(resolved)
	return toPageMarkdownDTO(p, resolvedMarkdown(resolved), resolved.SourcePath, resolvedLang(resolved))
}

func resolvedMarkdown(resolved site.ResolvedPage) string {
	if resolved.Source != nil {
		return resolved.Source.Body
	}
	if resolved.Public != nil {
		return site.ExtractMarkdown(resolved.Public.RawHTML)
	}
	return ""
}

func resolvedPublicPage(resolved site.ResolvedPage) site.Page {
	if resolved.Public != nil {
		p := *resolved.Public
		if resolved.Source != nil {
			p.Tags = nullsafeStrings(resolved.Source.Tags)
			p.Categories = nullsafeStrings(resolved.Source.Categories)
		}
		return p
	}
	return sourcePageAsPublic(resolved.Source)
}

func sourcePageAsPublic(src *hugosite.SourcePage) site.Page {
	if src == nil {
		return site.Page{}
	}
	return site.Page{
		Slug:       "/" + strings.Trim(src.Slug, "/") + "/",
		Title:      src.Title,
		Date:       src.Date,
		Tags:       src.Tags,
		Categories: src.Categories,
	}
}

func toFrontmatterDTO(p site.Page, sourcePath, resolvedLanguage string, readingTimeMin int) frontmatterDTO {
	identity := pageIdentityFromPage(p, sourcePath, readingTimeMin)
	return frontmatterDTO{
		Slug:               identity.Slug,
		Title:              identity.Title,
		Date:               p.Date,
		Tags:               nullsafeStrings(p.Tags),
		Categories:         nullsafeStrings(p.Categories),
		TagTerms:           identity.Tags,
		CategoryTerms:      identity.Categories,
		URL:                identity.URL,
		Lang:               identity.Lang,
		ResolvedLang:       resolvedLanguage,
		ResolvedSourcePath: identity.SourcePath,
		ReadingTimeMin:     identity.ReadingTime,
	}
}

func resolvedLang(resolved site.ResolvedPage) string {
	if resolved.Source != nil {
		return resolved.Source.Lang
	}
	if resolved.Public != nil {
		return resolved.Public.Lang
	}
	return ""
}

func computeRelated(idx *site.Index, ref site.Page, limit int) []relatedPageDTO {
	refTagSlugs := make(map[string]bool, len(ref.Tags))
	for _, t := range ref.Tags {
		if s := taxonomy.Slug(t); s != "" {
			refTagSlugs[s] = true
		}
	}
	refCatSlugs := make(map[string]bool, len(ref.Categories))
	for _, c := range ref.Categories {
		if s := taxonomy.Slug(c); s != "" {
			refCatSlugs[s] = true
		}
	}

	type scored struct {
		page  site.Page
		score int
		dto   relatedPageDTO
	}
	var candidates []scored
	refTranslationKey := translationKey(ref.Slug)
	for _, pg := range idx.Sitemap() {
		if pg.Slug == ref.Slug {
			continue
		}
		if isTranslationVariant(refTranslationKey, pg.Slug) {
			continue
		}
		sharedTagTerms := taxonomy.SharedTerms(pg.Tags, ref.Tags)
		sharedCatTerms := taxonomy.SharedTerms(pg.Categories, ref.Categories)
		score := len(sharedTagTerms) + len(sharedCatTerms)
		if score == 0 {
			continue
		}
		candidates = append(candidates, scored{
			page:  pg,
			score: score,
			dto: relatedPageDTO{
				Slug:                pg.Slug,
				Title:               pg.Title,
				URL:                 pg.URL,
				Lang:                pg.Lang,
				SharedTags:          taxonomy.Slugs(sharedTagTerms),
				SharedCategories:    taxonomy.Slugs(sharedCatTerms),
				SharedTagTerms:      sharedTagTerms,
				SharedCategoryTerms: sharedCatTerms,
			},
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].page.Date > candidates[j].page.Date
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]relatedPageDTO, len(candidates))
	for i, c := range candidates {
		out[i] = c.dto
	}
	return out
}

func collectTranslations(idx *site.Index, ref site.Page) []translationPageDTO {
	if idx == nil {
		return []translationPageDTO{}
	}
	key := translationKey(ref.Slug)
	if key == "" {
		return []translationPageDTO{}
	}
	out := make([]translationPageDTO, 0, 2)
	for _, pg := range idx.ContentPages() {
		if pg.Slug == ref.Slug {
			continue
		}
		if translationKey(pg.Slug) != key {
			continue
		}
		out = append(out, translationPageDTO{Slug: pg.Slug, Title: pg.Title, URL: pg.URL, Lang: pg.Lang})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

func translationKey(slug string) string {
	candidates := site.SourceSlugCandidates(strings.Trim(slug, "/"))
	if len(candidates) == 0 {
		return ""
	}
	return candidates[len(candidates)-1]
}

func isTranslationVariant(refKey, candidateSlug string) bool {
	if refKey == "" {
		return false
	}
	return translationKey(candidateSlug) == refKey
}

func readingTimeMinutes(md string) int {
	words := len(strings.Fields(md))
	if words == 0 {
		return 1
	}
	minutes := words / 200
	if words%200 > 0 {
		minutes++
	}
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}

func addReadOnlyTool[In, Out any](s *mcp.Server, name, title, description string, handler mcp.ToolHandlerFor[In, Out]) {
	wrapped := func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := handler(ctx, req, in)
		if err != nil {
			var zero Out
			return toolcontract.ErrorResult(err, toolcontract.NewMeta(toolResultVersion, time.Now())), zero, nil
		}
		return res, out, nil
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:         name,
		Title:        title,
		Description:  description,
		InputSchema:  tools.MustSchema[In](),
		OutputSchema: tools.MustSchema[Out](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, wrapped)
}

func boolPtr(v bool) *bool { return &v }

func clampLimit(v, defaultVal, maxVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func nullsafeStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Defs returns the tool definitions for this package (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "get_full_page_markdown", RequiredScope: "content.read"},
		{Name: "get_page_frontmatter", RequiredScope: "content.read"},
		{Name: "get_related_content", RequiredScope: "content.read"},
		{Name: "build_agent_context", RequiredScope: "content.read"},
		{Name: "export_agent_context", RequiredScope: "content.read"},
		{Name: "search_content", RequiredScope: "content.read"},
		{Name: "explain_site_structure", RequiredScope: "content.read"},
		{Name: "get_site_health", RequiredScope: "content.read"},
		{Name: "get_broken_links", RequiredScope: "content.read"},
		{Name: "get_backlinks", RequiredScope: "content.read"},
		{Name: "suggest_internal_links", RequiredScope: "content.read"},
		{Name: "diff_page", RequiredScope: "content.read"},
		{Name: "validate_front_matter", RequiredScope: "content.read"},
		{Name: "validate_site", RequiredScope: "content.read"},
	}
}
