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
	Slug    string   `json:"slug"`
	Limit   int      `json:"limit,omitempty"`
	Include []string `json:"include,omitempty"`
}

// getRelatedContentAllInclude is the allowed vocabulary for get_related_content's
// opt-in `include` param (#434). The four base facets (translations,
// related_pages, backlinks, suggested_links) are always returned regardless
// of `include` — impact is a fifth, opt-in-only facet, the same pattern
// get_page_for_edit's `include=["backlinks"]` established (#465): it costs
// an extra taxonomy scan, so it isn't part of the always-returned bundle.
var getRelatedContentAllInclude = map[string]bool{
	"impact": true,
}

func resolveRelatedContentInclude(raw []string) (map[string]bool, error) {
	out := make(map[string]bool, len(raw))
	for _, r := range raw {
		if !getRelatedContentAllInclude[r] {
			return nil, fmt.Errorf("invalid_params: include must be a subset of impact (got %q)", r)
		}
		out[r] = true
	}
	return out, nil
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
	Translations []translationPageDTO `json:"translations"`
	RelatedPages []relatedPageDTO     `json:"related_pages"`
	Backlinks    []backlinkDTO        `json:"backlinks"`

	SuggestedLinks []linkSuggestionDTO `json:"suggested_links"`

	// EmptyReason is populated only when RelatedPages is empty (#458), so an
	// agent can tell "no other content shares enough tag/category overlap"
	// from "the heuristic never even had candidates to evaluate" instead of
	// just seeing an empty array with no explanation.
	EmptyReason *emptyResultExplanationDTO `json:"empty_reason,omitempty"`

	// Impact is populated only when include=["impact"] is requested (#434):
	// a pre-mutation impact summary answering "what does changing this
	// page affect?" — taxonomy terms that would be orphaned, sitemap/feed
	// presence, and any redirect aliases pointing at this slug.
	Impact *impactDTO `json:"impact,omitempty"`
}

// impactDTO is the pre-mutation impact summary for get_related_content's
// opt-in impact facet (#434). Advisory only, same posture as
// get_broken_links — never blocks a mutation.
type impactDTO struct {
	// TaxonomyOrphans lists this page's own tags/categories for which no
	// other published content page carries the same (alias-normalized)
	// term — removing the term from this page would leave it with zero
	// carriers.
	TaxonomyOrphans []string `json:"taxonomy_orphans"`
	SitemapPresent  bool     `json:"sitemap_present"`
	FeedPresent     bool     `json:"feed_present"`
	// Aliases is this page's own front-matter `aliases:` list (Hugo's
	// redirect-alias convention) — unrelated to the taxonomy package's
	// tag/category alias concept.
	Aliases []string `json:"aliases"`
}

// emptyResultExplanationDTO is additive context returned alongside an empty
// result list (#458) — it never replaces the empty array, only explains it.
type emptyResultExplanationDTO struct {
	Reason              string `json:"reason"`
	CandidatesEvaluated int    `json:"candidates_evaluated"`
	MinimumScore        int    `json:"minimum_score"`
}

// minTaxonomyAffinityScore is the lowest shared-tag/category score that
// qualifies a candidate for related_pages/suggest_links output; both
// computeRelated and scoreLinkSuggestions discard score-0 candidates.
const minTaxonomyAffinityScore = 1

func newEmptyResultExplanation(candidatesEvaluated, minimumScore int) *emptyResultExplanationDTO {
	reason := "no_candidates_with_sufficient_taxonomy_affinity"
	if candidatesEvaluated == 0 {
		reason = "no_other_published_content_to_compare"
	}
	return &emptyResultExplanationDTO{
		Reason:              reason,
		CandidatesEvaluated: candidatesEvaluated,
		MinimumScore:        minimumScore,
	}
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
	Translations   []translationPageDTO       `json:"translations"`
	RelatedPages   []relatedPageDTO           `json:"related_pages"`
	Backlinks      []backlinkDTO              `json:"backlinks"`
	SuggestedLinks []linkSuggestionDTO        `json:"suggested_links"`
	EmptyReason    *emptyResultExplanationDTO `json:"empty_reason,omitempty"`
	Impact         *impactDTO                 `json:"impact,omitempty"`
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
	// Backlinks is opt-in only via include=["backlinks"] (#465) — unlike
	// the other four sections, it's deliberately NOT part of the default
	// bundle returned when `include` is omitted, so existing callers see no
	// change in default behavior. Reuses the same collectBacklinks helper
	// get_related_content calls, so the data is identical to a standalone
	// get_backlinks call for the same slug.
	Backlinks *[]backlinkDTO `json:"backlinks,omitempty"`
}

type getPageForEditData struct {
	Page pageForEditDTO `json:"page"`
}

type getPageForEditOutput struct {
	toolcontract.ToolResponse[getPageForEditData]
	Page pageForEditDTO `json:"page"`
}

// getPageForEditDefaultSections is what an empty/omitted `include` expands
// to — the original four-section bundle, matching this repo's established
// shaping convention (#337) that omitting shaping params never reduces the
// default response. backlinks is deliberately excluded from this default
// set (#465): it's a fifth, opt-in-only vocabulary entry, not part of the
// "full bundle" omitting `include` already promises callers.
var getPageForEditDefaultSections = map[string]bool{
	"frontmatter": true,
	"markdown":    true,
	"state":       true,
	"quality":     true,
}

// getPageForEditAllSections is the full allowed vocabulary for the
// `include` param, used to validate explicitly-requested values.
var getPageForEditAllSections = map[string]bool{
	"frontmatter": true,
	"markdown":    true,
	"state":       true,
	"quality":     true,
	"backlinks":   true,
}

func resolveEditInclude(raw []string) (map[string]bool, error) {
	if len(raw) == 0 {
		// Return a copy, not the package-level map itself: callers treat
		// the result as theirs to hold, and the shared vocabulary maps must
		// not be mutated by an accidental caller edit.
		out := make(map[string]bool, len(getPageForEditDefaultSections))
		for k, v := range getPageForEditDefaultSections {
			out[k] = v
		}
		return out, nil
	}
	out := make(map[string]bool, len(raw))
	for _, r := range raw {
		if !getPageForEditAllSections[r] {
			return nil, fmt.Errorf("invalid_params: include must be a subset of frontmatter, markdown, state, quality, backlinks (got %q)", r)
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
		"Read the full Markdown-formatted content of a published page. Use this when you need the raw article body rather than rendered HTML. The response includes a `state` object so agents can tell whether they are reading built public content, source-only content, or stale source ahead of the last build. If you're about to edit or delete this page, prefer get_page_for_edit instead — it bundles this same Markdown body alongside frontmatter, revision, and quality signals in one call. Input: indexed slug only.",
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
		"Read structured metadata for a published page, including title, tags, categories, date, URL, estimated reading time, and a `state` object describing source/build/public/index freshness. `lang` is now populated immediately for a source-only page read back before the next Hugo build (e.g. right after create_page) — it no longer lags behind `resolved_lang` until the page is built. If you're about to edit or delete this page, prefer get_page_for_edit instead — it bundles this same metadata alongside markdown, revision, and quality signals in one call. Input: indexed slug only.",
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
		"Return the four editorial surfaces for a slug: related_pages (tag/category overlap), backlinks (pages that link here), suggested_links (link candidates scored by tag affinity), and translations. Use this for content recommendations and editorial linking. If you only need one facet, get_backlinks (backlinks alone) and suggest_links (also works for a draft not yet indexed, via tags/categories/body) are cheaper standalone alternatives. When related_pages comes back empty, `empty_reason` explains why (candidates_evaluated, minimum_score) instead of leaving you to guess whether nothing qualifies or nothing else exists at all. Pass `include: [\"impact\"]` for a pre-mutation impact summary (`impact.taxonomy_orphans`, `impact.sitemap_present`, `impact.feed_present`, `impact.aliases`) answering \"what does changing this page affect?\" before a risky edit/delete — advisory only, never blocks a mutation, same posture as get_broken_links (#434). Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getRelatedContentInput) (*mcp.CallToolResult, getRelatedContentOutput, error) {
			if idx == nil {
				return nil, getRelatedContentOutput{}, fmt.Errorf("index not initialized")
			}
			include, err := resolveRelatedContentInclude(in.Include)
			if err != nil {
				return nil, getRelatedContentOutput{}, err
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getRelatedContentOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getRelatedContentOutput{}, err
			}
			limit := clampLimit(in.Limit, 5, 20)
			ref := resolvedPublicPage(resolved)
			if resolved.Public != nil {
				ref = *resolved.Public
			}
			translations := collectTranslations(idx, ref)
			related, evaluated := computeRelated(idx, ref, limit)
			backlinks := collectBacklinks(idx, ref.Slug)
			suggestedLinks, _ := scoreLinkSuggestions(idx, ref.Slug, ref.Tags, ref.Categories, "", limit)
			data := getRelatedContentData{
				Translations:   translations,
				RelatedPages:   related,
				Backlinks:      backlinks,
				SuggestedLinks: suggestedLinks,
			}
			if len(related) == 0 {
				data.EmptyReason = newEmptyResultExplanation(evaluated, minTaxonomyAffinityScore)
			}
			if include["impact"] {
				impact := computeImpact(idx, resolved, ref, aliases)
				data.Impact = &impact
			}
			return nil, newGetRelatedContentOutput(data, time.Now().UTC()), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 20) })

	addReadOnlyTool(s, "build_agent_context", "Build agent context",
		"Build a complete context bundle for a published page: metadata, reading time, full Markdown content, related pages, and explicit lifecycle `state`. Use this before summarizing or discussing a page. If you're about to mutate this page instead, prefer get_page_for_edit — it adds `revision` and `quality` (needed for create_page/update_page/delete_page) but omits translations/related_pages. Supports response shaping: `response_mode: \"compact\"` drops translations/related_pages and returns only frontmatter, markdown, and state; `max_body_chars: N` truncates the Markdown body to N characters (applies in either mode). Omitting both preserves the full default shape. `lang` is now populated immediately for a source-only page read back before the next Hugo build — it no longer lags behind `resolved_lang` until the page is built. Input: indexed slug only.",
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
				relatedPages, _ := computeRelated(idx, ref, 5)
				ac = agentContextDTO{
					Frontmatter:  fm,
					Markdown:     md,
					State:        state,
					Translations: collectTranslations(idx, ref),
					RelatedPages: relatedPages,
				}
			}
			var warnings []string
			if truncated {
				warnings = append(warnings, fmt.Sprintf("markdown truncated to max_body_chars=%d; set a higher value or omit the parameter to get the full body.", in.MaxBodyChars))
			}
			return nil, newBuildAgentContextOutput(buildAgentContextData{Context: ac}, warnings, time.Now().UTC()), nil
		}, func(s any) any {
			// "" is included because ResolveResponseMode treats an omitted
			// or explicitly empty value the same as "standard" — the enum
			// must not reject a value runtime already accepts. "full" and
			// "ids_only" are deliberately excluded: they're reserved
			// vocabulary rejected at runtime (#337), not yet implemented.
			return tools.WithEnum(s, "response_mode", "", string(toolcontract.ResponseModeStandard), string(toolcontract.ResponseModeCompact))
		})

	addReadOnlyTool(s, "export_agent_context", "Export agent context",
		"Paginated export of page context bundles filtered by tag or category. Each page includes front matter, reading time, and lifecycle `state`. By default also includes full Markdown content, which caps `limit` at 10 pages to keep the response within MCP message size limits; set `include_body=false` to fetch metadata only (frontmatter + state, no Markdown) at a higher cap of 50 pages. Use this for bulk analysis or migration work across many pages; for a single page use build_agent_context instead, which additionally includes translations and related pages.",
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
		}, func(s any) any {
			// The published ceiling is the loosest of the two runtime caps
			// (exportAgentContextMaxLimitMetadataOnly=50, with include_body
			// defaulting true and further capping to
			// exportAgentContextMaxLimitWithBody=10) — a schema minimum
			// couldn't express the include_body-dependent cap without two
			// mutually exclusive schemas, and the tool's own warnings-on-cap
			// behavior already tells the caller when their limit was
			// narrowed by include_body at runtime.
			return tools.WithMaxLimit(s, "limit", exportAgentContextMaxLimitMetadataOnly)
		})

	addReadOnlyTool(s, "get_page_for_edit", "Get page for edit",
		"Compact edit-oriented read: returns the core bundle an agent needs before modifying a page (frontmatter, markdown, lifecycle `state`, quality signals, and a stable `revision`) in a single call instead of chaining get_page_frontmatter + get_page_markdown + build_agent_context. `include: [...]` (subset of frontmatter, markdown, state, quality; default all four) and `max_body_chars` (rune-aware truncation of the markdown body) shape the response down. `quality.valid`/`quality.broken_links` are omitted when quality wasn't requested or the caller's profile has no source access. `frontmatter.lang` is now populated immediately for a source-only page read back before the next Hugo build (e.g. immediately after create_page) — it no longer lags behind `frontmatter.resolved_lang` until the page is built. Pass `include: [\"backlinks\", ...]` to also get impact-analysis data (pages linking here) in the same call before a risky edit/delete — `page.backlinks` carries the same backlinks array as a standalone get_backlinks call for this slug (not its `count`/`slug` envelope fields), and it's opt-in only, never included in the default four-section bundle when `include` is omitted. Lower-level tools remain available; this is an addition, not a replacement. Input: indexed slug only.",
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
			if include["backlinks"] {
				backlinks := collectBacklinks(idx, p.Slug)
				page.Backlinks = &backlinks
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
		EmptyReason:    data.EmptyReason,
		Impact:         data.Impact,
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
		Lang:       src.Lang,
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

func computeRelated(idx *site.Index, ref site.Page, limit int) ([]relatedPageDTO, int) {
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
	evaluated := 0
	classifier := site.NewClassifierFromPages(idx.Sitemap())
	refTranslationKey := translationKey(ref.Slug)
	for _, pg := range idx.Sitemap() {
		// Only count actual content candidates (#458) — matches
		// scoreLinkSuggestions' IsContent filter so candidates_evaluated
		// means the same thing across both tools, excluding structural
		// pages (home, taxonomy/term lists) that could never be a real
		// related-content match.
		if !classifier.IsContent(pg) {
			continue
		}
		if pg.Slug == ref.Slug {
			continue
		}
		if isTranslationVariant(refTranslationKey, pg.Slug) {
			continue
		}
		evaluated++
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
	return out, evaluated
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

// schemaOpts, when provided, post-process the inferred input schema (#418) —
// e.g. tools.WithEnum/tools.WithRange to publish a real enum/range constraint
// that jsonschema-go's struct-tag inference can't express directly.
func addReadOnlyTool[In, Out any](s *mcp.Server, name, title, description string, handler mcp.ToolHandlerFor[In, Out], schemaOpts ...func(any) any) {
	inputSchema := tools.MustSchema[In]()
	for _, opt := range schemaOpts {
		inputSchema = opt(inputSchema)
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:         name,
		Title:        title,
		Description:  description,
		InputSchema:  inputSchema,
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

// frontmatterStringSlice extracts a []string from a raw front-matter value
// decoded by yaml.v3 (either []string or []any of scalars) — the same
// permissive shape hugosite.SourceIndex's own unexported stringSlice
// handles when parsing tags/categories, needed here for the `aliases:`
// field, which nothing in the codebase reads today (#434).
func frontmatterStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	default:
		return nil
	}
}

// computeImpact builds get_related_content's opt-in impact facet (#434):
// a pre-mutation summary of what changing ref would affect. Advisory only
// — never blocks a mutation, same posture as get_broken_links.
func computeImpact(idx *site.Index, resolved site.ResolvedPage, ref site.Page, aliases map[string]string) impactDTO {
	impact := impactDTO{
		TaxonomyOrphans: []string{},
		Aliases:         []string{},
	}

	if idx != nil {
		// Hoisted out of the per-term loop below (design budget: one
		// ContentPages() scan per call, not one per term).
		contentPages := idx.ContentPages()
		for _, term := range append(append([]string{}, ref.Tags...), ref.Categories...) {
			target := taxonomy.ResolveAlias(taxonomy.Slug(term), aliases)
			if target == "" {
				continue
			}
			orphaned := true
			for _, pg := range contentPages {
				if pg.Slug == ref.Slug {
					continue
				}
				if taxonomy.MatchesSlugWithAliases(pg.Tags, target, aliases) ||
					taxonomy.MatchesSlugWithAliases(pg.Categories, target, aliases) {
					orphaned = false
					break
				}
			}
			if orphaned {
				impact.TaxonomyOrphans = append(impact.TaxonomyOrphans, term)
			}
		}
		for _, pg := range idx.Sitemap() {
			if pg.Slug == ref.Slug {
				impact.SitemapPresent = true
				break
			}
		}
		for _, pg := range idx.GetFeed(0) {
			if pg.Slug == ref.Slug {
				impact.FeedPresent = true
				break
			}
		}
	}

	if resolved.Source != nil {
		if raw, ok := resolved.Source.FrontmatterRaw["aliases"]; ok {
			impact.Aliases = frontmatterStringSlice(raw)
			if impact.Aliases == nil {
				impact.Aliases = []string{}
			}
		}
	}

	return impact
}

// Defs returns the tool definitions for this package (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "get_page_markdown", RequiredScope: ""},
		{Name: "get_page_frontmatter", RequiredScope: ""},
		{Name: "get_related_content", RequiredScope: ""},
		{Name: "build_agent_context", RequiredScope: ""},
		{Name: "export_agent_context", RequiredScope: ""},
		{Name: "get_page_for_edit", RequiredScope: ""},
		{Name: "list_content_types", RequiredScope: ""},
		{Name: "list_page_assets", RequiredScope: ""},
		{Name: "search_content", RequiredScope: ""},
		{Name: "explain_structure", RequiredScope: ""},
		{Name: "get_site_health", RequiredScope: ""},
		{Name: "get_broken_links", RequiredScope: ""},
		{Name: "inspect_rendered", RequiredScope: ""},
		{Name: "get_backlinks", RequiredScope: ""},
		{Name: "suggest_links", RequiredScope: ""},
		{Name: "diff_page", RequiredScope: ""},
		{Name: "validate_frontmatter", RequiredScope: ""},
		{Name: "validate_site", RequiredScope: ""},
	}
}
