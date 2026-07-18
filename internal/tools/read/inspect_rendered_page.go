package read

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

type inspectRenderedPageInput struct {
	Slug string `json:"slug"`
	// IncludePreview opts into the pre-publish preview facet (#435): a
	// diff summary, this page's broken-link count, and front-matter
	// validity, composed from diff_page/get_broken_links/
	// validate_frontmatter's own logic rather than a new preview_page
	// tool. Off by default — every existing caller's cost is unchanged.
	IncludePreview bool `json:"include_preview,omitempty"`
}

// renderCheckResult is one structured render check, e.g. "title" or
// "internal_links". Status is one of "pass", "warn", "fail". Callers should
// treat "fail" as something to act on, "warn" as advisory, and "pass" as
// clean.
type renderCheckResult struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// previewDTO is the opt-in include_preview facet (#435): a pre-publish
// summary combining diff_page's git-diff status, get_broken_links'
// per-page scan, and validate_frontmatter's per-page checks into one
// risks list, so an agent doesn't have to chain three separate calls
// before publishing. Advisory only, same posture as get_broken_links —
// never blocks anything.
type previewDTO struct {
	DiffStatus        string   `json:"diff_status"`
	DiffSummary       string   `json:"diff_summary"`
	BrokenLinksCount  int      `json:"broken_links_count"`
	FrontmatterValid  bool     `json:"frontmatter_valid"`
	FrontmatterIssues []string `json:"frontmatter_issues,omitempty"`
	Risks             []string `json:"risks"`
}

type inspectRenderedPageData struct {
	Slug       string              `json:"slug"`
	URL        string              `json:"url"`
	Lang       string              `json:"lang"`
	OutputPath string              `json:"output_path"`
	State      site.LifecycleState `json:"state"`
	Status     string              `json:"status"`
	Checks     []renderCheckResult `json:"checks"`
	// Preview is populated only when include_preview=true is requested.
	Preview *previewDTO `json:"preview,omitempty"`
}

type inspectRenderedPageOutput struct {
	toolcontract.ToolResponse[inspectRenderedPageData]
}

func newInspectRenderedPageOutput(data inspectRenderedPageData, now time.Time) inspectRenderedPageOutput {
	return inspectRenderedPageOutput{ToolResponse: successEnvelope(data, now)}
}

// hugoRenderErrorRe matches known Hugo render/shortcode failure signatures
// that Hugo itself embeds directly into the output HTML rather than failing
// the build (e.g. a shortcode error inside a "warn" ErrorLevel build, or
// `--renderToMemory` continuing past a broken partial). It is deliberately
// narrow: it does not flag literal "{{ }}" template syntax that a page might
// display as example content (e.g. a blog post about Hugo templating),
// because that would be a false positive on legitimate content.
var hugoRenderErrorRe = regexp.MustCompile(`(?i)error calling |failed to render|html/template:|text/template:|shortcode "[^"]*" not found|partial "[^"]*" not found`)

// RegisterInspectRenderedPage registers inspect_rendered on s. It is
// called from RegisterWithSourceIndex alongside the other content.read tools
// that need both the public site index and the source index.
func RegisterInspectRenderedPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "inspect_rendered", "Inspect rendered page", "Validate the rendered HTML/SEO/link surface of a single page from the current public build output: title, meta description, canonical URL, hreflang alternates, internal links, missing images, and heuristic shortcode/render-error markers. Complements validate_frontmatter (source-only) and get_broken_links (site-wide). The `hreflang` check parses the rendered <link> tags directly, independent of attribute order/case, and flags a tag with an empty href as incomplete rather than accepting it; a `warn` here can still mean the page genuinely has no translations, or that the Hugo theme's template doesn't emit hreflang tags for translated pages at all — confirm the page-bundle actually has a translated sibling before treating a warn as a theme bug. Set `include_preview=true` for a combined pre-publish summary (`preview.diff_status`/`diff_summary` from the current git diff, `preview.broken_links_count` scoped to this page, `preview.frontmatter_valid`/`frontmatter_issues`, and an overall `preview.risks` list) instead of chaining diff_page + get_broken_links + validate_frontmatter separately — composes their existing logic, doesn't duplicate it; off by default, so this costs nothing unless requested (#435). Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in inspectRenderedPageInput) (*mcp.CallToolResult, inspectRenderedPageOutput, error) {
			if idx == nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("index not initialized")
			}
			slug := strings.TrimSpace(in.Slug)
			if slug == "" {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}

			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.Resolve(slug)
			if !ok || resolved.Public == nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("content_not_found: no rendered public page found for slug %q", slug)
			}
			page := *resolved.Public

			if strings.TrimSpace(cfg.SiteRoot) == "" || strings.TrimSpace(page.OutputPath) == "" {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("render_output_unavailable: rendered HTML file location is unknown for slug %q", slug)
			}
			fullPath := filepath.Join(cfg.SiteRoot, filepath.FromSlash(page.OutputPath))
			raw, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("render_output_unavailable: %v", err)
			}
			doc, err := html.Parse(bytes.NewReader(raw))
			if err != nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("render_output_unavailable: rendered HTML could not be parsed: %v", err)
			}

			checks := []renderCheckResult{
				checkTitle(doc),
				checkMetaDescription(doc),
				checkCanonical(doc, cfg.SiteURL, page.Slug),
				checkHreflang(doc, idx, page),
				checkInternalLinks(idx, page, doc),
				checkMissingImages(cfg, page, doc),
				checkRenderErrors(raw),
			}

			state := site.LifecycleState{}
			if srcIdx != nil {
				state = resolvedState(resolved, cfg.SiteRoot)
			}

			overall := "ok"
			for _, c := range checks {
				if c.Status == "fail" {
					overall = "issues_found"
					break
				}
				if c.Status == "warn" && overall == "ok" {
					overall = "warnings_found"
				}
			}

			data := inspectRenderedPageData{
				Slug:       page.Slug,
				URL:        page.URL,
				Lang:       page.Lang,
				OutputPath: page.OutputPath,
				State:      state,
				Status:     overall,
				Checks:     checks,
			}
			if in.IncludePreview {
				preview := computePreview(ctx, idx, cfg, resolved, page, doc)
				data.Preview = &preview
			}
			return nil, newInspectRenderedPageOutput(data, time.Now().UTC()), nil
		})
}

// computePreview builds inspect_rendered's opt-in include_preview facet
// (#435) by composing diff_page's git-diff logic, the same
// brokenInternalLinksFromDoc scan checkInternalLinks already ran against
// this same freshly-parsed doc (not brokenLinksForPage's cached, possibly
// stale RawHTML — using a different source here would let this response
// contradict its own checks[].internal_links result under build drift),
// and validate_frontmatter's per-page checks (validateFrontMatterPage) —
// rather than re-deriving any of their logic. Advisory only: never fails
// the call, never blocks a mutation.
func computePreview(ctx context.Context, idx *site.Index, cfg config.Config, resolved site.ResolvedPage, page site.Page, doc *html.Node) previewDTO {
	preview := previewDTO{Risks: []string{}}

	diffStatus, diffSummary := computeDiffPreviewSummary(ctx, resolved, cfg)
	preview.DiffStatus = diffStatus
	preview.DiffSummary = diffSummary
	if diffStatus == "modified" {
		preview.Risks = append(preview.Risks, "uncommitted source changes: "+diffSummary)
	}

	broken, _ := brokenInternalLinksFromDoc(idx, page, doc)
	preview.BrokenLinksCount = len(broken)
	if len(broken) > 0 {
		preview.Risks = append(preview.Risks, fmt.Sprintf("%d broken internal link(s) on this page", len(broken)))
	}

	preview.FrontmatterValid = true
	if resolved.Source != nil {
		aliases := taxonomy.NormalizeAliasMap(cfg.TaxonomyAliases)
		issues := validateFrontMatterPage(*resolved.Source, aliases)
		if len(issues) > 0 {
			preview.FrontmatterValid = false
			preview.FrontmatterIssues = issues
			preview.Risks = append(preview.Risks, fmt.Sprintf("%d front-matter issue(s)", len(issues)))
		}
	}

	return preview
}

// computeDiffPreviewSummary mirrors diff_page's own git-diff resolution
// (findGitRoot/gitShowFile/diffStatus/unifiedDiff) but returns a compact
// line-count summary instead of the full diff text — preview only needs
// enough to flag "this page has uncommitted changes," not the diff itself.
func computeDiffPreviewSummary(ctx context.Context, resolved site.ResolvedPage, cfg config.Config) (status, summary string) {
	contentRoot := strings.TrimSpace(cfg.ContentRoot)
	if resolved.Source == nil || contentRoot == "" || cfg.GitBaseline.Mode == "disabled" {
		return "git_unavailable", "diff unavailable"
	}
	gitRoot, err := findGitRoot(ctx, contentRoot)
	if err != nil {
		return "git_unavailable", "diff unavailable: git repository not found"
	}
	absPath := resolved.SourcePath
	if absPath == "" {
		return "git_unavailable", "diff unavailable"
	}
	relRepoPath, err := filepath.Rel(gitRoot, absPath)
	if err != nil || strings.HasPrefix(relRepoPath, "..") {
		return "git_unavailable", "diff unavailable: source page is outside the repository root"
	}
	baseContent, baseExists, err := gitShowFile(ctx, gitRoot, relRepoPath)
	if err != nil && !isGitPathMissing(err) {
		return "git_unavailable", "diff unavailable"
	}
	if !baseExists {
		baseContent = nil
	}
	currentContent, err := os.ReadFile(absPath)
	if err != nil {
		return "git_unavailable", "diff unavailable"
	}
	dStatus := diffStatus(baseExists, currentContent, baseContent)
	if dStatus == "git_untracked" {
		return dStatus, "file is new and not yet tracked by git"
	}
	if dStatus == "unchanged" {
		return dStatus, "no uncommitted changes"
	}
	diffText, err := unifiedDiff(relRepoPath, baseContent, currentContent)
	if err != nil {
		return "git_unavailable", "diff unavailable"
	}
	added, removed := countDiffLines(diffText)
	return dStatus, fmt.Sprintf("%d line(s) added, %d removed", added, removed)
}

// countDiffLines counts +/- content lines in a unified diff, excluding the
// +++/--- file-header lines (which also start with +/-).
func countDiffLines(diffText string) (added, removed int) {
	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}

func checkTitle(doc *html.Node) renderCheckResult {
	title := strings.TrimSpace(firstElementText(doc, "title"))
	if title == "" {
		return renderCheckResult{Check: "title", Status: "fail", Detail: "no <title> element found in rendered output"}
	}
	if len(title) > 70 {
		return renderCheckResult{Check: "title", Status: "warn", Detail: "title is " + strconv.Itoa(len(title)) + " characters, longer than the ~70 character SEO guideline"}
	}
	return renderCheckResult{Check: "title", Status: "pass"}
}

func checkMetaDescription(doc *html.Node) renderCheckResult {
	desc := ""
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "meta" && strings.EqualFold(htmlAttr(n, "name"), "description") {
			desc = strings.TrimSpace(htmlAttr(n, "content"))
			return false
		}
		return true
	})
	if desc == "" {
		return renderCheckResult{Check: "meta_description", Status: "fail", Detail: "no <meta name=\"description\"> found in rendered output"}
	}
	if len(desc) > 160 {
		return renderCheckResult{Check: "meta_description", Status: "warn", Detail: "meta description is " + strconv.Itoa(len(desc)) + " characters, longer than the ~160 character SEO guideline"}
	}
	return renderCheckResult{Check: "meta_description", Status: "pass"}
}

// checkCanonical compares the rendered <link rel="canonical"> against an
// expected self-URL built independently from cfg.SiteURL + the page's own
// slug — not against page.URL, which the index copies straight out of the
// same canonical tag during indexing (comparing against that would be
// comparing the tag to itself and could never detect a real mismatch).
func checkCanonical(doc *html.Node, siteURL, slug string) renderCheckResult {
	canonical := ""
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "link" && strings.Contains(strings.ToLower(htmlAttr(n, "rel")), "canonical") {
			canonical = strings.TrimSpace(htmlAttr(n, "href"))
			return false
		}
		return true
	})
	if canonical == "" {
		return renderCheckResult{Check: "canonical", Status: "fail", Detail: "no <link rel=\"canonical\"> found in rendered output"}
	}
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if siteURL == "" {
		return renderCheckResult{Check: "canonical", Status: "pass"}
	}
	expected := siteURL + slug
	if canonical != expected {
		return renderCheckResult{Check: "canonical", Status: "warn", Detail: fmt.Sprintf("canonical %q does not match the page's own URL %q", canonical, expected)}
	}
	return renderCheckResult{Check: "canonical", Status: "pass"}
}

// checkHreflang only asserts that hreflang alternates exist when the site is
// genuinely multilingual (more than one language present across the public
// index). It does not attempt to verify that every language variant is
// individually cross-linked — that requires bundle-relation data this tool
// does not have — so a "pass" here means "hreflang present", not "hreflang
// complete."
//
// Detection walks the parsed DOM (*html.Node from golang.org/x/net/html),
// not raw HTML text, so it is inherently immune to attribute order and to
// attribute-*name* case (the tokenizer already lowercases element/attribute
// names per the HTML spec, and htmlAttr compares case-insensitively too);
// rel is compared case-insensitively as a substring since Hugo/theme output
// may combine it with other rel values (e.g. rel="alternate stylesheet").
// #420 added the href-non-empty check: a <link rel="alternate" hreflang="fr">
// with no href is a broken/incomplete tag, not a valid alternate, and must
// not be silently accepted as "found".
//
// A "warn" here can mean one of three different things this tool cannot
// distinguish without operator confirmation: (1) a real parser miss (should
// no longer happen after #420's DOM-based, order/case-independent check),
// (2) this specific page genuinely has no translations even though other
// site content does, or (3) the Hugo theme's <head> template simply never
// emits hreflang tags for translated pages — a theme-side gap, not a bug in
// this server. Confirm the page-bundle actually has a translated sibling
// before treating a "warn" as a theme bug report.
func checkHreflang(doc *html.Node, idx *site.Index, page site.Page) renderCheckResult {
	if !siteIsMultilingual(idx) {
		return renderCheckResult{Check: "hreflang", Status: "pass", Detail: "single-language site, hreflang not applicable"}
	}
	foundComplete, foundIncomplete := false, false
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "link" &&
			strings.Contains(strings.ToLower(htmlAttr(n, "rel")), "alternate") &&
			strings.TrimSpace(htmlAttr(n, "hreflang")) != "" {
			if strings.TrimSpace(htmlAttr(n, "href")) != "" {
				foundComplete = true
				return false
			}
			foundIncomplete = true
		}
		return true
	})
	if foundComplete {
		return renderCheckResult{Check: "hreflang", Status: "pass"}
	}
	if foundIncomplete {
		return renderCheckResult{Check: "hreflang", Status: "warn", Detail: "found <link rel=\"alternate\" hreflang=...> for this page but its href is empty"}
	}
	return renderCheckResult{Check: "hreflang", Status: "warn", Detail: "site has multiple languages but no <link rel=\"alternate\" hreflang=...> found for this page"}
}

func siteIsMultilingual(idx *site.Index) bool {
	if idx == nil {
		return false
	}
	langs := map[string]struct{}{}
	for _, p := range idx.ContentPages() {
		if p.Lang != "" {
			langs[p.Lang] = struct{}{}
		}
		if len(langs) > 1 {
			return true
		}
	}
	return false
}

// brokenInternalLinksFromDoc walks the freshly-parsed doc (not the cached
// page.RawHTML from idx, which was read at server-start and can be stale
// relative to the current on-disk build) so callers validate the current
// rendered output. Shared by checkInternalLinks and computePreview (#435)
// so the two can never disagree about how many broken links this page has
// — using brokenLinksForPage's stale RawHTML for one and this for the
// other would let a single inspect_rendered response contradict itself
// under build drift, exactly the condition this tool exists to catch.
func brokenInternalLinksFromDoc(idx *site.Index, page site.Page, doc *html.Node) ([]string, error) {
	base, err := url.Parse(page.URL)
	if err != nil {
		return nil, err
	}
	classifier := site.NewClassifier(idx)
	var broken []string
	for _, href := range extractLinksFromDoc(doc) {
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
		broken = append(broken, href)
	}
	return broken, nil
}

func checkInternalLinks(idx *site.Index, page site.Page, doc *html.Node) renderCheckResult {
	broken, err := brokenInternalLinksFromDoc(idx, page, doc)
	if err != nil {
		return renderCheckResult{Check: "internal_links", Status: "warn", Detail: "page URL could not be parsed, skipped internal link check"}
	}
	if len(broken) > 0 {
		sample := broken
		if len(sample) > 5 {
			sample = sample[:5]
		}
		return renderCheckResult{Check: "internal_links", Status: "fail", Detail: fmt.Sprintf("%d broken internal link(s), e.g. %s", len(broken), strings.Join(sample, ", "))}
	}
	return renderCheckResult{Check: "internal_links", Status: "pass"}
}

func checkMissingImages(cfg config.Config, page site.Page, doc *html.Node) renderCheckResult {
	base, err := url.Parse(page.URL)
	if err != nil {
		return renderCheckResult{Check: "missing_images", Status: "warn", Detail: "page URL could not be parsed, skipped image check"}
	}
	var missing []string
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			src := strings.TrimSpace(htmlAttr(n, "src"))
			if src == "" {
				return true
			}
			target, ok := resolveInternalLink(base, src)
			if !ok {
				return true
			}
			localPath := filepath.Join(cfg.SiteRoot, filepath.FromSlash(strings.TrimPrefix(target.Path, "/")))
			if _, statErr := os.Stat(localPath); statErr != nil {
				missing = append(missing, src)
			}
		}
		return true
	})
	if len(missing) > 0 {
		sample := missing
		if len(sample) > 5 {
			sample = sample[:5]
		}
		return renderCheckResult{Check: "missing_images", Status: "fail", Detail: fmt.Sprintf("%d missing local image(s), e.g. %s", len(missing), strings.Join(sample, ", "))}
	}
	return renderCheckResult{Check: "missing_images", Status: "pass"}
}

func checkRenderErrors(raw []byte) renderCheckResult {
	if hugoRenderErrorRe.Match(raw) {
		return renderCheckResult{Check: "render_errors", Status: "fail", Detail: "rendered output contains a Hugo shortcode/template error marker"}
	}
	return renderCheckResult{Check: "render_errors", Status: "pass"}
}

// walkNodes runs visit over doc and every descendant in document order,
// depth-first. visit returns false to stop the walk early (e.g. once a
// unique element has been found).
func walkNodes(doc *html.Node, visit func(*html.Node) bool) {
	if doc == nil {
		return
	}
	var stop bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || stop {
			return
		}
		if !visit(n) {
			stop = true
			return
		}
		for c := n.FirstChild; c != nil && !stop; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// extractLinksFromDoc collects every <a href> in doc, in document order.
// Mirrors extended.go's extractLinks (which operates on an HTML string from
// the cached index), but works directly off an already-parsed *html.Node so
// callers reading a fresh file don't need to re-serialize it back to a
// string first.
func extractLinksFromDoc(doc *html.Node) []string {
	var links []string
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "a" {
			if href := strings.TrimSpace(htmlAttr(n, "href")); href != "" {
				links = append(links, href)
			}
		}
		return true
	})
	return links
}

func firstElementText(doc *html.Node, tag string) string {
	text := ""
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == tag {
			text = renderTextContent(n)
			return false
		}
		return true
	})
	return text
}

// renderTextContent concatenates all descendant text nodes of n, space
// separated. Mirrors internal/site's unexported textContent helper; kept
// separate since that one isn't exported across packages.
func renderTextContent(n *html.Node) string {
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil {
			return
		}
		if cur.Type == html.TextNode {
			buf.WriteString(cur.Data)
			buf.WriteByte(' ')
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(buf.String())
}
