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
}

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
	RateLimitRemaining       int                        `json:"rate_limit_remaining"`
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
	RateLimitRemaining      int                        `json:"rate_limit_remaining"`
}

type deletePageInput struct {
	Slug             string `json:"slug"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	DryRun           bool   `json:"dry_run,omitempty"`
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
	Warning            string                   `json:"warning,omitempty"`
	State              *site.LifecycleState     `json:"state,omitempty"`
	RateLimitRemaining int                      `json:"rate_limit_remaining"`
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

func newCreatePageOutput(data createPageData) createPageOutput {
	return createPageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: data.RateLimitRemaining,
	}
}

func newUpdatePageOutput(data updatePageData) updatePageOutput {
	return updatePageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: data.RateLimitRemaining,
	}
}

func newDeletePageOutput(data deletePageData) deletePageOutput {
	return deletePageOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: data.RateLimitRemaining,
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

// normalizeInputSlug strips leading and trailing slashes so agents that pass
// /posts/foo/ and posts/foo reach the same content directory and source-index
// entry (#265).
func normalizeInputSlug(s string) string { return strings.Trim(s, "/") }

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
	registerContentPlanTools(s, pg, idx, cfg, siteDB, siteIdx, &mutationMu, mutationLimiters, idem, plans)

	mcp.AddTool(s, &mcp.Tool{
		Name:         "create_page",
		Title:        "Publish page",
		Description:  "Create a new Hugo content page at {slug}/index.md with front matter and body content. Fails with `already_exists` if the destination already exists; use update_page for edits. Repeating the same non-dry-run request normally fails once the page exists, but callers may provide `idempotency_key` to safely replay the exact same create attempt after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether the page only exists in source so far or is already publicly available. Set `normalize_taxonomy_casing: true` (default off) to rewrite each submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index to that existing spelling — preventing new drift instead of just letting get_site_health report it afterward (#589); rewrites are reported in `data.taxonomy_casing_normalized`, and a term left untouched because the index already has two or more conflicting spellings for it (pre-existing drift, never guessed at) is reported in `data.taxonomy_casing_ambiguous` instead. `body` is rejected with `invalid_params` if it invokes a server-configured blocked shortcode (default: `raw`, `rawhtml`, `script`, `style`) — a best-effort denylist of theme shortcodes known to render unescaped HTML/JavaScript/CSS on the public page, bypassing Hugo's own Markdown-level sanitization; not a guarantee every theme's shortcode surface is safe, and this check cannot be opted out of per call (#590). `rate_limit_remaining` reports the caller's remaining budget on this shared create/update/upload quota (#466); if exceeded, the error's `resolution.retry_after_seconds` gives a concrete wait time instead of forcing you to guess a safe pacing.",
		InputSchema:  tools.MustSchema[createPageInput](),
		OutputSchema: tools.MustSchema[createPageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createPageInput) (*mcp.CallToolResult, createPageOutput, error) {
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
		content := buildFrontmatter(in.Title, writeTags, writeCategories, in.Body)

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
				RateLimitRemaining:       rateLimitRemaining(limiter),
			}), nil
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
			hit, replayErr := idem.replay("create_page", in.IdempotencyKey, idemHash, &cached)
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
		created := hugosite.SourcePage{
			Slug:           in.Slug,
			FilePath:       filePath,
			Lang:           resolvedLang,
			Title:          in.Title,
			Date:           now,
			Tags:           writeTags,
			Categories:     writeCategories,
			Body:           in.Body,
			FrontmatterRaw: map[string]any{"title": in.Title, "date": now, "draft": false},
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
			RateLimitRemaining:       rateLimitRemaining(limiter),
		})
		if idemHash != "" {
			if err := idem.remember("create_page", in.IdempotencyKey, idemHash, out); err != nil {
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
			"For bilingual sites, provide lang (e.g. \"fr\", \"en\") to target the correct language file; " +
			"omitting lang on a page with multiple language files returns an ambiguous_language error listing available langs. " +
			"Non-dry-run calls require `expected_revision`, the `revision` value from a prior read of this page (e.g. get_page); " +
			"a missing value fails with `invalid_params` and a stale value fails with `revision_conflict`, telling the agent to re-read and replan. " +
			"Callers may provide `idempotency_key` to safely replay the exact same non-dry-run update after a timeout or uncertain delivery. " +
			"Successful non-dry-run responses include a `state` object that tells agents whether the source changed ahead of the public build/index state. " +
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
			hit, replayErr := idem.replay("update_page", in.IdempotencyKey, idemHash, &cached)
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
				RateLimitRemaining:       rateLimitRemaining(limiter),
			}), nil
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("update_page: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if err := fileutil.AtomicWriteChecked(filePath, content, pg); err != nil {
			slog.Error("update_page: write failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}
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
			RateLimitRemaining:       rateLimitRemaining(limiter),
		})
		if idemHash != "" {
			if err := idem.remember("update_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("update_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:         "delete_page",
		Title:        "Delete page",
		Description:  "Delete a Hugo content page. This is destructive and rate limited to 5 deletions per minute. Non-dry-run calls require `expected_revision`, the `revision` value from a prior read of this page (e.g. get_page), unless the page has no source file to protect; a stale value fails with `revision_conflict`, telling the agent to re-read and replan. Callers may provide `idempotency_key` to safely replay the exact same non-dry-run delete after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether source, public output, and derived indexes were all removed cleanly. `rate_limit_remaining` reports the caller's remaining delete budget (#466), separate from create_page/update_page/upload_page_asset's shared quota; if exceeded, the error's `resolution.retry_after_seconds` gives a concrete wait time instead of forcing you to guess a safe pacing.",
		InputSchema:  tools.MustSchema[deletePageInput](),
		OutputSchema: tools.MustSchema[deletePageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in deletePageInput) (*mcp.CallToolResult, deletePageOutput, error) {
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
		resolvedSource := inspectDeleteSource(dir)

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
			return nil, newDeletePageOutput(deletePageData{
				Status:             "ok",
				Slug:               canonicalPublicSlug(in.Slug),
				SourceKey:          in.Slug,
				ResolvedLang:       strPtr(resolvedSource.Lang),
				ResolvedSourcePath: strPtr(fileutil.LogicalContentPath(cfg.ContentRoot, resolvedSource.SourcePath)),
				DryRun:             true,
				Content:            content,
				Backlinks:          &bls,
				RateLimitRemaining: rateLimitRemaining(limiter),
			}), nil
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
				ExpectedRevision string `json:"expected_revision,omitempty"`
			}{
				Slug:             in.Slug,
				ExpectedRevision: in.ExpectedRevision,
			})
			if hashErr != nil {
				return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached deletePageOutput
			hit, replayErr := idem.replay("delete_page", in.IdempotencyKey, idemHash, &cached)
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

		if err := os.RemoveAll(dir); err != nil {
			slog.Error("delete_page: remove failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, wrapErrWithLimiter(fmt.Errorf("delete_error: failed to delete page"))
		}
		idx.Delete(in.Slug)
		if siteIdx != nil {
			siteIdx.RemoveBySlug(in.Slug)
		}
		var deleteWarning string
		dbDeleteFailed := false
		if siteDB != nil {
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
		if cfg.SiteRoot != "" {
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
			RateLimitRemaining: rateLimitRemaining(limiter),
		})
		if idemHash != "" {
			if err := idem.remember("delete_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("delete_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	registerUploadPageAsset(s, pg, idx, cfg, idem, &mutationMu, mutationLimiters)
	registerDeletePageAsset(s, pg, idx, cfg, idem, &deleteMu, deleteLimiters)
	registerGetMutationStatus(s, idem)
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

type frontmatterDoc struct {
	Title      string   `yaml:"title"`
	Date       string   `yaml:"date"`
	Tags       []string `yaml:"tags"`
	Categories []string `yaml:"categories"`
	Draft      bool     `yaml:"draft"`
}

func buildFrontmatter(title string, tags, categories []string, body string) string {
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
	raw, _ := marshalWithIndent(doc, 2)
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
		{Name: "plan_content_change", RequiredScope: ""},
		{Name: "apply_content_plan", RequiredScope: "write"},
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
