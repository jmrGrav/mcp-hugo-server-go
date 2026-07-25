package write

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/cloudflare"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

type createPageInput struct {
	Slug                    string   `json:"slug"`
	Lang                    string   `json:"lang,omitempty"`
	Title                   string   `json:"title"`
	Body                    string   `json:"body"`
	Tags                    []string `json:"tags"`
	Categories              []string `json:"categories"`
	NormalizeTaxonomyCasing bool     `json:"normalize_taxonomy_casing,omitempty"`
	IdempotencyKey          string   `json:"idempotency_key,omitempty"`
	DryRun                  bool     `json:"dry_run,omitempty"`
	// TestContent (#661) is a deliberate, explicit opt-in — never inferred
	// from slug/title — marking this page as disposable test/audit content.
	// When set, the page is always created with draft:true regardless of
	// any other setting, and frontmatter records test_content:true,
	// test_content_owner (if given), and test_content_expires_at (now +
	// TTLHours, default 24h). #608's post-build stale-content check honors
	// test_content_expires_at unconditionally, independent of the
	// server-wide stale_test_content_threshold_hours setting — the caller
	// explicitly asked for TTL tracking on this specific page, so it must
	// work even when the server-wide sweep is disabled.
	TestContent *testContentInput `json:"test_content,omitempty"`
}

// testContentInput is create_page's opt-in test-content marker (#661).
// TTLHours <= 0 means "use the default" (24h); a negative value is
// rejected as invalid_params the same way negative limit values are
// elsewhere in this repo (#641) — zero/omitted is a legitimate "use
// default," a genuinely negative value is a caller-side bug.
type testContentInput struct {
	TTLHours int    `json:"ttl_hours,omitempty"`
	Owner    string `json:"owner,omitempty"`
}

const testContentDefaultTTLHours = 24

type createPageOutput struct {
	toolcontract.ToolResponse[createPageData]
	// RequestContext echoes the caller's normalized slug/lang on failure
	// (#455) — always populated by toolcontract.WrapTool when the handler
	// wraps its error via toolcontract.WithRequestContext. This is an
	// error-path-only field, not a success-payload duplicate, so it survives
	// the root/data convergence below (#520).
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
	// RateLimitRemaining is intentionally still mirrored at the root on both
	// success and error (#466, #510, #522) — a documented, deliberately kept
	// exception to the root/data duplication removed here (#520), not an
	// oversight: it lets an agent self-regulate pacing from the root alone
	// without inspecting data on every call.
	RateLimitRemaining int `json:"rate_limit_remaining"`
}

type createPageData struct {
	Status             string               `json:"status,omitempty"`
	Slug               string               `json:"slug,omitempty"`
	SourceKey          string               `json:"source_key,omitempty"`
	Path               string               `json:"path,omitempty"`
	ResolvedLang       *string              `json:"resolved_lang,omitempty"`
	ResolvedSourcePath *string              `json:"resolved_source_path,omitempty"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	Content            string               `json:"content,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	NewRevision        string               `json:"new_revision,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
	// TaxonomyCasingNormalized/TaxonomyCasingAmbiguous — see the comment on
	// updatePageData's fields of the same name (#589); create_page shares
	// the identical normalize_taxonomy_casing contract.
	TaxonomyCasingNormalized []taxonomyCasingChangeDTO  `json:"taxonomy_casing_normalized,omitempty"`
	TaxonomyCasingAmbiguous  []taxonomyCasingSkippedDTO `json:"taxonomy_casing_ambiguous,omitempty"`
	// TestContentExpiresAt echoes the effective test_content expiry (#661)
	// back to the caller when the request opted in via `test_content` —
	// confirms the TTL actually applied (in case ttl_hours was omitted and
	// the default was used) without requiring a follow-up read.
	TestContentExpiresAt string `json:"test_content_expires_at,omitempty"`
	// RateLimitRemaining is deliberately never set on a success response
	// (#520, #605) — see the comment on newCreatePageOutput. omitempty (not
	// present in prior success responses' JSON either, since it was always
	// explicitly set) keeps the field itself in the schema so the error path
	// below, which populates it via toolcontract.WithDataFields as a
	// documented root+data duplication (#466/#510/#522), remains valid
	// against the same shared OutputSchema.
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
}

type updatePageInput struct {
	Slug                    string   `json:"slug"`
	Lang                    string   `json:"lang,omitempty"`
	Title                   string   `json:"title,omitempty"`
	Body                    string   `json:"body,omitempty"`
	Tags                    []string `json:"tags,omitempty"`
	Categories              []string `json:"categories,omitempty"`
	Draft                   *bool    `json:"draft,omitempty"`
	Description             string   `json:"description,omitempty"`
	NormalizeTaxonomyCasing bool     `json:"normalize_taxonomy_casing,omitempty"`
	ExpectedRevision        string   `json:"expected_revision,omitempty"`
	IdempotencyKey          string   `json:"idempotency_key,omitempty"`
	DryRun                  bool     `json:"dry_run,omitempty"`
}

type updatePageOutput struct {
	toolcontract.ToolResponse[updatePageData]
	// RequestContext — see the comment on createPageOutput.RequestContext.
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
	// RateLimitRemaining — see the comment on createPageOutput.RateLimitRemaining (#466, #520).
	RateLimitRemaining int `json:"rate_limit_remaining"`
}

type updatePageData struct {
	Status             string               `json:"status,omitempty"`
	Slug               string               `json:"slug,omitempty"`
	SourceKey          string               `json:"source_key,omitempty"`
	ResolvedLang       *string              `json:"resolved_lang,omitempty"`
	ResolvedSourcePath *string              `json:"resolved_source_path,omitempty"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	Diff               string               `json:"diff,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	NewRevision        string               `json:"new_revision,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
	// TaxonomyCasingNormalized lists tags/categories rewritten to match a
	// casing already present elsewhere in the index (#589), populated only
	// when the caller opted in via normalize_taxonomy_casing. Present only
	// on non-dry-run success; dry_run previews the diff but does not
	// resolve casing, so this stays empty on a dry-run response.
	TaxonomyCasingNormalized []taxonomyCasingChangeDTO `json:"taxonomy_casing_normalized,omitempty"`
	// TaxonomyCasingAmbiguous lists tags/categories left exactly as typed
	// because the index already has more than one distinct casing for that
	// term (pre-existing drift, the #577 casing_variant scenario) —
	// normalize_taxonomy_casing never guesses which of several existing
	// spellings is correct.
	TaxonomyCasingAmbiguous []taxonomyCasingSkippedDTO `json:"taxonomy_casing_ambiguous,omitempty"`
	// TagsDelta/CategoriesDelta give update_page's whole-list-replacement
	// tags/categories the same "which parts of my request would actually
	// apply" visibility plan_content_change's operations_applied/rejected
	// gives its add_tag/remove_tag vocabulary (#645) — narrowly scoped to
	// tags/categories specifically, since update_page already computes a
	// delta-like signal for them (normalize_taxonomy_casing's rewrite
	// detection), unlike title/body/draft/description, which stay a single
	// whole-value diff. Populated whenever the caller explicitly includes
	// `tags`/`categories` in the request (even an empty list, meaning
	// "clear them all") — omitted when the key is left out entirely,
	// matching applyPageUpdates's own nil-means-unchanged contract. A pure,
	// cheap list comparison against the existing page's current tags/
	// categories (post taxonomy-casing-normalization, if requested) — no
	// index lookups beyond what normalize_taxonomy_casing already does — so
	// unlike TaxonomyCasingNormalized/TaxonomyCasingAmbiguous this is always
	// populated on both dry_run and a real write, not withheld on either.
	TagsDelta       *taxonomyDeltaDTO `json:"tags_delta,omitempty"`
	CategoriesDelta *taxonomyDeltaDTO `json:"categories_delta,omitempty"`
	// RateLimitRemaining — see the comment on createPageData's field of the
	// same name (#520, #605).
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
}

// taxonomyDeltaDTO reports which tags/categories a whole-list-replacement
// update_page call actually adds, removes, or leaves unchanged relative to
// the page's current value (#645) — each list omitted when empty, so an
// unchanged field's delta reads as an empty object rather than three empty
// arrays.
type taxonomyDeltaDTO struct {
	Added     []string `json:"added,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
}

// computeTaxonomyDelta compares old (the page's current value) against
// next (the value that will actually be written, post any taxonomy-casing
// normalization) and reports the per-term outcome. Order-independent,
// duplicate-safe (a term appearing twice in either list is reported once).
func computeTaxonomyDelta(old, next []string) taxonomyDeltaDTO {
	oldSet := make(map[string]bool, len(old))
	for _, v := range old {
		oldSet[v] = true
	}
	nextSet := make(map[string]bool, len(next))
	for _, v := range next {
		nextSet[v] = true
	}
	var d taxonomyDeltaDTO
	seen := make(map[string]bool, len(next))
	for _, v := range next {
		if seen[v] {
			continue
		}
		seen[v] = true
		if oldSet[v] {
			d.Unchanged = append(d.Unchanged, v)
		} else {
			d.Added = append(d.Added, v)
		}
	}
	seenOld := make(map[string]bool, len(old))
	for _, v := range old {
		if seenOld[v] || nextSet[v] {
			continue
		}
		seenOld[v] = true
		d.Removed = append(d.Removed, v)
	}
	return d
}

type deletePageInput struct {
	Slug             string `json:"slug"`
	Lang             string `json:"lang,omitempty"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	DryRun           bool   `json:"dry_run,omitempty"`
	ResponseMode     string `json:"response_mode,omitempty"`
}

type deletePageBacklinkDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type deletePageOutput struct {
	toolcontract.ToolResponse[deletePageData]
	// RequestContext echoes the caller's normalized slug on failure (#455)
	// — see the comment on createPageOutput.RequestContext.
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
	// RateLimitRemaining — see the comment on createPageOutput.RateLimitRemaining (#466, #520).
	RateLimitRemaining int `json:"rate_limit_remaining"`
}

type deletePageData struct {
	Status             string                   `json:"status,omitempty"`
	Slug               string                   `json:"slug,omitempty"`
	SourceKey          string                   `json:"source_key,omitempty"`
	ResolvedLang       *string                  `json:"resolved_lang,omitempty"`
	ResolvedSourcePath *string                  `json:"resolved_source_path,omitempty"`
	DryRun             bool                     `json:"dry_run,omitempty"`
	Content            string                   `json:"content,omitempty"`
	Backlinks          *[]deletePageBacklinkDTO `json:"backlinks,omitempty"`
	// BacklinksCount is only ever populated on a dry_run response (compact
	// or not) — it's a pointer with omitempty so it stays entirely absent
	// from a real (non-dry-run) delete's response, where no backlink scan
	// ever ran and a bare 0 would misleadingly read as "verified zero
	// backlinks" rather than "not computed" (#687).
	BacklinksCount *int                 `json:"backlinks_count,omitempty"`
	Warning        string               `json:"warning,omitempty"`
	State          *site.LifecycleState `json:"state,omitempty"`
	// BundleFullyRemoved (#682) is true when the entire page bundle
	// directory (every language file, plus any shared assets) was removed —
	// either because no lang was in play (no source file at all) or because
	// the deleted language was the last one remaining. false means only the
	// single resolved language's source file was removed and the bundle
	// (other language(s), assets) still exists on disk.
	BundleFullyRemoved bool `json:"bundle_fully_removed,omitempty"`
	// RateLimitRemaining — see the comment on createPageData's field of the
	// same name (#520, #605).
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
}

// strPtr distinguishes "resolved to the empty string" (e.g. the default
// language, which legitimately has no lang code) from "resolution never
// happened" (#455) — a plain string can't carry that distinction since both
// cases marshal to "", so ResolvedLang/ResolvedSourcePath use *string and are
// only ever set via this helper once resolution actually succeeds.
func strPtr(s string) *string { return &s }

func writeSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

// newCreatePageOutput/newUpdatePageOutput/newDeletePageOutput take
// rateLimitRemaining as an explicit parameter rather than reading it off
// data (#520, #605): data itself never carries this field — it is
// documented as root-only (docs/mcp-contract.md) — so there is nothing to
// mirror from, and the two copies previously silently duplicated the value.
func newCreatePageOutput(data createPageData, rateLimitRemaining int) createPageOutput {
	return createPageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

func newUpdatePageOutput(data updatePageData, rateLimitRemaining int) updatePageOutput {
	return updatePageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

func newDeletePageOutput(data deletePageData, rateLimitRemaining int) deletePageOutput {
	return deletePageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: rateLimitRemaining,
	}
}

// appendLastBuildWarning appends a lightweight advisory to warning (#467) if
// the most recent build_site attempt in this process failed — so an agent
// writing content notices a broken publish pipeline from this write call
// itself, instead of only discovering it by calling build_site at the end of
// its write cycle. Never blocks the write; purely advisory. Existing/empty
// warning is preserved and the two messages are combined if both are set.
func appendLastBuildWarning(warning string) string {
	snap := buildstatus.Last()
	if !snap.Attempted || snap.Status != "failed" {
		return warning
	}
	advisory := fmt.Sprintf("the last build_site attempt failed (%s) — this write succeeded but may not go live until build_site is retried", snap.ErrorClass)
	if warning == "" {
		return advisory
	}
	return warning + "; " + advisory
}

// normalizeInputSlug canonicalizes the two supported caller forms:
//   - posts/foo
//   - /posts/foo/
//
// It deliberately does NOT rewrite a lone-leading-slash form like
// "/tmp/escape" or "/posts/foo" into a valid source slug. Those inputs are
// left intact so the existing slug/path validators reject them fail-closed
// instead of silently normalizing a hostile absolute-looking path into a
// writable relative slug (#691).
func normalizeInputSlug(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") {
		return s
	}
	return strings.Trim(s, "/")
}

// canonicalPublicSlug converts a source-relative slug ("posts/x") to the
// canonical public-route form ("/posts/x/") already used by read tools
// (read.canonicalSourceSlug, #519). Write tools' success responses use this
// for the public-facing Slug field while SourceKey keeps the raw
// source-relative form (#554) — the same distinction read tools already
// draw between source_key and slug.
//
// This is a draft-scope port: it does not yet special-case Hugo section
// index pages (_index.md) the way read.canonicalSourceSlug does, since none
// of the four write tools in scope here (create_page/update_page/
// upload_page_asset/delete_page) can target a section index. If that
// changes, share the logic with read.canonicalSourceSlug instead of
// duplicating the section-index handling here.
func canonicalPublicSlug(sourceSlug string) string {
	slug := strings.Trim(sourceSlug, "/")
	if slug == "" {
		return ""
	}
	return "/" + slug + "/"
}

var reservedSlugs = map[string]bool{
	"_index": true,
	"index":  true,
}

var validLangPattern = regexp.MustCompile(`^[A-Za-z0-9-]{2,5}$`)

// mutationCallerKey builds a rate-limit key that isolates mutation budgets
// by caller IP. Falls back to "unknown" when context carries no IP (e.g. in
// tests). Shared by every per-tool-class caller limiter (delete, create/
// update/upload) — same identity signal, same "IP is the only caller
// identity available in context today" limitation noted in #378.
func mutationCallerKey(ctx context.Context) string {
	ip, _ := ctx.Value(oauth.CtxCallerIP).(string)
	if ip == "" {
		ip = "unknown"
	}
	return ip
}

// idempotencyCallerKey isolates the idempotency store by requesting bearer
// token (#627): distinct tokens always belong to distinct OAuth clients (or
// distinct sessions of the same client), so keying on the token hash already
// carried in context (oauth.CtxTokenID) closes the cross-client leak where
// any caller could look up or replay another caller's idempotency-key
// result via the same tool+key. Falls back to a shared "" bucket when OAuth
// is disabled entirely (no bearer, no isolation boundary to enforce) or in
// tests that don't populate the context value.
func idempotencyCallerKey(ctx context.Context) string {
	id, _ := ctx.Value(oauth.CtxTokenID).(string)
	return id
}

// callerLimiter returns (or creates) a per-caller rate.Limiter allowing
// perMinute calls/minute with a burst equal to perMinute, generalizing the
// pattern originally hardcoded to delete_page's 5/min. perMinute <= 0
// disables the limiter (Allow always returns true) rather than dividing by
// zero, so a zero-valued/unset config field fails open instead of panicking
// — callers that want a hard deny-by-default should set an explicit
// positive value.
func callerLimiter(mu *sync.Mutex, m map[string]*rate.Limiter, key string, perMinute int) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := m[key]; ok {
		return l
	}
	var l *rate.Limiter
	if perMinute <= 0 {
		l = rate.NewLimiter(rate.Inf, 0)
	} else {
		l = rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), perMinute)
	}
	m[key] = l
	return l
}

// rateLimitRemaining reports the caller's current available quota on l,
// rounded down to a whole call (#466) — surfaced directly in tool responses
// so an agent can self-regulate pacing instead of inferring a safe rate from
// the tool description alone. l.Tokens() is a pure read (it doesn't mutate
// limiter state), so this is safe to call after Allow() without disturbing
// the budget it just consumed.
func rateLimitRemaining(l *rate.Limiter) int {
	if l == nil {
		return 0
	}
	tokens := l.Tokens()
	if tokens < 0 {
		return 0
	}
	return int(tokens)
}

// rateLimitRetryAfterSeconds reports how long the caller must wait before
// its next call to l would succeed, or 0 if a call would succeed now (#466).
func rateLimitRetryAfterSeconds(l *rate.Limiter) float64 {
	if l == nil {
		return 0
	}
	tokens := l.Tokens()
	if tokens >= 1 {
		return 0
	}
	limit := float64(l.Limit())
	if limit <= 0 {
		return 0
	}
	wait := (1 - tokens) / limit
	if wait < 0 {
		return 0
	}
	return wait
}

// rateLimitExceededErr builds the rate_limit_exceeded error for tool,
// embedding retry_after_seconds in the message (#466) so
// toolcontract.ParseToolError can surface it in ErrorResolution without a
// separate error-wrapping mechanism — the same message-embedding convention
// already used for allowed-values parsing.
func rateLimitExceededErr(tool string, perMinute int, l *rate.Limiter) error {
	return fmt.Errorf("rate_limit_exceeded: %s is limited to %d per minute (retry_after_seconds=%.1f)", tool, perMinute, rateLimitRetryAfterSeconds(l))
}

func validateLangParam(lang string) (string, error) {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "", nil
	}
	if !validLangPattern.MatchString(lang) || strings.Contains(lang, "..") || strings.ContainsAny(lang, `/\`) {
		return "", fmt.Errorf("invalid_params: lang must be a simple language code")
	}
	return lang, nil
}

func Register(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, siteDB *db.DB, siteIdxs ...*site.Index) {
	var siteIdx *site.Index
	if len(siteIdxs) > 0 {
		siteIdx = siteIdxs[0]
	}
	if s == nil {
		return
	}

	var deleteMu sync.Mutex
	deleteLimiters := make(map[string]*rate.Limiter)
	// mutationMu/mutationLimiters (#378): a separate per-caller budget for
	// create_page/update_page/upload_page_asset, layered on top of (not
	// instead of) the existing per-scope-per-IP content.write limit in
	// internal/oauth's RateLimiter — that one is a single shared budget
	// across every content.write tool, this one mirrors delete_page's own
	// tool-class-scoped defense-in-depth so one operation type can't
	// silently consume another's headroom.
	var mutationMu sync.Mutex
	mutationLimiters := make(map[string]*rate.Limiter)
	idem := newIdempotencyStore(idempotencyTTLFromConfig(cfg), 256)
	plans := newPlanStore(planTTL, planMaxEntries)
	snapshots := newSnapshotStore(snapshotTTL, snapshotMaxEntries)
	registerContentPlanTools(s, pg, idx, cfg, siteDB, siteIdx, &mutationMu, mutationLimiters, idem, plans, snapshots)
	registerRollbackChange(s, pg, idx, cfg, siteDB, siteIdx, &mutationMu, mutationLimiters, idem, snapshots)

	mcp.AddTool(s, &mcp.Tool{
		Name:         "create_page",
		Title:        "Publish page",
		Description:  "Create a new Hugo content page at {slug}/index.md with front matter and body content. Fails with `already_exists` if the destination already exists; use update_page for edits. Repeating the same non-dry-run request normally fails once the page exists, but callers may provide `idempotency_key` to safely replay the exact same create attempt after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether the page only exists in source so far or is already publicly available. Before writing, consider calling suggest_links(tags, categories, body) on your draft to surface internal-linking candidates while the content is still easy to adjust (#623). For disposable test/audit content (e.g. a live audit exercising the write cycle), set `test_content: {ttl_hours?, owner?}` (default `ttl_hours`: 24) — a deliberate, explicit opt-in, never inferred from `slug`/`title` (so a real published page that happens to start with e.g. `codex-` is never wrongly constrained). This forces `draft: true` regardless of any other setting and writes `test_content`/`test_content_owner`/`test_content_expires_at` into the page's own frontmatter; the effective expiry is echoed back in `data.test_content_expires_at`. `build_site`/`publish_changes`'s post-build advisory (#608) honors `test_content_expires_at` unconditionally, independent of the server-wide `stale_test_content_threshold_hours` setting, so cleanup is nagged for even when that server-wide sweep is disabled — it never auto-deletes, only surfaces a warning recommending `delete_page` (#661). IMPORTANT for `normalize_taxonomy_casing`: it is scoped to the *exact* `lang` you pass on this call (or the empty-string bucket if you omit `lang`); on a bilingual site where every real page specifies `lang` explicitly, omitting `lang` here typically no-ops — the empty bucket has no existing forms to match against — so always pass `lang` explicitly when using it (#604, #677). Set `normalize_taxonomy_casing: true` (default off) to rewrite each submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index to that existing spelling — preventing new drift instead of just letting get_site_health report it afterward (#589); rewrites are reported in `data.taxonomy_casing_normalized`, and a term left untouched because the index already has two or more conflicting spellings for it (pre-existing drift, never guessed at) is reported in `data.taxonomy_casing_ambiguous` instead. `body` is rejected with `invalid_params` if it invokes a server-configured blocked shortcode (default: `raw`, `rawhtml`, `script`, `style`) — a best-effort denylist of theme shortcodes known to render unescaped HTML/JavaScript/CSS on the public page, bypassing Hugo's own Markdown-level sanitization; not a guarantee every theme's shortcode surface is safe, and this check cannot be opted out of per call (#590). `rate_limit_remaining` reports the caller's remaining budget on this shared create/update/upload quota (#466); if exceeded, the error's `resolution.retry_after_seconds` gives a concrete wait time instead of forcing you to guess a safe pacing.",
		InputSchema:  tools.MustSchema[createPageInput](),
		OutputSchema: tools.MustSchema[createPageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createPageInput) (*mcp.CallToolResult, createPageOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug, RequestedLang: in.Lang})
		}
		if in.Slug == "" {
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		resolvedLang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, createPageOutput{}, wrapErr(err)
		}
		if in.Title == "" {
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("invalid_params: title must not be empty"))
		}
		if reservedSlugs[in.Slug] {
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("invalid_params: slug is reserved"))
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, createPageOutput{}, wrapErr(err)
		}
		if err := validateTitleFormat(in.Title); err != nil {
			return nil, createPageOutput{}, wrapErr(err)
		}
		if err := validateBodyFormat(in.Body, cfg.BlockedShortcodes); err != nil {
			return nil, createPageOutput{}, wrapErr(err)
		}
		if in.TestContent != nil && in.TestContent.TTLHours < 0 {
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("invalid_params: test_content.ttl_hours must not be negative"))
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(&mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			fields := map[string]any{
				"rate_limit_remaining": rateLimitRemaining(limiter),
			}
			return toolcontract.WithDataFields(toolcontract.WithRootFields(wrapErr(err), fields), fields)
		}
		// Allow() is skipped for dry-run (#588) but otherwise stays at its
		// original position, so every non-dry-run failure path below
		// (already_exists, build_in_progress, etc.) keeps consuming quota
		// exactly as it did before — only the dry-run path changes here.
		if !in.DryRun && !limiter.Allow() {
			return nil, createPageOutput{}, wrapErrWithLimiter(rateLimitExceededErr("create_page", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("create_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("invalid_params: path validation failed"))
		}

		filePath := filepath.Join(dir, "index.md")
		if resolvedLang != "" {
			filePath = filepath.Join(dir, "index."+resolvedLang+".md")
		}
		// normalize_taxonomy_casing (#589) resolves against the index as it
		// stands right now, before this page is written — deliberately
		// computed from the caller's original in.Tags/in.Categories, not
		// reused for the idempotency hash below, so a retry's hash never
		// shifts just because intervening writes changed what "existing
		// casing" means.
		writeTags, writeCategories := in.Tags, in.Categories
		var taxonomyNormalized []taxonomyCasingChangeDTO
		var taxonomyAmbiguous []taxonomyCasingSkippedDTO
		if in.NormalizeTaxonomyCasing {
			var tagChanges, catChanges []taxonomyCasingChangeDTO
			var tagSkipped, catSkipped []taxonomyCasingSkippedDTO
			writeTags, tagChanges, tagSkipped = normalizeTaxonomyCasing(taxonomyRawForms(idx, "tag"), "tag", resolvedLang, in.Tags)
			writeCategories, catChanges, catSkipped = normalizeTaxonomyCasing(taxonomyRawForms(idx, "category"), "category", resolvedLang, in.Categories)
			taxonomyNormalized = append(tagChanges, catChanges...)
			taxonomyAmbiguous = append(tagSkipped, catSkipped...)
		}
		content, testContentExpiresAt := buildFrontmatter(in.Title, writeTags, writeCategories, in.Body, in.TestContent)

		// Round-trip guard: verify the generated content parses correctly.
		if err := validateFrontmatterRoundTrip(content); err != nil {
			return nil, createPageOutput{}, wrapErr(fmt.Errorf("validation_error: %w", err))
		}

		if in.DryRun {
			if _, err := os.Stat(filePath); err == nil {
				return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("already_exists: page already exists at slug %q", in.Slug))
			} else if !os.IsNotExist(err) {
				slog.Error("create_page: dry-run stat failed", "slug", in.Slug, "error", err)
				return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to inspect destination path"))
			}
			logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
			return nil, newCreatePageOutput(createPageData{
				Status:                   "ok",
				Slug:                     canonicalPublicSlug(in.Slug),
				SourceKey:                in.Slug,
				ResolvedLang:             strPtr(resolvedLang),
				ResolvedSourcePath:       strPtr(logicalPath),
				DryRun:                   true,
				Content:                  content,
				TaxonomyCasingNormalized: taxonomyNormalized,
				TaxonomyCasingAmbiguous:  taxonomyAmbiguous,
				TestContentExpiresAt:     testContentExpiresAt,
			}, rateLimitRemaining(limiter)), nil
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("create_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("create_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("create_page: lock_released")
		}()

		// Idempotency replay check must happen under the content lock: two
		// genuinely concurrent retries with the same key (the exact
		// uncertain-delivery scenario this feature protects against) would
		// otherwise both miss the cache before either has a chance to
		// remember its result, and the loser would see already_exists
		// instead of the intended idempotent replay.
		idemHash := ""
		if strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				Slug       string   `json:"slug"`
				Lang       string   `json:"lang,omitempty"`
				Title      string   `json:"title"`
				Body       string   `json:"body"`
				Tags       []string `json:"tags"`
				Categories []string `json:"categories"`
			}{
				Slug:       in.Slug,
				Lang:       resolvedLang,
				Title:      in.Title,
				Body:       in.Body,
				Tags:       in.Tags,
				Categories: in.Categories,
			})
			if hashErr != nil {
				return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached createPageOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "create_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, createPageOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("create_page: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if err := fileutil.AtomicCreateChecked(filePath, content, pg); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("already_exists: page already exists at slug %q", in.Slug))
			}
			slog.Error("create_page: write failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}
		now := time.Now().UTC().Format(time.RFC3339)
		frontmatterRaw := map[string]any{"title": in.Title, "date": now, "draft": in.TestContent != nil}
		if in.TestContent != nil {
			frontmatterRaw["test_content"] = true
			if in.TestContent.Owner != "" {
				frontmatterRaw["test_content_owner"] = in.TestContent.Owner
			}
			frontmatterRaw["test_content_expires_at"] = testContentExpiresAt
		}
		created := hugosite.SourcePage{
			Slug:           in.Slug,
			FilePath:       filePath,
			Lang:           resolvedLang,
			Title:          in.Title,
			Date:           now,
			Tags:           writeTags,
			Categories:     writeCategories,
			Body:           in.Body,
			Draft:          in.TestContent != nil,
			FrontmatterRaw: frontmatterRaw,
			BuildPending:   true,
		}
		idx.Upsert(created)
		// Do NOT insert into the public site index — the page is source-only until
		// Hugo builds it. UpsertPage here would break allow_source_fallback detection.
		status := "ok"
		warning := ""
		if siteDB != nil {
			if err := siteDB.SyncSourcePage(created); err != nil {
				slog.Warn("create_page: db sync failed", "slug", in.Slug, "error", err)
				status = "partial_success"
				warning = fmt.Sprintf("source created but derived DB could not be updated: %v", err)
			}
		}

		state := createPageState()
		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
		out := newCreatePageOutput(createPageData{
			Status:                   status,
			Slug:                     canonicalPublicSlug(in.Slug),
			SourceKey:                in.Slug,
			Path:                     logicalPath,
			ResolvedLang:             strPtr(resolvedLang),
			ResolvedSourcePath:       strPtr(logicalPath),
			NewRevision:              contentmodel.SourceRevisionBytes([]byte(content)),
			Warning:                  appendLastBuildWarning(warning),
			State:                    &state,
			TaxonomyCasingNormalized: taxonomyNormalized,
			TaxonomyCasingAmbiguous:  taxonomyAmbiguous,
			TestContentExpiresAt:     testContentExpiresAt,
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "create_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("create_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:  "update_page",
		Title: "Update page",
		Description: "Update an existing Hugo content page while preserving unspecified front matter fields. " +
			"Use title/body to revise content. Use tags/categories/draft/description to update front matter fields. " +
			"`tags`/`categories` are a whole-list replacement, not an add/remove delta — but the response reports one anyway: `data.tags_delta`/`data.categories_delta` (`added`/`removed`/`unchanged`) compare the submitted list against the page's current value, on both dry_run and a real write, whenever `tags`/`categories` is included in the request at all (an empty list is a valid, explicit \"clear them all\"; omitting the key entirely leaves the field unchanged and reports no delta) (#645). " +
			"For bilingual sites, provide lang (e.g. \"fr\", \"en\") to target the correct language file; " +
			"omitting lang on a page with multiple language files returns an ambiguous_language error listing available langs. " +
			"Non-dry-run calls require `expected_revision`, the `revision` value from a prior read of this page (e.g. get_page); " +
			"a missing value fails with `invalid_params` and a stale value fails with `revision_conflict`, telling the agent to re-read and replan. " +
			"Callers may provide `idempotency_key` to safely replay the exact same non-dry-run update after a timeout or uncertain delivery. " +
			"Successful non-dry-run responses include a `state` object that tells agents whether the source changed ahead of the public build/index state. " +
			"IMPORTANT for `normalize_taxonomy_casing`: it is scoped to the *exact* `lang` you pass on this call (or the empty-string bucket if you omit `lang`); on a bilingual site where every real page specifies `lang` explicitly, omitting `lang` here typically no-ops — the empty bucket has no existing forms to match against — so always pass `lang` explicitly when using it (#604, #677). " +
			"Set `normalize_taxonomy_casing: true` (default off) to rewrite each submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index to that existing spelling — preventing new drift instead of just letting get_site_health report it afterward (#589); rewrites are reported in `data.taxonomy_casing_normalized`, and a term left untouched because the index already has two or more conflicting spellings for it (pre-existing drift, never guessed at) is reported in `data.taxonomy_casing_ambiguous` instead. " +
			"`body` is rejected with `invalid_params` (including on `dry_run`) if it invokes a server-configured blocked shortcode (default: `raw`, `rawhtml`, `script`, `style`) — a best-effort denylist of theme shortcodes known to render unescaped HTML/JavaScript/CSS on the public page, bypassing Hugo's own Markdown-level sanitization; not a guarantee every theme's shortcode surface is safe, and this check cannot be opted out of per call (#590). " +
			"`rate_limit_remaining` reports the caller's remaining budget on this shared create/update/upload quota (#466); if exceeded, the error's `resolution.retry_after_seconds` gives a concrete wait time instead of forcing you to guess a safe pacing.",
		InputSchema:  tools.MustSchema[updatePageInput](),
		OutputSchema: tools.MustSchema[updatePageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in updatePageInput) (*mcp.CallToolResult, updatePageOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug, RequestedLang: in.Lang})
		}
		if in.Slug == "" {
			return nil, updatePageOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		lang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, updatePageOutput{}, wrapErr(err)
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, updatePageOutput{}, wrapErr(err)
		}
		// Title/body are optional on update (empty means "leave unchanged" —
		// see applyPageUpdates), so only validate format when the caller is
		// actually setting a new value.
		if in.Title != "" {
			if err := validateTitleFormat(in.Title); err != nil {
				return nil, updatePageOutput{}, wrapErr(err)
			}
		}
		if in.Body != "" {
			if err := validateBodyFormat(in.Body, cfg.BlockedShortcodes); err != nil {
				return nil, updatePageOutput{}, wrapErr(err)
			}
		}
		if in.Description != "" {
			if err := rejectUnsafeText(in.Description); err != nil {
				return nil, updatePageOutput{}, wrapErr(fmt.Errorf("invalid_params: description %w", err))
			}
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(&mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			fields := map[string]any{
				"rate_limit_remaining": rateLimitRemaining(limiter),
			}
			return toolcontract.WithDataFields(toolcontract.WithRootFields(wrapErr(err), fields), fields)
		}
		// Allow() is skipped for dry-run (#588) but otherwise stays at its
		// original position — before the missing/stale expected_revision
		// checks further down, which is existing, tested behavior: a real
		// (non-dry-run) update_page attempt that fails revision validation
		// still consumes 1 token (TestUpdatePageRequiresExpectedRevisionForWrite/
		// TestUpdatePageRejectsStaleExpectedRevision). Only the dry-run path
		// changes here.
		if !in.DryRun && !limiter.Allow() {
			return nil, updatePageOutput{}, wrapErrWithLimiter(rateLimitExceededErr("update_page", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("update_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("update_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("update_page: lock_released")
		}()

		existing, ok := idx.GetBySlug(in.Slug)
		if !ok {
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("not_found: page not found"))
		}

		if _, err := pg.SafeJoin(in.Slug); err != nil {
			slog.Warn("update_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: path validation failed"))
		}

		resolvedSource, langErr := resolveExistingSource(cfg.ContentRoot, in.Slug, lang)
		if langErr != nil {
			return nil, updatePageOutput{}, wrapErrWithLimiter(langErr)
		}
		filePath := resolvedSource.SourcePath

		// Idempotency replay must be checked before the expected_revision
		// staleness check: a true replay of an already-applied mutation is
		// not "the page changed" — it's the same logical request arriving
		// twice, and must return the original result regardless of what
		// happened to the file afterward. Checking revision first would
		// wrongly turn a safe replay into a revision_conflict.
		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				Slug             string   `json:"slug"`
				Lang             string   `json:"lang,omitempty"`
				Title            string   `json:"title,omitempty"`
				Body             string   `json:"body,omitempty"`
				Tags             []string `json:"tags,omitempty"`
				Categories       []string `json:"categories,omitempty"`
				Draft            *bool    `json:"draft,omitempty"`
				Description      string   `json:"description,omitempty"`
				ExpectedRevision string   `json:"expected_revision,omitempty"`
			}{
				Slug:             in.Slug,
				Lang:             lang,
				Title:            in.Title,
				Body:             in.Body,
				Tags:             in.Tags,
				Categories:       in.Categories,
				Draft:            in.Draft,
				Description:      in.Description,
				ExpectedRevision: in.ExpectedRevision,
			})
			if hashErr != nil {
				return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached updatePageOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "update_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, updatePageOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("update_page: read failed", "slug", in.Slug, "path", filePath, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read page"))
		}
		currentRevision := contentmodel.SourceRevisionBytes(raw)
		if !in.DryRun {
			if strings.TrimSpace(in.ExpectedRevision) == "" {
				return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: expected_revision is required for non-dry-run update_page"))
			}
			if in.ExpectedRevision != currentRevision {
				return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan"))
			}
		}
		// normalize_taxonomy_casing (#589) — see the comment on the identical
		// block in create_page above; scoped to resolvedSource.Lang, the
		// language this write actually targets.
		writeTags, writeCategories := in.Tags, in.Categories
		var taxonomyNormalized []taxonomyCasingChangeDTO
		var taxonomyAmbiguous []taxonomyCasingSkippedDTO
		if in.NormalizeTaxonomyCasing {
			var tagChanges, catChanges []taxonomyCasingChangeDTO
			var tagSkipped, catSkipped []taxonomyCasingSkippedDTO
			writeTags, tagChanges, tagSkipped = normalizeTaxonomyCasing(taxonomyRawForms(idx, "tag"), "tag", resolvedSource.Lang, in.Tags)
			writeCategories, catChanges, catSkipped = normalizeTaxonomyCasing(taxonomyRawForms(idx, "category"), "category", resolvedSource.Lang, in.Categories)
			taxonomyNormalized = append(tagChanges, catChanges...)
			taxonomyAmbiguous = append(tagSkipped, catSkipped...)
		}
		var tagsDelta, categoriesDelta *taxonomyDeltaDTO
		if in.Tags != nil {
			d := computeTaxonomyDelta(existing.Tags, writeTags)
			tagsDelta = &d
		}
		if in.Categories != nil {
			d := computeTaxonomyDelta(existing.Categories, writeCategories)
			categoriesDelta = &d
		}
		opts := pageUpdateOpts{
			Tags:        writeTags,
			Categories:  writeCategories,
			Draft:       in.Draft,
			Description: in.Description,
		}
		content, err := applyPageUpdates(string(raw), in.Title, in.Body, opts)
		if err != nil {
			slog.Error("update_page: frontmatter update failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("parse_error: failed to update frontmatter"))
		}
		// Round-trip guard: reject content with malformed/duplicated frontmatter.
		if err := validateFrontmatterRoundTrip(content); err != nil {
			slog.Error("update_page: round-trip guard failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("validation_error: %w", err))
		}
		if in.DryRun {
			// Use the resolved filename (e.g. index.fr.md) so the diff header
			// matches the file that a real write would touch.
			diffLabel := in.Slug + "/" + filepath.Base(filePath)
			diff := simpleDiff(diffLabel, string(raw), content)
			logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
			return nil, newUpdatePageOutput(updatePageData{
				Status:                   "ok",
				Slug:                     canonicalPublicSlug(in.Slug),
				SourceKey:                in.Slug,
				ResolvedLang:             strPtr(resolvedSource.Lang),
				ResolvedSourcePath:       strPtr(logicalPath),
				DryRun:                   true,
				Diff:                     diff,
				TaxonomyCasingNormalized: taxonomyNormalized,
				TaxonomyCasingAmbiguous:  taxonomyAmbiguous,
				TagsDelta:                tagsDelta,
				CategoriesDelta:          categoriesDelta,
			}, rateLimitRemaining(limiter)), nil
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("update_page: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if err := fileutil.AtomicWriteChecked(filePath, content, pg); err != nil {
			slog.Error("update_page: write failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}
		// Snapshot the pre-write content, keyed by the revision it's about
		// to stop being, so rollback_change can restore exactly this state
		// later (#629 — extends apply_content_plan's own snapshot capture,
		// see content_plan.go, to update_page's write path too; #379's
		// amended invariant, docs/transactional-edit-design.md §4). Only
		// captured on a successful write: a failed write never changed the
		// file, so there's nothing new to roll back from. create_page is
		// deliberately not snapshotted — there's no meaningful "pre-create"
		// state to restore to.
		snapshots.put(filePath, currentRevision, string(raw))
		updated := *existing
		updated.FilePath = filePath
		updated.Lang = resolvedSource.Lang
		if in.Title != "" {
			updated.Title = in.Title
			if updated.FrontmatterRaw == nil {
				updated.FrontmatterRaw = make(map[string]any)
			}
			updated.FrontmatterRaw["title"] = in.Title
		}
		if in.Body != "" {
			updated.Body = in.Body
		}
		if writeTags != nil {
			updated.Tags = writeTags
		}
		if writeCategories != nil {
			updated.Categories = writeCategories
		}
		updated.BuildPending = true
		idx.Upsert(updated)
		hadPublic := false
		if siteIdx != nil {
			if pub, ok := siteIdx.GetBySlug(in.Slug); ok {
				hadPublic = true
				pubUpdated := *pub
				if in.Title != "" {
					pubUpdated.Title = in.Title
				}
				if writeTags != nil {
					pubUpdated.Tags = writeTags
				}
				if writeCategories != nil {
					pubUpdated.Categories = writeCategories
				}
				siteIdx.UpsertPage(pubUpdated)
			}
		}
		status := "ok"
		warning := ""
		if siteDB != nil {
			if err := siteDB.SyncSourcePage(updated); err != nil {
				slog.Warn("update_page: db sync failed", "slug", in.Slug, "error", err)
				status = "partial_success"
				warning = fmt.Sprintf("source updated but derived DB could not be updated: %v", err)
			}
		}

		state := updatePageState(siteIdx != nil, hadPublic)
		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
		out := newUpdatePageOutput(updatePageData{
			Status:                   status,
			Slug:                     canonicalPublicSlug(in.Slug),
			SourceKey:                in.Slug,
			ResolvedLang:             strPtr(resolvedSource.Lang),
			ResolvedSourcePath:       strPtr(logicalPath),
			NewRevision:              contentmodel.SourceRevisionBytes([]byte(content)),
			Warning:                  appendLastBuildWarning(warning),
			State:                    &state,
			TaxonomyCasingNormalized: taxonomyNormalized,
			TaxonomyCasingAmbiguous:  taxonomyAmbiguous,
			TagsDelta:                tagsDelta,
			CategoriesDelta:          categoriesDelta,
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "update_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("update_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:         "delete_page",
		Title:        "Delete page",
		Description:  "Delete a Hugo content page. This is destructive and rate limited to 5 deletions per minute. IMPORTANT for bilingual/multilingual bundles (index.fr.md + index.en.md under the same slug): pass `lang` to delete exactly that translation; omitting `lang` on a bundle with more than one language file fails with `ambiguous_language` rather than guessing (#682). Only the resolved language's source file is removed — the bundle directory, any shared assets, and other translations are left untouched unless the deleted language was the last one remaining, in which case the whole bundle is removed. `data.bundle_fully_removed` reports which of those happened. Deleting the last (or only) language of a bundle removes public output/derived-index/DB entries too; deleting one of several surviving languages leaves public output untouched (surfaced as a warning) since reconciling it needs a rebuild. Non-dry-run calls require `expected_revision`, the `revision` value from a prior read of this page (e.g. get_page), unless the page has no source file to protect; a stale value fails with `revision_conflict`, telling the agent to re-read and replan. Callers may provide `idempotency_key` to safely replay the exact same non-dry-run delete after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether source, public output, and derived indexes were all removed cleanly. `rate_limit_remaining` reports the caller's remaining delete budget (#466), separate from create_page/update_page/upload_page_asset's shared quota; if exceeded, the error's `resolution.retry_after_seconds` gives a concrete wait time instead of forcing you to guess a safe pacing.",
		InputSchema:  tools.MustSchema[deletePageInput](),
		OutputSchema: tools.MustSchema[deletePageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in deletePageInput) (*mcp.CallToolResult, deletePageOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, deletePageOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("delete_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, wrapErr(fmt.Errorf("invalid_params: path validation failed"))
		}

		// Return not_found when the source directory does not exist (#266).
		// Check this before the rate limiter to avoid burning the budget on client errors.
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return nil, deletePageOutput{}, wrapErr(fmt.Errorf("not_found: page not found for slug %q", in.Slug))
		}
		// Validate lang the same way create_page/update_page already do
		// (validateLangParam) before it ever reaches path resolution —
		// contentmodel.ResolvePageSource builds candidate paths with
		// filepath.Join("index."+lang+".md"), so an unvalidated lang like
		// "../../victim" would let a caller resolve (and then delete)
		// an arbitrary file outside the requested slug's bundle, bypassing
		// the slug's own PathGuard check entirely (caught by Strix on the
		// first version of this fix).
		validatedLang, langErr := validateLangParam(in.Lang)
		if langErr != nil {
			return nil, deletePageOutput{}, wrapErr(langErr)
		}

		// Resolve to a single language file via the same
		// contentmodel.ResolvePageSource machinery update_page already uses
		// (#682), instead of the old inspectDeleteSource helper which just
		// picked the alphabetically-first index.*.md file and never errored
		// on ambiguity — that let one call silently target either language
		// of a bilingual bundle depending on file naming. A page with no
		// source file at all (public-only content) is not an error here;
		// delete_page has always tolerated that case — but only when the
		// caller never asked for a specific lang. If lang was explicitly
		// given and simply doesn't match any file, that must be rejected
		// outright, not silently downgraded to the source-less path: the
		// source-less path skips the expected_revision requirement and
		// drives the whole-bundle-deletion branch, which would let an
		// invalid lang wipe every translation instead of failing cleanly
		// (also caught by Strix on the first version of this fix).
		resolved, resolveErr := contentmodel.ResolvePageSource(in.Slug, validatedLang, cfg.ContentRoot)
		var resolvedSource contentmodel.ResolvedSource
		switch {
		case resolveErr == nil:
			resolvedSource = resolved
		case strings.HasPrefix(resolveErr.Error(), "ambiguous_language:"):
			return nil, deletePageOutput{}, wrapErr(fmt.Errorf("%s", resolveErr.Error()))
		case strings.HasPrefix(resolveErr.Error(), "source_file_not_found:"):
			if validatedLang != "" {
				return nil, deletePageOutput{}, wrapErr(resolveErr)
			}
			resolvedSource = contentmodel.ResolvedSource{}
		default:
			return nil, deletePageOutput{}, wrapErr(resolveErr)
		}

		// Fetching the limiter is not itself a budget-consuming operation —
		// only Allow() below is — so hoisting it above the dry-run block
		// lets dry-run report an accurate rate_limit_remaining without
		// violating the "don't burn budget on dry-run/not_found" invariant
		// the not_found check above already established (#466).
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(&deleteMu, deleteLimiters, callerKey, cfg.RateLimit.DestructivePerMin)
		wrapErrWithLimiter := func(err error) error {
			fields := map[string]any{
				"rate_limit_remaining": rateLimitRemaining(limiter),
			}
			return toolcontract.WithDataFields(toolcontract.WithRootFields(wrapErr(err), fields), fields)
		}
		mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
		if err != nil {
			return nil, deletePageOutput{}, wrapErr(err)
		}

		// dry_run: return page content + backlinks that would break, without touching disk (#267).
		if in.DryRun {
			content := ""
			if resolvedSource.SourcePath != "" {
				if raw, readErr := os.ReadFile(resolvedSource.SourcePath); readErr == nil {
					content = string(raw)
				}
			}
			bls := []deletePageBacklinkDTO{}
			if siteIdx != nil {
				for _, e := range siteIdx.GetBacklinks(in.Slug) {
					bls = append(bls, deletePageBacklinkDTO{Slug: e.FromSlug, Title: e.FromTitle, URL: e.FromURL})
				}
			}
			includeContent := mode != toolcontract.ResponseModeCompact
			includeBacklinks := mode != toolcontract.ResponseModeCompact
			var contentValue string
			var backlinksValue *[]deletePageBacklinkDTO
			if includeContent {
				contentValue = content
			}
			if includeBacklinks {
				backlinksValue = &bls
			}
			backlinksCount := len(bls)
			return nil, newDeletePageOutput(deletePageData{
				Status:             "ok",
				Slug:               canonicalPublicSlug(in.Slug),
				SourceKey:          in.Slug,
				ResolvedLang:       strPtr(resolvedSource.Lang),
				ResolvedSourcePath: strPtr(fileutil.LogicalContentPath(cfg.ContentRoot, resolvedSource.SourcePath)),
				DryRun:             true,
				Content:            contentValue,
				Backlinks:          backlinksValue,
				BacklinksCount:     &backlinksCount,
			}, rateLimitRemaining(limiter)), nil
		}
		if resolvedSource.SourcePath != "" && strings.TrimSpace(in.ExpectedRevision) == "" {
			return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: expected_revision is required for non-dry-run delete_page"))
		}

		if !limiter.Allow() {
			return nil, deletePageOutput{}, wrapErrWithLimiter(rateLimitExceededErr("delete_page", cfg.RateLimit.DestructivePerMin, limiter))
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("delete_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("delete_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("delete_page: lock_released")
		}()

		// Idempotency replay check must happen under the content lock: two
		// genuinely concurrent retries with the same key (the exact
		// uncertain-delivery scenario this feature protects against) would
		// otherwise both miss the cache before either has a chance to
		// remember its result, and the loser would see an unwanted second
		// delete attempt instead of the intended idempotent replay.
		idemHash := ""
		if strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				Slug             string `json:"slug"`
				Lang             string `json:"lang,omitempty"`
				ExpectedRevision string `json:"expected_revision,omitempty"`
			}{
				Slug:             in.Slug,
				Lang:             validatedLang,
				ExpectedRevision: in.ExpectedRevision,
			})
			if hashErr != nil {
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached deletePageOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "delete_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, deletePageOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		currentRevision := ""
		if resolvedSource.SourcePath != "" {
			currentRevision, err = contentmodel.SourceRevision(resolvedSource.SourcePath)
			if err != nil {
				slog.Error("delete_page: read revision failed", "slug", in.Slug, "path", resolvedSource.SourcePath, "error", err)
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read page revision"))
			}
		}
		if in.ExpectedRevision != currentRevision {
			return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan"))
		}

		// Delete only the resolved language's source file, not the whole
		// bundle directory (#682) — a bilingual bundle (index.fr.md +
		// index.en.md) must survive the deletion of one translation. The
		// bundle directory (and any shared assets) is only removed once no
		// index.*.md file remains, or when there was never a source file to
		// begin with (public-only content, matching the pre-#682 behavior
		// for that case).
		bundleFullyRemoved := true
		if resolvedSource.SourcePath != "" {
			// Revalidate immediately before the single-file unlink, closing
			// the TOCTOU window between the earlier SafeJoin/resolve and
			// this delete: the slug directory could have been swapped for a
			// symlink in between, which would otherwise let os.Remove
			// follow it and delete a file outside content_root (Strix
			// finding on the #682 fix — every other write path already
			// revalidates this way right before touching disk).
			if err := pg.RevalidateForWrite(resolvedSource.SourcePath); err != nil {
				slog.Warn("delete_page: symlink detected before delete", "slug", in.Slug, "path", resolvedSource.SourcePath, "error", err)
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in delete path"))
			}
			if err := os.Remove(resolvedSource.SourcePath); err != nil {
				slog.Error("delete_page: remove source file failed", "slug", in.Slug, "path", resolvedSource.SourcePath, "error", err)
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("delete_error: failed to delete page"))
			}
			bundleFullyRemoved = !bundleHasRemainingLangFiles(dir)
			if bundleFullyRemoved {
				if err := os.RemoveAll(dir); err != nil {
					slog.Error("delete_page: remove bundle dir failed", "slug", in.Slug, "error", err)
					return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("delete_error: failed to delete page"))
				}
			}
		} else if err := os.RemoveAll(dir); err != nil {
			slog.Error("delete_page: remove failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("delete_error: failed to delete page"))
		}

		if bundleFullyRemoved {
			idx.Delete(in.Slug)
		} else {
			idx.DeleteLang(in.Slug, resolvedSource.Lang)
		}
		var deleteWarning string
		dbDeleteFailed := false
		if bundleFullyRemoved && siteIdx != nil {
			siteIdx.RemoveBySlug(in.Slug)
		}
		if bundleFullyRemoved && siteDB != nil {
			if err := siteDB.DeletePage(in.Slug); err != nil {
				// Source and in-memory indexes are already gone; surface the DB
				// staleness explicitly so callers know get_broken_links may be
				// stale until the next build (#242).
				deleteWarning = fmt.Sprintf("source deleted but derived DB could not be updated: %v", err)
				dbDeleteFailed = true
				slog.Warn("delete_page: db delete failed", "slug", in.Slug, "error", err)
			}
		}
		publicCleanupFailed := false
		if !bundleFullyRemoved {
			// A translation survives on disk — removing SiteRoot/slug would
			// wipe that survivor's live public output too, since Hugo's
			// rendered output for a bundle is not scoped per language file
			// the same way the source tree is (#682). Getting per-language
			// public-output mapping exactly right is a separate, harder
			// problem; for now, surface that a rebuild is needed instead of
			// silently deleting the surviving language's public page.
			deleteWarning = "public output for the removed language was not touched — the page bundle still has other language(s); run build_site/publish_changes to reconcile public output"
		} else if cfg.SiteRoot != "" {
			publicPath := filepath.Join(cfg.SiteRoot, in.Slug)
			if rmErr := os.RemoveAll(publicPath); rmErr != nil {
				// Source is already gone; surface the zombie so the caller knows
				// the public output is still live (#239).
				msg := fmt.Sprintf("source deleted but public output cleanup failed: %v", rmErr)
				if deleteWarning != "" {
					deleteWarning += "; " + msg
				} else {
					deleteWarning = msg
				}
				publicCleanupFailed = true
				slog.Warn("delete_page: could not remove public dir", "path", publicPath, "error", rmErr)
			}
		}

		// Best-effort removal of any hero image generate_hero_image left
		// behind for this slug (#606). generate_hero_image writes to
		// {HugoRoot}/static/images/{slug}-featured.jpg, a location outside
		// the page's own content bundle, so the bundle removal above never
		// touches it and it would otherwise accumulate as an orphaned file
		// every time a page with a generated hero image is deleted. Skipped
		// entirely when the bundle survives (#682) — the hero image is a
		// whole-page-level asset shared across translations, not scoped to
		// one language. Never fatal — mirrors the public-dir/DB/audit-log
		// cleanup steps above, which all surface failures as a non-blocking
		// warning rather than failing the delete outright, since the source
		// is already gone by this point and there's nothing to roll back to.
		if bundleFullyRemoved && cfg.HugoRoot != "" {
			if removed, rmErr := removeHeroImage(cfg.HugoRoot, in.Slug); rmErr != nil {
				msg := fmt.Sprintf("hero image cleanup failed: %v", rmErr)
				if deleteWarning != "" {
					deleteWarning += "; " + msg
				} else {
					deleteWarning = msg
				}
				slog.Warn("delete_page: hero image cleanup failed", "slug", in.Slug, "error", rmErr)
			} else if removed {
				slog.Debug("delete_page: removed orphaned hero image", "slug", in.Slug)
			}
		}

		auditLog := filepath.Join(cfg.ContentRoot, ".mcp-audit.log")
		entry := fmt.Sprintf("%s DELETE %s\n", time.Now().UTC().Format(time.RFC3339), in.Slug)
		if auditErr := appendAuditLog(auditLog, entry); auditErr != nil {
			// Deletion already committed — surface the audit failure as a warning
			// rather than a hard error; retrying would be a no-op.
			slog.Warn("delete_page: audit log write failed (delete already committed)", "slug", in.Slug, "error", auditErr)
			auditMsg := "audit_error: " + auditErr.Error()
			if deleteWarning != "" {
				deleteWarning += "; " + auditMsg
			} else {
				deleteWarning = auditMsg
			}
		}

		if cfg.Cloudflare.Enabled() {
			pageURL := strings.TrimRight(cfg.SiteURL, "/") + "/" + in.Slug + "/"
			if err := cloudflare.PurgeURLs(cfg.Cloudflare, []string{pageURL}); err != nil {
				slog.Warn("delete_page: cloudflare purge failed", "slug", in.Slug, "error", err)
			}
		}

		state := deletePageState(cfg.SiteRoot != "", publicCleanupFailed, dbDeleteFailed)
		status := "ok"
		if deleteWarning != "" {
			status = "partial_success"
		}
		out := newDeletePageOutput(deletePageData{
			Status:             status,
			Slug:               canonicalPublicSlug(in.Slug),
			SourceKey:          in.Slug,
			ResolvedLang:       strPtr(resolvedSource.Lang),
			ResolvedSourcePath: strPtr(fileutil.LogicalContentPath(cfg.ContentRoot, resolvedSource.SourcePath)),
			Warning:            deleteWarning,
			State:              &state,
			BundleFullyRemoved: bundleFullyRemoved,
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "delete_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("delete_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	registerUploadPageAsset(s, pg, idx, cfg, idem, &mutationMu, mutationLimiters)
	registerDeletePageAsset(s, pg, idx, cfg, idem, &deleteMu, deleteLimiters)
	registerGetMutationStatus(s, idem)
	registerGetRateLimits(s, cfg, &mutationMu, mutationLimiters, &deleteMu, deleteLimiters)
}

func createPageState() site.LifecycleState {
	return site.LifecycleState{
		SourceState: "present",
		BuildState:  "pending",
		PublicState: "not_yet_available",
		IndexState:  "source_only",
	}
}

func updatePageState(hasSiteIndex, hadPublic bool) site.LifecycleState {
	state := site.LifecycleState{
		SourceState: "present",
		BuildState:  "pending",
	}
	switch {
	case hadPublic:
		state.PublicState = "stale"
		state.IndexState = "stale"
	case hasSiteIndex:
		state.PublicState = "not_yet_available"
		state.IndexState = "source_only"
	default:
		state.PublicState = "unknown"
		state.IndexState = "unknown"
	}
	return state
}

func deletePageState(hasSiteRoot, publicCleanupFailed, dbDeleteFailed bool) site.LifecycleState {
	state := site.LifecycleState{
		SourceState: "deleted",
		BuildState:  "not_applicable",
		IndexState:  "removed",
	}
	switch {
	case !hasSiteRoot:
		state.PublicState = "unknown"
	case publicCleanupFailed:
		state.PublicState = "stale"
	default:
		state.PublicState = "removed"
	}
	if dbDeleteFailed {
		state.IndexState = "stale"
	}
	return state
}

// removeHeroImage best-effort removes the {slug}{admin.HeroImageSuffix} hero
// image admin.registerGenerateFeaturedImage (generate_hero_image) writes to
// {hugoRoot}/static/images/, keyed only by slug rather than living inside
// the page's own content bundle (#606). Deleting it here by re-deriving the
// path from the shared admin.HeroImageSuffix constant — rather than
// duplicating the "-featured.jpg" literal — keeps this in lockstep with
// generate_hero_image's own path construction, so a future rename there
// can't silently desync this cleanup logic from the code that actually
// produced the file. It's otherwise safe with no rename/reuse ambiguity: a
// different page necessarily has a different slug and therefore a different
// filename, so this can never remove another page's hero image, and
// static/images/ is not used for any other purpose elsewhere in this
// codebase (page assets uploaded via upload_page_asset live under the
// page's own content bundle, not here).
//
// Returns (removed, err). removed is true only when a file was actually
// deleted; a hero image that was never generated for this slug is not an
// error — most deleted pages won't have one.
func removeHeroImage(hugoRoot, slug string) (bool, error) {
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	guard, err := security.New(imagesRoot, true)
	if err != nil {
		return false, err
	}
	target, err := guard.SafeJoin(slug + admin.HeroImageSuffix)
	if err != nil {
		return false, err
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type frontmatterDoc struct {
	Title      string   `yaml:"title"`
	Date       string   `yaml:"date"`
	Tags       []string `yaml:"tags"`
	Categories []string `yaml:"categories"`
	Draft      bool     `yaml:"draft"`
	// TestContent/TestContentOwner/TestContentExpiresAt (#661) are only
	// ever set via create_page's explicit opt-in test_content parameter —
	// never inferred from slug/title, so a real published page that
	// happens to start with e.g. "codex-" is never wrongly constrained.
	TestContent          bool   `yaml:"test_content,omitempty"`
	TestContentOwner     string `yaml:"test_content_owner,omitempty"`
	TestContentExpiresAt string `yaml:"test_content_expires_at,omitempty"`
}

// buildFrontmatter renders a new page's frontmatter+body. testContent, when
// non-nil (#661), forces Draft:true regardless of any other setting and
// records test_content/test_content_owner/test_content_expires_at —
// computed here so the caller-visible response and the on-disk frontmatter
// always agree on the exact expiry that was applied.
func buildFrontmatter(title string, tags, categories []string, body string, testContent *testContentInput) (string, string) {
	if tags == nil {
		tags = []string{}
	}
	if categories == nil {
		categories = []string{}
	}
	doc := frontmatterDoc{
		Title:      title,
		Date:       time.Now().UTC().Format(time.RFC3339),
		Tags:       tags,
		Categories: categories,
		Draft:      false,
	}
	if testContent != nil {
		ttlHours := testContent.TTLHours
		if ttlHours <= 0 {
			ttlHours = testContentDefaultTTLHours
		}
		doc.Draft = true
		doc.TestContent = true
		doc.TestContentOwner = testContent.Owner
		doc.TestContentExpiresAt = time.Now().UTC().Add(time.Duration(ttlHours) * time.Hour).Format(time.RFC3339)
	}
	raw, _ := marshalWithIndent(doc, 2)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(raw)
	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String(), doc.TestContentExpiresAt
}

func buildFrontmatterFromMap(fm map[string]any, body string) string {
	raw, _ := marshalWithIndent(fm, 2)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(raw)
	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

type pageUpdateOpts struct {
	Tags        []string
	Categories  []string
	Draft       *bool
	Description string
}

// applyPageUpdates applies title, body, and optional front matter field changes
// to an existing page file using the yaml.v3 Node API to preserve field
// ordering, comments, and YAML style (issue #111).
func applyPageUpdates(fileContent, newTitle, newBody string, opts pageUpdateOpts) (string, error) {
	if !strings.HasPrefix(fileContent, "---\n") {
		return "", fmt.Errorf("no YAML frontmatter delimiter")
	}
	rest := fileContent[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", fmt.Errorf("unterminated YAML frontmatter")
	}
	yamlPart := rest[:end]
	bodyPart := rest[end+4:] // everything after the closing ---

	needsYAML := newTitle != "" || opts.Tags != nil || opts.Categories != nil ||
		opts.Draft != nil || opts.Description != ""

	if needsYAML {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(yamlPart), &doc); err != nil {
			return "", fmt.Errorf("YAML parse: %w", err)
		}
		if len(doc.Content) == 0 || doc.Content[0] == nil || doc.Content[0].Kind != yaml.MappingNode {
			return "", fmt.Errorf("YAML parse: frontmatter root must be a mapping")
		}
		mapping := doc.Content[0]
		if newTitle != "" {
			setYAMLKey(mapping, "title", newTitle)
		}
		if opts.Tags != nil {
			setYAMLSeq(mapping, "tags", opts.Tags)
		}
		if opts.Categories != nil {
			setYAMLSeq(mapping, "categories", opts.Categories)
		}
		if opts.Draft != nil {
			setYAMLBool(mapping, "draft", *opts.Draft)
		}
		if opts.Description != "" {
			setYAMLKey(mapping, "description", opts.Description)
		}
		out, err := marshalWithIndent(doc.Content[0], 2)
		if err != nil {
			return "", fmt.Errorf("YAML marshal: %w", err)
		}
		yamlPart = strings.TrimRight(string(out), "\n")
	}

	if newBody != "" {
		bodyPart = "\n\n" + newBody
	}

	return "---\n" + yamlPart + "\n---" + bodyPart, nil
}

// setYAMLKey updates the value of key in a YAML mapping node in-place,
// appending a new key-value pair when key is absent.
func setYAMLKey(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].SetString(value)
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// setYAMLSeq sets a sequence (list) value in a YAML mapping node in-place,
// appending a new key-sequence pair when key is absent.
func setYAMLSeq(mapping *yaml.Node, key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = seq
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
}

// setYAMLBool sets a boolean value in a YAML mapping node in-place,
// appending a new key-value pair when key is absent.
func setYAMLBool(mapping *yaml.Node, key string, value bool) {
	v := "false"
	if value {
		v = "true"
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = node
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		node,
	)
}

func marshalWithIndent(v any, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inspectDeleteSource(dir string) contentmodel.ResolvedSource {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return contentmodel.ResolvedSource{}
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.md" || (strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md")) {
			files = append(files, filepath.Join(dir, name))
		}
	}
	if len(files) == 0 {
		return contentmodel.ResolvedSource{}
	}
	sort.Strings(files)
	path := files[0]
	return contentmodel.ResolvedSource{
		SourcePath: path,
		Lang:       inferLangFromIndexFile(path),
	}
}

func inferLangFromIndexFile(path string) string {
	base := filepath.Base(path)
	if base == "index.md" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(base, "index."), ".md")
}

// bundleHasRemainingLangFiles reports whether dir still contains any
// index.md/index.<lang>.md file (#682) — used by delete_page after removing
// one language's source file to decide whether the bundle directory (and
// any shared assets) should also be removed, or whether another
// translation is still present and the directory must survive.
func bundleHasRemainingLangFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.md" || (strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md")) {
			return true
		}
	}
	return false
}

func resolveExistingSource(contentRoot, slug, lang string) (contentmodel.ResolvedSource, error) {
	resolved, err := contentmodel.ResolvePageSource(slug, lang, contentRoot)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.HasPrefix(msg, "source_file_not_found:"):
			return contentmodel.ResolvedSource{}, fmt.Errorf("not_found: page not found")
		default:
			return contentmodel.ResolvedSource{}, err
		}
	}
	return resolved, nil
}

// Defs returns the tool definitions for this package (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "create_page", RequiredScope: "write"},
		{Name: "update_page", RequiredScope: "write"},
		{Name: "delete_page", RequiredScope: "write"},
		{Name: "upload_page_asset", RequiredScope: "write"},
		{Name: "delete_page_asset", RequiredScope: "write"},
		{Name: "get_mutation_status", RequiredScope: "write"},
		{Name: "get_rate_limits", RequiredScope: "write"},
		{Name: "plan_content_change", RequiredScope: ""},
		{Name: "apply_content_plan", RequiredScope: "write"},
		{Name: "rollback_change", RequiredScope: "write"},
	}
}

// validateFrontmatterRoundTrip parses content's frontmatter block to confirm
// it can be re-parsed cleanly. A body that begins with a second YAML frontmatter
// block (duplicated-frontmatter corruption signature) is rejected.
func validateFrontmatterRoundTrip(content string) error {
	if !strings.HasPrefix(content, "---\n") {
		return fmt.Errorf("missing YAML frontmatter delimiter")
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fmt.Errorf("unterminated YAML frontmatter")
	}
	var fm any
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return fmt.Errorf("frontmatter YAML invalid after update: %w", err)
	}
	body := strings.TrimSpace(rest[end+4:])
	// Detect duplicated frontmatter: body starts with "---\n" and contains a
	// closing "---" within the first 30 lines. A bare thematic break ("---"
	// immediately followed by non-YAML content) is not rejected.
	if strings.HasPrefix(body, "---\n") {
		inner := body[4:]
		innerEnd := strings.Index(inner, "\n---")
		if innerEnd >= 0 {
			lines := strings.Count(inner[:innerEnd], "\n")
			if lines <= 30 {
				return fmt.Errorf("body contains a second frontmatter block — frontmatter appears to be duplicated")
			}
		}
	}
	return nil
}

// simpleDiff produces a unified diff between old and new, labelled with path.
// Returns an empty string when the contents are identical.
func simpleDiff(path, old, new string) string {
	if old == new {
		return ""
	}
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")
	m, n := len(oldLines), len(newLines)
	if m > 500 || n > 500 {
		return fmt.Sprintf("--- a/%s\n+++ b/%s\n# content changed (%d → %d lines)\n", path, path, m, n)
	}
	// Clamp after the guard so static analysis can verify allocation sizes are bounded.
	m, n = min(m, 500), min(n, 500)
	// Wagner-Fischer LCS
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	type edit struct {
		kind rune
		text string
	}
	edits := make([]edit, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && oldLines[i-1] == newLines[j-1]:
			edits = append(edits, edit{' ', oldLines[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			edits = append(edits, edit{'+', newLines[j-1]})
			j--
		default:
			edits = append(edits, edit{'-', oldLines[i-1]})
			i--
		}
	}
	// Reverse
	for l, r := 0, len(edits)-1; l < r; l, r = l+1, r-1 {
		edits[l], edits[r] = edits[r], edits[l]
	}
	// Locate changed regions and expand with context
	const ctx = 3
	type hunk struct{ s, e int }
	var hunks []hunk
	inChange := false
	cs := 0
	for k, ed := range edits {
		if ed.kind != ' ' {
			if !inChange {
				cs = k
				inChange = true
			}
		} else if inChange {
			hunks = append(hunks, hunk{max(0, cs-ctx), min(len(edits)-1, k+ctx-1)})
			inChange = false
		}
	}
	if inChange {
		hunks = append(hunks, hunk{max(0, cs-ctx), len(edits) - 1})
	}
	// Merge overlapping hunks
	merged := hunks[:0]
	for _, h := range hunks {
		if len(merged) > 0 && h.s <= merged[len(merged)-1].e+1 {
			if h.e > merged[len(merged)-1].e {
				merged[len(merged)-1].e = h.e
			}
		} else {
			merged = append(merged, h)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", path, path)
	for _, h := range merged {
		oldStart, newStart, oldCount, newCount := 1, 1, 0, 0
		for k := 0; k < h.s; k++ {
			if edits[k].kind != '+' {
				oldStart++
			}
			if edits[k].kind != '-' {
				newStart++
			}
		}
		for k := h.s; k <= h.e; k++ {
			if edits[k].kind != '+' {
				oldCount++
			}
			if edits[k].kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for k := h.s; k <= h.e; k++ {
			fmt.Fprintf(&sb, "%c%s\n", edits[k].kind, edits[k].text)
		}
	}
	return sb.String()
}

func appendAuditLog(path, entry string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}
