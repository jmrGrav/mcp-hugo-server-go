package read

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/html"
)

// misplacedFrontmatterRE deliberately has no (?m) flag: ^ must anchor only
// at the true start of the (trimmed) body, not at every line start, or a
// legitimate mid-article line like "tags: this is prose about frontmatter
// syntax" would false-positive far from the actual beginning (#1004 asks
// for detection "at the beginning of Markdown bodies" specifically).
var misplacedFrontmatterRE = regexp.MustCompile(`^\s*(?:aliases|title|draft|tags|categories|date|description):\s*(?:\n\s*-\s+|\S)`)

type searchContentInput struct {
	Query        string `json:"query,omitempty"`
	Type         string `json:"type,omitempty"`
	Tag          string `json:"tag,omitempty"`
	Category     string `json:"category,omitempty"`
	Language     string `json:"language,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Sort         string `json:"sort,omitempty"`
	Order        string `json:"order,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	IncludeTerms *bool  `json:"include_terms,omitempty"`
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
	// a typo or unintentional variant), "translation_pair" (the two
	// terms are used on exactly the same set of page-bundle slugs, just in
	// different languages — this is the site's own localization, not a
	// content inconsistency, and does not need an alias entry to resolve),
	// or "casing_variant" (the exact same term, spelled with different
	// casing, both used within the same language — e.g. "Infrastructure"
	// and "infrastructure" — a blind spot possible_duplicate/
	// translation_pair never covered, since they compare distinct slugs and
	// casing already collapses to one slug before either ever runs, #577).
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
	// TitleShape (#1105) catches a title that is structurally wrong in a way
	// front matter presence checks never would: a title field that is
	// non-empty (so it passes the pre-existing "missing title" check) but is
	// actually a raw URL, most often the page's own canonical URL written
	// into the title field by a corrupted render or copy/paste
	// (see #1099's grav-csp-nonce EN incident, where get_site_health kept
	// reporting healthy/100 through exactly this). Unlike taxonomy, this
	// category carries real weight — a URL-shaped title is a content defect
	// serious enough that "healthy" must not be reported while it's present.
	TitleShape scoreCategoryDTO `json:"title_shape"`
	// BrokenLinks (#1105) mirrors TitleShape's pattern exactly: weight 0 (never
	// moves the weighted score), but a nonzero count still forces
	// status/content_status off healthy/healthy_with_advisories and caps score
	// at 99. A pointer, unlike the other two categories, because it is only
	// ever computed when db_path is configured — get_broken_links's own O(1)
	// pre-computed link graph (siteDB.GetBrokenLinks()). Without db_path, the
	// only way to get this count is the same full-HTML-rescan
	// collectBrokenLinks pays on every get_broken_links call; running that
	// unconditionally on every get_site_health call (a tool meant to be cheap
	// enough to call before every publish) would impose that cost on every
	// reader-profile deployment, not just db_path-configured ones. Nil (the
	// whole score_breakdown.broken_links key omitted) means "not computed",
	// never a false "0" that would misrepresent an unchecked site as clean.
	BrokenLinks *scoreCategoryDTO `json:"broken_links,omitempty"`
}

// renderedSEOCoverageDTO (#1136) is a deliberate, always-present admission
// rather than a silently misleading gap: every score_breakdown category
// above is source-document or link-graph based (frontmatter, taxonomy,
// title_shape, broken_links) and none of them inspect rendered HTML output
// at all — a missing <title> tag, canonical, meta description, hreflang
// alternate, or an unsafe/leaked URL in the actually-published bytes can
// coexist with score:100 here, because nothing in this tool ever looks at
// that surface. inspect_rendered is the authoritative per-page check for
// exactly those findings.
//
// Aggregating inspect_rendered's checks into this score was evaluated for
// #1136 and declined, for a reason specific to this codebase, not a general
// cost excuse: a live full-site scan on every get_site_health call would
// make a tool meant to be "cheap enough to call before every publish" slow
// on any site of real size, and the alternative — a build-time cache
// mirroring broken_links_count's own pattern — would go stale exactly when
// it matters most. hashPublicPage (internal/db/db.go) hashes only each
// page's own title/summary/date/lang/URL/tags/categories/body; it has no
// input derived from the theme or shared templates. A regression in a
// site's head.html that drops the canonical tag from every page would
// change zero page hashes, so a cache built on that hash would report every
// page unchanged and never re-check it — silently green through the exact
// failure class #1136 was filed over (site-wide template regression, not a
// single bad page). Until that staleness gap has its own fix, per-page
// rendered/SEO checking stays exclusively on inspect_rendered.
type renderedSEOCoverageDTO struct {
	Aggregated        bool   `json:"aggregated"`
	Reason            string `json:"reason"`
	AuthoritativeTool string `json:"authoritative_tool"`
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
	// ContentStatus preserves the source/front-matter health classification
	// when the top-level status is degraded by an operational publication
	// failure. This prevents a score of 100 from hiding a broken public tree
	// while keeping the two dimensions explicit.
	ContentStatus string `json:"content_status,omitempty"`
	// AdvisoriesCount is additive (#591): the total count of taxonomy
	// findings across ALL severities (both "info" translation_pair and
	// "warning" casing_variant/alias_mismatch/possible_duplicate — none of
	// them move score/status), surfaced at the top level next to
	// score/status. Deliberately broader than
	// score_breakdown.taxonomy.advisories, which counts only info-severity
	// findings (that sub-field's pre-existing, narrower meaning from
	// #419/#577) — an info-only top-level count would report 0 for a site
	// with only casing-drift findings, exactly the case this field exists
	// to surface. Without this, an agent reading only status/score at a
	// glance ("healthy", 100) has no way to notice pending findings short
	// of deliberately drilling into score_breakdown.<category>. Never moves
	// score/status.
	AdvisoriesCount int `json:"advisories_count,omitempty"`
	// ActionableTaxonomyFindingsCount is the warning-severity subset of
	// advisories_count: alias mismatches, possible duplicates, and casing
	// variants that need editorial attention. It deliberately excludes
	// translation_pair/info findings, which describe expected multilingual
	// localization (#1061).
	ActionableTaxonomyFindingsCount int `json:"actionable_taxonomy_findings_count,omitempty"`
	// TranslationPairsDetected is the informational subset of taxonomy
	// findings. It is separate from actionable_taxonomy_findings_count so an
	// agent never has to infer whether an advisory asks for a correction.
	TranslationPairsDetected int `json:"translation_pairs_detected,omitempty"`
	// RuntimeDegraded is populated only by get_site_health. Content health and
	// operational/build health are separate signals, so expose the latest
	// failed build state without requiring an agent to infer it from a second
	// tool name (#972).
	RuntimeDegraded *bool `json:"runtime_degraded,omitempty"`
	// RuntimeDegradedReasons contains stable machine-readable reason codes,
	// never host paths or raw build errors.
	RuntimeDegradedReasons []string `json:"runtime_degraded_reasons,omitempty"`
	// ScoreBreakdown is additive to get_site_health (#419): per-category
	// score/weight/issues so an agent can see why `score` is what it is,
	// without re-deriving the scoring logic. Nil for tools other than
	// get_site_health.
	ScoreBreakdown *scoreBreakdownDTO `json:"score_breakdown,omitempty"`
	// RenderedSEOCoverage (#1136) is populated only by get_site_health (nil
	// for other tools reusing contentEnvelopeData, same convention as
	// ScoreBreakdown) — see renderedSEOCoverageDTO's own doc comment.
	RenderedSEOCoverage *renderedSEOCoverageDTO `json:"rendered_seo_coverage,omitempty"`
	// TaxonomyInconsistencies keeps its original string[] shape for
	// backward compatibility (#210/#328: no v1.x field-shape breaks).
	// TaxonomyInconsistencyDetails is the additive, structured sibling —
	// same findings, same order, with affected page slugs attached.
	PublishedPages               int                        `json:"published_pages,omitempty" jsonschema:"Rendered pages currently classified as content in the public index. This public population is independent from source categories and can contain routes not matched to publishable ordinary sources."`
	SourcePages                  int                        `json:"source_pages,omitempty" jsonschema:"All indexed source documents, including ordinary content, drafts, headless content, and section or language _index documents."`
	DraftPages                   int                        `json:"draft_pages,omitempty"`
	PublishableSourcePages       int                        `json:"publishable_source_pages,omitempty" jsonschema:"Backward-compatible count of ordinary non-draft source pages expected to have an individually resolvable public page; excludes section indexes, headless content, future or expired pages, and _build render exclusions. Prefer publication_coverage.publishable_content_sources for new clients."`
	PublishableContentPages      int                        `json:"publishable_content_pages,omitempty" jsonschema:"Ordinary non-draft source content expected to have an individually resolvable public page. This is the clearly named successor to publishable_source_pages and intentionally excludes section or language _index documents."`
	SectionIndexPages            int                        `json:"section_index_pages,omitempty" jsonschema:"Source section or language index documents named _index.md or _index.<lang>.md. They are counted in source_pages but excluded from publishable_content_pages because their public route is the containing section."`
	MissingPublicPages           int                        `json:"missing_public_pages,omitempty" jsonschema:"Publishable ordinary content sources for which no matching public page can be resolved. Section index documents are not part of this missing-page check."`
	PublicOutputComplete         *bool                      `json:"public_output_complete,omitempty" jsonschema:"True when missing_public_pages is zero for every publishable ordinary content source checked. Read publication_coverage for the exact populations behind this result."`
	PublicationCoverage          *publicationCoverageDTO    `json:"publication_coverage,omitempty" jsonschema:"Typed reconciliation of source-document populations with the rendered content-page population used by public-output health checks."`
	Tags                         int                        `json:"tags,omitempty"`
	Categories                   int                        `json:"categories,omitempty"`
	MissingTitles                int                        `json:"missing_titles,omitempty"`
	MissingDates                 int                        `json:"missing_dates,omitempty"`
	ValidationErrors             int                        `json:"validation_errors,omitempty"`
	TaxonomyInconsistencies      []string                   `json:"taxonomy_inconsistencies,omitempty"`
	TaxonomyInconsistencyDetails []taxonomyInconsistencyDTO `json:"taxonomy_inconsistency_details,omitempty"`
	OrphanPages                  []string                   `json:"orphan_pages,omitempty"`
	// BadTitleShapePages (#1105) lists slugs whose title field is a raw URL
	// rather than actual page text — see scoreBreakdownDTO.TitleShape.
	// Exposed the same way OrphanPages is, so an agent can go fix the
	// specific pages directly instead of only seeing an aggregate count.
	BadTitleShapePages []string `json:"bad_title_shape_pages,omitempty"`
	// BrokenLinksCount (#1105) is the same figure get_broken_links.data.broken_links
	// would report, surfaced here so an operator doesn't have to make a second
	// call to notice a link-graph problem alongside content-health ones. A
	// pointer for the same reason as score_breakdown.broken_links: nil means
	// "not computed" (db_path unset), never a misleading "0".
	BrokenLinksCount *int `json:"broken_links_count,omitempty"`
	// UntrackedSourcePages (#819) counts published pages whose source file
	// isn't tracked by git — surfaced proactively here instead of only
	// discovered per-page via diff_page's own git_untracked status. A
	// pointer so "0 untracked, checked" (empty object omitted by omitempty
	// only when nil) is distinguishable from "couldn't check at all" (no
	// git repo, git unavailable) — see buildSiteHealth's call to
	// untrackedSourcePageCount.
	UntrackedSourcePages *int         `json:"untracked_source_pages,omitempty"`
	Sections             []sectionDTO `json:"sections,omitempty"`
	Languages            []string     `json:"languages,omitempty"`
	Summary              string       `json:"summary,omitempty"`
	RecentPages          []pageDTO    `json:"recent_pages,omitempty"`
	Notes                []string     `json:"notes,omitempty"`
}

type publicationCoverageDTO struct {
	SourceDocuments                int    `json:"source_documents" jsonschema:"All indexed source documents represented by source_pages."`
	PublishableContentSources      int    `json:"publishable_content_sources" jsonschema:"Ordinary source content expected to resolve to an individual public page; excludes section indexes and non-publishable sources."`
	SectionIndexSources            int    `json:"section_index_sources" jsonschema:"Source _index documents whose public route is their containing section rather than a page derived from the source filename."`
	OtherExcludedSources           int    `json:"other_excluded_sources" jsonschema:"Source documents excluded from publication coverage for reasons other than being section indexes, such as draft, future, expired, headless, or _build render settings."`
	PublishedContentPages          int    `json:"published_content_pages" jsonschema:"Rendered pages classified as content in the public index. This is an independent public population, not the sum of source categories."`
	MissingPublishableContentPages int    `json:"missing_publishable_content_pages" jsonschema:"Publishable ordinary content sources with no matching public page."`
	CompletenessBasis              string `json:"completeness_basis" jsonschema:"Population used to compute complete; currently always publishable_content_sources."`
	CountersDirectlyComparable     bool   `json:"counters_directly_comparable" jsonschema:"Always false: source-document categories and the independently classified public content-page population must not be compared as if they were the same set."`
	Complete                       bool   `json:"complete" jsonschema:"True when missing_publishable_content_pages is zero."`
}

type contentEnvelope struct {
	toolcontract.ToolResponse[contentEnvelopeData]
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
}

type validateFrontMatterInput struct {
	Slug         string `json:"slug,omitempty"`
	Lang         string `json:"lang,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	// Owner (#894) optionally filters the test-content advisory
	// (test_content_slugs / test_content) to only entries whose
	// test_content_owner frontmatter matches — so a multi-agent caller can
	// safely enumerate just its own disposable residue. Omitting it lists all
	// test content (backward-compatible). Reserved-prefix legacy content has
	// no owner, so it is excluded whenever a non-empty owner filter is set.
	Owner string `json:"owner,omitempty"`
}

// validateSiteInput's InvalidOnly/IncludeValid are pointers so the handler
// can distinguish "omitted" (apply the new invalid-only-by-default behavior,
// #456) from an explicit true/false (always honored verbatim, preserving any
// caller that already depended on the old explicit invalid_only=false full
// listing).
type validateSiteInput struct {
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	InvalidOnly  *bool  `json:"invalid_only,omitempty"`
	IncludeValid *bool  `json:"include_valid,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
	// Owner (#894) — see validateFrontMatterInput.Owner.
	Owner string `json:"owner,omitempty"`
}

// effectiveInvalidOnly resolves validateSiteInput's default-flip precedence
// (#456): an explicit include_valid wins if present, then an explicit
// invalid_only, and only when both are omitted does the new default
// (invalid-only) apply.
func (in validateSiteInput) effectiveInvalidOnly() bool {
	if in.IncludeValid != nil {
		return !*in.IncludeValid
	}
	if in.InvalidOnly != nil {
		return *in.InvalidOnly
	}
	return true
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
	// Status is "valid" when Invalid == 0, "invalid" otherwise — lets a
	// simple caller branch on one field instead of deriving validity from
	// an empty Pages list plus the Invalid counter (#568).
	Status       string                `json:"status"`
	PagesChecked int                   `json:"pages_checked"`
	PagesPassed  int                   `json:"pages_passed"`
	Invalid      int                   `json:"invalid"`
	Returned     int                   `json:"returned_count,omitempty"`
	Limit        int                   `json:"limit,omitempty"`
	Offset       int                   `json:"offset,omitempty"`
	HasMore      bool                  `json:"has_more"`
	NextOffset   *int                  `json:"next_offset,omitempty"`
	Pages        []frontMatterIssueDTO `json:"pages"`

	// TestContentSlugs lists disposable test content — first from explicit
	// frontmatter markers, then from the reserved-prefix heuristic for
	// older content that predates that marker (#584, #832). Advisory only,
	// never affects Invalid/PagesPassed/Status. A slug landing here means
	// "confirm this isn't leftover throwaway content before publishing,"
	// not "this page's frontmatter is broken."
	TestContentSlugs []string `json:"test_content_slugs,omitempty"`
	// TestContent (#894) is the structured companion to TestContentSlugs: the
	// same disposable-test-content entries, but each carrying the page's
	// test_content_owner (already written to frontmatter by create_page's
	// test_content option) when present. Kept as a parallel, additive field so
	// test_content_slugs stays a plain []string for existing callers. Ordering
	// mirrors test_content_slugs. When the caller passes an owner filter, both
	// fields are narrowed to that owner.
	TestContent []testContentEntryDTO `json:"test_content,omitempty"`
}

// testContentEntryDTO is one disposable-test-content advisory entry with its
// owner (#894). Owner is omitempty because reserved-prefix legacy content and
// pre-#661 explicit test content have no recorded owner.
type testContentEntryDTO struct {
	Slug  string `json:"slug"`
	Owner string `json:"owner,omitempty"`
}

type validateOutput struct {
	toolcontract.ToolResponse[validateOutputData]
}

type brokenLinkInput struct {
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type brokenLinkDTO struct {
	PageSlug string `json:"page_slug"`
	Link     string `json:"link"`
	Target   string `json:"target,omitempty"`
	Reason   string `json:"reason"`
}

type brokenLinkData struct {
	TotalPages       int                `json:"total_pages"`
	DocumentsScanned int                `json:"documents_scanned"`
	BrokenLinks      int                `json:"broken_links"`
	Limit            int                `json:"limit"`
	Offset           int                `json:"offset"`
	Links            []brokenLinkDTO    `json:"links"`
	ContentPages     int                `json:"content_pages"`
	TaxonomyPages    int                `json:"taxonomy_pages"`
	SectionPages     int                `json:"section_pages"`
	OtherDocuments   int                `json:"other_documents"`
	IndexInfo        *indexStalenessDTO `json:"index_staleness,omitempty"`
}

type brokenLinkOutput struct {
	toolcontract.ToolResponse[brokenLinkData]
}

type getBacklinksInput struct {
	Slug         string `json:"slug"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type backlinkDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// indexStalenessDTO is populated only when the in-memory site index is
// behind on-disk content (#583) — e.g. a manual `hugo` build or direct
// filesystem edit that bypassed build_site/create_page/delete_page, the
// only paths that refresh the index. Omitted entirely when the index is
// current — the field's presence is itself the "stale" signal, so it
// carries no separate boolean, to keep the shape unambiguous and cheap on
// the common (fresh) path.
type indexStalenessDTO struct {
	NewestEdit string `json:"newest_edit"`
	// LikelySource is a coarse, best-effort hint at *why* the index is
	// stale (#617), distinguishing "this server's own known, expected
	// pending write" from "some other, unrecorded reason" (most plausibly
	// an out-of-band edit outside this server, e.g. direct SSH/git) —
	// without any new per-caller/per-session identity tracking. Derived
	// entirely from the existing BuildPending bookkeeping create_page/
	// update_page/apply_content_plan/rollback_change already maintain: if
	// any source page currently has a pending MCP-originated write not yet
	// built, that's reported as "mcp_pending_build"; otherwise
	// "external_or_unknown". This can't attribute staleness to a specific
	// caller/session, and a real coincidence (an MCP write pending AND an
	// unrelated external edit at the same time) would still report
	// "mcp_pending_build" — it is a hint for faster diagnosis, not a
	// guarantee of the true cause.
	LikelySource string `json:"likely_source,omitempty"`
}

// staleness checks idx against on-disk content and returns nil when
// current, so callers can attach it via omitempty without an extra nil check.
// srcIdx may be nil (some registration paths run without a source index);
// LikelySource is simply omitted in that case.
func staleness(idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) *indexStalenessDTO {
	stale, newest := idx.StaleAgainstDisk(cfg)
	if !stale {
		return nil
	}
	dto := &indexStalenessDTO{NewestEdit: newest.UTC().Format(time.RFC3339)}
	if srcIdx != nil {
		if srcIdx.HasPendingBuild() {
			dto.LikelySource = "mcp_pending_build"
		} else {
			dto.LikelySource = "external_or_unknown"
		}
	}
	return dto
}

type getBacklinksData struct {
	Slug      string             `json:"slug"`
	Count     int                `json:"count"`
	Backlinks []backlinkDTO      `json:"backlinks"`
	IndexInfo *indexStalenessDTO `json:"index_staleness,omitempty"`
}

type getBacklinksOutput struct {
	toolcontract.ToolResponse[getBacklinksData]
}

type suggestInternalLinksInput struct {
	Slug            string   `json:"slug,omitempty"`
	Language        string   `json:"language,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Categories      []string `json:"categories,omitempty"`
	Body            string   `json:"body,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	OnePerSourceKey bool     `json:"one_per_source_key,omitempty"`
	ResponseMode    string   `json:"response_mode,omitempty"`
}

type responseModeOnlyInput struct {
	ResponseMode string `json:"response_mode,omitempty"`
}

type linkSuggestionDTO struct {
	Slug             string   `json:"slug"`
	Title            string   `json:"title"`
	URL              string   `json:"url"`
	Lang             string   `json:"lang,omitempty"`
	AnchorText       string   `json:"anchor_text"`
	SharedTags       []string `json:"shared_tags,omitempty"`
	SharedCategories []string `json:"shared_categories,omitempty"`
	Score            int      `json:"score"`
	BodyMention      bool     `json:"body_mention,omitempty"`
}

type suggestInternalLinksData struct {
	Slug         string               `json:"slug,omitempty"`
	Total        int                  `json:"total"`
	Translations []translationPageDTO `json:"translations"`
	// SuggestedLinks is canonical (matches the suggest_links tool name).
	SuggestedLinks []linkSuggestionDTO `json:"suggested_links"`
	// EmptyReason is populated only when SuggestedLinks is empty (#458); see
	// the identically-named field on getRelatedContentData.
	EmptyReason *emptyResultExplanationDTO `json:"empty_reason,omitempty"`
}

type suggestInternalLinksOutput struct {
	toolcontract.ToolResponse[suggestInternalLinksData]
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
	registerReadExtendedFoundationTools(s, idx, srcIdx, cfg)
	registerReadExtendedSearchAndHealthTools(s, idx, srcIdx, cfg, siteDB, aliases)
	registerReadExtendedLinkAndSuggestionTools(s, idx, srcIdx, cfg, aliases)
}

func registerReadExtendedFoundationTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	RegisterDiffPage(s, idx, srcIdx, cfg)
	RegisterInspectRenderedPage(s, idx, srcIdx, cfg)
	RegisterListContentTypes(s, srcIdx, cfg)
	RegisterListPageAssets(s, idx, srcIdx, cfg)
	RegisterAIReadiness(s, idx, srcIdx, cfg)
	RegisterPlanPage(s, idx, srcIdx, cfg)
	RegisterListPageRevisions(s, idx, srcIdx, cfg)
}

func registerReadExtendedSearchAndHealthTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, siteDB *db.DB, aliases map[string]string) {
	addReadOnlyTool(s, "search_content", "Search content", "Filtered search across published content with type, tag, category, language, sort, and pagination. Returns a structured envelope with total count. When db_path is configured, uses FTS5 full-text search with ranked results and snippets. Also matches body text, unlike search_pages. `include_terms` defaults to true: pass `include_terms=false` to omit `tag_terms`/`category_terms` and keep only the plainer `tags`/`categories` arrays; `response_mode:\"compact\"` implies the same omission. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. Prefer this tool over search_pages whenever you already have a reader token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchContentInput) (*mcp.CallToolResult, searchContentEnvelope, error) {
			if idx == nil {
				return nil, searchContentEnvelope{}, fmt.Errorf("index not initialized")
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, searchContentEnvelope{}, err
			}
			terms := includeTerms(mode, in.IncludeTerms)
			readerSafe := site.IsReaderProfile(ctx)
			if t := strings.ToLower(strings.TrimSpace(in.Type)); t != "" && t != "all" && t != "post" && t != "posts" && t != "page" && t != "pages" {
				return nil, searchContentEnvelope{}, fmt.Errorf("invalid_params: type must be one of: all, post, posts, page, pages (got %q)", in.Type)
			}
			if err := validateSearchContentSort(in.Sort); err != nil {
				return nil, searchContentEnvelope{}, err
			}
			if err := validateSearchContentOrder(in.Order); err != nil {
				return nil, searchContentEnvelope{}, err
			}
			if err := validateSearchContentLanguage(in.Language, idx, cfg); err != nil {
				return nil, searchContentEnvelope{}, err
			}
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, searchContentEnvelope{}, err
			}
			if err := maxLimitError(in.Limit, 100); err != nil {
				return nil, searchContentEnvelope{}, err
			}
			if err := negativeOffsetError(in.Offset); err != nil {
				return nil, searchContentEnvelope{}, err
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
					dtos := toPageDTOsWithSnippets(pages, aliases, snippetMap, lookup, cfg.ContentRoot, cfg.SiteRoot, terms)
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
				Pages:         toPageDTOs(pages, aliases, sourceIndexForProfile(srcIdx, readerSafe), cfg.ContentRoot, cfg.SiteRoot, terms),
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
		})

	addReadOnlyTool(s, "explain_structure", "Explain site structure", "Summarize how the Hugo site is organized, including sections, taxonomies, languages, and recent content. Useful for onboarding or content planning. `response_mode:\"compact\"` keeps only the structural summary (summary, section counts, languages, taxonomy counts) and omits the heavier `recent_pages` examples and long `notes` list used for deeper onboarding. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in responseModeOnlyInput) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, contentEnvelope{}, err
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
			data := contentEnvelopeData{
				Summary:     summary,
				Sections:    sections,
				Languages:   languages,
				Tags:        tagCount,
				Categories:  catCount,
				RecentPages: toPageDTOsEnriched(recent, sourceIndexForProfile(srcIdx, readerSafe), aliases, cfg.ContentRoot, cfg.SiteRoot, includeTerms(mode, nil)),
				Notes: []string{
					"Top-level sections are derived from page slugs.",
					"Posts are detected from the /posts/ path prefix.",
					"A single root-level page (e.g. content/some-slug.md) is listed as its own one-off section named after its own slug (#642) — by design, not a bug, but a single stray or throwaway root-level page will appear as a distinct section.",
				},
			}
			if mode == toolcontract.ResponseModeCompact {
				data.RecentPages = nil
				data.Notes = nil
			}
			return nil, newContentEnvelope(data, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "get_site_health", "Get site health", "Return a concise health summary for the Hugo site, including content counts, validation signals, taxonomy inconsistency warnings, and public-output completeness. Counter populations are intentionally distinct: `source_pages` includes every indexed source document; `publishable_source_pages` is the backward-compatible count of ordinary content expected to resolve individually and excludes `_index` documents and other non-publishable sources; `published_pages` is the independently classified rendered content-page population and can include public routes not matched to publishable ordinary sources. New clients should use `publishable_content_pages`, `section_index_pages`, and the typed `publication_coverage` breakdown instead of comparing the three legacy counters directly. `public_output_complete` means every publishable ordinary content source has a public match, not that all three counters must be equal. `publication_coverage.completeness_basis` identifies that source population explicitly, while `counters_directly_comparable:false` warns agents not to subtract the independent source and public counters. `content_status` preserves the source/front-matter classification, while top-level `status` becomes `degraded` whenever `runtime_degraded` is true, OR whenever `bad_title_shape_pages` is non-empty (see below); `runtime_degraded_reasons`, `missing_public_pages`, and `public_output_complete` explain whether a failed build or an incomplete rendered tree caused that operational state. `bad_title_shape_pages` (#1105) lists slugs whose title field is a bare http(s) URL instead of actual page text — a corrupted-title defect a frontmatter-presence check cannot see, since the field is non-empty. `score_breakdown.title_shape` reports 0 whenever this list is non-empty, 100 otherwise; despite carrying weight 0 (like taxonomy, it never moves the weighted `score` calculation), a URL-shaped title still forces `status`/`content_status` off `healthy`/`healthy_with_advisories` directly and caps an otherwise-perfect `score` at 99 — do not read weight 0 as harmless for this category the way it is for taxonomy. `taxonomy_inconsistency_details` gives each warning's affected page slugs (`pages_with_term_a`/`pages_with_term_b`) so you can go fix front matter directly, without a separate list_pages/filter lookup; `taxonomy_inconsistencies` (plain strings) is kept for backward compatibility. Each detail's `kind` distinguishes an actionable finding (`alias_mismatch`, `possible_duplicate`, `casing_variant` — the same term spelled with different casing within one language) from `translation_pair` — two terms used on the same page bundle in different languages, which is the site's own localization, not a content problem to fix. Each detail's `severity` distinguishes an actionable content issue (`warning`) from expected localization (`info`). Info-only findings still do not move the top-level `score`; warning findings remain zero-weight in `score_breakdown.taxonomy.weight`, but now cap an otherwise-perfect top-level `score` at 99 so the response no longer advertises perfection while surfacing actionable taxonomy drift (#719). `advisories_count` is the total count of *all* `taxonomy_inconsistency_details` findings (both `info` and `warning` severity) at the top level next to `score`/`status`; a pure `translation_pair`/`info` finding stays visible there but no longer degrades `content_status` on an otherwise healthy site, while a `warning`-severity taxonomy finding promotes `content_status` to `healthy_with_advisories` without directly moving `score` (#761). This is broader than `score_breakdown.taxonomy.advisories`, which counts only `info`-severity findings specifically (a sub-field with its own narrower, pre-existing meaning) — `advisories_count` exists precisely so a `casing_variant`/`alias_mismatch`/`possible_duplicate` finding is just as visible as a `translation_pair` one. `score_breakdown` shows the per-category score/weight/issue-count behind the top-level `score` (weight 0 means that category is informational only and never directly contributes points to the score), so you don't have to re-derive why a finding did or didn't change it. `untracked_source_pages` (#819) counts source pages with no git-tracked file — an operational-hygiene signal (no git-based rollback path for that content) surfaced proactively instead of only discoverable per-page via diff_page's own git_untracked status; omitted entirely (not a zero) when git status can't be determined at all (no repo, git unavailable), never affects `score`/`content_status`. `broken_links_count` and `score_breakdown.broken_links` (#1105) surface the same figure `get_broken_links.data.broken_links` reports, but only when `db_path` is configured — computing it here otherwise would mean paying `get_broken_links`'s full-HTML-rescan cost on every `get_site_health` call, not just an explicit one. Both fields are entirely omitted (not a `0`) when not computed, the same `untracked_source_pages` distinction above; a present `0` means checked and clean. Like `title_shape`, `score_breakdown.broken_links` carries weight 0 (never moves the weighted `score`) but a nonzero count still forces `status`/`content_status` off `healthy`/`healthy_with_advisories` and caps an otherwise-perfect `score` at 99 — this deliberately never folds link-graph resolution logic into this tool's own scoring, keeping `get_broken_links` the single source of truth for what counts as broken; it only reacts to that count as an override. `rendered_seo_coverage` (#1136) is always present with `aggregated:false`: every check above is source-document or link-graph based — a missing `<title>`/canonical/meta description/hreflang, or an unsafe/leaked URL in the actually-rendered HTML, can coexist with `score:100` here, because this tool never inspects rendered output. `inspect_rendered` (per-page) is the authoritative check for that surface; see `docs/mcp-contract.md` §6.19 for why it isn't aggregated here. Use this before publishing or reviewing content. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ responseModeOnlyInput) (*mcp.CallToolResult, contentEnvelope, error) {
			if idx == nil {
				return nil, contentEnvelope{}, fmt.Errorf("index not initialized")
			}
			health := buildSiteHealth(ctx, idx, sourceIndexForProfile(srcIdx, site.IsReaderProfile(ctx)), aliases, cfg, siteDB)
			return nil, newContentEnvelope(contentEnvelopeData{
				Status:                          health.Status,
				Score:                           health.Score,
				ContentStatus:                   health.ContentStatus,
				AdvisoriesCount:                 health.AdvisoriesCount,
				ActionableTaxonomyFindingsCount: health.ActionableTaxonomyFindingsCount,
				TranslationPairsDetected:        health.TranslationPairsDetected,
				RuntimeDegraded:                 health.RuntimeDegraded,
				RuntimeDegradedReasons:          health.RuntimeDegradedReasons,
				ScoreBreakdown:                  health.ScoreBreakdown,
				RenderedSEOCoverage:             health.RenderedSEOCoverage,
				PublishedPages:                  health.PublishedPages,
				SourcePages:                     health.SourcePages,
				DraftPages:                      health.DraftPages,
				PublishableSourcePages:          health.PublishableSourcePages,
				PublishableContentPages:         health.PublishableContentPages,
				SectionIndexPages:               health.SectionIndexPages,
				MissingPublicPages:              health.MissingPublicPages,
				PublicOutputComplete:            health.PublicOutputComplete,
				PublicationCoverage:             health.PublicationCoverage,
				Tags:                            health.Tags,
				Categories:                      health.Categories,
				MissingTitles:                   health.MissingTitles,
				MissingDates:                    health.MissingDates,
				ValidationErrors:                health.ValidationErrors,
				TaxonomyInconsistencies:         health.TaxonomyInconsistencies,
				TaxonomyInconsistencyDetails:    health.TaxonomyInconsistencyDetails,
				UntrackedSourcePages:            health.UntrackedSourcePages,
				BadTitleShapePages:              health.BadTitleShapePages,
				BrokenLinksCount:                health.BrokenLinksCount,
			}, time.Now().UTC()), nil
		})

	addReadOnlyTool(s, "validate_frontmatter", "Validate front matter", "Validate Hugo front matter for missing titles, dates, or malformed metadata. Optionally target one slug. When a slug maps to a multilingual bundle, the default is bundle-wide validation across every translation sharing that source key; pass `lang` to restrict validation to one translation explicitly. `pages_checked`/`pages_passed`/`invalid` always describe the full matched scan scope, regardless of `limit`/`offset` — every matched page is validated. `pages` is a separate paginated view of the per-page detail rows; use `returned_count`/`has_more`/`next_offset` to page through it. `test_content_slugs` separately lists disposable test content — first from explicit `test_content: true` frontmatter, then (for older content) from reserved test/audit prefixes such as `mcp-audit-`, `test-audit-`, `codex-` — advisory only, never affects `invalid`/`status`; confirm it isn't leftover throwaway content before publishing (#584, #832). `test_content` is the structured companion to `test_content_slugs`: the same entries, each also carrying the page's `test_content_owner` (written to frontmatter by create_page's `test_content` option) when present (#894). Pass `owner` to narrow both lists to only test content whose recorded owner matches — so a multi-agent caller can safely enumerate just its own residue; reserved-prefix legacy content and ownerless test content are excluded when a non-empty `owner` filter is set (#894). Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in validateFrontMatterInput) (*mcp.CallToolResult, validateOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, validateOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source validation diagnostics")
			}
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			if err := negativeOffsetError(in.Offset); err != nil {
				return nil, validateOutput{}, err
			}
			pages, err := sourcePagesForValidation(srcIdx, in.Slug, in.Lang)
			if err != nil {
				return nil, validateOutput{}, err
			}
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			return nil, validatePagesWithIssues(pages, in.Offset, in.Limit, in.Owner, aliases, resolver), nil
		})

	addReadOnlyTool(s, "validate_site", "Validate site", "Run a validation pass over all Hugo source pages and report front matter issues. Equivalent to validate_frontmatter with no slug filter. `pages_checked`/`pages_passed`/`invalid` always describe the full site regardless of `limit`/`offset`/`invalid_only`/`include_valid`. `pages` is a separate paginated view of the per-page detail rows; use `limit`/`offset` and `returned_count`/`has_more`/`next_offset` to page through it. By default (no arguments) `pages` contains only invalid pages — on a large, mostly-valid site this avoids paying full response cost to confirm nothing is wrong. Set `include_valid=true` (or `invalid_only=false`) to get every page's detail row back, including passing ones. `test_content_slugs` separately lists disposable test content — first from explicit `test_content: true` frontmatter, then (for older content) from reserved test/audit prefixes such as `mcp-audit-`, `test-audit-`, `codex-` — advisory only, never affects `invalid`/`status`; confirm it isn't leftover throwaway content before publishing (#584, #832). `test_content` is the structured companion to `test_content_slugs`: the same entries, each also carrying the page's `test_content_owner` (written to frontmatter by create_page's `test_content` option) when present (#894). Pass `owner` to narrow both lists to only test content whose recorded owner matches — so a multi-agent caller can safely enumerate just its own residue; reserved-prefix legacy content and ownerless test content are excluded when a non-empty `owner` filter is set (#894). Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in validateSiteInput) (*mcp.CallToolResult, validateOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, validateOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source validation diagnostics")
			}
			if srcIdx == nil {
				return nil, validateOutput{}, fmt.Errorf("source index not initialized")
			}
			if err := negativeOffsetError(in.Offset); err != nil {
				return nil, validateOutput{}, err
			}
			pages := srcIdx.ListPages(0, 0)
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			return nil, validatePagesWithIssuesFiltered(pages, in.Offset, in.Limit, in.effectiveInvalidOnly(), in.Owner, aliases, resolver), nil
		})

	addReadOnlyTool(s, "get_broken_links", "Get broken links", "Audit internal links against the current Hugo index without making any external network calls. When db_path is configured, reads from a pre-computed link graph (O(1)); otherwise re-scans HTML on each call. Returns a limited sample of missing internal targets. `documents_scanned` is the canonical size of the rendered HTML surface this pass checked; the legacy `total_pages` field is kept for backward compatibility and mirrors the same value. `content_pages`/`taxonomy_pages`/`section_pages`/`other_documents` break that scan surface down by document class so agents don't have to guess what `total_pages` counted. `index_staleness` (in-memory path only, not the db_path path) is present only when the index is behind on-disk content — its absence means results reflect current source (#583). When present, `index_staleness.likely_source` is a coarse, best-effort hint at why: `\"mcp_pending_build\"` means this server has a known, expected write awaiting the next build_site/publish_changes; `\"external_or_unknown\"` means the disk changed with no such record on file — most plausibly an edit made outside this server (e.g. direct SSH/git) (#617). Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(_ context.Context, _ *mcp.CallToolRequest, in brokenLinkInput) (*mcp.CallToolResult, brokenLinkOutput, error) {
			if idx == nil {
				return nil, brokenLinkOutput{}, fmt.Errorf("index not initialized")
			}
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, brokenLinkOutput{}, err
			}
			if err := negativeOffsetError(in.Offset); err != nil {
				return nil, brokenLinkOutput{}, err
			}
			limit := clampLimit(in.Limit, 25, 100)
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}

			sitemap := idx.Sitemap()
			counts := idx.Classifier().CountKinds(sitemap)

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
						TotalPages:       len(sitemap),
						DocumentsScanned: len(sitemap),
						BrokenLinks:      len(issues),
						Limit:            limit,
						Offset:           offset,
						Links:            sliceBrokenLinks(issues, offset, limit),
						ContentPages:     counts.ContentPages,
						TaxonomyPages:    counts.TaxonomyPages,
						SectionPages:     counts.SectionPages,
						OtherDocuments:   counts.OtherDocuments,
					}, time.Now().UTC()), nil
				}
			}

			// In-memory fallback: re-scan HTML on each call.
			issues := collectBrokenLinks(idx)
			return nil, newBrokenLinkOutput(brokenLinkData{
				TotalPages:       len(sitemap),
				DocumentsScanned: len(sitemap),
				BrokenLinks:      len(issues),
				Limit:            limit,
				Offset:           offset,
				Links:            sliceBrokenLinks(issues, offset, limit),
				ContentPages:     counts.ContentPages,
				TaxonomyPages:    counts.TaxonomyPages,
				SectionPages:     counts.SectionPages,
				OtherDocuments:   counts.OtherDocuments,
				IndexInfo:        staleness(idx, srcIdx, cfg),
			}, time.Now().UTC()), nil
		}, func(s any) any { return tools.WithMaxLimit(s, "limit", 100) })
}

func registerReadExtendedLinkAndSuggestionTools(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, aliases map[string]string) {
	addReadOnlyTool(s, "get_backlinks", "Get backlinks", "Return all published pages that contain an internal link to the specified slug. Use this before delete_page (impact analysis) or when writing new content (find existing references). This is the same backlinks data get_related_content returns alongside related_pages/suggested_links/translations in one call — use this standalone version when you only need backlinks and want to avoid the cost of the other three facets. `index_staleness` is present only when the in-memory index is behind on-disk content (e.g. a manual Hugo build outside this server) — its absence means the index reflects current source; when present, treat the backlinks list as possibly outdated until the next build_site (#583). `index_staleness.likely_source` is a coarse, best-effort hint at why: `\"mcp_pending_build\"` (a known, expected write via this server awaiting the next build) vs. `\"external_or_unknown\"` (no such record — most plausibly an out-of-band edit, e.g. direct SSH/git) (#617). Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
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
			dtos := premutationBacklinks(idx, targetSlug)
			env := newGetBacklinksOutput(getBacklinksData{
				Slug:      targetSlug,
				Count:     len(dtos),
				Backlinks: dtos,
				IndexInfo: staleness(idx, srcIdx, cfg),
			}, time.Now().UTC())
			return nil, env, nil
		})

	addReadOnlyTool(s, "suggest_links", "Suggest internal links",
		"Recommend existing published pages to link from a draft or existing page, based on shared tags and categories. "+
			"Supply slug (for an indexed page), or tags/categories (for a draft not yet published), or both. "+
			"Optionally include body to detect pages whose titles already appear in the text (body_mention: true). "+
			"Returns ranked suggestions with anchor_text and shared taxonomy context. Use this specifically for a draft not yet indexed (via tags/categories/body); for an already-published page, get_related_content's suggested_links field covers the same case alongside backlinks/related_pages/translations in one call. When suggested_links comes back empty, `empty_reason` explains why (candidates_evaluated, minimum_score) instead of leaving you to guess whether nothing qualifies or nothing else exists at all. Options: `language` filters rows to one language; `one_per_source_key:true` collapses translated siblings; `response_mode:\"compact\"` keeps ranked slug/title/score/anchor rows while omitting translations and taxonomy detail. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in suggestInternalLinksInput) (*mcp.CallToolResult, suggestInternalLinksOutput, error) {
			if idx == nil {
				return nil, suggestInternalLinksOutput{}, fmt.Errorf("index not initialized")
			}
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, suggestInternalLinksOutput{}, err
			}
			limit := clampLimit(in.Limit, 10, 20)
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, suggestInternalLinksOutput{}, err
			}

			// Build the reference taxonomy: start from provided tags/categories, then merge in the
			// indexed page's taxonomy when a slug is given.
			refTags := make([]string, 0)
			refCats := make([]string, 0)
			refTags = append(refTags, in.Tags...)
			refCats = append(refCats, in.Categories...)

			var resolvedSlug string
			slugResolved := false
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
					slugResolved = true
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

			// A resolved slug is itself a valid, complete input per the tool's own contract
			// ("Supply slug ... or tags/categories ... or both") even if that page happens to
			// carry no tags/categories of its own — that's a legitimate "nothing to compare
			// against" case handled below via empty_reason, not a caller input error. Only
			// reject when the caller gave us nothing usable at all: no slug that resolved, and
			// no tags/categories either.
			if !slugResolved && len(refTags) == 0 && len(refCats) == 0 {
				return nil, suggestInternalLinksOutput{}, fmt.Errorf("invalid_params: provide at least one of slug, tags, or categories")
			}

			translations := []translationPageDTO{}
			if resolvedSlug != "" {
				if ref, ok := idx.GetBySlug(resolvedSlug); ok {
					translations = collectTranslations(idx, *ref)
				}
			}
			// #1041: over-fetch before filtering — see overfetchLimit's
			// comment for why truncating to limit before the language/
			// one_per_source_key filter would silently under-deliver.
			fetchLimit := limit
			if in.Language != "" || in.OnePerSourceKey {
				fetchLimit = overfetchLimit(limit)
			}
			suggestions, evaluated := scoreLinkSuggestions(idx, resolvedSlug, refTags, refCats, in.Body, fetchLimit)
			suggestionsBeforeFilter := len(suggestions)
			suggestions = filterSuggestedLinks(suggestions, in.Language, in.OnePerSourceKey)
			translations = filterRelationshipTranslations(translations, in.Language, in.OnePerSourceKey)
			if len(suggestions) > limit {
				suggestions = suggestions[:limit]
			}
			if mode == toolcontract.ResponseModeCompact {
				translations = nil
				for i := range suggestions {
					suggestions[i].SharedTags, suggestions[i].SharedCategories = nil, nil
				}
			}
			data := suggestInternalLinksData{
				Slug:           resolvedSlug,
				Total:          len(suggestions),
				Translations:   translations,
				SuggestedLinks: suggestions,
			}
			if len(suggestions) == 0 {
				filteredOut := suggestionsBeforeFilter > 0 && (in.Language != "" || in.OnePerSourceKey)
				data.EmptyReason = newEmptyResultExplanation(evaluated, minTaxonomyAffinityScore, filteredOut)
			}
			resp := newSuggestInternalLinksOutput(data, time.Now().UTC())
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

func scoreLinkSuggestions(idx *site.Index, excludeSlug string, refTags, refCats []string, body string, limit int) ([]linkSuggestionDTO, int) {
	type scored struct {
		dto  linkSuggestionDTO
		date string
	}
	bodyLower := strings.ToLower(body)
	lexicalQuery := lexicalSuggestionQuery(body)
	classifier := site.NewClassifierFromPages(idx.Sitemap())
	excludeTranslationKey := translationKey(excludeSlug)
	var taxonomyCandidates []scored
	var lexicalCandidates []scored
	evaluated := 0
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
		evaluated++
		sharedTagTerms := taxonomy.SharedTerms(pg.Tags, refTags)
		sharedCatTerms := taxonomy.SharedTerms(pg.Categories, refCats)
		score := len(sharedTagTerms)*2 + len(sharedCatTerms)
		// E1/W2: guard empty title; use phrase-boundary check to avoid false positives
		// (e.g. title "Go" matching "go to the store").
		titleLower := strings.ToLower(strings.TrimSpace(pg.Title))
		mention := bodyLower != "" && titleLower != "" && containsPhrase(bodyLower, titleLower)
		if score > 0 {
			taxonomyCandidates = append(taxonomyCandidates, scored{
				date: pg.Date,
				dto: linkSuggestionDTO{
					Slug:             pg.Slug,
					Title:            pg.Title,
					URL:              pg.URL,
					Lang:             pg.Lang,
					AnchorText:       pg.Title,
					SharedTags:       taxonomy.Slugs(sharedTagTerms),
					SharedCategories: taxonomy.Slugs(sharedCatTerms),
					Score:            score,
					BodyMention:      mention,
				},
			})
			continue
		}
		if lexicalQuery == "" {
			continue
		}
		lexicalScore := scoreContentPage(pg, lexicalQuery)
		if lexicalScore == 0 {
			continue
		}
		lexicalCandidates = append(lexicalCandidates, scored{
			date: pg.Date,
			dto: linkSuggestionDTO{
				Slug:        pg.Slug,
				Title:       pg.Title,
				URL:         pg.URL,
				Lang:        pg.Lang,
				AnchorText:  pg.Title,
				Score:       lexicalScore,
				BodyMention: mention,
			},
		})
	}
	candidates := taxonomyCandidates
	if len(candidates) == 0 {
		candidates = lexicalCandidates
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
	return out, evaluated
}

func lexicalSuggestionQuery(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	seen := map[string]bool{}
	terms := strings.FieldsFunc(strings.ToLower(body), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		if len(term) < 4 && !keepShortLexicalTerm(term) {
			continue
		}
		if seen[term] {
			continue
		}
		seen[term] = true
		filtered = append(filtered, term)
	}
	return strings.Join(filtered, " ")
}

func keepShortLexicalTerm(term string) bool {
	return len(term) == 3
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

func validateSearchContentSort(sortBy string) error {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "", "date", "title", "slug", "relevance":
		return nil
	default:
		return fmt.Errorf("invalid_params: sort must be one of: date, title, slug, relevance (got %q)", sortBy)
	}
}

func validateSearchContentOrder(order string) error {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "", "asc", "desc":
		return nil
	default:
		return fmt.Errorf("invalid_params: order must be one of: asc, desc (got %q)", order)
	}
}

func validateSearchContentLanguage(lang string, idx *site.Index, cfg config.Config) error {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return nil
	}
	known := availableSearchLanguages(idx, cfg)
	for _, candidate := range known {
		if strings.EqualFold(candidate, lang) {
			return nil
		}
	}
	return fmt.Errorf("invalid_params: language must be one of: %s (got %q)", strings.Join(known, ", "), lang)
}

func availableSearchLanguages(idx *site.Index, cfg config.Config) []string {
	seen := map[string]bool{}
	if d := strings.TrimSpace(cfg.DefaultLanguage); d != "" {
		seen[d] = true
	}
	if len(cfg.ConfiguredLanguages) > 0 {
		for _, lang := range cfg.ConfiguredLanguages {
			if lang = strings.TrimSpace(lang); lang != "" {
				seen[lang] = true
			}
		}
	} else if idx != nil {
		for _, page := range idx.ContentPages() {
			if lang := strings.TrimSpace(page.Lang); lang != "" {
				seen[lang] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for lang := range seen {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
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
		// A link to a canonical-collapsed alias's own URL is not broken —
		// the file genuinely exists on disk, it just isn't canonical for
		// its content (#184's dedup). Same fix on the SQL-backed path,
		// internal/db/db.go's txSyncLinks (#1112).
		if _, isAlias := idx.ResolveAlias(target.Path); isAlias {
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
		return site.ShouldIgnoreBrokenLinkTarget(rawPath)
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

func validatePagesWithIssues(pages []hugosite.SourcePage, offset, limit int, ownerFilter string, aliases map[string]string, resolver *site.PageResolver) validateOutput {
	return validatePagesWithIssuesFiltered(pages, offset, limit, false, ownerFilter, aliases, resolver)
}

// validatePagesWithIssuesFiltered is validatePagesWithIssues plus an
// invalidOnly filter (#431). pages_checked/pages_passed/invalid always
// describe the full scan scope regardless of invalidOnly — only the
// paginated `pages` detail rows (and the has_more/next_offset pagination
// built from them) are affected by the filter.
func validatePagesWithIssuesFiltered(pages []hugosite.SourcePage, offset, limit int, invalidOnly bool, ownerFilter string, aliases map[string]string, resolver *site.PageResolver) validateOutput {
	ownerFilter = strings.TrimSpace(ownerFilter)
	total := len(pages)
	if offset < 0 {
		offset = 0
	}

	allResults := make([]frontMatterIssueDTO, 0, len(pages))
	invalid := 0
	// seenTestContentSlugs dedups across language variants: ListPages
	// returns one SourcePage per (slug, lang), so a bilingual test bundle
	// (e.g. index.en.md + index.fr.md) would otherwise add the same
	// canonical slug to testContentSlugs twice.
	seenTestContentSlugs := make(map[string]bool)
	var testContentSlugs []string
	var testContent []testContentEntryDTO
	for _, p := range pages {
		issues := validateFrontMatterPage(p, aliases)
		if len(issues) > 0 {
			invalid++
		}
		slug := canonicalValidationSlug(p, resolver)
		if isTestContentPage(p) && !seenTestContentSlugs[slug] {
			owner := testContentOwner(p)
			// #894: when an owner filter is set, only surface entries whose
			// recorded owner matches — reserved-prefix legacy content and any
			// ownerless test content are excluded so an agent enumerating its
			// own residue never sees another agent's (or unattributed) pages.
			// The filter narrows ONLY the advisory test-content lists; it must
			// never drop the page from allResults / the validation detail rows
			// or from the invalid count (those describe the full scan scope,
			// gated solely by invalid_only), otherwise an owner filter could
			// silently mask an *invalid* page owned by another agent.
			if ownerFilter == "" || owner == ownerFilter {
				seenTestContentSlugs[slug] = true
				testContentSlugs = append(testContentSlugs, slug)
				testContent = append(testContent, testContentEntryDTO{Slug: slug, Owner: owner})
			}
		}
		allResults = append(allResults, frontMatterIssueDTO{Slug: slug, Lang: p.Lang, Issues: issues})
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
	status := "valid"
	if invalid > 0 {
		status = "invalid"
	}
	return newValidateOutput(validateOutputData{
		Status:           status,
		PagesChecked:     total,
		PagesPassed:      total - invalid,
		Invalid:          invalid,
		Returned:         len(results),
		Limit:            limit,
		Offset:           offset,
		HasMore:          meta.HasMore,
		NextOffset:       meta.NextOffset,
		Pages:            results,
		TestContentSlugs: testContentSlugs,
		TestContent:      testContent,
	}, time.Now().UTC())
}

func canonicalValidationSlug(p hugosite.SourcePage, resolver *site.PageResolver) string {
	if resolver != nil {
		if resolved, ok := resolver.Resolve(p.Slug); ok {
			if slug := canonicalResolvedSlug(resolved); slug != "" {
				return slug
			}
		}
	}
	return canonicalSourceSlug(p.Slug)
}

func newContentEnvelope(data contentEnvelopeData, now time.Time) contentEnvelope {
	return contentEnvelope{ToolResponse: successEnvelope(data, now)}
}

func newSearchContentEnvelope(data searchContentData, now time.Time) searchContentEnvelope {
	return searchContentEnvelope{ToolResponse: successEnvelope(data, now)}
}

func newValidateOutput(data validateOutputData, now time.Time) validateOutput {
	return validateOutput{ToolResponse: successEnvelope(data, now)}
}

func newBrokenLinkOutput(data brokenLinkData, now time.Time) brokenLinkOutput {
	return brokenLinkOutput{ToolResponse: successEnvelope(data, now)}
}

func newGetBacklinksOutput(data getBacklinksData, now time.Time) getBacklinksOutput {
	return getBacklinksOutput{ToolResponse: successEnvelope(data, now)}
}

func newSuggestInternalLinksOutput(data suggestInternalLinksData, now time.Time) suggestInternalLinksOutput {
	return suggestInternalLinksOutput{ToolResponse: successEnvelope(data, now)}
}

func effectiveSort(in searchContentInput) string {
	if strings.TrimSpace(in.Sort) == "" && strings.TrimSpace(in.Query) != "" {
		return "relevance"
	}
	return canonicalSort(in.Sort)
}

// hasReservedTestSlugPrefix now delegates to contentmodel.IsReservedTestSlug
// (#608), the single shared definition of "test content" used by both this
// package's test_content_slugs advisory and the post-build stale-content
// check.
func hasReservedTestSlugPrefix(slug string) bool {
	return contentmodel.IsReservedTestSlug(slug)
}

func isExplicitTestContent(frontmatterRaw map[string]any) bool {
	if frontmatterRaw == nil {
		return false
	}
	raw, ok := frontmatterRaw["test_content"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func isTestContentPage(p hugosite.SourcePage) bool {
	if isExplicitTestContent(p.FrontmatterRaw) {
		return true
	}
	return hasReservedTestSlugPrefix(p.Slug)
}

// testContentOwner returns the test_content_owner recorded in a page's
// frontmatter (#894, written by create_page's test_content option), or "" when
// the page has none — reserved-prefix legacy content and pre-#661 explicit test
// content never carried an owner.
func testContentOwner(p hugosite.SourcePage) string {
	if p.FrontmatterRaw == nil {
		return ""
	}
	if raw, ok := p.FrontmatterRaw["test_content_owner"]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// isURLShapedTitle reports whether title is a bare http(s) URL rather than
// actual page text (#1105, incident #1099: a page's title field was
// corrupted to its own raw canonical URL, and get_site_health kept reporting
// healthy/100 through it because a non-empty title already satisfies the
// pre-existing "missing title" check). Deliberately broader than an
// exact-match against the page's own canonical URL: any URL-shaped title is
// a content defect regardless of which URL it happens to be, and checking
// only self-referential titles would miss e.g. a title accidentally
// corrupted to a *different* page's URL.
func isURLShapedTitle(title string) bool {
	t := strings.TrimSpace(title)
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")
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
	if misplacedFrontmatterRE.MatchString(strings.TrimSpace(p.Body)) {
		issues = append(issues, "possible misplaced front matter at start of markdown body")
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
	if issues == nil {
		return []string{}
	}
	return issues
}

func sourcePagesForValidation(idx *hugosite.SourceIndex, slug, lang string) ([]hugosite.SourcePage, error) {
	if idx == nil {
		return nil, nil
	}
	rawSlug := strings.TrimSpace(slug)
	if err := validateSlugLangConsistency(rawSlug, lang); err != nil {
		return nil, err
	}
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	lang = strings.TrimSpace(lang)
	if slug == "" {
		if lang == "" {
			return idx.ListPages(0, 0), nil
		}
		var filtered []hugosite.SourcePage
		for _, p := range idx.ListPages(0, 0) {
			if p.Lang == lang {
				filtered = append(filtered, p)
			}
		}
		return filtered, nil
	}
	candidates := site.SourceSlugCandidates(strings.Trim(site.NormalizeSlug(rawSlug), "/"))
	if len(candidates) == 0 {
		candidates = []string{slug}
	}
	var matches []hugosite.SourcePage
	for _, p := range idx.ListPages(0, 0) {
		pageSlug := strings.Trim(strings.TrimSpace(p.Slug), "/")
		for _, candidate := range candidates {
			if pageSlug == candidate && (lang == "" || p.Lang == lang) {
				matches = append(matches, p)
				break
			}
		}
	}
	if len(matches) > 0 {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].Slug != matches[j].Slug {
				return matches[i].Slug < matches[j].Slug
			}
			return matches[i].Lang < matches[j].Lang
		})
		return matches, nil
	}
	if lang != "" {
		return nil, fmt.Errorf("content_not_found: no source page matched slug %q for lang %q", slug, lang)
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

// untrackedSourcePageCount (#819) reports how many source pages have no
// git-tracked file, via a single `git ls-files --others` invocation scoped
// to contentRoot rather than one `git show`/status check per page (the
// per-page approach diff_page uses is fine for one slug at a time, but
// get_site_health runs over the whole site and must stay cheap regardless
// of page count). Returns (0, false) — not an error — when git status can't
// be determined at all (content root unset, no git repo, git unavailable),
// so callers can omit the field entirely rather than report a misleading
// zero; diff_page's own get_site_health-independent per-page check already
// establishes the same "no git repo -> fall back gracefully" precedent.
func untrackedSourcePageCount(ctx context.Context, srcIdx *hugosite.SourceIndex, contentRoot string) (int, bool) {
	contentRoot = strings.TrimSpace(contentRoot)
	if srcIdx == nil || contentRoot == "" {
		return 0, false
	}
	gitRoot, err := gitutil.DiscoverRoot(contentRoot)
	if err != nil {
		return 0, false
	}
	relContentRoot, err := filepath.Rel(gitRoot, contentRoot)
	if err != nil {
		return 0, false
	}
	out, err := gitutil.Output(ctx, gitRoot, "ls-files", "--others", "--exclude-standard", "--", filepath.ToSlash(relContentRoot))
	if err != nil {
		return 0, false
	}
	untracked := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		untracked[filepath.Join(gitRoot, filepath.FromSlash(line))] = true
	}
	count := 0
	for _, p := range srcIdx.ListPages(0, 0) {
		if untracked[p.FilePath] {
			count++
		}
	}
	return count, true
}

func buildSiteHealth(ctx context.Context, idx *site.Index, srcIdx *hugosite.SourceIndex, aliases map[string]string, cfg config.Config, siteDB *db.DB) contentEnvelopeData {
	health := contentEnvelopeData{
		Status: "healthy",
		// See renderedSEOCoverageDTO's own doc comment for why this is
		// always false rather than computed — it must never be silently
		// omitted the way a genuinely optional/not-yet-computed field
		// would be, since this response's score is never influenced by
		// rendered/SEO checks at all, and an agent needs that to be
		// explicit every single call, not just discoverable in prose.
		RenderedSEOCoverage: &renderedSEOCoverageDTO{
			Aggregated:        false,
			Reason:            "rendered_html_checks_not_computed_here_see_inspect_rendered",
			AuthoritativeTool: "inspect_rendered",
		},
	}
	snapshot := buildstatus.Last()
	if snapshot.Attempted {
		degraded := snapshot.Status == "failed"
		health.RuntimeDegraded = &degraded
		if degraded {
			reason := "last_build_failed"
			if snapshot.ErrorClass != "" {
				reason += ":" + snapshot.ErrorClass
			}
			health.RuntimeDegradedReasons = append(health.RuntimeDegradedReasons, reason)
		}
	}
	if count, ok := untrackedSourcePageCount(ctx, srcIdx, cfg.ContentRoot); ok {
		health.UntrackedSourcePages = &count
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
			if isURLShapedTitle(p.Title) {
				health.BadTitleShapePages = append(health.BadTitleShapePages, p.Slug)
			}
		}
		if idx != nil {
			now := time.Now()
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			for _, p := range pages {
				if isSectionIndexSource(p) {
					health.SectionIndexPages++
					continue
				}
				if !sourceExpectedInPublic(p, now) {
					continue
				}
				health.PublishableSourcePages++
				if !sourceHasPublicOutput(idx, resolver, p) {
					health.MissingPublicPages++
				}
			}
			complete := health.MissingPublicPages == 0
			health.PublishableContentPages = health.PublishableSourcePages
			health.PublicOutputComplete = &complete
			health.PublicationCoverage = &publicationCoverageDTO{
				SourceDocuments:                health.SourcePages,
				PublishableContentSources:      health.PublishableSourcePages,
				SectionIndexSources:            health.SectionIndexPages,
				OtherExcludedSources:           health.SourcePages - health.PublishableSourcePages - health.SectionIndexPages,
				PublishedContentPages:          health.PublishedPages,
				MissingPublishableContentPages: health.MissingPublicPages,
				CompletenessBasis:              "publishable_content_sources",
				CountersDirectlyComparable:     false,
				Complete:                       complete,
			}
			if !complete {
				degraded := true
				health.RuntimeDegraded = &degraded
				health.RuntimeDegradedReasons = append(health.RuntimeDegradedReasons, "public_output_incomplete")
			}
		}
		details := detectTaxonomyInconsistencies(srcIdx, aliases)
		health.TaxonomyInconsistencyDetails = details
		for _, d := range details {
			health.TaxonomyInconsistencies = append(health.TaxonomyInconsistencies, d.Message)
		}
	}

	// #1105: broken-link volume only ever feeds this score when db_path is
	// configured — siteDB.GetBrokenLinks() reads the pre-computed link graph
	// (O(1)), the same source get_broken_links's own db_path path uses. A nil
	// siteDB (no db_path) deliberately never falls back to the full-HTML-rescan
	// collectBrokenLinks: that cost is acceptable for an explicit
	// get_broken_links call, not for every get_site_health call.
	var brokenLinksCount *int
	if siteDB != nil {
		if dbLinks, err := siteDB.GetBrokenLinks(); err == nil {
			n := len(dbLinks)
			brokenLinksCount = &n
		}
	}
	health.BrokenLinksCount = brokenLinksCount

	// score_breakdown (#419) is presentation only — it must not change what
	// `score` itself was computed from (that's the pre-existing formula
	// below, byte-for-byte). frontmatter carries 100% of the weight because
	// it's the only category this formula has ever penalized; taxonomy and
	// title_shape (#1105) both carry 0% weight: neither moves `score`
	// through the weighted-score formula, matching the pre-existing taxonomy
	// design (see #719/#1066's healthy_with_advisories/99-cap pattern
	// below). A URL-shaped title instead forces `status` directly, the same
	// way RuntimeDegraded does — see the status computation below — because
	// a content defect this severe (a title field is a raw URL, not text;
	// #1099's grav-csp-nonce incident) must never be reported as "healthy"
	// regardless of what a numeric score says. title_shape.score is shown
	// for reference only, same as taxonomy.score.
	const frontmatterWeight, titleShapeWeight, taxonomyWeight = 100, 0, 0
	frontmatterPenalty := (health.ValidationErrors * 10) + (health.MissingTitles * 5) + (health.MissingDates * 5)
	frontmatterScore := clampScore(100 - frontmatterPenalty)

	// Any single URL-shaped title zeroes this category's own score — unlike
	// frontmatter's linear per-issue penalty, this is a binary structural
	// defect (the title field holds a URL instead of text) with no natural
	// "how bad" gradient, and #1099 showed exactly one such page is already
	// bad enough that reporting anything but 0 here would understate it.
	titleShapeScore := 100
	if len(health.BadTitleShapePages) > 0 {
		titleShapeScore = 0
	}

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
		TitleShape:  scoreCategoryDTO{Score: titleShapeScore, Weight: titleShapeWeight, Issues: len(health.BadTitleShapePages)},
	}
	if brokenLinksCount != nil {
		// Binary, same as title_shape: any broken link at all is a real
		// content defect, not a gradient to score linearly — mirrors #1101's
		// framing that a nonzero count on a previously-clean site (baseline
		// near 0) is exactly the signal that mattered during #1099/#1105's
		// own incident.
		brokenLinksScore := 100
		if *brokenLinksCount > 0 {
			brokenLinksScore = 0
		}
		health.ScoreBreakdown.BrokenLinks = &scoreCategoryDTO{Score: brokenLinksScore, Weight: 0, Issues: *brokenLinksCount}
	}
	// AdvisoriesCount deliberately counts every taxonomy finding regardless
	// of severity (taxonomyWarnings + taxonomyAdvisories == len(details)),
	// NOT just score_breakdown.taxonomy.advisories (info-severity only,
	// #419/#577's established meaning for that sub-field). Both info
	// (translation_pair) and warning (casing_variant/alias_mismatch/
	// possible_duplicate) findings remain visible here for operators who
	// want the full picture, even though only warning-severity findings now
	// degrade the top-level healthy/healthy_with_advisories status (#761).
	// Using the narrower info-only definition would have reported 0 for the
	// exact casing_variant case #591 was filed to catch.
	health.AdvisoriesCount = len(health.TaxonomyInconsistencyDetails)
	health.ActionableTaxonomyFindingsCount = taxonomyWarnings
	health.TranslationPairsDetected = taxonomyAdvisories

	// #1105: score is now the weighted combination of frontmatter and
	// title_shape (taxonomy stays informational, weight 0, per the comment
	// on the weights above). With no title-shape issues this reduces to the
	// pre-existing frontmatterScore exactly, since frontmatterWeight +
	// titleShapeWeight == 100 and titleShapeScore == 100.
	score := (frontmatterScore*frontmatterWeight + titleShapeScore*titleShapeWeight) / 100
	// #719/#1066: a perfect 100 alongside either actionable taxonomy drift
	// or a failed build_site attempt is semantically misleading. Keep
	// info-only translation pairs non-penalizing, and don't cap for
	// public_output_incomplete alone — that reason fires for perfectly
	// normal create_page -> build_site windows and is already surfaced via
	// status/runtime_degraded_reasons without needing to move score too.
	if score == 100 && taxonomyWarnings > 0 {
		score = 99
	}
	if score == 100 && snapshot.Attempted && snapshot.Status == "failed" {
		score = 99
	}
	// #1105: a URL-shaped title is a stronger defect than taxonomy drift by
	// this same "perfect 100 is misleading" logic — status is already forced
	// off healthy below, so this keeps score consistent with that.
	if score == 100 && len(health.BadTitleShapePages) > 0 {
		score = 99
	}
	// #1105: same "perfect 100 is misleading" cap for a nonzero broken-link
	// count, when we were actually able to check (db_path configured).
	if score == 100 && brokenLinksCount != nil && *brokenLinksCount > 0 {
		score = 99
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
	if health.Status == "healthy" && taxonomyWarnings > 0 {
		health.Status = "healthy_with_advisories"
	}
	// A URL-shaped title is a content defect, not an operational one — it
	// must never be masked as "healthy"/"healthy_with_advisories" regardless
	// of where the weighted score lands (#1105: this is the exact case
	// get_site_health silently passed as healthy/100 during #1099).
	if len(health.BadTitleShapePages) > 0 && (health.Status == "healthy" || health.Status == "healthy_with_advisories") {
		health.Status = "degraded"
	}
	// #1105: resolves this issue's own design question — broken-link volume
	// does feed get_site_health, but only as a status override (same
	// treatment as title_shape), never by folding link-graph scoring into the
	// weighted score itself. That keeps get_broken_links's link-graph
	// resolution logic as the single source of truth this tool defers to
	// (avoiding the "quiet coupling" #1101 already showed drifts easily),
	// while still closing the exact gap #1099 exposed: a previously-clean
	// site (baseline near 0) suddenly showing broken links must not report
	// healthy.
	if brokenLinksCount != nil && *brokenLinksCount > 0 && (health.Status == "healthy" || health.Status == "healthy_with_advisories") {
		health.Status = "degraded"
	}
	health.ContentStatus = health.Status
	if health.RuntimeDegraded != nil && *health.RuntimeDegraded {
		health.Status = "degraded"
	}
	return health
}

func sourceExpectedInPublic(p hugosite.SourcePage, now time.Time) bool {
	// Hugo section/homepage bundles (_index.md, _index.<lang>.md at any
	// directory depth) route to their section's own URL (e.g. "/", "/posts/"),
	// not to a slug derived from the bundle's own filename — SlugFromRel gives
	// them a literal slug like "_index.en" that the public index never uses.
	// Checking these against the resolver as if they were ordinary content
	// pages always "fails" even when the section is fully published, which
	// would flip get_site_health to degraded on every real site that uses
	// Hugo section indexes at all.
	if isSectionIndexSource(p) {
		return false
	}
	if p.Draft {
		return false
	}
	if !p.PublishDate.IsZero() && p.PublishDate.After(now) {
		return false
	}
	if !p.ExpiryDate.IsZero() && !p.ExpiryDate.After(now) {
		return false
	}
	if raw, ok := p.FrontmatterRaw["headless"]; ok {
		if headless, ok := raw.(bool); ok && headless {
			return false
		}
	}
	if build, ok := p.FrontmatterRaw["_build"].(map[string]any); ok {
		render := strings.ToLower(strings.TrimSpace(frontmatterStringValue(build["render"])))
		if render == "never" || render == "link" {
			return false
		}
	}
	return true
}

func isSectionIndexSource(p hugosite.SourcePage) bool {
	return strings.HasPrefix(filepath.Base(p.FilePath), "_index.")
}

func sourceHasPublicOutput(idx *site.Index, resolver *site.PageResolver, p hugosite.SourcePage) bool {
	resolved, ok := resolver.ResolveWithLang(p.Slug, p.Lang)
	if ok && resolved.Public != nil {
		return true
	}
	customURL := strings.TrimSpace(frontmatterStringValue(p.FrontmatterRaw["url"]))
	if customURL == "" {
		return false
	}
	public, ok := idx.GetBySlug(customURL)
	if !ok {
		return false
	}
	return p.Lang == "" || public.Lang == "" || p.Lang == public.Lang
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
	// tagRawForms/catRawForms track, per slug, every distinct raw spelling
	// seen and which languages used it — the same-slug rows that
	// possible_duplicate/translation_pair never see, since Slug() already
	// collapses casing before those two ever look at a term (#577).
	tagRawForms := map[string]map[string]map[string][]string{}
	catRawForms := map[string]map[string]map[string][]string{}
	for _, p := range pages {
		for _, t := range p.Tags {
			s := taxonomy.Slug(t)
			tagPages[s] = append(tagPages[s], p.Slug)
			if tagOccurrence[s] == nil {
				tagOccurrence[s] = map[string]string{}
			}
			tagOccurrence[s][p.Slug] = p.Lang
			recordRawForm(tagRawForms, s, t, p.Lang, p.Slug)
		}
		for _, c := range p.Categories {
			s := taxonomy.Slug(c)
			catPages[s] = append(catPages[s], p.Slug)
			if catOccurrence[s] == nil {
				catOccurrence[s] = map[string]string{}
			}
			catOccurrence[s][p.Slug] = p.Lang
			recordRawForm(catRawForms, s, c, p.Lang, p.Slug)
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

	out = append(out, detectCasingVariants(tagRawForms, "tag")...)
	out = append(out, detectCasingVariants(catRawForms, "category")...)

	for i := range out {
		out[i].Severity = taxonomyFindingSeverity(out[i].Kind)
	}
	return out
}

// recordRawForm records that raw was used, in lang, on page slug — indexed
// under its normalized taxonomy.Slug (#577).
func recordRawForm(dst map[string]map[string]map[string][]string, slug, raw, lang, pageSlug string) {
	if dst[slug] == nil {
		dst[slug] = map[string]map[string][]string{}
	}
	if dst[slug][raw] == nil {
		dst[slug][raw] = map[string][]string{}
	}
	dst[slug][raw][lang] = append(dst[slug][raw][lang], pageSlug)
}

// detectCasingVariants finds normalized-slug groups with more than one
// distinct raw spelling sharing at least one language in common (#577):
// e.g. "Infrastructure" and "infrastructure" both used on English pages.
// This is a different, previously undetected failure mode from
// possible_duplicate/translation_pair above, which only ever compare
// *different* slugs (Slug() already collapses casing before those two
// checks run, so two same-slug spellings never even reach them) — and from
// translation_pair, which is two *different* words used per language, not
// the same word spelled two ways within one language. A pair of raw forms
// used only in disjoint languages (never overlapping) is left alone: that
// could be a deliberate per-language style choice, not necessarily a bug.
func detectCasingVariants(rawForms map[string]map[string]map[string][]string, noun string) []taxonomyInconsistencyDTO {
	var out []taxonomyInconsistencyDTO
	slugs := make([]string, 0, len(rawForms))
	for s := range rawForms {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		forms := rawForms[slug]
		if len(forms) < 2 {
			continue
		}
		rawList := make([]string, 0, len(forms))
		for r := range forms {
			rawList = append(rawList, r)
		}
		sort.Strings(rawList)
		for i := 0; i < len(rawList); i++ {
			for j := i + 1; j < len(rawList); j++ {
				a, b := rawList[i], rawList[j]
				sharedLang := false
				for lang := range forms[a] {
					if _, ok := forms[b][lang]; ok {
						sharedLang = true
						break
					}
				}
				if !sharedLang {
					continue
				}
				pagesA := dedupSortedPages(forms[a])
				pagesB := dedupSortedPages(forms[b])
				out = append(out, taxonomyInconsistencyDTO{
					Message:        fmt.Sprintf("%s %q and %q are the same term spelled differently, both used within the same language — normalize to one spelling", noun, a, b),
					TermA:          a,
					TermB:          b,
					PagesWithTermA: pagesA,
					PagesWithTermB: pagesB,
					Kind:           "casing_variant",
				})
			}
		}
	}
	return out
}

// dedupSortedPages flattens a raw form's per-language page-slug lists into
// one deduplicated, sorted slice — a bundle whose index.en.md/index.fr.md
// both carry the exact same raw spelling would otherwise list that page
// slug twice (once per language) in a casing_variant finding.
func dedupSortedPages(byLang map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pages := range byLang {
		for _, p := range pages {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
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

func toPageDTO(p site.Page, aliases map[string]string, siteRoot string, includeTerms bool) pageDTO {
	tags := taxonomy.ApplyAliases(nullsafeStrings(p.Tags), aliases)
	cats := taxonomy.ApplyAliases(nullsafeStrings(p.Categories), aliases)
	dto := pageDTO{
		Slug:       p.Slug,
		Title:      p.Title,
		Summary:    p.Summary,
		Tags:       canonicalTaxonomyStrings(tags),
		Categories: canonicalTaxonomyStrings(cats),
		Date:       p.Date,
		URL:        p.URL,
		Lang:       p.Lang,
		State:      site.StateForResolvedPage(site.ResolvedPage{Public: &p}, siteRoot),
	}
	if includeTerms {
		dto.TagTerms = site.NormalizeTaxonomyTerms(tags)
		dto.CategoryTerms = site.NormalizeTaxonomyTerms(cats)
	}
	return dto
}

func toPageDTOs(pages []site.Page, aliases map[string]string, srcIdx *hugosite.SourceIndex, contentRoot, siteRoot string, includeTerms bool) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot, includeTerms)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot, includeTerms)
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
func toPageDTOsEnriched(pages []site.Page, srcIdx *hugosite.SourceIndex, aliases map[string]string, contentRoot, siteRoot string, includeTerms bool) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot, includeTerms)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot, includeTerms)
		out[i] = dto
	}
	return out
}

func toPageDTOsWithSnippets(pages []site.Page, aliases map[string]string, snippets map[string]string, srcIdx *hugosite.SourceIndex, contentRoot, siteRoot string, includeTerms bool) []pageDTO {
	lookup := newSourceLookup(srcIdx)
	out := make([]pageDTO, len(pages))
	for i, p := range pages {
		dto := toPageDTO(p, aliases, siteRoot, includeTerms)
		enrichPageDTOFromSource(&dto, p, lookup, aliases, contentRoot, siteRoot, includeTerms)
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

func enrichPageDTOFromSource(dto *pageDTO, p site.Page, lookup *sourceLookup, aliases map[string]string, contentRoot, siteRoot string, includeTerms bool) {
	if dto == nil || lookup == nil {
		return
	}
	if match, ok := resolveSourceForPage(p, lookup); ok {
		src := match.Page
		dto.Categories = taxonomy.ApplyAliases(nullsafeStrings(src.Categories), aliases)
		if includeTerms {
			dto.CategoryTerms = site.NormalizeTaxonomyTerms(dto.Categories)
		} else {
			dto.CategoryTerms = nil
		}
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
