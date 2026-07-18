package anonymous

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"slices"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
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

type listPagesInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type getPageInput struct {
	Slug                string `json:"slug"`
	ContentOnly         bool   `json:"content_only,omitempty"`
	AllowSourceFallback bool   `json:"allow_source_fallback,omitempty"`
}

type searchPagesInput struct {
	Query        string   `json:"query"`
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`
	ResponseMode string   `json:"response_mode,omitempty"`
	Fields       []string `json:"fields,omitempty"`
	Match        string   `json:"match,omitempty"`
}

// resolveSearchMatch validates the match mode (#332). "any" (the default,
// pre-existing behavior) does broad term-based substring matching across
// title/summary/tags/categories/url. "title_exact" is a stricter mode for
// callers that need to know whether a specific page still exists (e.g.
// verifying a delete) without wading through loosely related hits.
func resolveSearchMatch(raw string) (string, error) {
	switch raw {
	case "", "any":
		return "any", nil
	case "title_exact":
		return "title_exact", nil
	default:
		return "", fmt.Errorf("invalid_params: match must be one of: any, title_exact (got %q)", raw)
	}
}

// pageDTOCompact is the reduced shape returned when response_mode=compact:
// just enough to identify and link to a page during a selection pass,
// without the fields (summary, tags, categories, date, lang) an agent
// typically doesn't need until it has picked a candidate.
type pageDTOCompact struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type getRecentPostsInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type getFeedInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type pageDTO struct {
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
	Date       string   `json:"date"`
	URL        string   `json:"url"`
	Lang       string   `json:"lang"`
	// Score is only populated by search_pages (#332): the count of query
	// terms that matched somewhere in the page. Other tools that build a
	// pageDTO (list_pages, get_recent_posts, ...) never set it, so it's
	// omitted from their output via omitempty.
	Score int `json:"score,omitempty"`
}

type pageDetailDTO struct {
	Slug               string                  `json:"slug"`
	Title              string                  `json:"title"`
	Summary            string                  `json:"summary"`
	Tags               []string                `json:"tags"`
	Categories         []string                `json:"categories"`
	TagTerms           []taxonomy.TaxonomyTerm `json:"tag_terms,omitempty"`
	CategoryTerms      []taxonomy.TaxonomyTerm `json:"category_terms,omitempty"`
	Date               string                  `json:"date"`
	URL                string                  `json:"url"`
	Lang               string                  `json:"lang"`
	ResolvedLang       string                  `json:"resolved_lang"`
	ResolvedSourcePath string                  `json:"resolved_source_path"`
	Revision           string                  `json:"revision,omitempty"`
	HTML               string                  `json:"html"`
	HTMLOrigin         string                  `json:"html_origin"`
	RenderedHTMLAvail  bool                    `json:"rendered_html_available"`
	State              site.LifecycleState     `json:"state"`
}

type getSitemapInput struct {
	Limit             int  `json:"limit,omitempty"`
	Offset            int  `json:"offset,omitempty"`
	ExcludeTaxonomies bool `json:"exclude_taxonomies,omitempty"`
}

type sitemapEntryDTO struct {
	Slug string `json:"slug"`
	URL  string `json:"url"`
	Date string `json:"date"`
}

type feedItemDTO struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Date    string `json:"date"`
	URL     string `json:"url"`
}

type siteInfoDTO struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

type listPagesData struct {
	Pages         []pageDTO `json:"pages"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
}

type getPageData struct {
	Page pageDetailDTO `json:"page"`
}

type searchPagesData struct {
	Pages         []any `json:"pages"`
	Total         int   `json:"total"`
	Limit         int   `json:"limit"`
	Offset        int   `json:"offset"`
	ReturnedCount int   `json:"returned_count"`
	HasMore       bool  `json:"has_more"`
	NextOffset    *int  `json:"next_offset,omitempty"`
}

type getRecentPostsData struct {
	Pages         []pageDTO `json:"pages"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
}

type listTagsData struct {
	Tags []string `json:"tags"`
}

type listCategoriesData struct {
	Categories []string `json:"categories"`
}

type getSitemapData struct {
	Entries       []sitemapEntryDTO `json:"entries"`
	Total         int               `json:"total"`
	Limit         int               `json:"limit"`
	Offset        int               `json:"offset"`
	ReturnedCount int               `json:"returned_count"`
	HasMore       bool              `json:"has_more"`
	NextOffset    *int              `json:"next_offset,omitempty"`
}

type getFeedData struct {
	Items         []feedItemDTO `json:"items"`
	Total         int           `json:"total"`
	Limit         int           `json:"limit"`
	Offset        int           `json:"offset"`
	ReturnedCount int           `json:"returned_count"`
	HasMore       bool          `json:"has_more"`
	NextOffset    *int          `json:"next_offset,omitempty"`
}

type getSiteInformationData struct {
	Site siteInfoDTO `json:"site"`
}

// Output types below intentionally carry nothing beyond the embedded
// toolcontract.ToolResponse[T] — data.X (inside the envelope) is the sole,
// canonical location for the payload. They previously also duplicated every
// field at the top level (data.pages AND pages, etc.), roughly doubling
// response size for these tools with no functional benefit; removed as the
// approved resolution to #433.
type listPagesOutput struct {
	toolcontract.ToolResponse[listPagesData]
}

type getPageOutput struct {
	toolcontract.ToolResponse[getPageData]
}

type searchPagesOutput struct {
	toolcontract.ToolResponse[searchPagesData]
}

type getRecentPostsOutput struct {
	toolcontract.ToolResponse[getRecentPostsData]
}

type listTagsOutput struct {
	toolcontract.ToolResponse[listTagsData]
}

type listCategoriesOutput struct {
	toolcontract.ToolResponse[listCategoriesData]
}

type getSitemapOutput struct {
	toolcontract.ToolResponse[getSitemapData]
}

type getFeedOutput struct {
	toolcontract.ToolResponse[getFeedData]
}

type getSiteInformationOutput struct {
	toolcontract.ToolResponse[getSiteInformationData]
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
	addReadOnlyTool(s, "list_pages", "Browse pages", "Browse published content pages (articles and pages, not taxonomy list pages) with pagination. Returns slug, title, summary, tags, categories, date, URL. Does not require authentication. For the full URL inventory including taxonomy pages use get_sitemap.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPagesInput) (*mcp.CallToolResult, listPagesOutput, error) {
			if idx == nil {
				return nil, listPagesOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 50, 50)
			all := idx.ContentPages()
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			total := len(all)
			if offset >= len(all) {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, newListPagesOutput(listPagesData{Pages: []pageDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
			}
			slice := all[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(slice))
			return nil, newListPagesOutput(listPagesData{Pages: toPageDTOsForProfile(slice, srcIdx, aliases, site.IsReaderProfile(ctx)), Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 50) })

	addReadOnlyTool(s, "get_page", "Read page",
		"Read a Hugo page by slug. Returns metadata, rendered HTML, and a short summary. "+
			"By default only published pages are returned. "+
			"Pass allow_source_fallback=true to also return source-index entries for pages not yet built "+
			"(e.g. immediately after create_page, before the next Hugo build); draft pages are always excluded. "+
			"Pass content_only=true to return just the article body (prefers the theme's id=\"content\" wrapper when present, excluding title, TOC, post metadata, share buttons, and prev/next navigation; falls back to <article>/<main>/<body>-minus-chrome otherwise) from the rendered HTML of published pages "+
			"(source-only fallback normally carries raw Markdown rather than rendered HTML; `lang` and `url` are empty until the page is built; if `content_only=true` is also set, the `html` field is returned empty for source-only fallback results). "+
			"`html_origin` and `rendered_html_available` make that distinction explicit: published reads return `rendered_public`/`true`, source fallback returns `source_fallback`/`false`, and source-only `content_only=true` returns `none`/`false`. "+
			"The response includes a `state` object with explicit source/build/public/index visibility hints so agents do not have to infer lifecycle state from empty fields alone. "+
			"For the raw Markdown source, use get_page_markdown (requires content.read); for metadata only (no body), use get_page_frontmatter; if you're about to edit or delete this page, use get_page_for_edit instead — it bundles frontmatter, markdown, revision, and quality signals in one call. "+
			"Does not require authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getPageInput) (*mcp.CallToolResult, getPageOutput, error) {
			if idx == nil && srcIdx == nil {
				return nil, getPageOutput{}, fmt.Errorf("index not initialized")
			}
			if in.Slug == "" {
				return nil, getPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			if site.IsReaderProfile(ctx) {
				publicOnly, ok := site.ReaderSafeResolvedPage(resolved)
				if !ok {
					return nil, getPageOutput{}, fmt.Errorf("content_not_public: page is not publicly available for slug %q", in.Slug)
				}
				resolved = publicOnly
			}
			if resolved.Public == nil {
				// Source-only: require explicit opt-in; drafts, future, and expired pages are blocked.
				if !in.AllowSourceFallback {
					return nil, getPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
				}
				if s := resolved.Source; s != nil {
					now := time.Now().UTC()
					if s.Draft ||
						(!s.PublishDate.IsZero() && now.Before(s.PublishDate)) ||
						(!s.ExpiryDate.IsZero() && now.After(s.ExpiryDate)) {
						return nil, getPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
					}
				}
			}
			dto := toResolvedPageDetailDTO(resolved, cfg.ContentRoot)
			dto.State = site.StateForResolvedPage(resolved, cfg.SiteRoot)
			if in.ContentOnly && resolved.Public != nil {
				dto.HTML = site.ExtractArticleHTML(dto.HTML)
			} else if in.ContentOnly {
				dto.HTML = ""
				dto.HTMLOrigin = "none"
			}
			return nil, newGetPageOutput(getPageData{Page: dto}), nil
		})

	addReadOnlyTool(s, "search_pages", "Search content", "Keyword search across published pages (title, summary, tags, categories, URL). No authentication required. Anonymous alternative to search_content — if you have content.read scope, prefer search_content instead: it also matches body text, and supports type/language/sort filtering that this tool doesn't. Matching is intentionally broad: any page containing at least one query term in any indexed field is returned, ranked by `score` (count of matching terms, highest first) — it is not an exact-match search. Each result's `score` field indicates match strength; a low score means a loose/partial match. Use `match: \"title_exact\"` for a strict case-insensitive full-title match instead (e.g. to verify whether a specific page still exists after deleting it), which returns zero results rather than loosely related hits when there's no exact title match. Supports response shaping: `response_mode: \"compact\"` returns only slug/title/url per page (use during selection, before fetching full content); `fields: [...]` restricts each page to the named JSON fields, applied after response_mode. Omitting both preserves the full default shape.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchPagesInput) (*mcp.CallToolResult, searchPagesOutput, error) {
			if idx == nil {
				return nil, searchPagesOutput{}, fmt.Errorf("index not initialized")
			}
			if in.Query == "" {
				return nil, searchPagesOutput{}, fmt.Errorf("invalid_params: query must not be empty")
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, searchPagesOutput{}, err
			}
			match, err := resolveSearchMatch(in.Match)
			if err != nil {
				return nil, searchPagesOutput{}, err
			}
			limit := clampLimit(in.Limit, 50, 50)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			allScored := idx.SearchScored(in.Query, 0)
			if match == "title_exact" {
				q := strings.TrimSpace(strings.ToLower(in.Query))
				filtered := make([]site.ScoredPage, 0, len(allScored))
				for _, s := range allScored {
					if strings.TrimSpace(strings.ToLower(s.Page.Title)) == q {
						filtered = append(filtered, s)
					}
				}
				allScored = filtered
			}
			total := len(allScored)
			if offset >= total {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, newSearchPagesOutput(searchPagesData{Pages: []any{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
			}
			scoredPage := allScored[offset:]
			if len(scoredPage) > limit {
				scoredPage = scoredPage[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(scoredPage))
			pages := make([]site.Page, len(scoredPage))
			for i, s := range scoredPage {
				pages[i] = s.Page
			}
			// toPageDTOsForProfile (both its reader-safe and enriched branches)
			// always emits exactly one DTO per input page, in the same order —
			// so attaching scoredPage[i].Score by index here is safe.
			dtos := toPageDTOsForProfile(pages, srcIdx, aliases, site.IsReaderProfile(ctx))
			for i := range dtos {
				dtos[i].Score = scoredPage[i].Score
			}
			shaped := shapeSearchPages(dtos, mode, in.Fields)
			return nil, newSearchPagesOutput(searchPagesData{Pages: shaped, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
		},
		func(s any) any {
			return tools.WithEnum(s, "match", "", "any", "title_exact")
		},
		func(s any) any {
			return tools.WithEnum(s, "response_mode", "", string(toolcontract.ResponseModeStandard), string(toolcontract.ResponseModeCompact))
		},
		func(s any) any { return tools.WithMaxLimit(s, "limit", 50) },
	)

	addReadOnlyTool(s, "get_recent_posts", "Read recent posts", "Return the most recent published posts from the index. Use this for timeline-style summaries without authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getRecentPostsInput) (*mcp.CallToolResult, getRecentPostsOutput, error) {
			if idx == nil {
				return nil, getRecentPostsOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 10, 50)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			all := idx.RecentPosts(0)
			total := len(all)
			if offset >= total {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, newGetRecentPostsOutput(getRecentPostsData{Pages: []pageDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
			}
			pages := all[offset:]
			if len(pages) > limit {
				pages = pages[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(pages))
			return nil, newGetRecentPostsOutput(getRecentPostsData{Pages: toPageDTOsForProfile(pages, srcIdx, aliases, site.IsReaderProfile(ctx)), Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 50) })

	addReadOnlyTool(s, "list_tags", "Browse tags", "List the tags discovered from the index. Returns a sorted tag list and does not require authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTagsOutput, error) {
			if idx == nil {
				return nil, listTagsOutput{}, fmt.Errorf("index not initialized")
			}
			tags := idx.AllTags()
			if srcIdx != nil && !site.IsReaderProfile(ctx) {
				tags = srcIdx.AllTags()
			}
			if tags == nil {
				tags = []string{}
			}
			tags = taxonomy.ApplyAliases(tags, aliases)
			return nil, newListTagsOutput(listTagsData{Tags: tags}), nil
		})

	addReadOnlyTool(s, "list_categories", "Browse categories", "List the categories discovered from the index. Returns a sorted category list and does not require authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listCategoriesOutput, error) {
			if idx == nil {
				return nil, listCategoriesOutput{}, fmt.Errorf("index not initialized")
			}
			cats := idx.AllCategories()
			if srcIdx != nil && !site.IsReaderProfile(ctx) {
				cats = srcIdx.AllCategories()
			}
			if cats == nil {
				cats = []string{}
			}
			cats = taxonomy.ApplyAliases(cats, aliases)
			return nil, newListCategoriesOutput(listCategoriesData{Categories: cats}), nil
		})

	addReadOnlyTool(s, "get_sitemap", "Read sitemap",
		"Return the full published URL inventory (slug, URL, date) including taxonomy list pages (/tags/…, /categories/…). No authentication required. "+
			"Pass exclude_taxonomies=true to restrict to content pages only. For content-page browsing with titles and summaries use list_pages.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getSitemapInput) (*mcp.CallToolResult, getSitemapOutput, error) {
			if idx == nil {
				return nil, getSitemapOutput{}, fmt.Errorf("index not initialized")
			}
			all := idx.Sitemap()
			if in.ExcludeTaxonomies {
				classifier := site.NewClassifierFromPages(all)
				filtered := all[:0]
				for _, p := range all {
					if classifier.IsContent(p) && !isTaxonomyURL(p.Slug) && !isTaxonomyURL(p.URL) {
						filtered = append(filtered, p)
					}
				}
				all = filtered
			}
			limit := clampLimit(in.Limit, 200, 200)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			total := len(all)
			if offset >= len(all) {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, newGetSitemapOutput(getSitemapData{Entries: []sitemapEntryDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
			}
			slice := all[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			entries := make([]sitemapEntryDTO, len(slice))
			for i, p := range slice {
				entries[i] = sitemapEntryDTO{Slug: p.Slug, URL: p.URL, Date: p.Date}
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(entries))
			return nil, newGetSitemapOutput(getSitemapData{Entries: entries, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 200) })

	addReadOnlyTool(s, "get_feed", "Read feed", "Return recent published items as a feed-like list. Use this for lightweight content digests without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getFeedInput) (*mcp.CallToolResult, getFeedOutput, error) {
			if idx == nil {
				return nil, getFeedOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 20, 50)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			all := idx.GetFeed(0)
			total := len(all)
			if offset >= total {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, newGetFeedOutput(getFeedData{Items: []feedItemDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
			}
			pages := all[offset:]
			if len(pages) > limit {
				pages = pages[:limit]
			}
			items := make([]feedItemDTO, len(pages))
			for i, p := range pages {
				items[i] = feedItemDTO{Slug: p.Slug, Title: p.Title, Summary: p.Summary, Date: p.Date, URL: p.URL}
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(items))
			return nil, newGetFeedOutput(getFeedData{Items: items, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 50) })

	addReadOnlyTool(s, "get_site_information", "Read site metadata", "Return basic metadata for the indexed site, including name, URL, and language. Useful for onboarding and discovery without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getSiteInformationOutput, error) {
			if idx == nil {
				return nil, getSiteInformationOutput{}, fmt.Errorf("index not initialized")
			}
			info := idx.SiteInfo()
			return nil, newGetSiteInformationOutput(getSiteInformationData{Site: siteInfoDTO{
				Name: info["name"],
				URL:  info["url"],
				Lang: info["lang"],
			}}), nil
		})
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

func success[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func newListPagesOutput(data listPagesData) listPagesOutput {
	return listPagesOutput{ToolResponse: success(data)}
}

func newGetPageOutput(data getPageData) getPageOutput {
	return getPageOutput{ToolResponse: success(data)}
}

func newSearchPagesOutput(data searchPagesData) searchPagesOutput {
	return searchPagesOutput{ToolResponse: success(data)}
}

func newGetRecentPostsOutput(data getRecentPostsData) getRecentPostsOutput {
	return getRecentPostsOutput{ToolResponse: success(data)}
}

func newListTagsOutput(data listTagsData) listTagsOutput {
	return listTagsOutput{ToolResponse: success(data)}
}

func newListCategoriesOutput(data listCategoriesData) listCategoriesOutput {
	return listCategoriesOutput{ToolResponse: success(data)}
}

func newGetSitemapOutput(data getSitemapData) getSitemapOutput {
	return getSitemapOutput{ToolResponse: success(data)}
}

func newGetFeedOutput(data getFeedData) getFeedOutput {
	return getFeedOutput{ToolResponse: success(data)}
}

func newGetSiteInformationOutput(data getSiteInformationData) getSiteInformationOutput {
	return getSiteInformationOutput{ToolResponse: success(data)}
}

func clampLimit(v, defaultVal, maxVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func toPageDTO(p site.Page) pageDTO {
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	cats := p.Categories
	if cats == nil {
		cats = []string{}
	}
	return pageDTO{
		Slug:       p.Slug,
		Title:      p.Title,
		Summary:    p.Summary,
		Tags:       tags,
		Categories: cats,
		Date:       p.Date,
		URL:        p.URL,
		Lang:       p.Lang,
	}
}

// toPageDTOsEnriched builds page DTOs with source-index category enrichment and
// alias folding applied. Both are optional: srcIdx may be nil, aliases may be empty.
// The source index is authoritative for categories; language-prefixed slugs
// (e.g. /en/posts/foo/) are resolved via site.SourceSlugCandidates.
func toPageDTOsEnriched(pages []site.Page, srcIdx *hugosite.SourceIndex, aliases map[string]string) []pageDTO {
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p)
		if srcIdx != nil {
			for _, candidate := range site.SourceSlugCandidates(p.Slug) {
				if src, ok := srcIdx.GetBySlug(candidate); ok {
					cats := src.Categories
					if cats == nil {
						cats = []string{}
					}
					dto.Categories = cats
					break
				}
			}
		}
		if len(aliases) > 0 {
			dto.Tags = taxonomy.ApplyAliases(dto.Tags, aliases)
			dto.Categories = taxonomy.ApplyAliases(dto.Categories, aliases)
		}
		out[i] = dto
	}
	return out
}

func toPageDTOsForProfile(pages []site.Page, srcIdx *hugosite.SourceIndex, aliases map[string]string, readerSafe bool) []pageDTO {
	if readerSafe {
		out := make([]pageDTO, len(pages))
		for i, p := range pages {
			dto := toPageDTO(p)
			if len(aliases) > 0 {
				dto.Tags = taxonomy.ApplyAliases(dto.Tags, aliases)
				dto.Categories = taxonomy.ApplyAliases(dto.Categories, aliases)
			}
			out[i] = dto
		}
		return out
	}
	return toPageDTOsEnriched(pages, srcIdx, aliases)
}

// shapeSearchPages applies response_mode then fields selection to a slice
// of pageDTO, in that order. Both are no-ops when unset, so the default
// call (mode=standard, fields=nil) returns rows byte-identical to the
// pre-shaping []pageDTO output.
func shapeSearchPages(dtos []pageDTO, mode toolcontract.ResponseMode, fields []string) []any {
	out := make([]any, len(dtos))
	for i, dto := range dtos {
		var row any = dto
		if mode == toolcontract.ResponseModeCompact {
			row = pageDTOCompact{Slug: dto.Slug, Title: dto.Title, URL: dto.URL}
		}
		if len(fields) > 0 {
			row = toolcontract.SelectFields(toFieldMap(row), fields)
		}
		out[i] = row
	}
	return out
}

// toFieldMap round-trips v through JSON so SelectFields can operate on its
// field names generically, regardless of which concrete DTO v holds.
func toFieldMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func toPageDetailDTO(p site.Page) pageDetailDTO {
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	cats := p.Categories
	if cats == nil {
		cats = []string{}
	}
	return pageDetailDTO{
		Slug:              p.Slug,
		Title:             p.Title,
		Summary:           p.Summary,
		Tags:              tags,
		Categories:        cats,
		TagTerms:          taxonomy.Normalize(tags),
		CategoryTerms:     taxonomy.Normalize(cats),
		Date:              p.Date,
		URL:               p.URL,
		Lang:              p.Lang,
		HTML:              p.RawHTML,
		HTMLOrigin:        "rendered_public",
		RenderedHTMLAvail: true,
	}
}

func toResolvedPageDetailDTO(resolved site.ResolvedPage, contentRoot string) pageDetailDTO {
	if resolved.Public != nil {
		page := *resolved.Public
		if resolved.Source != nil {
			page.Tags = resolved.Source.Tags
			page.Categories = resolved.Source.Categories
		}
		dto := toPageDetailDTO(page)
		if resolved.Source != nil {
			dto.ResolvedLang = resolved.Source.Lang
			dto.ResolvedSourcePath = fileutil.LogicalContentPath(contentRoot, resolved.SourcePath)
			dto.Revision = resolvedSourceRevision(resolved.SourcePath)
		}
		return dto
	}
	src := resolved.Source
	if src == nil {
		return pageDetailDTO{}
	}
	tags := src.Tags
	if tags == nil {
		tags = []string{}
	}
	cats := src.Categories
	if cats == nil {
		cats = []string{}
	}
	return pageDetailDTO{
		Slug:               "/" + src.Slug + "/",
		Title:              src.Title,
		Tags:               tags,
		Categories:         cats,
		TagTerms:           taxonomy.Normalize(tags),
		CategoryTerms:      taxonomy.Normalize(cats),
		Date:               src.Date,
		ResolvedLang:       src.Lang,
		ResolvedSourcePath: fileutil.LogicalContentPath(contentRoot, resolved.SourcePath),
		Revision:           resolvedSourceRevision(resolved.SourcePath),
		HTML:               src.Body,
		HTMLOrigin:         "source_fallback",
		RenderedHTMLAvail:  false,
	}
}

func resolvedSourceRevision(path string) string {
	if path == "" {
		return ""
	}
	rev, err := contentmodel.SourceRevision(path)
	if err != nil {
		return ""
	}
	return rev
}

// isTaxonomyURL returns true if the URL belongs to a Hugo taxonomy listing page
// (e.g. /tags/hugo/, /categories/infrastructure/, /tags/, /categories/).
// It matches the default Hugo taxonomy URL structure; custom taxonomies may need
// to be excluded manually.
func isTaxonomyURL(rawURL string) bool {
	path := rawURL
	if parsed, err := neturl.Parse(rawURL); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	taxPrefixes := []string{"/tags/", "/categories/", "/authors/"}
	if parts := strings.Split(strings.Trim(path, "/"), "/"); len(parts) >= 2 {
		if looksLikeLanguageCode(parts[0]) && slices.Contains(taxPrefixes, "/"+parts[1]+"/") {
			path = "/" + strings.Join(parts[1:], "/")
			if !strings.HasSuffix(path, "/") {
				path += "/"
			}
		}
	}
	for _, prefix := range taxPrefixes {
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

func looksLikeLanguageCode(v string) bool {
	if len(v) != 2 && len(v) != 5 {
		return false
	}
	for i, r := range v {
		if i == 2 {
			if r != '-' && r != '_' {
				return false
			}
			continue
		}
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// Defs returns the tool definitions for this package (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "list_pages", RequiredScope: ""},
		{Name: "get_page", RequiredScope: ""},
		{Name: "search_pages", RequiredScope: ""},
		{Name: "get_recent_posts", RequiredScope: ""},
		{Name: "list_tags", RequiredScope: ""},
		{Name: "list_categories", RequiredScope: ""},
		{Name: "get_sitemap", RequiredScope: ""},
		{Name: "get_feed", RequiredScope: ""},
		{Name: "get_site_information", RequiredScope: ""},
	}
}
