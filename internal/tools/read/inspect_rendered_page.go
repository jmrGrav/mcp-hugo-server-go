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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

type inspectRenderedPageInput struct {
	Slug string `json:"slug"`
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

type inspectRenderedPageData struct {
	Slug       string              `json:"slug"`
	URL        string              `json:"url"`
	Lang       string              `json:"lang"`
	OutputPath string              `json:"output_path"`
	State      site.LifecycleState `json:"state"`
	Status     string              `json:"status"`
	Checks     []renderCheckResult `json:"checks"`
}

type inspectRenderedPageOutput struct {
	toolcontract.ToolResponse[inspectRenderedPageData]
	Slug   string              `json:"slug"`
	URL    string              `json:"url"`
	Status string              `json:"status"`
	Checks []renderCheckResult `json:"checks"`
}

func newInspectRenderedPageOutput(data inspectRenderedPageData, now time.Time) inspectRenderedPageOutput {
	return inspectRenderedPageOutput{
		ToolResponse: successEnvelope(data, now),
		Slug:         data.Slug,
		URL:          data.URL,
		Status:       data.Status,
		Checks:       data.Checks,
	}
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
	addReadOnlyTool(s, "inspect_rendered", "Inspect rendered page", "Validate the rendered HTML/SEO/link surface of a single page from the current public build output: title, meta description, canonical URL, hreflang alternates, internal links, missing images, and heuristic shortcode/render-error markers. Complements validate_frontmatter (source-only) and get_broken_links (site-wide). The `hreflang` check parses the rendered <link> tags directly, independent of attribute order/case, and flags a tag with an empty href as incomplete rather than accepting it; a `warn` here can still mean the page genuinely has no translations, or that the Hugo theme's template doesn't emit hreflang tags for translated pages at all — confirm the page-bundle actually has a translated sibling before treating a warn as a theme bug. Requires content.read.",
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
			return nil, newInspectRenderedPageOutput(data, time.Now().UTC()), nil
		})
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

// checkInternalLinks walks the freshly-parsed doc (not the cached
// page.RawHTML from idx, which was read at server-start and can be stale
// relative to the current on-disk build) so this check validates the
// current rendered output, matching the rest of these checks.
func checkInternalLinks(idx *site.Index, page site.Page, doc *html.Node) renderCheckResult {
	base, err := url.Parse(page.URL)
	if err != nil {
		return renderCheckResult{Check: "internal_links", Status: "warn", Detail: "page URL could not be parsed, skipped internal link check"}
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
