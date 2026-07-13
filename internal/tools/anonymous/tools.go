package anonymous

import (
	"context"
	"fmt"
	neturl "net/url"
	"slices"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
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
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
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
	HTML               string                  `json:"html"`
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

type listPagesOutput struct {
	Pages         []pageDTO `json:"pages"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
}

type getPageOutput struct {
	Page pageDetailDTO `json:"page"`
}

type searchPagesOutput struct {
	Pages         []pageDTO `json:"pages"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
}

type getRecentPostsOutput struct {
	Pages         []pageDTO `json:"pages"`
	Total         int       `json:"total"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	ReturnedCount int       `json:"returned_count"`
	HasMore       bool      `json:"has_more"`
	NextOffset    *int      `json:"next_offset,omitempty"`
}

type listTagsOutput struct {
	Tags []string `json:"tags"`
}

type listCategoriesOutput struct {
	Categories []string `json:"categories"`
}

type getSitemapOutput struct {
	Entries       []sitemapEntryDTO `json:"entries"`
	Total         int               `json:"total"`
	Limit         int               `json:"limit"`
	Offset        int               `json:"offset"`
	ReturnedCount int               `json:"returned_count"`
	HasMore       bool              `json:"has_more"`
	NextOffset    *int              `json:"next_offset,omitempty"`
}

type getFeedOutput struct {
	Items         []feedItemDTO `json:"items"`
	Total         int           `json:"total"`
	Limit         int           `json:"limit"`
	Offset        int           `json:"offset"`
	ReturnedCount int           `json:"returned_count"`
	HasMore       bool          `json:"has_more"`
	NextOffset    *int          `json:"next_offset,omitempty"`
}

type getSiteInformationOutput struct {
	Site siteInfoDTO `json:"site"`
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
		func(_ context.Context, _ *mcp.CallToolRequest, in listPagesInput) (*mcp.CallToolResult, listPagesOutput, error) {
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
				return nil, listPagesOutput{Pages: []pageDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
			}
			slice := all[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(slice))
			return nil, listPagesOutput{Pages: toPageDTOsEnriched(slice, srcIdx, aliases), Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
		})

	addReadOnlyTool(s, "get_page", "Read page",
		"Read a Hugo page by slug. Returns metadata, rendered HTML, and a short summary. "+
			"By default only published pages are returned. "+
			"Pass allow_source_fallback=true to also return source-index entries for pages not yet built "+
			"(e.g. immediately after create_page, before the next Hugo build); draft pages are always excluded. "+
			"Pass content_only=true to strip navigation, header, and footer from the rendered HTML of published pages "+
			"(source-only fallback normally carries raw Markdown rather than rendered HTML; `lang` and `url` are empty until the page is built; if `content_only=true` is also set, the `html` field is returned empty for source-only fallback results). "+
			"The response includes a `state` object with explicit source/build/public/index visibility hints so agents do not have to infer lifecycle state from empty fields alone. "+
			"For the raw Markdown source, use get_full_page_markdown (requires content.read). "+
			"Does not require authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getPageInput) (*mcp.CallToolResult, getPageOutput, error) {
			if idx == nil && srcIdx == nil {
				return nil, getPageOutput{}, fmt.Errorf("index not initialized")
			}
			if in.Slug == "" {
				return nil, getPageOutput{}, fmt.Errorf("content_not_found: slug must not be empty")
			}
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok {
				return nil, getPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
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
			dto := toResolvedPageDetailDTO(resolved)
			dto.State = site.StateForResolvedPage(resolved, cfg.SiteRoot)
			if in.ContentOnly && resolved.Public != nil {
				dto.HTML = site.ExtractArticleHTML(dto.HTML)
			} else if in.ContentOnly {
				dto.HTML = ""
			}
			return nil, getPageOutput{Page: dto}, nil
		})

	addReadOnlyTool(s, "search_pages", "Search content", "Keyword search across published pages (title, summary, tags, categories, URL). No authentication required. For filtered search with type, language, sort, pagination, or to search source-only content use search_content (requires content.read).",
		func(_ context.Context, _ *mcp.CallToolRequest, in searchPagesInput) (*mcp.CallToolResult, searchPagesOutput, error) {
			if idx == nil {
				return nil, searchPagesOutput{}, fmt.Errorf("index not initialized")
			}
			if in.Query == "" {
				return nil, searchPagesOutput{}, fmt.Errorf("invalid_params: query must not be empty")
			}
			limit := clampLimit(in.Limit, 50, 50)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			all := idx.Search(in.Query, 0)
			total := len(all)
			if offset >= total {
				meta := toolcontract.ComputePagination(total, limit, offset, 0)
				return nil, searchPagesOutput{Pages: []pageDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
			}
			pages := all[offset:]
			if len(pages) > limit {
				pages = pages[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(pages))
			return nil, searchPagesOutput{Pages: toPageDTOsEnriched(pages, srcIdx, aliases), Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
		})

	addReadOnlyTool(s, "get_recent_posts", "Read recent posts", "Return the most recent published posts from the index. Use this for timeline-style summaries without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getRecentPostsInput) (*mcp.CallToolResult, getRecentPostsOutput, error) {
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
				return nil, getRecentPostsOutput{Pages: []pageDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
			}
			pages := all[offset:]
			if len(pages) > limit {
				pages = pages[:limit]
			}
			meta := toolcontract.ComputePagination(total, limit, offset, len(pages))
			return nil, getRecentPostsOutput{Pages: toPageDTOsEnriched(pages, srcIdx, aliases), Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
		})

	addReadOnlyTool(s, "list_tags", "Browse tags", "List the tags discovered from the index. Returns a sorted tag list and does not require authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTagsOutput, error) {
			if idx == nil {
				return nil, listTagsOutput{}, fmt.Errorf("index not initialized")
			}
			tags := idx.AllTags()
			if srcIdx != nil {
				tags = srcIdx.AllTags()
			}
			if tags == nil {
				tags = []string{}
			}
			tags = taxonomy.ApplyAliases(tags, aliases)
			return nil, listTagsOutput{Tags: tags}, nil
		})

	addReadOnlyTool(s, "list_categories", "Browse categories", "List the categories discovered from the index. Returns a sorted category list and does not require authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listCategoriesOutput, error) {
			if idx == nil {
				return nil, listCategoriesOutput{}, fmt.Errorf("index not initialized")
			}
			cats := idx.AllCategories()
			if srcIdx != nil {
				cats = srcIdx.AllCategories()
			}
			if cats == nil {
				cats = []string{}
			}
			cats = taxonomy.ApplyAliases(cats, aliases)
			return nil, listCategoriesOutput{Categories: cats}, nil
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
				return nil, getSitemapOutput{Entries: []sitemapEntryDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
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
			return nil, getSitemapOutput{Entries: entries, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
		})

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
				return nil, getFeedOutput{Items: []feedItemDTO{}, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
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
			return nil, getFeedOutput{Items: items, Total: meta.Total, Limit: meta.Limit, Offset: meta.Offset, ReturnedCount: meta.ReturnedCount, HasMore: meta.HasMore, NextOffset: meta.NextOffset}, nil
		})

	addReadOnlyTool(s, "get_site_information", "Read site metadata", "Return basic metadata for the indexed site, including name, URL, and language. Useful for onboarding and discovery without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getSiteInformationOutput, error) {
			if idx == nil {
				return nil, getSiteInformationOutput{}, fmt.Errorf("index not initialized")
			}
			info := idx.SiteInfo()
			return nil, getSiteInformationOutput{Site: siteInfoDTO{
				Name: info["name"],
				URL:  info["url"],
				Lang: info["lang"],
			}}, nil
		})
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
		Slug:          p.Slug,
		Title:         p.Title,
		Summary:       p.Summary,
		Tags:          tags,
		Categories:    cats,
		TagTerms:      taxonomy.Normalize(tags),
		CategoryTerms: taxonomy.Normalize(cats),
		Date:          p.Date,
		URL:           p.URL,
		Lang:          p.Lang,
		HTML:          p.RawHTML,
	}
}

func toResolvedPageDetailDTO(resolved site.ResolvedPage) pageDetailDTO {
	if resolved.Public != nil {
		page := *resolved.Public
		if resolved.Source != nil {
			page.Tags = resolved.Source.Tags
			page.Categories = resolved.Source.Categories
		}
		dto := toPageDetailDTO(page)
		if resolved.Source != nil {
			dto.ResolvedLang = resolved.Source.Lang
			dto.ResolvedSourcePath = resolved.SourcePath
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
		ResolvedSourcePath: resolved.SourcePath,
		HTML:               src.Body,
	}
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
