package read

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
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
	Revision           string              `json:"revision,omitempty"`
	State              site.LifecycleState `json:"state"`
	Markdown           string              `json:"markdown"`
}

type getFullPageMarkdownData struct {
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
	Revision           string                      `json:"revision,omitempty"`
	State              site.LifecycleState         `json:"state"`
	ReadingTimeMin     int                         `json:"reading_time_minutes"`
}

type getPageFrontmatterData struct {
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

type getRelatedContentData struct {
	Translations   []translationPageDTO `json:"translations"`
	RelatedPages   []relatedPageDTO     `json:"related_pages"`
	Backlinks      []backlinkDTO        `json:"backlinks"`
	SuggestedLinks []linkSuggestionDTO  `json:"suggested_links"`
	Related        []relatedPageDTO     `json:"related"`
}

type buildAgentContextInput struct {
	Slug         string `json:"slug"`
	ResponseMode string `json:"response_mode,omitempty"`
	MaxBodyChars int    `json:"max_body_chars,omitempty"`
}

type agentContextDTO struct {
	Frontmatter  frontmatterDTO       `json:"frontmatter"`
	Markdown     string               `json:"markdown"`
	State        site.LifecycleState  `json:"state"`
	Translations []translationPageDTO `json:"translations"`
	RelatedPages []relatedPageDTO     `json:"related_pages"`
}

// agentContextCompactDTO is the reduced shape returned when
// response_mode=compact: frontmatter, body, and lifecycle state only —
// drops translations/related_pages, which cost a lookup and payload bytes
// an agent doesn't need once it already knows which page it wants.
type agentContextCompactDTO struct {
	Frontmatter frontmatterDTO      `json:"frontmatter"`
	Markdown    string              `json:"markdown"`
	State       site.LifecycleState `json:"state"`
}

type buildAgentContextData struct {
	Context any `json:"context"`
}

type exportAgentContextInput struct {
	Tag         string `json:"tag,omitempty"`
	Category    string `json:"category,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	IncludeBody *bool  `json:"include_body,omitempty"`
}

type pageExportDTO struct {
	Frontmatter frontmatterDTO      `json:"frontmatter"`
	State       site.LifecycleState `json:"state"`
	Markdown    string              `json:"markdown,omitempty"`
}

type exportResultDTO struct {
	Pages         []pageExportDTO `json:"pages"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
	ReturnedCount int             `json:"returned_count"`
	HasMore       bool            `json:"has_more"`
	NextOffset    *int            `json:"next_offset,omitempty"`
	IncludeBody   bool            `json:"include_body"`
}

// exportAgentContextMaxLimitWithBody caps the page count when full Markdown
// bodies are included, since a single page body can run tens of KB and an
// uncapped multi-page export can exceed MCP message size limits. Callers
// that only need metadata can set include_body=false to use the higher
// exportAgentContextMaxLimitMetadataOnly cap instead.
const (
	exportAgentContextMaxLimitWithBody     = 10
	exportAgentContextMaxLimitMetadataOnly = 50
	exportAgentContextDefaultLimit         = 10
)

type exportAgentContextData = exportResultDTO

type getFullPageMarkdownOutput struct {
	toolcontract.ToolResponse[getFullPageMarkdownData]
	Page pageMarkdownDTO `json:"page"`
}

type getPageFrontmatterOutput struct {
	toolcontract.ToolResponse[getPageFrontmatterData]
	Frontmatter frontmatterDTO `json:"frontmatter"`
}

type getRelatedContentOutput struct {
	toolcontract.ToolResponse[getRelatedContentData]
	Translations   []translationPageDTO `json:"translations"`
	RelatedPages   []relatedPageDTO     `json:"related_pages"`
	Backlinks      []backlinkDTO        `json:"backlinks"`
	SuggestedLinks []linkSuggestionDTO  `json:"suggested_links"`
	Related        []relatedPageDTO     `json:"related"`
}

type buildAgentContextOutput struct {
	toolcontract.ToolResponse[buildAgentContextData]
	Context any `json:"context"`
}

type exportAgentContextOutput struct {
	toolcontract.ToolResponse[exportAgentContextData]
	Export        exportResultDTO `json:"export"`
	Pages         []pageExportDTO `json:"pages"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
	ReturnedCount int             `json:"returned_count"`
	HasMore       bool            `json:"has_more"`
	NextOffset    *int            `json:"next_offset,omitempty"`
	IncludeBody   bool            `json:"include_body"`
}

type getPageForEditInput struct {
	Slug         string   `json:"slug"`
	Include      []string `json:"include,omitempty"`
	MaxBodyChars int      `json:"max_body_chars,omitempty"`
}

// pageQualityDTO surfaces enough signal to decide whether a page is safe to
// edit/publish without a separate validate_front_matter/get_broken_links
// call. It is nil (omitted) when quality wasn't requested, or when quality
// requires source access the caller's profile doesn't have (reader scope).
type pageQualityDTO struct {
	Valid       bool `json:"valid"`
	BrokenLinks int  `json:"broken_links"`
}

// pageForEditDTO is the compact edit bundle (#339): the fields an agent
// needs before modifying a page, gathered in one call instead of chaining
// get_page_frontmatter + get_page_markdown + build_agent_context. Each
// section is a pointer so an unrequested (via `include`) or unavailable
// section is omitted from the JSON entirely rather than serialized as a
// zero value that could be mistaken for real data.
type pageForEditDTO struct {
	Slug        string               `json:"slug"`
	Revision    string               `json:"revision,omitempty"`
	Frontmatter *frontmatterDTO      `json:"frontmatter,omitempty"`
	Markdown    string               `json:"markdown,omitempty"`
	State       *site.LifecycleState `json:"state,omitempty"`
	Quality     *pageQualityDTO      `json:"quality,omitempty"`
}

type getPageForEditData struct {
	Page pageForEditDTO `json:"page"`
}

type getPageForEditOutput struct {
	toolcontract.ToolResponse[getPageForEditData]
	Page pageForEditDTO `json:"page"`
}

// getPageForEditSections is the allowed vocabulary for the `include` param.
// An empty/omitted `include` defaults to all four sections (the full
// pre-shaping bundle), matching this repo's established shaping convention
// (#337) that omitting shaping params never reduces the default response.
var getPageForEditSections = map[string]bool{
	"frontmatter": true,
	"markdown":    true,
	"state":       true,
	"quality":     true,
}

func resolveEditInclude(raw []string) (map[string]bool, error) {
	if len(raw) == 0 {
		// Return a copy, not the package-level map itself: callers treat
		// the result as theirs to hold, and getPageForEditSections also
		// backs the validation lookup below — an accidental mutation by a
		// future caller must not corrupt the shared vocabulary.
		out := make(map[string]bool, len(getPageForEditSections))
		for k, v := range getPageForEditSections {
			out[k] = v
		}
		return out, nil
	}
	out := make(map[string]bool, len(raw))
	for _, r := range raw {
		if !getPageForEditSections[r] {
			return nil, fmt.Errorf("invalid_params: include must be a subset of frontmatter, markdown, state, quality (got %q)", r)
		}
		out[r] = true
	}
	return out, nil
}

func newGetPageForEditOutput(data getPageForEditData, warnings []string, now time.Time) getPageForEditOutput {
	resp := successEnvelope(data, now)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return getPageForEditOutput{ToolResponse: resp, Page: data.Page}
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

	addReadOnlyTool(s, "get_page_markdown", "Read page Markdown",
		"Read the full Markdown-formatted content of a published page. Use this when you need the raw article body rather than rendered HTML. The response includes a `state` object so agents can tell whether they are reading built public content, source-only content, or stale source ahead of the last build. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getFullPageMarkdownInput) (*mcp.CallToolResult, getFullPageMarkdownOutput, error) {
			if idx == nil && srcIdx == nil {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err := readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getFullPageMarkdownOutput{}, err
			}
			return nil, newGetFullPageMarkdownOutput(getFullPageMarkdownData{Page: toResolvedPageMarkdownDTO(resolved, cfg.ContentRoot, cfg.SiteRoot)}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_page_frontmatter", "Read page metadata",
		"Read structured metadata for a published page, including title, tags, categories, date, URL, estimated reading time, and a `state` object describing source/build/public/index freshness. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getPageFrontmatterInput) (*mcp.CallToolResult, getPageFrontmatterOutput, error) {
			if idx == nil {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err := readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getPageFrontmatterOutput{}, err
			}
			p := resolvedPublicPage(resolved)
			md := resolvedMarkdown(resolved)
			rt := readingTimeMinutes(md)
			return nil, newGetPageFrontmatterOutput(getPageFrontmatterData{Frontmatter: toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt)}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_related_content", "Get related content",
		"Return the four editorial surfaces for a slug: related_pages (tag/category overlap), backlinks (pages that link here), suggested_links (link candidates scored by tag affinity), and translations. Use this for content recommendations and editorial linking. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getRelatedContentInput) (*mcp.CallToolResult, getRelatedContentOutput, error) {
			if idx == nil {
				return nil, getRelatedContentOutput{}, fmt.Errorf("index not initialized")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getRelatedContentOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err := readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getRelatedContentOutput{}, err
			}
			limit := clampLimit(in.Limit, 5, 20)
			ref := resolvedPublicPage(resolved)
			if resolved.Public != nil {
				ref = *resolved.Public
			}
			translations := collectTranslations(idx, ref)
			related := computeRelated(idx, ref, limit)
			backlinks := collectBacklinks(idx, ref.Slug)
			suggestedLinks := scoreLinkSuggestions(idx, ref.Slug, ref.Tags, ref.Categories, "", limit)
			return nil, newGetRelatedContentOutput(getRelatedContentData{
				Translations:   translations,
				RelatedPages:   related,
				Backlinks:      backlinks,
				SuggestedLinks: suggestedLinks,
				Related:        related,
			}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "build_agent_context", "Build agent context",
		"Build a complete context bundle for a published page: metadata, reading time, full Markdown content, related pages, and explicit lifecycle `state`. Use this before summarizing or editing a page. Supports response shaping: `response_mode: \"compact\"` drops translations/related_pages and returns only frontmatter, markdown, and state; `max_body_chars: N` truncates the Markdown body to N characters (applies in either mode). Omitting both preserves the full default shape. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in buildAgentContextInput) (*mcp.CallToolResult, buildAgentContextOutput, error) {
			if idx == nil {
				return nil, buildAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, buildAgentContextOutput{}, err
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, buildAgentContextOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, buildAgentContextOutput{}, err
			}
			p := resolvedPublicPage(resolved)
			md := resolvedMarkdown(resolved)
			rt := readingTimeMinutes(md)
			fm := toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt)
			state := resolvedState(resolved, cfg.SiteRoot)
			md, truncated := toolcontract.TruncateBody(md, in.MaxBodyChars)

			var ac any
			if mode == toolcontract.ResponseModeCompact {
				ac = agentContextCompactDTO{Frontmatter: fm, Markdown: md, State: state}
			} else {
				ref := p
				if resolved.Public != nil {
					ref = *resolved.Public
				}
				ac = agentContextDTO{
					Frontmatter:  fm,
					Markdown:     md,
					State:        state,
					Translations: collectTranslations(idx, ref),
					RelatedPages: computeRelated(idx, ref, 5),
				}
			}
			var warnings []string
			if truncated {
				warnings = append(warnings, fmt.Sprintf("markdown truncated to max_body_chars=%d; set a higher value or omit the parameter to get the full body.", in.MaxBodyChars))
			}
			return nil, newBuildAgentContextOutput(buildAgentContextData{Context: ac}, warnings, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "export_agent_context", "Export agent context",
		"Paginated export of page context bundles filtered by tag or category. Each page includes front matter, reading time, and lifecycle `state`. By default also includes full Markdown content, which caps `limit` at 10 pages to keep the response within MCP message size limits; set `include_body=false` to fetch metadata only (frontmatter + state, no Markdown) at a higher cap of 50 pages. Use this for bulk analysis or migration work.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in exportAgentContextInput) (*mcp.CallToolResult, exportAgentContextOutput, error) {
			if idx == nil {
				return nil, exportAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			readerSafe := site.IsReaderProfile(ctx)
			includeBody := true
			if in.IncludeBody != nil {
				includeBody = *in.IncludeBody
			}
			maxLimit := exportAgentContextMaxLimitMetadataOnly
			if includeBody {
				maxLimit = exportAgentContextMaxLimitWithBody
			}
			limit := clampLimit(in.Limit, exportAgentContextDefaultLimit, maxLimit)
			var warnings []string
			if in.Limit > maxLimit {
				warnings = append(warnings, fmt.Sprintf(
					"requested limit %d exceeds the maximum of %d for include_body=%t; results were capped. Set include_body=false to raise the cap to %d.",
					in.Limit, maxLimit, includeBody, exportAgentContextMaxLimitMetadataOnly))
			}
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
					if len(pgCats) == 0 && srcIdx != nil && !readerSafe {
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
				payload := exportAgentContextData{
					Pages:         []pageExportDTO{},
					Total:         meta.Total,
					Limit:         meta.Limit,
					Offset:        meta.Offset,
					ReturnedCount: meta.ReturnedCount,
					HasMore:       meta.HasMore,
					NextOffset:    meta.NextOffset,
					IncludeBody:   includeBody,
				}
				return nil, newExportAgentContextOutput(payload, warnings, time.Now().UTC()), nil
			}
			slice := filtered[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(slice))
			pages := make([]pageExportDTO, 0, len(slice))
			for _, pg := range slice {
				resolved, _ := resolver.Resolve(pg.Slug)
				resolved, err := readerSafeResolvedPage(ctx, resolved, pg.Slug)
				if err != nil {
					continue
				}
				p := resolvedPublicPage(resolved)
				md := resolvedMarkdown(resolved)
				rt := readingTimeMinutes(md)
				page := pageExportDTO{
					Frontmatter: toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt),
					State:       resolvedState(resolved, cfg.SiteRoot),
				}
				if includeBody {
					page.Markdown = md
				}
				pages = append(pages, page)
			}
			payload := exportAgentContextData{
				Pages:         pages,
				Total:         meta.Total,
				Limit:         meta.Limit,
				Offset:        meta.Offset,
				ReturnedCount: meta.ReturnedCount,
				HasMore:       meta.HasMore,
				NextOffset:    meta.NextOffset,
				IncludeBody:   includeBody,
			}
			return nil, newExportAgentContextOutput(payload, warnings, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_page_for_edit", "Get page for edit",
		"Compact edit-oriented read: returns the core bundle an agent needs before modifying a page (frontmatter, markdown, lifecycle `state`, quality signals, and a stable `revision`) in a single call instead of chaining get_page_frontmatter + get_page_markdown + build_agent_context. `include: [...]` (subset of frontmatter, markdown, state, quality; default all four) and `max_body_chars` (rune-aware truncation of the markdown body) shape the response down. `quality.valid`/`quality.broken_links` are omitted when quality wasn't requested or the caller's profile has no source access. Lower-level tools remain available; this is an addition, not a replacement. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getPageForEditInput) (*mcp.CallToolResult, getPageForEditOutput, error) {
			if idx == nil {
				return nil, getPageForEditOutput{}, fmt.Errorf("index not initialized")
			}
			include, err := resolveEditInclude(in.Include)
			if err != nil {
				return nil, getPageForEditOutput{}, err
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getPageForEditOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getPageForEditOutput{}, err
			}
			p := resolvedPublicPage(resolved)
			page := pageForEditDTO{Slug: p.Slug, Revision: resolvedRevision(resolved)}
			var warnings []string

			if include["frontmatter"] {
				rt := readingTimeMinutes(resolvedMarkdown(resolved))
				fm := toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt)
				page.Frontmatter = &fm
			}
			if include["markdown"] {
				md, truncated := toolcontract.TruncateBody(resolvedMarkdown(resolved), in.MaxBodyChars)
				page.Markdown = md
				if truncated {
					warnings = append(warnings, fmt.Sprintf("markdown truncated to max_body_chars=%d; set a higher value or omit the parameter to get the full body.", in.MaxBodyChars))
				}
			}
			if include["state"] {
				st := resolvedState(resolved, cfg.SiteRoot)
				page.State = &st
			}
			if include["quality"] {
				qSrc := sourceIndexForProfile(srcIdx, site.IsReaderProfile(ctx))
				if qSrc != nil {
					if srcPages, err := sourcePagesForValidation(qSrc, in.Slug); err == nil && len(srcPages) > 0 {
						issues := validateFrontMatterPage(srcPages[0], aliases)
						broken := 0
						if indexedPage, found := idx.GetBySlug(p.Slug); found {
							broken = len(brokenLinksForPage(idx, idx.Classifier(), *indexedPage))
						}
						page.Quality = &pageQualityDTO{Valid: len(issues) == 0, BrokenLinks: broken}
					}
				}
			}
			return nil, newGetPageForEditOutput(getPageForEditData{Page: page}, warnings, time.Now().UTC()), nil
		})
}

func newGetFullPageMarkdownOutput(data getFullPageMarkdownData, now time.Time) getFullPageMarkdownOutput {
	return getFullPageMarkdownOutput{ToolResponse: successEnvelope(data, now), Page: data.Page}
}

func newGetPageFrontmatterOutput(data getPageFrontmatterData, now time.Time) getPageFrontmatterOutput {
	return getPageFrontmatterOutput{ToolResponse: successEnvelope(data, now), Frontmatter: data.Frontmatter}
}

func newGetRelatedContentOutput(data getRelatedContentData, now time.Time) getRelatedContentOutput {
	return getRelatedContentOutput{
		ToolResponse:   successEnvelope(data, now),
		Translations:   data.Translations,
		RelatedPages:   data.RelatedPages,
		Backlinks:      data.Backlinks,
		SuggestedLinks: data.SuggestedLinks,
		Related:        data.Related,
	}
}

func newBuildAgentContextOutput(data buildAgentContextData, warnings []string, now time.Time) buildAgentContextOutput {
	resp := successEnvelope(data, now)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return buildAgentContextOutput{ToolResponse: resp, Context: data.Context}
}

func newExportAgentContextOutput(data exportAgentContextData, warnings []string, now time.Time) exportAgentContextOutput {
	resp := successEnvelope(data, now)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return exportAgentContextOutput{
		ToolResponse:  resp,
		Export:        exportResultDTO(data),
		Pages:         data.Pages,
		Total:         data.Total,
		Limit:         data.Limit,
		Offset:        data.Offset,
		ReturnedCount: data.ReturnedCount,
		HasMore:       data.HasMore,
		NextOffset:    data.NextOffset,
		IncludeBody:   data.IncludeBody,
	}
}

func toPageMarkdownDTO(p site.Page, md, resolvedSourcePath, resolvedLang, revision string, state site.LifecycleState) pageMarkdownDTO {
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
		Revision:           revision,
		State:              state,
		Markdown:           md,
	}
}

func toResolvedPageMarkdownDTO(resolved site.ResolvedPage, contentRoot, siteRoot string) pageMarkdownDTO {
	p := resolvedPublicPage(resolved)
	return toPageMarkdownDTO(p, resolvedMarkdown(resolved), fileutil.LogicalContentPath(contentRoot, resolved.SourcePath), resolvedLang(resolved), resolvedRevision(resolved), resolvedState(resolved, siteRoot))
}

func readerSafeResolvedPage(ctx context.Context, resolved site.ResolvedPage, slug string) (site.ResolvedPage, error) {
	if !site.IsReaderProfile(ctx) {
		return resolved, nil
	}
	publicOnly, ok := site.ReaderSafeResolvedPage(resolved)
	if !ok {
		return site.ResolvedPage{}, fmt.Errorf("content_not_public: page is not publicly available for slug %q", slug)
	}
	return publicOnly, nil
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

func toFrontmatterDTO(p site.Page, resolved site.ResolvedPage, contentRoot, siteRoot string, readingTimeMin int) frontmatterDTO {
	identity := pageIdentityFromPage(p, fileutil.LogicalContentPath(contentRoot, resolved.SourcePath), resolvedRevision(resolved), readingTimeMin)
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
		ResolvedLang:       resolvedLang(resolved),
		ResolvedSourcePath: identity.SourcePath,
		Revision:           identity.Revision,
		State:              resolvedState(resolved, siteRoot),
		ReadingTimeMin:     identity.ReadingTime,
	}
}

func resolvedState(resolved site.ResolvedPage, siteRoot string) site.LifecycleState {
	return site.StateForResolvedPage(resolved, siteRoot)
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

func resolvedRevision(resolved site.ResolvedPage) string {
	if resolved.SourcePath == "" {
		return ""
	}
	rev, err := contentmodel.SourceRevision(resolved.SourcePath)
	if err != nil {
		return ""
	}
	return rev
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

func collectBacklinks(idx *site.Index, slug string) []backlinkDTO {
	if idx == nil || strings.TrimSpace(slug) == "" {
		return []backlinkDTO{}
	}
	entries := idx.GetBacklinks(slug)
	out := make([]backlinkDTO, len(entries))
	for i, e := range entries {
		out[i] = backlinkDTO{Slug: e.FromSlug, Title: e.FromTitle, URL: e.FromURL}
	}
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
	}, toolcontract.WrapTool(handler))
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
		{Name: "get_page_markdown", RequiredScope: "content.read"},
		{Name: "get_page_frontmatter", RequiredScope: "content.read"},
		{Name: "get_related_content", RequiredScope: "content.read"},
		{Name: "build_agent_context", RequiredScope: "content.read"},
		{Name: "export_agent_context", RequiredScope: "content.read"},
		{Name: "get_page_for_edit", RequiredScope: "content.read"},
		{Name: "search_content", RequiredScope: "content.read"},
		{Name: "explain_structure", RequiredScope: "content.read"},
		{Name: "get_site_health", RequiredScope: "content.read"},
		{Name: "get_broken_links", RequiredScope: "content.read"},
		{Name: "inspect_rendered", RequiredScope: "content.read"},
		{Name: "get_backlinks", RequiredScope: "content.read"},
		{Name: "suggest_links", RequiredScope: "content.read"},
		{Name: "diff_page", RequiredScope: "content.read"},
		{Name: "validate_frontmatter", RequiredScope: "content.read"},
		{Name: "validate_site", RequiredScope: "content.read"},
	}
}
