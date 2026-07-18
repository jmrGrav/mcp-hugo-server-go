package read

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

type searchContentInput struct {
	Query    string `json:"query,omitempty"`
	Type     string `json:"type,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Category string `json:"category,omitempty"`
	Language string `json:"language,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Sort     string `json:"sort,omitempty"`
	Order    string `json:"order,omitempty"`
}

// taxonomyInconsistencyDTO is the structured, actionable form of a
// taxonomy_inconsistencies entry (#324): the plain-string message alone
// tells an agent *what* is wrong but not *where*, forcing a separate
// list_pages/filter round trip. TermB/PagesWithTermB are omitted for
// single-term findings (e.g. an alias mismatch), which have no "other side".
type taxonomyInconsistencyDTO struct {
	Message        string   `json:"message"`
	TermA          string   `json:"term_a"`
	TermB          string   `json:"term_b,omitempty"`
	PagesWithTermA []string `json:"pages_with_term_a,omitempty"`
	PagesWithTermB []string `json:"pages_with_term_b,omitempty"`
	// Kind distinguishes an actionable finding from an expected one (#183):
	// "alias_mismatch" (a term should use its declared canonical form),
	// "possible_duplicate" (similar spelling, no known relationship — likely
	// a typo or unintentional variant), or "translation_pair" (the two
	// terms are used on exactly the same set of page-bundle slugs, just in
	// different languages — this is the site's own localization, not a
	// content inconsistency, and does not need an alias entry to resolve).
	Kind string `json:"kind,omitempty"`
	// Severity tells an agent whether this finding is expected to be
	// actionable at all (#419), instead of leaving it to infer that from a
	// static score: "info" (translation_pair — the site's own localization,
	// not a content problem) or "warning" (alias_mismatch/possible_duplicate
	// — a real content issue worth fixing). Neither ever moves the
	// top-level `score`/`status` (#419 is presentation only); "warning"
	// findings do show a local penalty in score_breakdown.taxonomy.score.
	Severity string `json:"severity,omitempty"`
}

// taxonomyFindingSeverity maps a finding's Kind to its Severity (#419).
func taxonomyFindingSeverity(kind string) string {
	if kind == "translation_pair" {
		return "info"
	}
	return "warning"
}

// scoreCategoryDTO is one line item of get_site_health's score_breakdown
// (#419). Weight is each category's actual share of the top-level `score`
// (it reconciles: score == the weighted category the top-level score is
// computed from), not a decorative number — a weight of 0 means that
// category's Score is shown for reference only and never moves the
// top-level score.
type scoreCategoryDTO struct {
	Score      int `json:"score"`
	Weight     int `json:"weight"`
	Issues     int `json:"issues"`
	Advisories int `json:"advisories,omitempty"`
}

// scoreBreakdownDTO is additive to get_site_health's response (#419) and
// presentation-only: it explains the pre-existing `score`/`status` formula,
// it does not change it (#419's scope note: "not a scoring algorithm
// change"). Frontmatter carries all the weight because it's the only
// category the formula has ever penalized; taxonomy findings — even
// "warning"-severity ones — have never moved `score` and still don't, so
// taxonomy carries zero weight.
//
// Only covers the categories the server actually computes a real signal
// for today (front matter validation, taxonomy findings). It deliberately
// omits "links"/"rendering"/"publication" placeholders that #419's proposal
// sketched but that this server has no corresponding check for yet —
// publishing a fabricated 100 for an uncomputed category would be more
// misleading than omitting it.
type scoreBreakdownDTO struct {
	Frontmatter scoreCategoryDTO `json:"frontmatter"`
	Taxonomy    scoreCategoryDTO `json:"taxonomy"`
}

type contentEnvelopeData struct {
	Pages    []pageDTO `json:"pages,omitempty"`
	Total    int       `json:"total,omitempty"`
	Limit    int       `json:"limit,omitempty"`
	Offset   int       `json:"offset,omitempty"`
	Sort     string    `json:"sort,omitempty"`
	Order    string    `json:"order,omitempty"`
	Query    string    `json:"query,omitempty"`
	Type     string    `json:"type,omitempty"`
	Tag      string    `json:"tag,omitempty"`
	Category string    `json:"category,omitempty"`
	Language string    `json:"language,omitempty"`

	Status string `json:"status,omitempty"`
	Score  int    `json:"score,omitempty"`
	// ScoreBreakdown is additive to get_site_health (#419): per-category
	// score/weight/issues so an agent can see why `score` is what it is,
	// without re-deriving the scoring logic. Nil for tools other than
	// get_site_health.
	ScoreBreakdown *scoreBreakdownDTO `json:"score_breakdown,omitempty"`
	// TaxonomyInconsistencies keeps its original string[] shape for
	// backward compatibility (#210/#328: no v1.x field-shape breaks).
	// TaxonomyInconsistencyDetails is the additive, structured sibling —
	// same findings, same order, with affected page slugs attached.
	PublishedPages               int                        `json:"published_pages,omitempty"`
	SourcePages                  int                        `json:"source_pages,omitempty"`
	DraftPages                   int                        `json:"draft_pages,omitempty"`
	Tags                         int                        `json:"tags,omitempty"`
	Categories                   int                        `json:"categories,omitempty"`
	MissingTitles                int                        `json:"missing_titles,omitempty"`
	MissingDates                 int                        `json:"missing_dates,omitempty"`
	ValidationErrors             int                        `json:"validation_errors,omitempty"`
	TaxonomyInconsistencies      []string                   `json:"taxonomy_inconsistencies,omitempty"`
	TaxonomyInconsistencyDetails []taxonomyInconsistencyDTO `json:"taxonomy_inconsistency_details,omitempty"`
	OrphanPages                  []string                   `json:"orphan_pages,omitempty"`
	Sections                     []sectionDTO               `json:"sections,omitempty"`
	Languages                    []string                   `json:"languages,omitempty"`
	Summary                      string                     `json:"summary,omitempty"`
	RecentPages                  []pageDTO                  `json:"recent_pages,omitempty"`
	Notes                        []string                   `json:"notes,omitempty"`
}

type contentEnvelope struct {
	toolcontract.ToolResponse[contentEnvelopeData]
	Status                       string                     `json:"status,omitempty"`
	Score                        int                        `json:"score,omitempty"`
	ScoreBreakdown               *scoreBreakdownDTO         `json:"score_breakdown,omitempty"`
	PublishedPages               int                        `json:"published_pages,omitempty"`
	SourcePages                  int                        `json:"source_pages,omitempty"`
	DraftPages                   int                        `json:"draft_pages,omitempty"`
	Tags                         int                        `json:"tags,omitempty"`
	Categories                   int                        `json:"categories,omitempty"`
	MissingTitles                int                        `json:"missing_titles,omitempty"`
	MissingDates                 int                        `json:"missing_dates,omitempty"`
	ValidationErrors             int                        `json:"validation_errors,omitempty"`
	TaxonomyInconsistencies      []string                   `json:"taxonomy_inconsistencies,omitempty"`
	TaxonomyInconsistencyDetails []taxonomyInconsistencyDTO `json:"taxonomy_inconsistency_details,omitempty"`
	OrphanPages                  []string                   `json:"orphan_pages,omitempty"`
	Sections                     []sectionDTO               `json:"sections,omitempty"`
	Languages                    []string                   `json:"languages,omitempty"`
	Summary                      string                     `json:"summary,omitempty"`
	RecentPages                  []pageDTO                  `json:"recent_pages,omitempty"`
	Notes                        []string                   `json:"notes,omitempty"`
}

type searchContentData struct {
	Pages         []pageDTO `json:"pages,omitempty"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
	Sort          string    `json:"sort,omitempty"`
	Order         string    `json:"order,omitempty"`
	Query         string    `json:"query,omitempty"`
	Type          string    `json:"type,omitempty"`
	Tag           string    `json:"tag,omitempty"`
	Category      string    `json:"category,omitempty"`
	Language      string    `json:"language,omitempty"`
}

type searchContentEnvelope struct {
	toolcontract.ToolResponse[searchContentData]
	Pages         []pageDTO `json:"pages,omitempty"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
	Sort          string    `json:"sort,omitempty"`
	Order         string    `json:"order,omitempty"`
	Query         string    `json:"query,omitempty"`
	Type          string    `json:"type,omitempty"`
	Tag           string    `json:"tag,omitempty"`
	Category      string    `json:"category,omitempty"`
	Language      string    `json:"language,omitempty"`
}

type validateFrontMatterInput struct {
	Slug   string `json:"slug,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type validateSiteInput struct {
	Limit       int  `json:"limit,omitempty"`
	Offset      int  `json:"offset,omitempty"`
	InvalidOnly bool `json:"invalid_only,omitempty"`
}

type frontMatterIssueDTO struct {
	Slug   string   `json:"slug"`
	Lang   string   `json:"lang"`
	Issues []string `json:"issues"`
}

type sectionDTO struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type pageDTO struct {
	Slug               string              `json:"slug"`
	Title              string              `json:"title"`
	Summary            string              `json:"summary"`
	Tags               []string            `json:"tags"`
	Categories         []string            `json:"categories"`
	TagTerms           []site.TaxonomyTerm `json:"tag_terms,omitempty"`
	CategoryTerms      []site.TaxonomyTerm `json:"category_terms,omitempty"`
	Date               string              `json:"date"`
	URL                string              `json:"url"`
	Lang               string              `json:"lang"`
	ResolvedLang       string              `json:"resolved_lang"`
	ResolvedSourcePath string              `json:"resolved_source_path"`
	State              site.LifecycleState `json:"state"`
	Snippet            string              `json:"snippet,omitempty"`
}

// validateOutputData separates two distinct counters that #333 found
// conflated: pages_checked (the full scan scope — every matched page is
// always validated, regardless of limit/offset) versus the returned_count/
// has_more/next_offset pagination of the *detail rows* in pages. A caller
// must be able to tell "all 80 pages were scanned, only 5 detail rows came
// back, and there are more" without guessing at what pages_checked means.
type validateOutputData struct {
	PagesChecked int                   `json:"pages_checked"`
	PagesPassed  int                   `json:"pages_passed"`
	Invalid      int                   `json:"invalid"`
	Returned     int                   `json:"returned_count,omitempty"`
	Limit        int                   `json:"limit,omitempty"`
	Offset       int                   `json:"offset,omitempty"`
	HasMore      bool                  `json:"has_more"`
	NextOffset   *int                  `json:"next_offset,omitempty"`
	Pages        []frontMatterIssueDTO `json:"pages"`
}

type validateOutput struct {
	toolcontract.ToolResponse[validateOutputData]
	PagesChecked int                   `json:"pages_checked"`
	PagesPassed  int                   `json:"pages_passed"`
	Invalid      int                   `json:"invalid"`
	Returned     int                   `json:"returned_count,omitempty"`
	Limit        int                   `json:"limit,omitempty"`
	Offset       int                   `json:"offset,omitempty"`
	HasMore      bool                  `json:"has_more"`
	NextOffset   *int                  `json:"next_offset,omitempty"`
	Pages        []frontMatterIssueDTO `json:"pages"`
}

type brokenLinkInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type brokenLinkDTO struct {
	PageSlug string `json:"page_slug"`
	Link     string `json:"link"`
	Target   string `json:"target,omitempty"`
	Reason   string `json:"reason"`
}

type brokenLinkData struct {
	TotalPages  int             `json:"total_pages"`
	BrokenLinks int             `json:"broken_links"`
	Limit       int             `json:"limit"`
	Offset      int             `json:"offset"`
	Links       []brokenLinkDTO `json:"links"`
}

type brokenLinkOutput struct {
	toolcontract.ToolResponse[brokenLinkData]
	TotalPages  int             `json:"total_pages"`
	BrokenLinks int             `json:"broken_links"`
	Limit       int             `json:"limit"`
	Offset      int             `json:"offset"`
	Links       []brokenLinkDTO `json:"links"`
}

type getBacklinksInput struct {
	Slug string `json:"slug"`
}

type backlinkDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type getBacklinksData struct {
	Slug      string        `json:"slug"`
	Count     int           `json:"count"`
	Backlinks []backlinkDTO `json:"backlinks"`
}

type getBacklinksOutput struct {
	toolcontract.ToolResponse[getBacklinksData]
	Slug      string        `json:"slug"`
	Count     int           `json:"count"`
	Backlinks []backlinkDTO `json:"backlinks"`
}

type suggestInternalLinksInput struct {
	Slug       string   `json:"slug,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Body       string   `json:"body,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type linkSuggestionDTO struct {
	Slug             string   `json:"slug"`
	Title            string   `json:"title"`
	URL              string   `json:"url"`
	AnchorText       string   `json:"anchor_text"`
	SharedTags       []string `json:"shared_tags,omitempty"`
	SharedCategories []string `json:"shared_categories,omitempty"`
	Score            int      `json:"score"`
	BodyMention      bool     `json:"body_mention,omitempty"`
}

type suggestInternalLinksData struct {
	Slug           string               `json:"slug,omitempty"`
	Total          int                  `json:"total"`
	Translations   []translationPageDTO `json:"translations"`
	Suggestions    []linkSuggestionDTO  `json:"suggestions"`
	SuggestedLinks []linkSuggestionDTO  `json:"suggested_links"`
}

type suggestInternalLinksOutput struct {
	toolcontract.ToolResponse[suggestInternalLinksData]
	Slug           string               `json:"slug,omitempty"`
	Total          int                  `json:"total"`
	Translations   []translationPageDTO `json:"translations"`
	Suggestions    []linkSuggestionDTO  `json:"suggestions"`
	SuggestedLinks []linkSuggestionDTO  `json:"suggested_links"`
}

// RegisterWithSourceIndex wires additional read-only tools that benefit from the
// source index. Existing tools remain registered via Register.
func RegisterWithSourceIndex(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, dbs ...*db.DB) {
	if s == nil {
		return
	}
	var siteDB *db.DB
	if len(dbs) > 0 {
		siteDB = dbs[0]
	}
	aliases := taxonomy.NormalizeAliasMap(cfg.TaxonomyAliases)

	RegisterDiffPage(s, idx, srcIdx, cfg)
	RegisterInspectRenderedPage(s, idx, srcIdx, cfg)
	RegisterListContentTypes(s, srcIdx, cfg)
	RegisterListPageAssets(s, idx, srcIdx, cfg)

	addReadOnlyTool(s, "search_content", "Search content", "Filtered search across published content with type, tag, category, language, sort, and pagination. Returns a structured envelope with total count. When db_path is configured, uses FTS5 full-text search with ranked results and snippets. Also matches body text, unlike search_pages. Requires content.read — prefer this tool over search_pages whenever you have that scope; use search_pages only when you're calling anonymously.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchContentInput) (*mcp.CallToolResult, searchContentEnvelope, error) {
			if idx == nil {
				return nil, searchContentEnvelope{}, fmt.Errorf("index not initialized")
			}
			readerSafe := site.IsReaderProfile(ctx)
			if t := strings.ToLower(strings.TrimSpace(in.Type)); t != "" && t != "all" && t != "post" && t != "posts" && t != "page" && t != "pages" {
				return nil, searchContentEnvelope{}, fmt.Errorf("invalid_params: type must be one of: all, post, posts, page, pages (got %q)", in.Type)
			}

			// FTS5 path: use SQLite full-text search for ranked, snippet-annotated results.
			q := strings.TrimSpace(in.Query)
			if siteDB != nil && q != "" {
				ftsResults, err := siteDB.Search(q, 1000)
				if err == nil && len(ftsResults) > 0 {
					snippetMap := make(map[string]string, len(ftsResults))
					classifier := site.NewClassifierFromPages(idx.Sitemap())
					var ranked []site.Page
					inNoQuery := in
					inNoQuery.Query = "" // non-query filters applied below; FTS handles text matching
					for _, r := range ftsResults {
						p, found := idx.GetBySlug(r.Slug)
						if !found || !classifier.IsContent(*p) {
							continue
						}
						if !matchContentFilters(*p, inNoQuery, classifier, aliases) {
							continue
						}
						ranked = append(ranked, *p)
						snippetMap[r.Slug] = r.Snippet
					}
					total := len(ranked)
					limit := clampLimit(in.Limit, 20, 100)
					offset := in.Offset
					if offset < 0 {
						offset = 0
					}
					pages := sliceContentPages(ranked, offset, limit)
					meta := toolcontract.ComputePagination(total, limit, offset, len(pages))
					lookup := srcIdx
					if readerSafe {
						lookup = nil
					}
					dtos := toPageDTOsWithSnippets(pages, aliases, snippetMap, lookup, cfg.ContentRoot, cfg.SiteRoot)
					return nil, newSearchContentEnvelope(searchContentData{
						Pages:         dtos,
						Total:         meta.Total,
						Limit:         meta.Limit,
						Offset:        meta.Offset,
						ReturnedCount: meta.ReturnedCount,
						HasMore:       meta.HasMore,
						NextOffset:    meta.NextOffset,
						Sort:          "relevance",
						Order:         "desc",
						Query:         q,
						Type:          strings.TrimSpace(in.Type),
						Tag:           strings.TrimSpace(in.Tag),
						Category:      strings.TrimSpace(in.Category),
						Language:      strings.TrimSpace(in.Language),
					}, time.Now().UTC()), nil
				}
			}

			// In-memory fallback path (db_path unset or FTS returned no results).
			sitemap := idx.Sitemap()
			if srcIdx != nil && in.Category != "" && !readerSafe {
				enriched := make([]site.Page, len(sitemap))
				copy(enriched, sitemap)
				for i, pg := range enriched {
					if len(pg.Categories) == 0 {
						if src, ok := srcIdx.GetBySlug(strings.Trim(pg.Slug, "/")); ok {
							enriched[i].Categories = src.Categories
						}
					}
				}
				sitemap = enriched
			}
			filtered := filterContentPages(sitemap, in, aliases)
			total := len(filtered)
			limit := clampLimit(in.Limit, 20, 100)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			pages := sliceContentPages(filtered, offset, limit)
			meta := toolcontract.ComputePagination(total, limit, offset, len(pages))
			return nil, newSearchContentEnvelope(searchContentData{
				Pages:         toPageDTOs(pages, aliases, sourceIndexForProfile(srcIdx, readerSafe), cfg.ContentRoot, cfg.SiteRoot),
				Total:         meta.Total,
				Limit:         meta.Limit,
				Offset:        meta.Offset,
				ReturnedCount: meta.ReturnedCount,
				HasMore:       meta.HasMore,
				NextOffset:    meta.NextOffset,
				Sort:          effectiveSort(in),
				Order:         canonicalOrder(in.Order),
				Query:         strings.TrimSpace(in.Query),
				Type:          strings.TrimSpace(in.Type),
				Tag:           strings.TrimSpace(in.Tag),
				Category:      strings.TrimSpace(in.Category),
				Language:      strings.TrimSpace(in.Language),
			}, time.Now().UTC()), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 100) })

	addReadOnlyTool(s, "explain_structure", "Explain site structure", "Summarize how the Hugo site is organized, including sections, taxonomies, languages, and recent content. Useful for onboarding or content planning. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			readerSafe := site.IsReaderProfile(ctx)
			contentPages := idx.ContentPages()
			sections := countSections(contentPages)
			languages := uniqueLanguages(contentPages)
			recent := contentPages
			if len(recent) > 5 {
				recent = recent[:5]
			}
			rawTags := idx.AllTags()
			rawCats := idx.AllCategories()
			if srcIdx != nil && !readerSafe {
				rawTags = srcIdx.AllTags()
				rawCats = srcIdx.AllCategories()
			}
			tagCount := len(taxonomy.ApplyAliases(rawTags, aliases))
			catCount := len(taxonomy.ApplyAliases(rawCats, aliases))
			summary := fmt.Sprintf("%d published pages across %d sections, %d tags, and %d categories.",
				len(contentPages), len(sections), tagCount, catCount)
			return nil, newContentEnvelope(contentEnvelopeData{
				Summary:     summary,
				Sections:    sections,
				Languages:   languages,
				Tags:        tagCount,
				Categories:  catCount,
				RecentPages: toPageDTOsEnriched(recent, sourceIndexForProfile(srcIdx, readerSafe), aliases, cfg.ContentRoot, cfg.SiteRoot),
				Notes: []string{
					"Top-level sections are derived from page slugs.",
					"Posts are detected from the /posts/ path prefix.",
				},
			}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_site_health", "Get site health", "Return a concise health summary for the Hugo site, including content counts, validation signals, and taxonomy inconsistency warnings. `taxonomy_inconsistency_details` gives each warning's affected page slugs (`pages_with_term_a`/`pages_with_term_b`) so you can go fix front matter directly, without a separate list_pages/filter lookup; `taxonomy_inconsistencies` (plain strings) is kept for backward compatibility. Each detail's `kind` distinguishes an actionable finding (`alias_mismatch`, `possible_duplicate`) from `translation_pair` — two terms used on the same page bundle in different languages, which is the site's own localization, not a content problem to fix. Each detail's `severity` distinguishes an actionable content issue (`warning`) from expected localization (`info`) — neither moves the top-level `score`/`status`, but a `warning` finding does show a local penalty in `score_breakdown.taxonomy.score`. `score_breakdown` shows the per-category score/weight/issue-count behind the top-level `score` (weight 0 means that category is informational only and never contributed to `score`), so you don't have to re-derive why a finding did or didn't change it. Use this before publishing or reviewing content. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			health := buildSiteHealth(idx, sourceIndexForProfile(srcIdx, site.IsReaderProfile(ctx)), aliases)
			return nil, newContentEnvelope(contentEnvelopeData{
				Status:                       health.Status,
				Score:                        health.Score,
				ScoreBreakdown:               health.ScoreBreakdown,
				PublishedPages:               health.PublishedPages,
				SourcePages:                  health.SourcePages,
				DraftPages:                   health.DraftPages,
				Tags:                         health.Tags,
				Categories:                   health.Categories,
				MissingTitles:                health.MissingTitles,
				MissingDates:                 health.MissingDates,
				ValidationErrors:             health.ValidationErrors,
				TaxonomyInconsistencies:      health.TaxonomyInconsistencies,
				TaxonomyInconsistencyDetails: health.TaxonomyInconsistencyDetails,
			}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "validate_frontmatter", "Validate front matter", "Validate Hugo front matter for missing titles, dates, or malformed metadata. Optionally target one slug. `pages_checked`/`pages_passed`/`invalid` always describe the full matched scan scope, regardless of `limit`/`offset` — every matched page is validated. `pages` is a separate paginated view of the per-page detail rows; use `returned_count`/`has_more`/`next_offset` to page through it. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in validateFrontMatterInput) (*mcp.CallToolResult, validateOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, validateOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source validation diagnostics")
			}
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			pages, err := sourcePagesForValidation(srcIdx, in.Slug)
			if err != nil {
				return nil, validateOutput{}, err
			}
			return nil, validatePagesWithIssues(pages, in.Offset, in.Limit, aliases), nil
		})

	addReadOnlyTool(s, "validate_site", "Validate site", "Run a validation pass over all Hugo source pages and report front matter issues. Equivalent to validate_frontmatter with no slug filter. `pages_checked`/`pages_passed`/`invalid` always describe the full site regardless of `limit`/`offset`/`invalid_only`. `pages` is a separate paginated view of the per-page detail rows; use `limit`/`offset` and `returned_count`/`has_more`/`next_offset` to page through it. Set `invalid_only=true` to skip passing pages in the `pages` view entirely — useful on a large site where most pages pass and returning all of them wastes context. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in validateSiteInput) (*mcp.CallToolResult, validateOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, validateOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source validation diagnostics")
			}
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			pages := srcIdx.ListPages(0, 0)
			return nil, validatePagesWithIssuesFiltered(pages, in.Offset, in.Limit, in.InvalidOnly, aliases), nil
		})

	addReadOnlyTool(s, "get_broken_links", "Get broken links", "Audit internal links against the current Hugo index without making any external network calls. When db_path is configured, reads from a pre-computed link graph (O(1)); otherwise re-scans HTML on each call. Returns a limited sample of missing internal targets and requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, in brokenLinkInput) (*mcp.CallToolResult, brokenLinkOutput, error) {
			if idx == nil {
				return nil, brokenLinkOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 25, 100)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}

			// DB path: read pre-computed broken links from the links table.
			if siteDB != nil {
				dbLinks, err := siteDB.GetBrokenLinks()
				if err == nil {
					issues := make([]brokenLinkDTO, 0, len(dbLinks))
					for _, r := range dbLinks {
						issues = append(issues, brokenLinkDTO{
							PageSlug: r.SourceSlug,
							Link:     r.Target,
							Target:   r.Target,
							Reason:   "missing target page",
						})
					}
					return nil, newBrokenLinkOutput(brokenLinkData{
						TotalPages:  len(idx.Sitemap()),
						BrokenLinks: len(issues),
						Limit:       limit,
						Offset:      offset,
						Links:       sliceBrokenLinks(issues, offset, limit),
					}, time.Now().UTC()), nil
				}
			}

			// In-memory fallback: re-scan HTML on each call.
			issues := collectBrokenLinks(idx)
			return nil, newBrokenLinkOutput(brokenLinkData{
				TotalPages:  len(idx.Sitemap()),
				BrokenLinks: len(issues),
				Limit:       limit,
				Offset:      offset,
				Links:       sliceBrokenLinks(issues, offset, limit),
			}, time.Now().UTC()), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 100) })

	addReadOnlyTool(s, "get_backlinks", "Get backlinks", "Return all published pages that contain an internal link to the specified slug. Use this before delete_page (impact analysis) or when writing new content (find existing references). This is the same backlinks data get_related_content returns alongside related_pages/suggested_links/translations in one call — use this standalone version when you only need backlinks and want to avoid the cost of the other three facets. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getBacklinksInput) (*mcp.CallToolResult, getBacklinksOutput, error) {
			if idx == nil {
				return nil, getBacklinksOutput{}, fmt.Errorf("index not initialized")
			}
			if strings.TrimSpace(in.Slug) == "" {
				return nil, getBacklinksOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			// Resolve slug to normalise it (same logic as get_page)
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getBacklinksOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			resolved, err := readerSafeResolvedPage(ctx, resolved, in.Slug)
			if err != nil {
				return nil, getBacklinksOutput{}, err
			}
			var targetSlug string
			if resolved.Public != nil {
				targetSlug = resolved.Public.Slug
			} else if resolved.Source != nil {
				targetSlug = "/" + strings.Trim(resolved.Source.Slug, "/") + "/"
			}
			entries := idx.GetBacklinks(targetSlug)
			dtos := make([]backlinkDTO, len(entries))
			for i, e := range entries {
				dtos[i] = backlinkDTO{Slug: e.FromSlug, Title: e.FromTitle, URL: e.FromURL}
			}
			env := newGetBacklinksOutput(getBacklinksData{
				Slug:      targetSlug,
				Count:     len(dtos),
				Backlinks: dtos,
			}, time.Now().UTC())
			return nil, env, nil
		})

	addReadOnlyTool(s, "suggest_links", "Suggest internal links",
		"Recommend existing published pages to link from a draft or existing page, based on shared tags and categories. "+
			"Supply slug (for an indexed page), or tags/categories (for a draft not yet published), or both. "+
			"Optionally include body to detect pages whose titles already appear in the text (body_mention: true). "+
			"Returns ranked suggestions with anchor_text and shared taxonomy context. Use this specifically for a draft not yet indexed (via tags/categories/body); for an already-published page, get_related_content's suggested_links field covers the same case alongside backlinks/related_pages/translations in one call. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in suggestInternalLinksInput) (*mcp.CallToolResult, suggestInternalLinksOutput, error) {
			if idx == nil {
				return nil, suggestInternalLinksOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 10, 20)

			// Build the reference taxonomy: start from provided tags/categories, then merge in the
			// indexed page's taxonomy when a slug is given.
			refTags := make([]string, 0)
			refCats := make([]string, 0)
			refTags = append(refTags, in.Tags...)
			refCats = append(refCats, in.Categories...)

			var resolvedSlug string
			warnings := []string{}

			if strings.TrimSpace(in.Slug) != "" {
				resolver := site.NewPageResolver(idx, srcIdx, cfg)
				resolved, ok := resolver.Resolve(in.Slug)
				if !ok {
					warnings = append(warnings, fmt.Sprintf("slug %q not found in index; using only provided tags/categories", in.Slug))
				} else {
					resolved, err := readerSafeResolvedPage(ctx, resolved, in.Slug)
					if err != nil {
						return nil, suggestInternalLinksOutput{}, err
					}
					if resolved.Public != nil {
						resolvedSlug = resolved.Public.Slug
						refTags = append(refTags, resolved.Public.Tags...)
						refCats = append(refCats, resolved.Public.Categories...)
					} else if resolved.Source != nil {
						resolvedSlug = "/" + strings.Trim(resolved.Source.Slug, "/") + "/"
						// Merge source-page taxonomy so draft-slug callers get suggestions (W1).
						refTags = append(refTags, resolved.Source.Tags...)
						refCats = append(refCats, resolved.Source.Categories...)
					}
				}
			}

			if len(refTags) == 0 && len(refCats) == 0 {
				return nil, suggestInternalLinksOutput{}, fmt.Errorf("invalid_params: provide at least one of slug, tags, or categories")
			}

			translations := []translationPageDTO{}
			if resolvedSlug != "" {
				if ref, ok := idx.GetBySlug(resolvedSlug); ok {
					translations = collectTranslations(idx, *ref)
				}
			}
			suggestions := scoreLinkSuggestions(idx, resolvedSlug, refTags, refCats, in.Body, limit)
			resp := newSuggestInternalLinksOutput(suggestInternalLinksData{
				Slug:           resolvedSlug,
				Total:          len(suggestions),
				Translations:   translations,
				Suggestions:    suggestions,
				SuggestedLinks: suggestions,
			}, time.Now().UTC())
			resp.Warnings = warnings
			return nil, resp, nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 20) })
}

// containsPhrase reports whether phrase appears in text with word-boundary
// delimiters on both sides. Both text and phrase must already be lowercased.
func containsPhrase(text, phrase string) bool {
	for {
		i := strings.Index(text, phrase)
		if i < 0 {
			return false
		}
		before := i == 0 || !isWordRune(rune(text[i-1]))
		after := i+len(phrase) >= len(text) || !isWordRune(rune(text[i+len(phrase)]))
		if before && after {
			return true
		}
		text = text[i+1:]
	}
}

func isWordRune(r rune) bool {
	return r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func scoreLinkSuggestions(idx *site.Index, excludeSlug string, refTags, refCats []string, body string, limit int) []linkSuggestionDTO {
	type scored struct {
		dto  linkSuggestionDTO
		date string
	}
	bodyLower := strings.ToLower(body)
	classifier := site.NewClassifierFromPages(idx.Sitemap())
	excludeTranslationKey := translationKey(excludeSlug)
	var candidates []scored
	for _, pg := range idx.Sitemap() {
		// Skip taxonomy list pages, home page, and the source page itself (N1).
		if !classifier.IsContent(pg) {
			continue
		}
		if pg.Slug == excludeSlug {
			continue
		}
		if isTranslationVariant(excludeTranslationKey, pg.Slug) {
			continue
		}
		sharedTagTerms := taxonomy.SharedTerms(pg.Tags, refTags)
		sharedCatTerms := taxonomy.SharedTerms(pg.Categories, refCats)
		score := len(sharedTagTerms)*2 + len(sharedCatTerms)
		if score == 0 {
			continue
		}
		// E1/W2: guard empty title; use phrase-boundary check to avoid false positives
		// (e.g. title "Go" matching "go to the store").
		titleLower := strings.ToLower(strings.TrimSpace(pg.Title))
		mention := bodyLower != "" && titleLower != "" && containsPhrase(bodyLower, titleLower)
		candidates = append(candidates, scored{
			date: pg.Date,
			dto: linkSuggestionDTO{
				Slug:             pg.Slug,
				Title:            pg.Title,
				URL:              pg.URL,
				AnchorText:       pg.Title,
				SharedTags:       taxonomy.Slugs(sharedTagTerms),
				SharedCategories: taxonomy.Slugs(sharedCatTerms),
				Score:            score,
				BodyMention:      mention,
			},
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		// Priority: body mention → score → recency (W3).
		mi, mj := candidates[i].dto.BodyMention, candidates[j].dto.BodyMention
		if mi != mj {
			return mi
		}
		si, sj := candidates[i].dto.Score, candidates[j].dto.Score
		if si != sj {
			return si > sj
		}
		return candidates[i].date > candidates[j].date
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]linkSuggestionDTO, len(candidates))
	for i, c := range candidates {
		out[i] = c.dto
	}
	return out
}

func sourceIndexForProfile(srcIdx *hugosite.SourceIndex, readerSafe bool) *hugosite.SourceIndex {
	if readerSafe {
		return nil
	}
	return srcIdx
}

func filterContentPages(pages []site.Page, in searchContentInput, aliases map[string]string) []site.Page {
	out := make([]site.Page, 0, len(pages))
	classifier := site.NewClassifierFromPages(pages)
	for _, p := range pages {
		if !classifier.IsContent(p) {
			continue
		}
		if !matchContentFilters(p, in, classifier, aliases) {
			continue
		}
		out = append(out, p)
	}
	sortContentPages(out, in)
	return out
}

func matchContentFilters(p site.Page, in searchContentInput, classifier *site.ContentClassifier, aliases map[string]string) bool {
	query := strings.TrimSpace(in.Query)
	if query != "" && scoreContentPage(p, query) == 0 {
		return false
	}
	if in.Tag != "" && !taxonomy.MatchesSlugWithAliases(p.Tags, taxonomy.Slug(in.Tag), aliases) {
		return false
	}
	if in.Category != "" && !taxonomy.MatchesSlugWithAliases(p.Categories, taxonomy.Slug(in.Category), aliases) {
		return false
	}
	if in.Language != "" && !strings.EqualFold(p.Lang, in.Language) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(in.Type)) {
	case "", "all":
		return true
	case "post", "posts":
		return classifier.IsArticle(p)
	case "page", "pages":
		return classifier.IsContent(p) && !classifier.IsArticle(p)
	default:
		return false
	}
}

func scoreContentPage(p site.Page, query string) int {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 1
	}
	fields := []string{
		strings.ToLower(p.Title),
		strings.ToLower(p.Summary),
		strings.ToLower(p.URL),
		strings.ToLower(strings.Join(p.Tags, " ")),
		strings.ToLower(strings.Join(p.Categories, " ")),
		strings.ToLower(p.Lang),
	}
	score := 0
	for _, term := range terms {
		for _, field := range fields {
			if strings.Contains(field, term) {
				score++
				break
			}
		}
	}
	return score
}

func sortContentPages(pages []site.Page, in searchContentInput) {
	sortBy := canonicalSort(in.Sort)
	if strings.TrimSpace(in.Sort) == "" && strings.TrimSpace(in.Query) != "" {
		sortBy = "relevance"
	}
	order := canonicalOrder(in.Order)
	sort.SliceStable(pages, func(i, j int) bool {
		switch sortBy {
		case "title":
			if order == "asc" {
				return strings.ToLower(pages[i].Title) < strings.ToLower(pages[j].Title)
			}
			return strings.ToLower(pages[i].Title) > strings.ToLower(pages[j].Title)
		case "slug":
			if order == "asc" {
				return pages[i].Slug < pages[j].Slug
			}
			return pages[i].Slug > pages[j].Slug
		case "relevance":
			li := scoreContentPage(pages[i], in.Query)
			lj := scoreContentPage(pages[j], in.Query)
			if li != lj {
				if order == "asc" {
					return li < lj
				}
				return li > lj
			}
			if pages[i].Date != pages[j].Date {
				if order == "asc" {
					return pages[i].Date < pages[j].Date
				}
				return pages[i].Date > pages[j].Date
			}
			if order == "asc" {
				return pages[i].Slug < pages[j].Slug
			}
			return pages[i].Slug > pages[j].Slug
		default:
			if order == "asc" {
				return pages[i].Date < pages[j].Date
			}
			return pages[i].Date > pages[j].Date
		}
	})
}

func canonicalSort(sortBy string) string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "", "date":
		return "date"
	case "title":
		return "title"
	case "slug":
		return "slug"
	case "relevance":
		return "relevance"
	default:
		return "date"
	}
}

func canonicalOrder(order string) string {
	if strings.ToLower(strings.TrimSpace(order)) == "asc" {
		return "asc"
	}
	return "desc"
}

func sliceContentPages(pages []site.Page, offset, limit int) []site.Page {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(pages) {
		return []site.Page{}
	}
	out := pages[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func collectBrokenLinks(idx *site.Index) []brokenLinkDTO {
	if idx == nil {
		return nil
	}
	var issues []brokenLinkDTO
	classifier := site.NewClassifier(idx)
	for _, page := range idx.ContentPages() {
		issues = append(issues, brokenLinksForPage(idx, classifier, page)...)
	}
	return issues
}

// brokenLinksForPage scopes the broken-link scan to a single page instead
// of walking the whole site (collectBrokenLinks's job). Used by
// get_page_for_edit's quality signal, which must stay cheap since it runs
// on the default path of a tool meant to be called before every edit.
func brokenLinksForPage(idx *site.Index, classifier *site.ContentClassifier, page site.Page) []brokenLinkDTO {
	base, err := url.Parse(page.URL)
	if err != nil {
		return nil
	}
	var issues []brokenLinkDTO
	for _, href := range extractLinks(page.RawHTML) {
		target, ok := resolveInternalLink(base, href)
		if !ok {
			continue
		}
		if shouldIgnoreBrokenLinkTarget(classifier, target.Path) {
			continue
		}
		if targetPage, found := idx.GetBySlug(target.Path); found && classifier.IsContent(*targetPage) {
			continue
		}
		issues = append(issues, brokenLinkDTO{
			PageSlug: page.Slug,
			Link:     href,
			Target:   target.String(),
			Reason:   "missing target page",
		})
	}
	return issues
}

func extractLinks(rawHTML string) []string {
	if strings.TrimSpace(rawHTML) == "" {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil
	}
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if href := strings.TrimSpace(htmlAttr(n, "href")); href != "" {
				links = append(links, href)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links
}

func resolveInternalLink(base *url.URL, raw string) (*url.URL, bool) {
	if base == nil {
		return nil, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "mailto:") || strings.HasPrefix(raw, "tel:") {
		return nil, false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https" {
		return nil, false
	}
	target := base.ResolveReference(ref)
	if target.Host != "" && target.Host != base.Host {
		return nil, false
	}
	if strings.HasSuffix(target.Path, ".md") {
		return nil, false
	}
	return target, true
}

func shouldIgnoreBrokenLinkTarget(classifier *site.ContentClassifier, rawPath string) bool {
	if classifier == nil {
		classifier = site.NewClassifier(nil)
	}
	return !classifier.IsContent(site.Page{Slug: rawPath})
}

func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func sliceBrokenLinks(issues []brokenLinkDTO, offset, limit int) []brokenLinkDTO {
	if offset >= len(issues) {
		return []brokenLinkDTO{}
	}
	out := issues[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func validatePagesWithIssues(pages []hugosite.SourcePage, offset, limit int, aliases map[string]string) validateOutput {
	return validatePagesWithIssuesFiltered(pages, offset, limit, false, aliases)
}

// validatePagesWithIssuesFiltered is validatePagesWithIssues plus an
// invalidOnly filter (#431). pages_checked/pages_passed/invalid always
// describe the full scan scope regardless of invalidOnly — only the
// paginated `pages` detail rows (and the has_more/next_offset pagination
// built from them) are affected by the filter.
func validatePagesWithIssuesFiltered(pages []hugosite.SourcePage, offset, limit int, invalidOnly bool, aliases map[string]string) validateOutput {
	total := len(pages)
	if offset < 0 {
		offset = 0
	}

	allResults := make([]frontMatterIssueDTO, 0, len(pages))
	invalid := 0
	for _, p := range pages {
		issues := validateFrontMatterPage(p, aliases)
		if len(issues) > 0 {
			invalid++
		}
		allResults = append(allResults, frontMatterIssueDTO{Slug: p.Slug, Lang: p.Lang, Issues: issues})
	}

	filtered := allResults
	if invalidOnly {
		filtered = make([]frontMatterIssueDTO, 0, invalid)
		for _, r := range allResults {
			if len(r.Issues) > 0 {
				filtered = append(filtered, r)
			}
		}
	}
	filteredTotal := len(filtered)
	if limit <= 0 {
		limit = filteredTotal
	}

	results := filtered
	if offset < len(results) {
		results = results[offset:]
	} else {
		results = []frontMatterIssueDTO{}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	meta := toolcontract.ComputePagination(filteredTotal, limit, offset, len(results))
	return newValidateOutput(validateOutputData{
		PagesChecked: total,
		PagesPassed:  total - invalid,
		Invalid:      invalid,
		Returned:     len(results),
		Limit:        limit,
		Offset:       offset,
		HasMore:      meta.HasMore,
		NextOffset:   meta.NextOffset,
		Pages:        results,
	}, time.Now().UTC())
}

func newContentEnvelope(data contentEnvelopeData, now time.Time) contentEnvelope {
	return contentEnvelope{
		ToolResponse:                 successEnvelope(data, now),
		Status:                       data.Status,
		Score:                        data.Score,
		ScoreBreakdown:               data.ScoreBreakdown,
		PublishedPages:               data.PublishedPages,
		SourcePages:                  data.SourcePages,
		DraftPages:                   data.DraftPages,
		Tags:                         data.Tags,
		Categories:                   data.Categories,
		MissingTitles:                data.MissingTitles,
		MissingDates:                 data.MissingDates,
		ValidationErrors:             data.ValidationErrors,
		TaxonomyInconsistencies:      data.TaxonomyInconsistencies,
		TaxonomyInconsistencyDetails: data.TaxonomyInconsistencyDetails,
		OrphanPages:                  data.OrphanPages,
		Sections:                     data.Sections,
		Languages:                    data.Languages,
		Summary:                      data.Summary,
		RecentPages:                  data.RecentPages,
		Notes:                        data.Notes,
	}
}

func newSearchContentEnvelope(data searchContentData, now time.Time) searchContentEnvelope {
	return searchContentEnvelope{
		ToolResponse:  successEnvelope(data, now),
		Pages:         data.Pages,
		Total:         data.Total,
		Limit:         data.Limit,
		Offset:        data.Offset,
		ReturnedCount: data.ReturnedCount,
		HasMore:       data.HasMore,
		NextOffset:    data.NextOffset,
		Sort:          data.Sort,
		Order:         data.Order,
		Query:         data.Query,
		Type:          data.Type,
		Tag:           data.Tag,
		Category:      data.Category,
		Language:      data.Language,
	}
}

func newValidateOutput(data validateOutputData, now time.Time) validateOutput {
	return validateOutput{
		ToolResponse: successEnvelope(data, now),
		PagesChecked: data.PagesChecked,
		PagesPassed:  data.PagesPassed,
		Invalid:      data.Invalid,
		Returned:     data.Returned,
		Limit:        data.Limit,
		Offset:       data.Offset,
		HasMore:      data.HasMore,
		NextOffset:   data.NextOffset,
		Pages:        data.Pages,
	}
}

func newBrokenLinkOutput(data brokenLinkData, now time.Time) brokenLinkOutput {
	return brokenLinkOutput{
		ToolResponse: successEnvelope(data, now),
		TotalPages:   data.TotalPages,
		BrokenLinks:  data.BrokenLinks,
		Limit:        data.Limit,
		Offset:       data.Offset,
		Links:        data.Links,
	}
}

func newGetBacklinksOutput(data getBacklinksData, now time.Time) getBacklinksOutput {
	return getBacklinksOutput{
		ToolResponse: successEnvelope(data, now),
		Slug:         data.Slug,
		Count:        data.Count,
		Backlinks:    data.Backlinks,
	}
}

func newSuggestInternalLinksOutput(data suggestInternalLinksData, now time.Time) suggestInternalLinksOutput {
	return suggestInternalLinksOutput{
		ToolResponse:   successEnvelope(data, now),
		Slug:           data.Slug,
		Total:          data.Total,
		Translations:   data.Translations,
		Suggestions:    data.Suggestions,
		SuggestedLinks: data.SuggestedLinks,
	}
}

func effectiveSort(in searchContentInput) string {
	if strings.TrimSpace(in.Sort) == "" && strings.TrimSpace(in.Query) != "" {
		return "relevance"
	}
	return canonicalSort(in.Sort)
}

func validateFrontMatterPage(p hugosite.SourcePage, aliases map[string]string) []string {
	var issues []string
	if strings.TrimSpace(p.Title) == "" {
		issues = append(issues, "missing title")
	}
	if strings.TrimSpace(p.Date) == "" {
		issues = append(issues, "missing date")
	}
	if p.FrontmatterRaw != nil {
		if _, ok := p.FrontmatterRaw["title"]; !ok {
			issues = append(issues, "front matter missing title field")
		}
		if _, ok := p.FrontmatterRaw["date"]; !ok {
			issues = append(issues, "front matter missing date field")
		}
	}
	if len(aliases) > 0 {
		for _, raw := range p.Tags {
			s := taxonomy.Slug(raw)
			if canonical, ok := aliases[s]; ok {
				issues = append(issues, fmt.Sprintf("tag %q is an alias for %q; consider using the canonical form", raw, canonical))
			}
		}
		for _, raw := range p.Categories {
			s := taxonomy.Slug(raw)
			if canonical, ok := aliases[s]; ok {
				issues = append(issues, fmt.Sprintf("category %q is an alias for %q; consider using the canonical form", raw, canonical))
			}
		}
	}
	return issues
}

func sourcePagesForValidation(idx *hugosite.SourceIndex, slug string) ([]hugosite.SourcePage, error) {
	if idx == nil {
		return nil, nil
	}
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return idx.ListPages(0, 0), nil
	}
	if p, ok := idx.GetBySlug(slug); ok {
		return []hugosite.SourcePage{*p}, nil
	}
	return nil, fmt.Errorf("content_not_found: no source page matched slug %q", slug)
}

// clampScore clamps a score component to [0, 100].
func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func buildSiteHealth(idx *site.Index, srcIdx *hugosite.SourceIndex, aliases map[string]string) contentEnvelopeData {
	health := contentEnvelopeData{
		Status: "healthy",
	}
	if idx != nil {
		contentPages := idx.ContentPages()
		health.PublishedPages = len(contentPages)
		health.Tags = len(idx.AllTags())
		health.Categories = len(idx.AllCategories())
		// Detect orphans: article pages with zero incoming internal links.
		classifier := site.NewClassifier(idx)
		for _, p := range contentPages {
			if !classifier.IsArticle(p) {
				continue
			}
			if len(idx.GetBacklinks(p.Slug)) == 0 {
				health.OrphanPages = append(health.OrphanPages, p.Slug)
			}
		}
	}
	if srcIdx != nil {
		pages := srcIdx.ListPages(0, 0)
		health.Tags = len(srcIdx.AllTags())
		health.Categories = len(srcIdx.AllCategories())
		health.SourcePages = len(pages)
		for _, p := range pages {
			if p.Draft {
				health.DraftPages++
			}
			issues := validateFrontMatterPage(p, aliases)
			if len(issues) > 0 {
				health.ValidationErrors++
				for _, issue := range issues {
					switch issue {
					case "missing title", "front matter missing title field":
						health.MissingTitles++
					case "missing date", "front matter missing date field":
						health.MissingDates++
					}
				}
			}
		}
		details := detectTaxonomyInconsistencies(srcIdx, aliases)
		health.TaxonomyInconsistencyDetails = details
		for _, d := range details {
			health.TaxonomyInconsistencies = append(health.TaxonomyInconsistencies, d.Message)
		}
	}

	// score_breakdown (#419) is presentation only — it must not change what
	// `score` itself was computed from (that's the pre-existing formula
	// below, byte-for-byte). frontmatter carries 100% of the weight because
	// it's the only category this formula has ever penalized; taxonomy
	// carries 0% because a taxonomy finding — even a "warning"-severity one
	// — has never moved `score` and still doesn't. taxonomy.score is shown
	// for reference only (a per-finding informational penalty local to that
	// category) and does not feed into the top-level score.
	const frontmatterWeight, taxonomyWeight = 100, 0
	frontmatterPenalty := (health.ValidationErrors * 10) + (health.MissingTitles * 5) + (health.MissingDates * 5)
	frontmatterScore := clampScore(100 - frontmatterPenalty)

	var taxonomyWarnings, taxonomyAdvisories int
	for _, d := range health.TaxonomyInconsistencyDetails {
		if d.Severity == "info" {
			taxonomyAdvisories++
		} else {
			taxonomyWarnings++
		}
	}
	taxonomyScore := clampScore(100 - taxonomyWarnings*2)

	health.ScoreBreakdown = &scoreBreakdownDTO{
		Frontmatter: scoreCategoryDTO{Score: frontmatterScore, Weight: frontmatterWeight, Issues: health.ValidationErrors},
		Taxonomy:    scoreCategoryDTO{Score: taxonomyScore, Weight: taxonomyWeight, Issues: taxonomyWarnings, Advisories: taxonomyAdvisories},
	}

	score := frontmatterScore
	health.Score = score
	switch {
	case score >= 90:
		health.Status = "healthy"
	case score >= 70:
		health.Status = "degraded"
	default:
		health.Status = "critical"
	}
	return health
}

// detectTaxonomyInconsistencies finds slug pairs that look like duplicates or
// transliterations and flags alias-key terms that should use their canonical
// form. Each finding carries the slugs of affected pages (#324) so an agent
// can act on it directly instead of running a separate list_pages/filter
// round trip to find which pages use which term.
func detectTaxonomyInconsistencies(srcIdx *hugosite.SourceIndex, aliases map[string]string) []taxonomyInconsistencyDTO {
	if srcIdx == nil {
		return nil
	}
	var out []taxonomyInconsistencyDTO

	pages := srcIdx.ListPages(0, 0)
	tagPages := map[string][]string{}
	catPages := map[string][]string{}
	// tagOccurrence/catOccurrence track (page slug -> lang) per term, used
	// by isTranslationPair below to confirm two terms never both land on
	// the exact same (slug, lang) — i.e. they're genuinely different
	// language variants of the same bundle, not two spelling variants
	// applied together on one (possibly monolingual) page.
	tagOccurrence := map[string]map[string]string{}
	catOccurrence := map[string]map[string]string{}
	for _, p := range pages {
		for _, t := range p.Tags {
			s := taxonomy.Slug(t)
			tagPages[s] = append(tagPages[s], p.Slug)
			if tagOccurrence[s] == nil {
				tagOccurrence[s] = map[string]string{}
			}
			tagOccurrence[s][p.Slug] = p.Lang
		}
		for _, c := range p.Categories {
			s := taxonomy.Slug(c)
			catPages[s] = append(catPages[s], p.Slug)
			if catOccurrence[s] == nil {
				catOccurrence[s] = map[string]string{}
			}
			catOccurrence[s][p.Slug] = p.Lang
		}
	}

	// Report alias mismatches: terms in content that should use the canonical form.
	tagSlugs := make([]string, 0)
	for _, raw := range srcIdx.AllTags() {
		s := taxonomy.Slug(raw)
		if canonical, ok := aliases[s]; ok {
			out = append(out, taxonomyInconsistencyDTO{
				Message:        fmt.Sprintf("tag %q is an alias for %q; use the canonical form", raw, canonical),
				TermA:          raw,
				PagesWithTermA: tagPages[s],
				Kind:           "alias_mismatch",
			})
		}
		tagSlugs = append(tagSlugs, s)
	}
	catSlugs := make([]string, 0)
	for _, raw := range srcIdx.AllCategories() {
		s := taxonomy.Slug(raw)
		if canonical, ok := aliases[s]; ok {
			out = append(out, taxonomyInconsistencyDTO{
				Message:        fmt.Sprintf("category %q is an alias for %q; use the canonical form", raw, canonical),
				TermA:          raw,
				PagesWithTermA: catPages[s],
				Kind:           "alias_mismatch",
			})
		}
		catSlugs = append(catSlugs, s)
	}

	// Report similar slug pairs. #183: a pair used on exactly the same set
	// of page-bundle slugs (just in different languages — the same Hugo
	// page bundle uses one Slug across index.en.md/index.fr.md, see
	// hugosite.SlugFromRel) is the site's own localization, not a content
	// inconsistency — classify it as translation_pair/info instead of
	// possible_duplicate/warning so it doesn't read as an actionable
	// finding needing a taxonomy_aliases entry to go away.
	const maxDist, minLen = 2, 5
	for _, pair := range taxonomy.FindSimilarPairs(tagSlugs, maxDist, minLen, aliases) {
		kind, verb := "possible_duplicate", "may be duplicates"
		if isTranslationPair(tagPages[pair[0]], tagPages[pair[1]], tagOccurrence[pair[0]], tagOccurrence[pair[1]]) {
			kind, verb = "translation_pair", "are used on the same page bundle in different languages, not a duplicate"
		}
		out = append(out, taxonomyInconsistencyDTO{
			Message:        fmt.Sprintf("tags %q and %q %s (edit distance ≤ %d)", pair[0], pair[1], verb, maxDist),
			TermA:          pair[0],
			TermB:          pair[1],
			PagesWithTermA: tagPages[pair[0]],
			PagesWithTermB: tagPages[pair[1]],
			Kind:           kind,
		})
	}
	for _, pair := range taxonomy.FindSimilarPairs(catSlugs, maxDist, minLen, aliases) {
		kind, verb := "possible_duplicate", "may be duplicates"
		if isTranslationPair(catPages[pair[0]], catPages[pair[1]], catOccurrence[pair[0]], catOccurrence[pair[1]]) {
			kind, verb = "translation_pair", "are used on the same page bundle in different languages, not a duplicate"
		}
		out = append(out, taxonomyInconsistencyDTO{
			Message:        fmt.Sprintf("categories %q and %q %s (edit distance ≤ %d)", pair[0], pair[1], verb, maxDist),
			TermA:          pair[0],
			TermB:          pair[1],
			PagesWithTermA: catPages[pair[0]],
			PagesWithTermB: catPages[pair[1]],
			Kind:           kind,
		})
	}

	for i := range out {
		out[i].Severity = taxonomyFindingSeverity(out[i].Kind)
	}
	return out
}

// isTranslationPair reports whether two taxonomy terms are genuinely
// different-language variants of the same page bundle rather than two
// spelling variants applied to the same or unrelated pages (#183). Two
// conditions must both hold:
//  1. the terms are used on exactly the same set of page-bundle slugs
//     (pagesA/pagesB, order and duplicate count ignored) — a bundle's
//     index.en.md/index.fr.md share one Slug per hugosite.SlugFromRel;
//  2. no single (slug, lang) pair carries both terms — otherwise a
//     monolingual page tagged with both spelling variants directly
//     (e.g. tags: [postmortem, post-mortems] on one index.md, lang="")
//     would be wrongly classified as a translation instead of the typo
//     it actually is.
func isTranslationPair(pagesA, pagesB []string, occA, occB map[string]string) bool {
	if len(pagesA) == 0 || len(pagesA) != len(pagesB) {
		return false
	}
	counts := make(map[string]int, len(pagesA))
	for _, s := range pagesA {
		counts[s]++
	}
	for _, s := range pagesB {
		counts[s]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	for slug, langA := range occA {
		if langB, ok := occB[slug]; ok && langA == langB {
			return false
		}
	}
	return true
}

func countSections(pages []site.Page) []sectionDTO {
	counts := map[string]int{}
	classifier := site.NewClassifierFromPages(pages)
	for _, p := range pages {
		if !classifier.IsContent(p) {
			continue
		}
		seg := topSection(p.Slug, p.Lang)
		counts[seg]++
	}
	out := make([]sectionDTO, 0, len(counts))
	for name, count := range counts {
		out = append(out, sectionDTO{Name: name, Count: count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// topSection derives a page's editorial section from its slug. lang, when
// non-empty, is the page's own resolved language: if the slug's first path
// segment is that language's route prefix (e.g. "/en/posts/foo/" for an
// English page), it's stripped before section detection so a language code
// is never reported as if it were a content section (#459) — languages are
// already surfaced separately via the sibling `languages` field.
func topSection(slug, lang string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" || slug == "/" {
		return "root"
	}
	trimmed := strings.TrimPrefix(slug, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "root"
	}
	if lang != "" && parts[0] == lang {
		parts = parts[1:]
	}
	if len(parts) == 0 || parts[0] == "" {
		return "root"
	}
	if parts[0] == "posts" {
		return "posts"
	}
	return parts[0]
}

func uniqueLanguages(pages []site.Page) []string {
	seen := map[string]struct{}{}
	for _, p := range pages {
		if strings.TrimSpace(p.Lang) != "" {
			seen[p.Lang] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for lang := range seen {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

func toPageDTO(p site.Page, aliases map[string]string, siteRoot string) pageDTO {
	tags := taxonomy.ApplyAliases(nullsafeStrings(p.Tags), aliases)
	cats := taxonomy.ApplyAliases(nullsafeStrings(p.Categories), aliases)
	return pageDTO{
		Slug:          p.Slug,
		Title:         p.Title,
		Summary:       p.Summary,
		Tags:          tags,
		Categories:    cats,
		TagTerms:      site.NormalizeTaxonomyTerms(tags),
		CategoryTerms: site.NormalizeTaxonomyTerms(cats),
		Date:          p.Date,
		URL:           p.URL,
		Lang:          p.Lang,
		State:         site.StateForResolvedPage(site.ResolvedPage{Public: &p}, siteRoot),
	}
}

func toPageDTOs(pages []site.Page, aliases map[string]string, srcIdx *hugosite.SourceIndex, contentRoot, siteRoot string) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot)
		out[i] = dto
	}
	return out
}

// toPageDTOsEnriched enriches public-index pages with source-frontmatter
// categories. The source index is authoritative: when a match is found its
// categories replace whatever the HTML index carries (which may be stale or
// empty — Hugo never emits article:category meta tags).
// Language-prefixed slugs (e.g. /en/posts/foo/) are handled via
// site.SourceSlugCandidates, which tries the bare slug then strips the lang
// prefix to match the source-index key (posts/foo).
func toPageDTOsEnriched(pages []site.Page, srcIdx *hugosite.SourceIndex, aliases map[string]string, contentRoot, siteRoot string) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot)
		out[i] = dto
	}
	return out
}

func toPageDTOsWithSnippets(pages []site.Page, aliases map[string]string, snippets map[string]string, srcIdx *hugosite.SourceIndex, contentRoot, siteRoot string) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot)
		dto.Snippet = snippets[p.Slug]
		out[i] = dto
	}
	return out
}

type sourceLookup struct {
	byLang    map[string]hugosite.SourcePage
	byDefault map[string]hugosite.SourcePage
	bySlug    map[string]hugosite.SourcePage
}

type resolvedSourceMatch struct {
	Page         hugosite.SourcePage
	ResolvedLang string
}

func newSourceLookup(srcIdx *hugosite.SourceIndex) *sourceLookup {
	if srcIdx == nil {
		return nil
	}
	pages := srcIdx.ListPages(0, 0)
	lookup := &sourceLookup{
		byLang:    make(map[string]hugosite.SourcePage, len(pages)),
		byDefault: make(map[string]hugosite.SourcePage, len(pages)),
		bySlug:    make(map[string]hugosite.SourcePage, len(pages)),
	}
	for _, src := range pages {
		if _, ok := lookup.bySlug[src.Slug]; !ok {
			lookup.bySlug[src.Slug] = src
		}
		if src.Lang == "" {
			if _, ok := lookup.byDefault[src.Slug]; !ok {
				lookup.byDefault[src.Slug] = src
			}
			continue
		}
		key := sourceLookupKey(src.Slug, src.Lang)
		if _, ok := lookup.byLang[key]; !ok {
			lookup.byLang[key] = src
		}
	}
	return lookup
}

func sourceLookupKey(slug, lang string) string {
	return slug + "\x00" + lang
}

func sourceSlugCandidatesForPage(p site.Page) []string {
	seen := map[string]struct{}{}
	add := func(out []string, slug string) []string {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return out
		}
		if _, ok := seen[slug]; ok {
			return out
		}
		seen[slug] = struct{}{}
		return append(out, slug)
	}

	var out []string
	for _, candidate := range site.SourceSlugCandidates(p.Slug) {
		out = add(out, candidate)
		if p.Lang != "" {
			out = add(out, candidate+"."+p.Lang)
		}
	}
	return out
}

func resolveSourceForPage(p site.Page, lookup *sourceLookup) (resolvedSourceMatch, bool) {
	if lookup == nil {
		return resolvedSourceMatch{}, false
	}
	candidates := sourceSlugCandidatesForPage(p)
	var languageSpecific []string
	var base []string
	for _, candidate := range candidates {
		if p.Lang != "" && strings.HasSuffix(candidate, "."+p.Lang) {
			languageSpecific = append(languageSpecific, candidate)
			continue
		}
		base = append(base, candidate)
	}
	if p.Lang != "" {
		for _, candidate := range candidates {
			if src, ok := lookup.byLang[sourceLookupKey(candidate, p.Lang)]; ok {
				return resolvedSourceMatch{Page: src, ResolvedLang: p.Lang}, true
			}
		}
	}
	for _, candidate := range languageSpecific {
		if src, ok := lookup.bySlug[candidate]; ok {
			return resolvedSourceMatch{Page: src, ResolvedLang: firstNonEmpty(src.Lang, p.Lang)}, true
		}
	}
	for _, candidate := range base {
		if src, ok := lookup.byDefault[candidate]; ok {
			return resolvedSourceMatch{Page: src, ResolvedLang: src.Lang}, true
		}
	}
	for _, candidate := range base {
		if src, ok := lookup.bySlug[candidate]; ok {
			return resolvedSourceMatch{Page: src, ResolvedLang: firstNonEmpty(src.Lang, p.Lang)}, true
		}
	}
	for _, candidate := range languageSpecific {
		if src, ok := lookup.bySlug[candidate]; ok {
			return resolvedSourceMatch{Page: src, ResolvedLang: firstNonEmpty(src.Lang, p.Lang)}, true
		}
	}
	return resolvedSourceMatch{}, false
}

func enrichPageDTOFromSource(dto *pageDTO, p site.Page, lookup *sourceLookup, aliases map[string]string, contentRoot, siteRoot string) {
	if dto == nil || lookup == nil {
		return
	}
	if match, ok := resolveSourceForPage(p, lookup); ok {
		src := match.Page
		dto.Categories = taxonomy.ApplyAliases(nullsafeStrings(src.Categories), aliases)
		dto.CategoryTerms = site.NormalizeTaxonomyTerms(dto.Categories)
		dto.ResolvedLang = match.ResolvedLang
		dto.ResolvedSourcePath = fileutil.LogicalContentPath(contentRoot, src.FilePath)
		dto.State = site.StateForResolvedPage(site.ResolvedPage{
			Public:     &p,
			Source:     &src,
			SourcePath: src.FilePath,
		}, siteRoot)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
