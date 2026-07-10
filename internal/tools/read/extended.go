package read

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

const toolResultVersion = "v1.0.0"

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

	Status                  string       `json:"status,omitempty"`
	Score                   int          `json:"score,omitempty"`
	PublishedPages          int          `json:"published_pages,omitempty"`
	SourcePages             int          `json:"source_pages,omitempty"`
	DraftPages              int          `json:"draft_pages,omitempty"`
	Tags                    int          `json:"tags,omitempty"`
	Categories              int          `json:"categories,omitempty"`
	MissingTitles           int          `json:"missing_titles,omitempty"`
	MissingDates            int          `json:"missing_dates,omitempty"`
	ValidationErrors        int          `json:"validation_errors,omitempty"`
	TaxonomyInconsistencies []string     `json:"taxonomy_inconsistencies,omitempty"`
	Sections                []sectionDTO `json:"sections,omitempty"`
	Languages               []string     `json:"languages,omitempty"`
	Summary                 string       `json:"summary,omitempty"`
	RecentPages             []pageDTO    `json:"recent_pages,omitempty"`
	Notes                   []string     `json:"notes,omitempty"`
}

type contentEnvelope struct {
	Success     bool                `json:"success"`
	Version     string              `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Data        contentEnvelopeData `json:"data"`
	Warnings    []string            `json:"warnings"`
	Errors      []string            `json:"errors"`
}

type validateFrontMatterInput struct {
	Slug   string `json:"slug,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
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
	Slug          string              `json:"slug"`
	Title         string              `json:"title"`
	Summary       string              `json:"summary"`
	Tags          []string            `json:"tags"`
	Categories    []string            `json:"categories"`
	TagTerms      []site.TaxonomyTerm `json:"tag_terms,omitempty"`
	CategoryTerms []site.TaxonomyTerm `json:"category_terms,omitempty"`
	Date          string              `json:"date"`
	URL           string              `json:"url"`
	Lang          string              `json:"lang"`
}

type validateOutputData struct {
	PagesChecked int                   `json:"pages_checked"`
	PagesPassed  int                   `json:"pages_passed"`
	Invalid      int                   `json:"invalid"`
	Returned     int                   `json:"returned_count,omitempty"`
	Limit        int                   `json:"limit,omitempty"`
	Offset       int                   `json:"offset,omitempty"`
	Pages        []frontMatterIssueDTO `json:"pages"`
}

type validateOutput struct {
	Success     bool               `json:"success"`
	Version     string             `json:"version"`
	GeneratedAt string             `json:"generated_at"`
	Data        validateOutputData `json:"data"`
	Warnings    []string           `json:"warnings"`
	Errors      []string           `json:"errors"`
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
	Success     bool           `json:"success"`
	Version     string         `json:"version"`
	GeneratedAt string         `json:"generated_at"`
	Data        brokenLinkData `json:"data"`
	Warnings    []string       `json:"warnings"`
	Errors      []string       `json:"errors"`
}

// RegisterWithSourceIndex wires additional read-only tools that benefit from the
// source index. Existing tools remain registered via Register.
func RegisterWithSourceIndex(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	aliases := taxonomy.NormalizeAliasMap(cfg.TaxonomyAliases)

	RegisterDiffPage(s, idx, srcIdx, cfg)

	addReadOnlyTool(s, "search_content", "Search content", "Search Hugo content with filters for type, tag, category, language, pagination, and sort order. Returns page metadata only. Requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, in searchContentInput) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			if t := strings.ToLower(strings.TrimSpace(in.Type)); t != "" && t != "all" && t != "post" && t != "posts" && t != "page" && t != "pages" {
				return nil, contentEnvelope{}, fmt.Errorf("invalid_params: type must be one of: all, post, posts, page, pages (got %q)", in.Type)
			}
			filtered := filterContentPages(idx.Sitemap(), in, aliases)
			total := len(filtered)
			limit := clampLimit(in.Limit, 20, 100)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			pages := sliceContentPages(filtered, offset, limit)
			now := time.Now().UTC().Format(time.RFC3339)
			return nil, contentEnvelope{
				Success:     true,
				Version:     toolResultVersion,
				GeneratedAt: now,
				Data: contentEnvelopeData{
					Pages:    toPageDTOs(pages, aliases),
					Total:    total,
					Limit:    limit,
					Offset:   offset,
					Sort:     effectiveSort(in),
					Order:    canonicalOrder(in.Order),
					Query:    strings.TrimSpace(in.Query),
					Type:     strings.TrimSpace(in.Type),
					Tag:      strings.TrimSpace(in.Tag),
					Category: strings.TrimSpace(in.Category),
					Language: strings.TrimSpace(in.Language),
				},
				Warnings: []string{},
				Errors:   []string{},
			}, nil
		})

	addReadOnlyTool(s, "explain_site_structure", "Explain site structure", "Summarize how the Hugo site is organized, including sections, taxonomies, languages, and recent content. Useful for onboarding or content planning. Requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			contentPages := idx.ContentPages()
			sections := countSections(contentPages)
			languages := uniqueLanguages(contentPages)
			recent := contentPages
			if len(recent) > 5 {
				recent = recent[:5]
			}
			tagCount := len(idx.AllTags())
			catCount := len(idx.AllCategories())
			if srcIdx != nil {
				tagCount = len(srcIdx.AllTags())
				catCount = len(srcIdx.AllCategories())
			}
			summary := fmt.Sprintf("%d published pages across %d sections, %d tags, and %d categories.",
				len(contentPages), len(sections), tagCount, catCount)
			now := time.Now().UTC().Format(time.RFC3339)
			return nil, contentEnvelope{
				Success:     true,
				Version:     toolResultVersion,
				GeneratedAt: now,
				Data: contentEnvelopeData{
					Summary:     summary,
					Sections:    sections,
					Languages:   languages,
					Tags:        tagCount,
					Categories:  catCount,
					RecentPages: toPageDTOs(recent, aliases),
					Notes: []string{
						"Top-level sections are derived from page slugs.",
						"Posts are detected from the /posts/ path prefix.",
					},
				},
				Warnings: []string{},
				Errors:   []string{},
			}, nil
		})

	addReadOnlyTool(s, "get_site_health", "Get site health", "Return a concise health summary for the Hugo site, including content counts, validation signals, and taxonomy inconsistency warnings. Use this before publishing or reviewing content. Requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			health := buildSiteHealth(idx, srcIdx, cfg.TaxonomyAliases)
			now := time.Now().UTC().Format(time.RFC3339)
			return nil, contentEnvelope{
				Success:     true,
				Version:     toolResultVersion,
				GeneratedAt: now,
				Data: contentEnvelopeData{
					Status:                  health.Status,
					Score:                   health.Score,
					PublishedPages:          health.PublishedPages,
					SourcePages:             health.SourcePages,
					DraftPages:              health.DraftPages,
					Tags:                    health.Tags,
					Categories:              health.Categories,
					MissingTitles:           health.MissingTitles,
					MissingDates:            health.MissingDates,
					ValidationErrors:        health.ValidationErrors,
					TaxonomyInconsistencies: health.TaxonomyInconsistencies,
				},
				Warnings: []string{},
				Errors:   []string{},
			}, nil
		})

	addReadOnlyTool(s, "validate_front_matter", "Validate front matter", "Validate Hugo front matter for missing titles, dates, or malformed metadata. Optionally target one slug. Requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, in validateFrontMatterInput) (*mcp.CallToolResult, validateOutput, error) {
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			pages := sourcePagesForValidation(srcIdx, in.Slug)
			return nil, validatePagesWithIssues(pages, in.Offset, in.Limit, cfg.TaxonomyAliases), nil
		})

	addReadOnlyTool(s, "validate_site", "Validate site", "Run a validation pass over all Hugo source pages and report front matter issues. Requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, validateOutput, error) {
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			pages := srcIdx.ListPages(0, 0)
			return nil, validatePagesWithIssues(pages, 0, 0, cfg.TaxonomyAliases), nil
		})

	addReadOnlyTool(s, "get_broken_links", "Get broken links", "Audit internal links against the current Hugo index without making any external network calls. Returns a limited sample of missing internal targets and requires content.read.",
		func(_ context.Context, _ *mcp.CallToolRequest, in brokenLinkInput) (*mcp.CallToolResult, brokenLinkOutput, error) {
			if idx == nil {
				return nil, brokenLinkOutput{}, fmt.Errorf("index not initialized")
			}
			issues := collectBrokenLinks(idx)
			limit := clampLimit(in.Limit, 25, 100)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			return nil, brokenLinkOutput{
				Success:     true,
				Version:     toolResultVersion,
				GeneratedAt: time.Now().UTC().Format(time.RFC3339),
				Data: brokenLinkData{
					TotalPages:  len(idx.Sitemap()),
					BrokenLinks: len(issues),
					Limit:       limit,
					Offset:      offset,
					Links:       sliceBrokenLinks(issues, offset, limit),
				},
				Warnings: []string{},
				Errors:   []string{},
			}, nil
		})
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
		base, err := url.Parse(page.URL)
		if err != nil {
			continue
		}
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
	total := len(pages)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = total
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

	results := allResults
	if offset < len(results) {
		results = results[offset:]
	} else {
		results = []frontMatterIssueDTO{}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return validateOutput{
		Success:     true,
		Version:     toolResultVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Data: validateOutputData{
			PagesChecked: total,
			PagesPassed:  total - invalid,
			Invalid:      invalid,
			Returned:     len(results),
			Limit:        limit,
			Offset:       offset,
			Pages:        results,
		},
		Warnings: []string{},
		Errors:   []string{},
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

func sourcePagesForValidation(idx *hugosite.SourceIndex, slug string) []hugosite.SourcePage {
	if idx == nil {
		return nil
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return idx.ListPages(0, 0)
	}
	if p, ok := idx.GetBySlug(slug); ok {
		return []hugosite.SourcePage{*p}
	}
	return []hugosite.SourcePage{}
}

func buildSiteHealth(idx *site.Index, srcIdx *hugosite.SourceIndex, aliases map[string]string) contentEnvelopeData {
	health := contentEnvelopeData{
		Status: "healthy",
	}
	if idx != nil {
		health.PublishedPages = len(idx.ContentPages())
		health.Tags = len(idx.AllTags())
		health.Categories = len(idx.AllCategories())
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
		health.TaxonomyInconsistencies = detectTaxonomyInconsistencies(srcIdx, aliases)
	}
	penalty := (health.ValidationErrors * 10) + (health.MissingTitles * 5) + (health.MissingDates * 5)
	score := 100 - penalty
	if score < 0 {
		score = 0
	}
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
// transliterations and flags alias-key terms that should use their canonical form.
func detectTaxonomyInconsistencies(srcIdx *hugosite.SourceIndex, aliases map[string]string) []string {
	if srcIdx == nil {
		return nil
	}
	var out []string

	// Report alias mismatches: terms in content that should use the canonical form.
	tagSlugs := make([]string, 0)
	for _, raw := range srcIdx.AllTags() {
		s := taxonomy.Slug(raw)
		if canonical, ok := aliases[s]; ok {
			out = append(out, fmt.Sprintf("tag %q is an alias for %q; use the canonical form", raw, canonical))
		}
		tagSlugs = append(tagSlugs, s)
	}
	catSlugs := make([]string, 0)
	for _, raw := range srcIdx.AllCategories() {
		s := taxonomy.Slug(raw)
		if canonical, ok := aliases[s]; ok {
			out = append(out, fmt.Sprintf("category %q is an alias for %q; use the canonical form", raw, canonical))
		}
		catSlugs = append(catSlugs, s)
	}

	// Report similar slug pairs (possible duplicates / cross-language variants).
	const maxDist, minLen = 2, 5
	for _, pair := range taxonomy.FindSimilarPairs(tagSlugs, maxDist, minLen, aliases) {
		out = append(out, fmt.Sprintf("tags %q and %q may be duplicates (edit distance ≤ %d)", pair[0], pair[1], maxDist))
	}
	for _, pair := range taxonomy.FindSimilarPairs(catSlugs, maxDist, minLen, aliases) {
		out = append(out, fmt.Sprintf("categories %q and %q may be duplicates (edit distance ≤ %d)", pair[0], pair[1], maxDist))
	}

	return out
}

func countSections(pages []site.Page) []sectionDTO {
	counts := map[string]int{}
	classifier := site.NewClassifierFromPages(pages)
	for _, p := range pages {
		if !classifier.IsContent(p) {
			continue
		}
		seg := topSection(p.Slug)
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

func topSection(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" || slug == "/" {
		return "root"
	}
	trimmed := strings.TrimPrefix(slug, "/")
	parts := strings.Split(trimmed, "/")
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

func toPageDTO(p site.Page, aliases map[string]string) pageDTO {
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
	}
}

func toPageDTOs(pages []site.Page, aliases map[string]string) []pageDTO {
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		out[i] = toPageDTO(p, aliases)
	}
	return out
}
