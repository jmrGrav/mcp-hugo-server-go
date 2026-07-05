package anonymous

import (
	"context"
	"fmt"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listPagesInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type getPageInput struct {
	Slug string `json:"slug"`
}

type searchPagesInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type getRecentPostsInput struct {
	Limit int `json:"limit,omitempty"`
}

type getFeedInput struct {
	Limit int `json:"limit,omitempty"`
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
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
	Date       string   `json:"date"`
	URL        string   `json:"url"`
	Lang       string   `json:"lang"`
	HTML       string   `json:"html"`
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
	Pages []pageDTO `json:"pages"`
}

type getPageOutput struct {
	Page pageDetailDTO `json:"page"`
}

type searchPagesOutput struct {
	Pages []pageDTO `json:"pages"`
}

type getRecentPostsOutput struct {
	Pages []pageDTO `json:"pages"`
}

type listTagsOutput struct {
	Tags []string `json:"tags"`
}

type listCategoriesOutput struct {
	Categories []string `json:"categories"`
}

type getSitemapOutput struct {
	Entries []sitemapEntryDTO `json:"entries"`
}

type getFeedOutput struct {
	Items []feedItemDTO `json:"items"`
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
	addReadOnlyTool(s, "list_pages", "Browse pages", "Browse published Hugo pages with pagination. Returns page metadata only and does not require authentication.",
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
			if offset >= len(all) {
				return nil, listPagesOutput{Pages: []pageDTO{}}, nil
			}
			slice := all[offset:]
			if len(slice) > limit {
				slice = slice[:limit]
			}
			return nil, listPagesOutput{Pages: toPageDTOs(slice)}, nil
		})

	addReadOnlyTool(s, "get_page", "Read page", "Read a published Hugo page by slug. Returns metadata, rendered HTML, and a short summary. Does not require authentication.",
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
			return nil, getPageOutput{Page: toResolvedPageDetailDTO(resolved)}, nil
		})

	addReadOnlyTool(s, "search_pages", "Search content", "Search the published index by title, summary, tags, categories, and URL. Use this for simple keyword lookup without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in searchPagesInput) (*mcp.CallToolResult, searchPagesOutput, error) {
			if idx == nil {
				return nil, searchPagesOutput{}, fmt.Errorf("index not initialized")
			}
			if in.Query == "" {
				return nil, searchPagesOutput{}, fmt.Errorf("invalid_params: query must not be empty")
			}
			limit := clampLimit(in.Limit, 50, 50)
			pages := idx.Search(in.Query, limit)
			return nil, searchPagesOutput{Pages: toPageDTOs(pages)}, nil
		})

	addReadOnlyTool(s, "get_recent_posts", "Read recent posts", "Return the most recent published posts from the index. Use this for timeline-style summaries without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getRecentPostsInput) (*mcp.CallToolResult, getRecentPostsOutput, error) {
			if idx == nil {
				return nil, getRecentPostsOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 10, 50)
			pages := idx.RecentPosts(limit)
			return nil, getRecentPostsOutput{Pages: toPageDTOs(pages)}, nil
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
			return nil, listCategoriesOutput{Categories: cats}, nil
		})

	addReadOnlyTool(s, "get_sitemap", "Read sitemap", "Return the published sitemap with URL and publication date. Useful for site-wide discovery without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getSitemapOutput, error) {
			if idx == nil {
				return nil, getSitemapOutput{}, fmt.Errorf("index not initialized")
			}
			all := idx.Sitemap()
			entries := make([]sitemapEntryDTO, len(all))
			for i, p := range all {
				entries[i] = sitemapEntryDTO{Slug: p.Slug, URL: p.URL, Date: p.Date}
			}
			return nil, getSitemapOutput{Entries: entries}, nil
		})

	addReadOnlyTool(s, "get_feed", "Read feed", "Return recent published items as a feed-like list. Use this for lightweight content digests without authentication.",
		func(_ context.Context, _ *mcp.CallToolRequest, in getFeedInput) (*mcp.CallToolResult, getFeedOutput, error) {
			if idx == nil {
				return nil, getFeedOutput{}, fmt.Errorf("index not initialized")
			}
			limit := clampLimit(in.Limit, 20, 50)
			pages := idx.GetFeed(limit)
			items := make([]feedItemDTO, len(pages))
			for i, p := range pages {
				items[i] = feedItemDTO{Slug: p.Slug, Title: p.Title, Summary: p.Summary, Date: p.Date, URL: p.URL}
			}
			return nil, getFeedOutput{Items: items}, nil
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
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, handler)
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

func toPageDTOs(pages []site.Page) []pageDTO {
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		out[i] = toPageDTO(p)
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
		Slug:       p.Slug,
		Title:      p.Title,
		Summary:    p.Summary,
		Tags:       tags,
		Categories: cats,
		Date:       p.Date,
		URL:        p.URL,
		Lang:       p.Lang,
		HTML:       p.RawHTML,
	}
}

func toResolvedPageDetailDTO(resolved site.ResolvedPage) pageDetailDTO {
	if resolved.Public != nil {
		page := *resolved.Public
		if resolved.Source != nil {
			page.Tags = resolved.Source.Tags
			page.Categories = resolved.Source.Categories
		}
		return toPageDetailDTO(page)
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
		Slug:       "/" + src.Slug + "/",
		Title:      src.Title,
		Tags:       tags,
		Categories: cats,
		Date:       src.Date,
		HTML:       src.Body,
	}
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
