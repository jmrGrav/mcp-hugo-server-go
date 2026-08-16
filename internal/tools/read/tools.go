package read

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/aireadiness"
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
	Slug         string `json:"slug"`
	Lang         string `json:"lang,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	IncludeTerms *bool  `json:"include_terms,omitempty"`
}

type pageMarkdownDTO struct {
	Slug               string              `json:"slug"`
	SourceKey          string              `json:"source_key,omitempty"`
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
	Slug         string `json:"slug"`
	Lang         string `json:"lang,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	IncludeTerms *bool  `json:"include_terms,omitempty"`
}

type frontmatterDTO struct {
	Slug               string                      `json:"slug"`
	SourceKey          string                      `json:"source_key,omitempty"`
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
	// FeaturedImage/FeaturedImagePreview/Description/Draft (#817) surface
	// frontmatter fields that were already writable (update_page's
	// featured_image/description/draft params) but invisible to a caller
	// reading through get_page_frontmatter/get_page_for_edit/
	// build_agent_context/export_agent_context — the only way to discover
	// e.g. a page's featuredImage was an indirect tool like diff_page or
	// list_page_assets. Named to match update_page's write-side parameter
	// names (snake_case, featured_image not featuredImage) so a caller can
	// round-trip a read value straight back into a write call. Omitted
	// (never a zero value) when unset in frontmatter or when only
	// resolved.Public is available (no source frontmatter to read at all).
	FeaturedImage        string `json:"featured_image,omitempty"`
	FeaturedImagePreview string `json:"featured_image_preview,omitempty"`
	Description          string `json:"description,omitempty"`
	Draft                *bool  `json:"draft,omitempty"`
}

type getPageFrontmatterData struct {
	Frontmatter frontmatterDTO `json:"frontmatter"`
}

type getRelatedContentInput struct {
	Slug            string   `json:"slug"`
	Language        string   `json:"language,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	Include         []string `json:"include,omitempty"`
	OnePerSourceKey bool     `json:"one_per_source_key,omitempty"`
	ResponseMode    string   `json:"response_mode,omitempty"`
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

	// IndexInfo flags when the in-memory index (backing Backlinks/RelatedPages)
	// is behind on-disk content — see indexStalenessDTO (#583).
	IndexInfo *indexStalenessDTO `json:"index_staleness,omitempty"`
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

func newEmptyResultExplanation(candidatesEvaluated, minimumScore int, filteredOut bool) *emptyResultExplanationDTO {
	reason := "no_candidates_with_sufficient_taxonomy_affinity"
	switch {
	case filteredOut:
		// Scored candidates existed but language/one_per_source_key
		// filtering removed every one of them — a different situation
		// from "nothing scored", and worth distinguishing so a caller
		// isn't told to loosen scoring when the fix is to drop the filter.
		reason = "no_candidates_matching_language_or_source_key_filter"
	case candidatesEvaluated == 0:
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
	Lang         string `json:"lang,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	MaxBodyChars *int   `json:"max_body_chars,omitempty"`
	IncludeTerms *bool  `json:"include_terms,omitempty"`
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
	ResponseMode string `json:"response_mode,omitempty"`
	Tag          string `json:"tag,omitempty"`
	Category     string `json:"category,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	IncludeBody  *bool  `json:"include_body,omitempty"`
	IncludeTerms *bool  `json:"include_terms,omitempty"`
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
}

type getPageFrontmatterOutput struct {
	toolcontract.ToolResponse[getPageFrontmatterData]
}

type getRelatedContentOutput struct {
	toolcontract.ToolResponse[getRelatedContentData]
}

type buildAgentContextOutput struct {
	toolcontract.ToolResponse[buildAgentContextData]
}

type exportAgentContextOutput struct {
	toolcontract.ToolResponse[exportAgentContextData]
}

type getPageForEditInput struct {
	Slug         string   `json:"slug"`
	Lang         string   `json:"lang,omitempty"`
	Include      []string `json:"include,omitempty"`
	MaxBodyChars *int     `json:"max_body_chars,omitempty"`
	ResponseMode string   `json:"response_mode,omitempty"`
	IncludeTerms *bool    `json:"include_terms,omitempty"`
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
	Slug      string `json:"slug"`
	SourceKey string `json:"source_key,omitempty"`
	Revision  string `json:"revision,omitempty"`
	// BundleRevision (#857) is the optimistic-concurrency token for the WHOLE
	// page bundle (every translation + bundle-local assets) as one unit, not
	// just this single resolved file that `revision` covers. Use `revision`
	// when your edit touches only this one file; check `bundle_revision`
	// before a bundle-aware operation that must be sure no sibling
	// translation or shared asset changed behind your back. Omitted if the
	// bundle directory can't be resolved (e.g. a public-only page with no
	// source file).
	BundleRevision string               `json:"bundle_revision,omitempty"`
	Frontmatter    *frontmatterDTO      `json:"frontmatter,omitempty"`
	Markdown       string               `json:"markdown,omitempty"`
	State          *site.LifecycleState `json:"state,omitempty"`
	Quality        *pageQualityDTO      `json:"quality,omitempty"`
	// Backlinks is opt-in only via include=["backlinks"] (#465) — unlike
	// the other four sections, it's deliberately NOT part of the default
	// bundle returned when `include` is omitted, so existing callers see no
	// change in default behavior. Reuses the same premutationBacklinks helper
	// get_related_content calls, so the data is identical to a standalone
	// get_backlinks call for the same slug.
	Backlinks *[]backlinkDTO `json:"backlinks,omitempty"`
	// Impact is opt-in only via include=["impact"] (#527). It carries the
	// same impact facet get_related_content(include=["impact"]) returns.
	Impact *impactDTO `json:"impact,omitempty"`
	// Preview is opt-in only via include=["preview"] (#527). It carries the
	// same preview facet inspect_rendered(include_preview=true) returns for
	// the same published page. Source-only pages omit it and surface a
	// warning instead of failing the whole edit-prep bundle.
	Preview *previewDTO `json:"preview,omitempty"`
	// Readiness is opt-in only via include=["readiness"] (#621). It carries
	// the same source-structure audit check_ai_readiness returns for the
	// same slug. A page with no matching source (public-only legacy
	// content) omits it and surfaces a warning instead of failing the whole
	// edit-prep bundle, matching preview's existing fallback behavior.
	Readiness *pageReadinessDTO `json:"readiness,omitempty"`
}

// pageReadinessDTO mirrors check_ai_readiness's own response fields, minus
// slug/resolved_lang/resolved_source_path/revision/state — those already
// exist at the page level in this bundle, so this is not a byte-identical
// copy of that tool's whole payload the way backlinks/impact/preview are,
// just the audit result itself.
type pageReadinessDTO struct {
	Status      string             `json:"status"`
	Checks      aireadiness.Checks `json:"checks"`
	Warnings    []string           `json:"warnings"`
	Suggestions []string           `json:"suggestions"`
}

type getPageForEditData struct {
	Page pageForEditDTO `json:"page"`
}

type getPageForEditOutput struct {
	toolcontract.ToolResponse[getPageForEditData]
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
	"impact":      true,
	"preview":     true,
	"readiness":   true,
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
			return nil, fmt.Errorf("invalid_params: include must be a subset of frontmatter, markdown, state, quality, backlinks, impact, preview, readiness (got %q)", r)
		}
		out[r] = true
	}
	return out, nil
}

func newGetPageForEditOutput(data getPageForEditData, warnings []string, now time.Time) getPageForEditOutput {
	resp := successEnvelopeWithContentProvenance(data, now, contentProvenanceSiteSourceUntrusted)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return getPageForEditOutput{ToolResponse: resp}
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
	registerReadPageTools(s, idx, srcIdx, resolver, cfg)
	registerReadRelationshipTools(s, idx, srcIdx, resolver, cfg, aliases)
	registerReadAgentContextTools(s, idx, srcIdx, resolver, cfg, aliases)
}

func registerReadPageTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, resolver *site.PageResolver, cfg config.Config) {
	addReadOnlyTool(s, "get_page_markdown", "Read page Markdown",
		"Read the full Markdown-formatted content of a published page. Use this when you need the raw article body rather than rendered HTML. The response includes a `state` object so agents can tell whether they are reading built public content, source-only content, or stale source ahead of the last build. `include_terms` defaults to true: pass `include_terms=false` to omit `tag_terms`/`category_terms` and keep only the plainer `tags`/`categories` arrays; `response_mode:\"compact\"` implies the same omission. For multilingual content, pass `lang` to resolve one translation explicitly; URL-based language selection (`/en/posts/.../`) remains accepted as a compatibility path. If you're about to edit or delete this page, prefer get_page_for_edit instead — it bundles this same Markdown body alongside frontmatter, revision, and quality signals in one call. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getFullPageMarkdownInput) (*mcp.CallToolResult, getFullPageMarkdownOutput, error) {
			if idx == nil && srcIdx == nil {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("index not initialized")
			}
			if err := validateSlugLangConsistency(in.Slug, in.Lang); err != nil {
				return nil, getFullPageMarkdownOutput{}, err
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, getFullPageMarkdownOutput{}, err
			}
			resolved, ok := resolver.ResolveWithLang(in.Slug, in.Lang)
			if !ok {
				return nil, getFullPageMarkdownOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getFullPageMarkdownOutput{}, err
			}
			return nil, newGetFullPageMarkdownOutput(getFullPageMarkdownData{
				Page: toResolvedPageMarkdownDTO(resolved, cfg.ContentRoot, cfg.SiteRoot, includeTerms(mode, in.IncludeTerms)),
			}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_page_frontmatter", "Read page metadata",
		"Read structured metadata for a published page, including title, tags, categories, date, URL, estimated reading time, and a `state` object describing source/build/public/index freshness. `featured_image`/`featured_image_preview`/`description`/`draft` (#817) are populated from source frontmatter when available and omitted (not a zero value) when unset or when only public output is resolvable — `featured_image`'s name matches update_page's `featured_image` write parameter for direct read-then-write round-tripping. `include_terms` defaults to true: pass `include_terms=false` to omit `tag_terms`/`category_terms` and keep only the plainer `tags`/`categories` arrays; `response_mode:\"compact\"` implies the same omission. For multilingual content, pass `lang` to resolve one translation explicitly; URL-based language selection (`/en/posts/.../`) remains accepted as a compatibility path. `lang` is now populated immediately for a source-only page read back before the next Hugo build (e.g. right after create_page) — it no longer lags behind `resolved_lang` until the page is built. If you're about to edit or delete this page, prefer get_page_for_edit instead — it bundles this same metadata alongside markdown, revision, and quality signals in one call. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getPageFrontmatterInput) (*mcp.CallToolResult, getPageFrontmatterOutput, error) {
			if idx == nil {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("index not initialized")
			}
			if err := validateSlugLangConsistency(in.Slug, in.Lang); err != nil {
				return nil, getPageFrontmatterOutput{}, err
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, getPageFrontmatterOutput{}, err
			}
			resolved, ok := resolver.ResolveWithLang(in.Slug, in.Lang)
			if !ok {
				return nil, getPageFrontmatterOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getPageFrontmatterOutput{}, err
			}
			p := resolvedPublicPage(resolved)
			md := resolvedMarkdown(resolved)
			rt := readingTimeMinutes(md)
			return nil, newGetPageFrontmatterOutput(getPageFrontmatterData{
				Frontmatter: toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt, includeTerms(mode, in.IncludeTerms)),
			}, time.Now().UTC()), nil
		})
}

func registerReadRelationshipTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, resolver *site.PageResolver, cfg config.Config, aliases map[string]string) {
	addReadOnlyTool(s, "get_related_content", "Get related content",
		"Return the four editorial surfaces for a slug: related_pages (tag/category overlap), backlinks (pages that link here), suggested_links (link candidates scored by tag affinity), and translations. Use this for content recommendations and editorial linking. If you only need one facet, get_backlinks (backlinks alone) and suggest_links (also works for a draft not yet indexed, via tags/categories/body) are cheaper standalone alternatives. When related_pages comes back empty, `empty_reason` explains why (candidates_evaluated, minimum_score) instead of leaving you to guess whether nothing qualifies or nothing else exists at all. Pass `include: [\"impact\"]` for a pre-mutation impact summary (`impact.taxonomy_orphans`, `impact.sitemap_present`, `impact.feed_present`, `impact.aliases`) answering \"what does changing this page affect?\" before a risky edit/delete — advisory only, never blocks a mutation, same posture as get_broken_links (#434). `index_staleness` is present only when the underlying index (backing related_pages/backlinks) is behind on-disk content — its absence means it's current (#583). `index_staleness.likely_source` is a coarse, best-effort hint at why: `\"mcp_pending_build\"` (a known, expected write via this server awaiting the next build) vs. `\"external_or_unknown\"` (no such record — most plausibly an out-of-band edit, e.g. direct SSH/git) (#617). Options: `language` filters rows to one language; `one_per_source_key:true` collapses translated siblings; `response_mode:\"compact\"` omits backlinks, translations, and taxonomy detail while retaining ranked recommendations. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Input: indexed slug only.",
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
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, getRelatedContentOutput{}, err
			}
			limit := clampLimit(in.Limit, 5, 20)
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, getRelatedContentOutput{}, err
			}
			ref := resolvedPublicPage(resolved)
			if resolved.Public != nil {
				ref = *resolved.Public
			}
			translations := collectTranslations(idx, ref)
			// #1041: computeRelated/scoreLinkSuggestions each truncate to
			// their own limit argument internally, before the
			// language/one_per_source_key filter below runs. Truncating
			// first would silently under-deliver relative to the requested
			// limit whenever a same-source-key sibling (near-guaranteed on
			// a bilingual site, since translations usually share tags/
			// categories and so score identically) occupied one of the
			// pre-filter top-limit slots. When filtering is requested,
			// over-fetch a generously larger candidate pool so there's
			// enough headroom to still fill limit distinct-source-key
			// results after filtering, then re-truncate to the real limit.
			fetchLimit := limit
			if in.Language != "" || in.OnePerSourceKey {
				fetchLimit = overfetchLimit(limit)
			}
			related, evaluated := computeRelated(idx, ref, fetchLimit)
			relatedBeforeFilter := len(related)
			backlinks := premutationBacklinks(idx, ref.Slug)
			suggestedLinks, _ := scoreLinkSuggestions(idx, ref.Slug, ref.Tags, ref.Categories, "", fetchLimit)
			translations = filterRelationshipTranslations(translations, in.Language, in.OnePerSourceKey)
			related = filterRelatedPages(related, in.Language, in.OnePerSourceKey)
			suggestedLinks = filterSuggestedLinks(suggestedLinks, in.Language, in.OnePerSourceKey)
			if len(related) > limit {
				related = related[:limit]
			}
			if len(suggestedLinks) > limit {
				suggestedLinks = suggestedLinks[:limit]
			}
			if mode == toolcontract.ResponseModeCompact {
				translations, backlinks = nil, nil
				for i := range related {
					related[i].SharedTags, related[i].SharedCategories, related[i].SharedTagTerms, related[i].SharedCategoryTerms = nil, nil, nil, nil
				}
				for i := range suggestedLinks {
					suggestedLinks[i].SharedTags, suggestedLinks[i].SharedCategories = nil, nil
				}
			} else {
				if translations == nil {
					translations = []translationPageDTO{}
				}
				if related == nil {
					related = []relatedPageDTO{}
				}
				if backlinks == nil {
					backlinks = []backlinkDTO{}
				}
				if suggestedLinks == nil {
					suggestedLinks = []linkSuggestionDTO{}
				}
			}
			data := getRelatedContentData{
				Translations:   translations,
				RelatedPages:   related,
				Backlinks:      backlinks,
				SuggestedLinks: suggestedLinks,
				IndexInfo:      staleness(idx, srcIdx, cfg),
			}
			if len(related) == 0 {
				filteredOut := relatedBeforeFilter > 0 && (in.Language != "" || in.OnePerSourceKey)
				data.EmptyReason = newEmptyResultExplanation(evaluated, minTaxonomyAffinityScore, filteredOut)
			}
			if include["impact"] {
				impact := premutationImpact(idx, resolved, ref, aliases)
				data.Impact = &impact
			}
			return nil, newGetRelatedContentOutput(data, time.Now().UTC()), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 20) })
}

func registerReadAgentContextTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, resolver *site.PageResolver, cfg config.Config, aliases map[string]string) {
	addReadOnlyTool(s, "build_agent_context", "Build agent context",
		"Build a complete context bundle for a published page: metadata, reading time, full Markdown content, related pages, and explicit lifecycle `state`. Use this before summarizing or discussing a page. If you're about to mutate this page instead, prefer get_page_for_edit — it adds `revision` and `quality` (needed for create_page/update_page/delete_page) but omits translations/related_pages. Supports response shaping: `response_mode: \"compact\"` drops translations/related_pages and returns only frontmatter, markdown, and state; `max_body_chars: N` truncates the Markdown body to N characters (applies in either mode, N must be greater than 0 when provided). `include_terms` defaults to true: pass `include_terms=false` to omit nested `frontmatter.tag_terms`/`frontmatter.category_terms`; `response_mode:\"compact\"` implies the same omission. For multilingual content, pass `lang` to resolve one translation explicitly; URL-based language selection (`/en/posts/.../`) remains accepted as a compatibility path. Omitting both preserves the full default shape. `lang` is now populated immediately for a source-only page read back before the next Hugo build — it no longer lags behind `resolved_lang` until the page is built. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in buildAgentContextInput) (*mcp.CallToolResult, buildAgentContextOutput, error) {
			if idx == nil {
				return nil, buildAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			if err := positiveMaxBodyCharsError(in.MaxBodyChars); err != nil {
				return nil, buildAgentContextOutput{}, err
			}
			if err := validateSlugLangConsistency(in.Slug, in.Lang); err != nil {
				return nil, buildAgentContextOutput{}, err
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, buildAgentContextOutput{}, err
			}
			resolved, ok := resolver.ResolveWithLang(in.Slug, in.Lang)
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
			fm := toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt, includeTerms(mode, in.IncludeTerms))
			state := resolvedState(resolved, cfg.SiteRoot)
			md, truncated := toolcontract.TruncateBody(md, derefMaxBodyChars(in.MaxBodyChars))

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
				warnings = append(warnings, fmt.Sprintf("markdown truncated to max_body_chars=%d; set a higher value or omit the parameter to get the full body.", *in.MaxBodyChars))
			}
			return nil, newBuildAgentContextOutput(buildAgentContextData{Context: ac}, warnings, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "export_agent_context", "Export agent context",
		"Paginated export of page context bundles filtered by tag or category. Each page includes front matter, reading time, and lifecycle `state`. By default also includes full Markdown content, which caps `limit` at 10 pages to keep the response within MCP message size limits; set `include_body=false` to fetch metadata only (frontmatter + state, no Markdown) at a higher cap of 50 pages. `include_terms` defaults to true: pass `include_terms=false` to omit nested `frontmatter.tag_terms`/`frontmatter.category_terms`; `response_mode:\"compact\"` implies the same omission. Use this for bulk analysis or migration work across many pages; for a single page use build_agent_context instead, which additionally includes translations and related pages. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in exportAgentContextInput) (*mcp.CallToolResult, exportAgentContextOutput, error) {
			if idx == nil {
				return nil, exportAgentContextOutput{}, fmt.Errorf("index not initialized")
			}
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, exportAgentContextOutput{}, err
			}
			if err := negativeOffsetError(in.Offset); err != nil {
				return nil, exportAgentContextOutput{}, err
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
					Frontmatter: toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt, includeTerms(toolcontract.ResponseModeStandard, in.IncludeTerms)),
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
		"Compact edit-oriented read: returns the core bundle an agent needs before modifying a page (frontmatter, markdown, lifecycle `state`, quality signals, and a stable `revision`) in a single call instead of chaining get_page_frontmatter + get_page_markdown + build_agent_context. `include: [...]` (subset of frontmatter, markdown, state, quality, backlinks, impact, preview, readiness; default still only the original four) and `max_body_chars` (rune-aware truncation of the markdown body; must be greater than 0 when provided) shape the response down. `include_terms` defaults to true: pass `include_terms=false` to omit nested `frontmatter.tag_terms`/`frontmatter.category_terms`; `response_mode:\"compact\"` implies the same omission. For multilingual content, pass `lang` to resolve one translation explicitly; URL-based language selection (`/en/posts/.../`) remains accepted as a compatibility path. `quality.valid`/`quality.broken_links` are omitted when quality wasn't requested or the caller's profile has no source access. `frontmatter.lang` is now populated immediately for a source-only page read back before the next Hugo build (e.g. immediately after create_page) — it no longer lags behind `frontmatter.resolved_lang` until the page is built. `frontmatter.featured_image`/`featured_image_preview`/`description`/`draft` (#817) are populated when set in source frontmatter, matching update_page's write parameter names for direct round-tripping. `page.backlinks` is identical to get_backlinks, `page.impact` is identical to get_related_content(include=[\"impact\"]), `page.preview` is identical to inspect_rendered(include_preview=true) when rendered output exists, and `page.readiness` is identical to check_ai_readiness's own check result for this slug — combining it with `preview`/`quality`/`backlinks` in one call covers the full pre-publish check (SEO/rendered validity, broken links, and source-structure quality) without three separate round-trips (#621). All four are opt-in only and never part of the default four-section bundle when `include` is omitted. Source-only pages omit `preview` with a warning instead of failing the whole bundle; a page with no matching source omits `readiness` the same way. Lower-level tools remain available; this is an addition, not a replacement. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Input: indexed slug only.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getPageForEditInput) (*mcp.CallToolResult, getPageForEditOutput, error) {
			if idx == nil {
				return nil, getPageForEditOutput{}, fmt.Errorf("index not initialized")
			}
			if err := positiveMaxBodyCharsError(in.MaxBodyChars); err != nil {
				return nil, getPageForEditOutput{}, err
			}
			if err := validateSlugLangConsistency(in.Slug, in.Lang); err != nil {
				return nil, getPageForEditOutput{}, err
			}
			include, err := resolveEditInclude(in.Include)
			if err != nil {
				return nil, getPageForEditOutput{}, err
			}
			resolved, ok := resolver.ResolveWithLang(in.Slug, in.Lang)
			if !ok {
				return nil, getPageForEditOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err = readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getPageForEditOutput{}, err
			}
			p := resolvedPublicPage(resolved)
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, getPageForEditOutput{}, err
			}
			page := pageForEditDTO{
				Slug:      p.Slug,
				SourceKey: contentmodel.SourceKeyFromLogicalPath(fileutil.LogicalContentPath(cfg.ContentRoot, resolved.SourcePath)),
				Revision:  resolvedRevision(resolved),
			}
			// bundle_revision (#857) covers the whole bundle directory (all
			// translations + bundle-local assets), so a bundle-aware caller can
			// detect a sibling translation or shared asset changing behind its
			// back — something the single-file `revision` above cannot see.
			if resolved.SourcePath != "" {
				if brev, brevErr := contentmodel.BundleRevision(filepath.Dir(resolved.SourcePath)); brevErr == nil {
					page.BundleRevision = brev
				}
			}
			var warnings []string

			if include["frontmatter"] {
				rt := readingTimeMinutes(resolvedMarkdown(resolved))
				fm := toFrontmatterDTO(p, resolved, cfg.ContentRoot, cfg.SiteRoot, rt, includeTerms(mode, in.IncludeTerms))
				page.Frontmatter = &fm
			}
			if include["markdown"] {
				md, truncated := toolcontract.TruncateBody(resolvedMarkdown(resolved), derefMaxBodyChars(in.MaxBodyChars))
				page.Markdown = md
				if truncated {
					warnings = append(warnings, fmt.Sprintf("markdown truncated to max_body_chars=%d; set a higher value or omit the parameter to get the full body.", *in.MaxBodyChars))
				}
			}
			if include["state"] {
				st := resolvedState(resolved, cfg.SiteRoot)
				page.State = &st
			}
			if include["quality"] {
				qSrc := sourceIndexForProfile(srcIdx, site.IsReaderProfile(ctx))
				if qSrc != nil {
					if srcPages, err := sourcePagesForValidation(qSrc, in.Slug, in.Lang); err == nil && len(srcPages) > 0 {
						issues := validateFrontMatterPage(srcPages[0], aliases)
						broken := 0
						if indexedPage, found := idx.GetBySlug(p.Slug); found {
							broken = len(brokenLinksForPage(cfg, idx, idx.Classifier(), *indexedPage))
						}
						page.Quality = &pageQualityDTO{Valid: len(issues) == 0, BrokenLinks: broken}
					}
				}
			}
			if include["backlinks"] {
				backlinks := premutationBacklinks(idx, p.Slug)
				page.Backlinks = &backlinks
			}
			if include["impact"] {
				impactRef := resolvedPublicPage(resolved)
				if resolved.Public != nil {
					impactRef = *resolved.Public
				}
				impact := premutationImpact(idx, resolved, impactRef, aliases)
				page.Impact = &impact
			}
			if include["preview"] {
				if preview, err := loadRenderedPreview(ctx, idx, cfg, resolved, p); err != nil {
					warnings = append(warnings, err.Error())
				} else {
					page.Preview = &preview
				}
			}
			if include["readiness"] {
				if resolved.Source == nil {
					warnings = append(warnings, fmt.Sprintf("readiness unavailable: no source content found for slug %q", in.Slug))
				} else {
					report := aireadiness.Analyze(aireadiness.Document{
						Title:       resolved.Source.Title,
						Date:        resolved.Source.Date,
						Summary:     frontmatterStringValue(resolved.Source.FrontmatterRaw["summary"]),
						Description: frontmatterStringValue(resolved.Source.FrontmatterRaw["description"]),
						Tags:        canonicalTaxonomyStrings(resolved.Source.Tags),
						Categories:  canonicalTaxonomyStrings(resolved.Source.Categories),
						Markdown:    resolved.Source.Body,
					})
					page.Readiness = &pageReadinessDTO{
						Status:      report.Status,
						Checks:      report.Checks,
						Warnings:    report.Warnings,
						Suggestions: report.Suggestions,
					}
				}
			}
			return nil, newGetPageForEditOutput(getPageForEditData{Page: page}, warnings, time.Now().UTC()), nil
		})
}

func newGetFullPageMarkdownOutput(data getFullPageMarkdownData, now time.Time) getFullPageMarkdownOutput {
	return getFullPageMarkdownOutput{ToolResponse: successEnvelopeWithContentProvenance(data, now, contentProvenanceSiteSourceUntrusted)}
}

func newGetPageFrontmatterOutput(data getPageFrontmatterData, now time.Time) getPageFrontmatterOutput {
	return getPageFrontmatterOutput{ToolResponse: successEnvelopeWithContentProvenance(data, now, contentProvenanceSiteSourceUntrusted)}
}

func newGetRelatedContentOutput(data getRelatedContentData, now time.Time) getRelatedContentOutput {
	return getRelatedContentOutput{ToolResponse: successEnvelope(data, now)}
}

func newBuildAgentContextOutput(data buildAgentContextData, warnings []string, now time.Time) buildAgentContextOutput {
	resp := successEnvelopeWithContentProvenance(data, now, contentProvenanceSiteSourceUntrusted)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return buildAgentContextOutput{ToolResponse: resp}
}

func newExportAgentContextOutput(data exportAgentContextData, warnings []string, now time.Time) exportAgentContextOutput {
	resp := successEnvelopeWithContentProvenance(data, now, contentProvenanceSiteSourceUntrusted)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	return exportAgentContextOutput{ToolResponse: resp}
}

func includeTerms(mode toolcontract.ResponseMode, requested *bool) bool {
	if mode == toolcontract.ResponseModeCompact {
		return false
	}
	if requested == nil {
		return true
	}
	return *requested
}

func toPageMarkdownDTO(p site.Page, md, resolvedSourcePath, resolvedLang, revision string, state site.LifecycleState, includeTerms bool) pageMarkdownDTO {
	dto := pageMarkdownDTO{
		Slug:               p.Slug,
		SourceKey:          contentmodel.SourceKeyFromLogicalPath(resolvedSourcePath),
		Title:              p.Title,
		Date:               p.Date,
		Tags:               canonicalTaxonomyStrings(nullsafeStrings(p.Tags)),
		Categories:         canonicalTaxonomyStrings(nullsafeStrings(p.Categories)),
		URL:                p.URL,
		Lang:               p.Lang,
		ResolvedLang:       resolvedLang,
		ResolvedSourcePath: resolvedSourcePath,
		Revision:           revision,
		State:              state,
		Markdown:           md,
	}
	if includeTerms {
		dto.TagTerms = site.NormalizeTaxonomyTerms(p.Tags)
		dto.CategoryTerms = site.NormalizeTaxonomyTerms(p.Categories)
	}
	return dto
}

func toResolvedPageMarkdownDTO(resolved site.ResolvedPage, contentRoot, siteRoot string, includeTerms bool) pageMarkdownDTO {
	p := resolvedPublicPage(resolved)
	return toPageMarkdownDTO(p, resolvedMarkdown(resolved), fileutil.LogicalContentPath(contentRoot, resolved.SourcePath), resolvedLang(resolved), resolvedRevision(resolved), resolvedState(resolved, siteRoot), includeTerms)
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

func validateSlugLangConsistency(rawSlug, explicitLang string) error {
	explicitLang = strings.TrimSpace(explicitLang)
	if explicitLang == "" {
		return nil
	}
	prefix := site.LanguagePrefixFromSlug(rawSlug)
	if prefix == "" || prefix == explicitLang {
		return nil
	}
	return fmt.Errorf("invalid_params: slug selects language %q but lang=%q was also requested; remove one or make them match", prefix, explicitLang)
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
	p := sourcePageAsPublic(resolved.Source)
	if site.LanguagePrefixFromSlug(resolved.RequestedSlug) != "" {
		p.Slug = site.NormalizeSlug(resolved.RequestedSlug)
	}
	return p
}

func sourcePageAsPublic(src *hugosite.SourcePage) site.Page {
	if src == nil {
		return site.Page{}
	}
	return site.Page{
		Slug:       canonicalSourceSlug(src.Slug),
		Title:      src.Title,
		Date:       src.Date,
		Tags:       canonicalTaxonomyStrings(src.Tags),
		Categories: canonicalTaxonomyStrings(src.Categories),
		Lang:       src.Lang,
	}
}

func toFrontmatterDTO(p site.Page, resolved site.ResolvedPage, contentRoot, siteRoot string, readingTimeMin int, includeTerms bool) frontmatterDTO {
	identity := pageIdentityFromPage(p, fileutil.LogicalContentPath(contentRoot, resolved.SourcePath), resolvedRevision(resolved), readingTimeMin)
	dto := frontmatterDTO{
		Slug:               identity.Slug,
		SourceKey:          identity.SourceKey,
		Title:              identity.Title,
		Date:               p.Date,
		Tags:               canonicalTaxonomyStrings(nullsafeStrings(p.Tags)),
		Categories:         canonicalTaxonomyStrings(nullsafeStrings(p.Categories)),
		URL:                identity.URL,
		Lang:               identity.Lang,
		ResolvedLang:       resolvedLang(resolved),
		ResolvedSourcePath: identity.SourcePath,
		Revision:           identity.Revision,
		State:              resolvedState(resolved, siteRoot),
		ReadingTimeMin:     identity.ReadingTime,
	}
	if includeTerms {
		dto.TagTerms = identity.Tags
		dto.CategoryTerms = identity.Categories
	}
	if resolved.Source != nil {
		fm := resolved.Source.FrontmatterRaw
		dto.FeaturedImage = frontmatterStringValue(fm["featuredImage"])
		dto.FeaturedImagePreview = frontmatterStringValue(fm["featuredImagePreview"])
		dto.Description = frontmatterStringValue(fm["description"])
		if b, ok := fm["draft"].(bool); ok {
			dto.Draft = &b
		}
	}
	return dto
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

func canonicalResolvedSlug(resolved site.ResolvedPage) string {
	if resolved.Public != nil && strings.TrimSpace(resolved.Public.Slug) != "" {
		return resolved.Public.Slug
	}
	if resolved.Source != nil {
		if site.LanguagePrefixFromSlug(resolved.RequestedSlug) != "" {
			return site.NormalizeSlug(resolved.RequestedSlug)
		}
		return canonicalSourceSlug(resolved.Source.Slug)
	}
	return ""
}

func canonicalSourceSlug(slug string) string {
	if section, ok := sectionIndexSection(slug); ok {
		section = strings.Trim(section, "/")
		if section == "" {
			return "/"
		}
		return "/" + section + "/"
	}
	slug = strings.Trim(slug, "/")
	if slug == "" {
		return ""
	}
	return "/" + slug + "/"
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

func filterRelationshipTranslations(in []translationPageDTO, lang string, onePerSourceKey bool) []translationPageDTO {
	lang = strings.TrimSpace(lang)
	seen := map[string]bool{}
	out := make([]translationPageDTO, 0, len(in))
	for _, item := range in {
		if lang != "" && item.Lang != lang {
			continue
		}
		key := translationKey(item.Slug)
		if onePerSourceKey && key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, item)
	}
	return out
}

func filterRelatedPages(in []relatedPageDTO, lang string, onePerSourceKey bool) []relatedPageDTO {
	lang = strings.TrimSpace(lang)
	seen := map[string]bool{}
	out := make([]relatedPageDTO, 0, len(in))
	for _, item := range in {
		if lang != "" && item.Lang != lang {
			continue
		}
		key := translationKey(item.Slug)
		if onePerSourceKey && key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, item)
	}
	return out
}

func filterSuggestedLinks(in []linkSuggestionDTO, lang string, onePerSourceKey bool) []linkSuggestionDTO {
	lang = strings.TrimSpace(lang)
	seen := map[string]bool{}
	out := make([]linkSuggestionDTO, 0, len(in))
	for _, item := range in {
		if lang != "" && item.Lang != lang {
			continue
		}
		key := translationKey(item.Slug)
		if onePerSourceKey && key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, item)
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
// e.g. tools.WithMaxLimit to publish a real range constraint that
// jsonschema-go's struct-tag inference can't express directly.
//
// response_mode is deliberately NOT published as a JSON-Schema enum (#892):
// the SDK enforces enum constraints in its argument-validation step *before*
// our WrapTool pipeline runs, so an out-of-enum value would surface as a bare
// text error with no StructuredContent/code. Validation lives in
// toolcontract.WrapTool (ResolveResponseMode), which yields a structured
// invalid_params error listing the accepted values instead.
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

// overfetchLimit computes how many raw (pre-language/pre-dedup) candidates
// to request from computeRelated/scoreLinkSuggestions when the caller will
// filter their result afterward (#1041): both functions truncate to the
// limit they're given internally, so requesting only the caller's real
// limit and filtering afterward could silently return fewer than limit
// results whenever a same-source-key/wrong-language sibling occupied one
// of the pre-filter top-limit slots. 8x is generous headroom for any
// realistic site's language count while staying bounded.
func overfetchLimit(limit int) int {
	const factor = 8
	const maxFetch = 200
	fetch := limit * factor
	if fetch > maxFetch {
		return maxFetch
	}
	return fetch
}

// negativeLimitError — see the identical helper's comment in
// internal/tools/anonymous/tools.go (#641): 0 must keep meaning "use the
// default" (clampLimit's existing, documented behavior), only negative
// values are rejected as a likely caller-side bug.
func negativeLimitError(v int) error {
	if v < 0 {
		return fmt.Errorf("invalid_params: limit must not be negative")
	}
	return nil
}

func maxLimitError(v, max int) error {
	if v > max {
		return fmt.Errorf("invalid_params: limit must not exceed %d", max)
	}
	return nil
}

// negativeOffsetError rejects a negative offset (#885), closing the
// asymmetry with negativeLimitError (#641): a negative offset was previously
// silently clamped to 0, hiding a likely caller-side pagination-arithmetic
// bug behind a first-page response instead of surfacing it. Unlike limit, an
// offset of 0 is a fully valid request (the first page), so only strictly
// negative values are rejected; the downstream `offset < 0 -> 0` clamps stay
// in place as defense in depth.
func negativeOffsetError(v int) error {
	if v < 0 {
		return fmt.Errorf("invalid_params: offset must not be negative")
	}
	return nil
}

func positiveMaxBodyCharsError(v *int) error {
	if v != nil && *v <= 0 {
		return fmt.Errorf("invalid_params: max_body_chars must be greater than 0")
	}
	return nil
}

func derefMaxBodyChars(v *int) int {
	if v == nil {
		return 0
	}
	return *v
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
		{Name: "check_ai_readiness", RequiredScope: ""},
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
		{Name: "plan_page", RequiredScope: ""},
		{Name: "list_page_revisions", RequiredScope: ""},
	}
}
