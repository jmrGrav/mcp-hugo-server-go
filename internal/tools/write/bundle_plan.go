package write

// plan_bundle_change / apply_bundle_plan / rollback_bundle (#854) — the
// bundle-scoped analog of plan_content_change / apply_content_plan /
// rollback_change. Where the single-page trio mutates one translation source
// file, these mutate an entire Hugo page bundle (a content/<slug>/ directory
// holding index.md + index.<lang>.md translations and any bundle-local
// assets) as one logical, all-or-nothing editorial unit.
//
// Atomicity design (see also the PR body / issue #854 AC2, AC5):
//
//   - Optimistic concurrency is anchored on a single contentmodel.BundleRevision
//     over the whole directory. apply_bundle_plan / rollback_bundle recompute it
//     under the content lock and reject the *whole* operation on any mismatch —
//     so a concurrent change to *any* translation or shared asset fails the
//     bundle apply rather than letting it partially land.
//
//   - Every translation's candidate content is validated up front (round-trip +
//     blocked-shortcode policy) before *any* file is written. The dominant
//     failure mode from AC5 — one translation failing validation — therefore
//     never writes a single byte: the bundle is rejected whole with nothing
//     applied.
//
//   - Writes then happen under one held content lock, with an in-memory
//     pre-write snapshot of every translation captured first. If an OS-level
//     write fails partway through the loop (disk full, EPERM, or the injected
//     test seam), already-written files are restored from those snapshots
//     (best-effort in-process rollback), so a runtime interruption mid-apply
//     leaves no partial editorial state.
//
//   - Documented limitation: this gives *runtime* atomicity, not crash
//     atomicity. POSIX offers no multi-file commit; a power loss between two
//     per-file atomic renames could leave the bundle half-written. Full
//     crash-atomicity across N files is deliberately out of scope for this pass
//     (it would need a journal / staging dir + replay on startup). Callers that
//     need to recover a half-applied bundle after a crash use rollback_bundle
//     against the pre-apply bundle revision, whose snapshot is retained 24h.
//
// Leaf multilingual pages (slug.<lang>.md with no bundle directory) are NOT
// supported here and are rejected with not_a_bundle — they share no directory
// or assets, so there is no bundle unit to make transactional; use
// plan_content_change per translation for those.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// applyBundleWriteHook is a test-only seam (#854 AC5, "interruption during
// apply"). When non-nil it is invoked with the zero-based index of each
// translation write *before* the write; returning a non-nil error simulates
// an OS write failure at that point, exercising the in-process rollback path.
// Production leaves it nil.
var applyBundleWriteHook func(index int) error

// bundleTranslationPlan is one translation's frozen candidate within a bundle
// plan, mirroring the fields planEntry carries for a single page.
type bundleTranslationPlan struct {
	Lang       string
	FilePath   string
	Revision   string // per-file source revision, for response reporting
	Content    string // exact candidate bytes apply will write
	Title      string
	Body       string
	Tags       []string
	Categories []string
	Applied    []string
	Rejected   []planRejectedOperationDTO
	Diff       string
}

type bundlePlanEntry struct {
	CallerKey      string
	Slug           string
	BundleDir      string
	BundleRevision string // whole-directory anchor recomputed at apply time
	Translations   []bundleTranslationPlan
	CreatedAt      time.Time
}

// bundlePlanStore mirrors planStore (map + mutex + TTL prune + max-entries
// eviction); a bundle plan is single-use exactly like a content plan.
type bundlePlanStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]bundlePlanEntry
}

func newBundlePlanStore(ttl time.Duration, maxEntries int) *bundlePlanStore {
	return &bundlePlanStore{ttl: ttl, maxEntries: maxEntries, entries: make(map[string]bundlePlanEntry)}
}

func (s *bundlePlanStore) put(id string, entry bundlePlanEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	s.entries[id] = entry
	s.trimLocked()
}

func (s *bundlePlanStore) get(id, callerKey string) (bundlePlanEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	if ok && entry.CallerKey != "" && entry.CallerKey != callerKey {
		return bundlePlanEntry{}, false
	}
	return entry, ok
}

func (s *bundlePlanStore) consume(id, callerKey string) (bundlePlanEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	if ok && entry.CallerKey != "" && entry.CallerKey != callerKey {
		return bundlePlanEntry{}, false
	}
	if ok {
		delete(s.entries, id)
	}
	return entry, ok
}

func (s *bundlePlanStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for id, entry := range s.entries {
		if now.Sub(entry.CreatedAt) > s.ttl {
			delete(s.entries, id)
		}
	}
}

func (s *bundlePlanStore) trimLocked() {
	if s.maxEntries <= 0 || len(s.entries) <= s.maxEntries {
		return
	}
	for len(s.entries) > s.maxEntries {
		var oldestID string
		var oldest time.Time
		first := true
		for id, entry := range s.entries {
			if first || entry.CreatedAt.Before(oldest) {
				oldestID, oldest, first = id, entry.CreatedAt, false
			}
		}
		delete(s.entries, oldestID)
	}
}

func newBundlePlanID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "bplan_" + hex.EncodeToString(b), nil
}

// bundleSnapshotStore records the pre-apply content of every translation in a
// bundle, keyed by the bundle directory + the BundleRevision it is about to
// stop being — so rollback_bundle can restore the whole editorial unit to
// exactly that prior state. Mirrors snapshotStore's lifetime (24h / 512).
type bundleSnapshot struct {
	CallerKey string
	Files     map[string]string // filePath -> content
	CreatedAt time.Time
}

type bundleSnapshotStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]bundleSnapshot
}

func newBundleSnapshotStore(ttl time.Duration, maxEntries int) *bundleSnapshotStore {
	return &bundleSnapshotStore{ttl: ttl, maxEntries: maxEntries, entries: make(map[string]bundleSnapshot)}
}

func bundleSnapshotKey(dir, revision string) string { return dir + "\x00" + revision }

func (s *bundleSnapshotStore) put(dir, revision, callerKey string, files map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneLocked(now)
	s.entries[bundleSnapshotKey(dir, revision)] = bundleSnapshot{CallerKey: callerKey, Files: files, CreatedAt: now}
	s.trimLocked()
}

func (s *bundleSnapshotStore) get(dir, revision, callerKey string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[bundleSnapshotKey(dir, revision)]
	if !ok {
		return nil, false
	}
	if entry.CallerKey != "" && entry.CallerKey != callerKey {
		return nil, false
	}
	return entry.Files, true
}

func (s *bundleSnapshotStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for key, entry := range s.entries {
		if now.Sub(entry.CreatedAt) > s.ttl {
			delete(s.entries, key)
		}
	}
}

func (s *bundleSnapshotStore) trimLocked() {
	if s.maxEntries <= 0 || len(s.entries) <= s.maxEntries {
		return
	}
	for len(s.entries) > s.maxEntries {
		var oldestKey string
		var oldest time.Time
		first := true
		for key, entry := range s.entries {
			if first || entry.CreatedAt.Before(oldest) {
				oldestKey, oldest, first = key, entry.CreatedAt, false
			}
		}
		delete(s.entries, oldestKey)
	}
}

// ---- input / output DTOs -------------------------------------------------

type bundleTranslationInput struct {
	// Lang is the translation to change: "fr", "en", ... or "" for the
	// default (unsuffixed index.md).
	Lang       string               `json:"lang"`
	Operations []planOperationInput `json:"operations"`
}

type planBundleChangeInput struct {
	Slug         string                   `json:"slug"`
	Translations []bundleTranslationInput `json:"translations"`
}

type bundleTranslationOutcomeDTO struct {
	Lang               string                     `json:"lang"`
	SourcePath         string                     `json:"source_path,omitempty"`
	Status             string                     `json:"status"`
	BeforeRevision     string                     `json:"before_revision,omitempty"`
	AfterRevision      string                     `json:"after_revision,omitempty"`
	OperationsApplied  []string                   `json:"operations_applied,omitempty"`
	OperationsRejected []planRejectedOperationDTO `json:"operations_rejected,omitempty"`
	Diff               string                     `json:"diff,omitempty"`
	Error              string                     `json:"error,omitempty"`
}

type planBundleChangeData struct {
	Slug           string                        `json:"slug"`
	BundleRevision string                        `json:"bundle_revision"`
	Translations   []bundleTranslationOutcomeDTO `json:"translations"`
	PlanID         string                        `json:"plan_id,omitempty"`
	PlanExpiresAt  string                        `json:"plan_expires_at,omitempty"`
}

type planBundleChangeOutput struct {
	toolcontract.ToolResponse[planBundleChangeData]
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
}

func newPlanBundleChangeOutput(data planBundleChangeData) planBundleChangeOutput {
	return planBundleChangeOutput{ToolResponse: writeSuccessEnvelope(data)}
}

type applyBundlePlanInput struct {
	PlanID         string `json:"plan_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type applyBundlePlanData struct {
	Status             string                        `json:"status"`
	PlanID             string                        `json:"plan_id,omitempty"`
	Slug               string                        `json:"slug,omitempty"`
	DryRun             bool                          `json:"dry_run,omitempty"`
	BundleStatus       string                        `json:"bundle_status"`
	BeforeRevision     string                        `json:"before_revision,omitempty"`
	AfterRevision      string                        `json:"after_revision,omitempty"`
	Translations       []bundleTranslationOutcomeDTO `json:"translations"`
	Warning            string                        `json:"warning,omitempty"`
	State              *site.LifecycleState          `json:"state,omitempty"`
	RateLimit          *rateLimitBucket              `json:"rate_limit,omitempty"`
	RateLimitRemaining int                           `json:"rate_limit_remaining,omitempty"`
}

type applyBundlePlanOutput struct {
	toolcontract.ToolResponse[applyBundlePlanData]
	RequestContext     *toolcontract.RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int                          `json:"rate_limit_remaining"`
}

func newApplyBundlePlanOutput(data applyBundlePlanData, rateLimitRemaining int) applyBundlePlanOutput {
	return applyBundlePlanOutput{ToolResponse: writeSuccessEnvelopeWithWarning(data, data.Warning), RateLimitRemaining: rateLimitRemaining}
}

type rollbackBundleInput struct {
	Slug             string `json:"slug"`
	ToBundleRevision string `json:"to_bundle_revision"`
	ExpectedRevision string `json:"expected_bundle_revision,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	DryRun           bool   `json:"dry_run,omitempty"`
}

type rollbackBundleData struct {
	Status             string                        `json:"status"`
	Slug               string                        `json:"slug,omitempty"`
	DryRun             bool                          `json:"dry_run,omitempty"`
	BundleStatus       string                        `json:"bundle_status"`
	BeforeRevision     string                        `json:"before_revision,omitempty"`
	AfterRevision      string                        `json:"after_revision,omitempty"`
	Translations       []bundleTranslationOutcomeDTO `json:"translations"`
	Warning            string                        `json:"warning,omitempty"`
	State              *site.LifecycleState          `json:"state,omitempty"`
	RateLimit          *rateLimitBucket              `json:"rate_limit,omitempty"`
	RateLimitRemaining int                           `json:"rate_limit_remaining,omitempty"`
}

type rollbackBundleOutput struct {
	toolcontract.ToolResponse[rollbackBundleData]
	RequestContext     *toolcontract.RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int                          `json:"rate_limit_remaining"`
}

func newRollbackBundleOutput(data rollbackBundleData, rateLimitRemaining int) rollbackBundleOutput {
	return rollbackBundleOutput{ToolResponse: writeSuccessEnvelopeWithWarning(data, data.Warning), RateLimitRemaining: rateLimitRemaining}
}

// ---- shared helpers ------------------------------------------------------

// resolveBundleDir validates slug and returns the absolute bundle directory,
// rejecting leaf-only slugs (no index.* files) with not_a_bundle.
func resolveBundleDir(pg *security.PathGuard, contentRoot, slug string) (string, map[string]string, error) {
	if _, err := pg.SafeJoin(slug); err != nil {
		return "", nil, fmt.Errorf("invalid_params: path validation failed")
	}
	dir := filepath.Join(contentRoot, slug)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", nil, fmt.Errorf("not_a_bundle: %q is not a page bundle directory; bundle transactions require content/<slug>/index.* files (use plan_content_change for leaf pages)", slug)
	}
	translations, err := contentmodel.BundleTranslations(dir)
	if err != nil {
		return "", nil, fmt.Errorf("read_error: failed to read bundle directory")
	}
	if len(translations) == 0 {
		return "", nil, fmt.Errorf("not_a_bundle: %q has no index.md/index.<lang>.md translation files", slug)
	}
	return dir, translations, nil
}

// writeBundleTranslations writes each candidate to disk under the already-held
// content lock, capturing a pre-write snapshot of every file first. On any
// write failure it restores the already-written files from those snapshots
// (best-effort) and returns the failing index. See the atomicity note at the
// top of this file.
func writeBundleTranslations(pg *security.PathGuard, files []struct {
	Path    string
	Content string
}) (priorContent map[string]string, err error) {
	priorContent = make(map[string]string, len(files))
	for _, f := range files {
		raw, readErr := os.ReadFile(f.Path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("read_error: failed to read %s", filepath.Base(f.Path))
		}
		priorContent[f.Path] = string(raw)
	}

	written := make([]string, 0, len(files))
	rollback := func() {
		for _, p := range written {
			if restoreErr := fileutil.AtomicWriteChecked(p, priorContent[p], pg); restoreErr != nil {
				slog.Error("bundle apply: rollback restore failed", "path", filepath.Base(p), "error", restoreErr)
			}
		}
	}

	for i, f := range files {
		if applyBundleWriteHook != nil {
			if hookErr := applyBundleWriteHook(i); hookErr != nil {
				rollback()
				return nil, fmt.Errorf("write_error: simulated interruption before writing %s: %w", filepath.Base(f.Path), hookErr)
			}
		}
		if revErr := pg.RevalidateForWrite(f.Path); revErr != nil {
			rollback()
			return nil, fmt.Errorf("security_error: symlink detected in write path")
		}
		if writeErr := fileutil.AtomicWriteChecked(f.Path, f.Content, pg); writeErr != nil {
			slog.Error("bundle apply: write failed, rolling back", "path", filepath.Base(f.Path), "error", writeErr)
			rollback()
			return nil, fmt.Errorf("write_error: failed to write %s (bundle rolled back)", filepath.Base(f.Path))
		}
		written = append(written, f.Path)
	}
	return priorContent, nil
}

// indexBundleTranslation refreshes the in-memory source (and public) indexes
// for one just-written translation, mirroring apply_content_plan's per-page
// index maintenance. Returns a warning string if a derived-DB sync failed.
func indexBundleTranslation(idx *hugosite.SourceIndex, siteIdx *site.Index, siteDB *db.DB, slug, lang, filePath, content string) string {
	var updated hugosite.SourcePage
	if existing, ok := idx.GetBySlugLang(slug, lang); ok {
		updated = *existing
	} else if existing, ok := idx.GetBySlug(slug); ok {
		updated = *existing
	} else {
		updated = hugosite.SourcePage{Slug: slug}
	}
	updated.FilePath = filePath
	updated.Lang = lang
	if fm := parseFrontmatterMap([]byte(content)); fm != nil {
		updated.FrontmatterRaw = fm
		if t, ok := fm["title"].(string); ok && t != "" {
			updated.Title = t
		}
	}
	tags, categories := currentTaxonomyFromRaw([]byte(content))
	updated.Tags = tags
	updated.Categories = categories
	updated.Body = bodyFromRaw([]byte(content))
	updated.BuildPending = true
	idx.Upsert(updated)

	if siteIdx != nil {
		if pub, ok := siteIdx.GetBySlug(slug); ok {
			pubUpdated := *pub
			if updated.Title != "" {
				pubUpdated.Title = updated.Title
			}
			pubUpdated.Tags = tags
			pubUpdated.Categories = categories
			siteIdx.UpsertPage(pubUpdated)
		}
	}
	if siteDB != nil {
		if err := siteDB.SyncSourcePage(updated); err != nil {
			slog.Warn("bundle apply: db sync failed", "slug", slug, "lang", lang, "error", err)
			return fmt.Sprintf("%s: source updated but derived DB could not be updated: %v", lang, err)
		}
	}
	return ""
}

// ---- registration --------------------------------------------------------

func registerBundleTools(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteDB *db.DB,
	siteIdx *site.Index,
	mutationMu *sync.Mutex,
	mutationLimiters map[string]*rate.Limiter,
	idem *idempotencyStore,
	plans *bundlePlanStore,
	snapshots *bundleSnapshotStore,
) {
	registerPlanBundleChange(s, pg, idx, cfg, siteIdx, plans)
	registerApplyBundlePlan(s, pg, idx, cfg, siteDB, siteIdx, mutationMu, mutationLimiters, idem, plans, snapshots)
	registerRollbackBundle(s, pg, idx, cfg, siteDB, siteIdx, mutationMu, mutationLimiters, idem, snapshots)
}

func registerPlanBundleChange(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteIdx *site.Index,
	plans *bundlePlanStore,
) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "plan_bundle_change",
		Title: "Plan bundle change",
		Description: "Preview a multi-translation edit to a whole page bundle (content/<slug>/) as one editorial unit, without writing anything. " +
			"Provide per-translation `operations` (same vocabulary as plan_content_change) keyed by `lang` (\"\" = default index.md). " +
			"Returns a single `bundle_revision` anchor over the entire bundle directory plus per-translation diffs, and a `plan_id` (single-use, 5-minute TTL) to pass to apply_bundle_plan. " +
			"Rejects `not_a_bundle` for leaf multilingual pages (slug.<lang>.md with no directory) — use plan_content_change per translation for those. " +
			"Requires no scope — planning never writes (mirrors plan_content_change, #450).",
		InputSchema:  tools.MustSchema[planBundleChangeInput](),
		OutputSchema: tools.MustSchema[planBundleChangeOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in planBundleChangeInput) (*mcp.CallToolResult, planBundleChangeOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, planBundleChangeOutput{}, wrapErr(err)
		}
		if len(in.Translations) == 0 {
			return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: translations must not be empty"))
		}
		dir, translations, err := resolveBundleDir(pg, cfg.ContentRoot, in.Slug)
		if err != nil {
			return nil, planBundleChangeOutput{}, wrapErr(err)
		}

		seen := map[string]bool{}
		var planTranslations []bundleTranslationPlan
		var outcomes []bundleTranslationOutcomeDTO
		for _, tr := range in.Translations {
			lang, langErr := validateLangParam(tr.Lang)
			if langErr != nil {
				return nil, planBundleChangeOutput{}, wrapErr(langErr)
			}
			if seen[lang] {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: translation %q listed more than once", tr.Lang))
			}
			seen[lang] = true
			if len(tr.Operations) == 0 {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: translation %q has no operations", tr.Lang))
			}
			filePath, ok := translations[lang]
			if !ok {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("not_found: bundle %q has no translation for lang %q", in.Slug, tr.Lang))
			}
			raw, readErr := os.ReadFile(filePath)
			if readErr != nil {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("read_error: failed to read translation %q", tr.Lang))
			}
			currentTags, currentCategories := currentTaxonomyFromRaw(raw)
			resolved, resErr := resolvePlanOperations(currentTags, currentCategories, tr.Operations, cfg.BlockedShortcodes)
			if resErr != nil {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("translation %q: %w", tr.Lang, resErr))
			}
			opts := pageUpdateOpts{Tags: resolved.Tags, Categories: resolved.Categories, Draft: resolved.Draft}
			if resolved.Description != "" {
				opts.Description = strPtr(resolved.Description)
			}
			content, updErr := applyPageUpdates(string(raw), resolved.Title, resolved.Body, opts)
			if updErr != nil {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("translation %q: parse_error: failed to update frontmatter", tr.Lang))
			}
			if valErr := validateFrontmatterRoundTrip(content); valErr != nil {
				return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("translation %q: validation_error: %w", tr.Lang, valErr))
			}
			revision := contentmodel.SourceRevisionBytes(raw)
			diff := simpleDiff(in.Slug+"/"+filepath.Base(filePath), string(raw), content)
			planTranslations = append(planTranslations, bundleTranslationPlan{
				Lang: lang, FilePath: filePath, Revision: revision, Content: content,
				Title: resolved.Title, Body: resolved.Body, Tags: resolved.Tags, Categories: resolved.Categories,
				Applied: resolved.Applied, Rejected: resolved.Rejected, Diff: diff,
			})
			outcomes = append(outcomes, bundleTranslationOutcomeDTO{
				Lang: lang, SourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, filePath), Status: "planned",
				BeforeRevision: revision, OperationsApplied: resolved.Applied, OperationsRejected: resolved.Rejected, Diff: diff,
			})
		}

		bundleRev, revErr := contentmodel.BundleRevision(dir)
		if revErr != nil {
			return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("read_error: failed to compute bundle revision"))
		}
		planID, idErr := newBundlePlanID()
		if idErr != nil {
			return nil, planBundleChangeOutput{}, wrapErr(fmt.Errorf("internal_error: failed to allocate plan id"))
		}
		now := time.Now().UTC()
		plans.put(planID, bundlePlanEntry{
			CallerKey: isolationCallerKey(ctx),
			Slug:      in.Slug, BundleDir: dir, BundleRevision: bundleRev,
			Translations: planTranslations, CreatedAt: now,
		})
		_ = siteIdx // state derivation intentionally deferred to apply
		return nil, newPlanBundleChangeOutput(planBundleChangeData{
			Slug: canonicalPublicSlug(in.Slug), BundleRevision: bundleRev, Translations: outcomes,
			PlanID: planID, PlanExpiresAt: now.Add(planTTL).Format(time.RFC3339),
		}), nil
	}))
}

func registerApplyBundlePlan(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteDB *db.DB,
	siteIdx *site.Index,
	mutationMu *sync.Mutex,
	mutationLimiters map[string]*rate.Limiter,
	idem *idempotencyStore,
	plans *bundlePlanStore,
	snapshots *bundleSnapshotStore,
) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "apply_bundle_plan",
		Title: "Apply bundle plan",
		Description: "Write every translation a prior plan_bundle_change previewed, atomically as one editorial unit. " +
			"Fails whole with `bundle_conflict` if the bundle changed since the plan (BundleRevision mismatch), `plan_not_found` if unknown/expired/consumed, or a validation error on ANY translation — in all cases nothing is written. " +
			"A write interrupted partway through is rolled back in-process so the bundle never lands partially (see rollback_bundle for crash recovery). " +
			"Returns per-translation outcomes plus one `bundle_status`. Single-use; `idempotency_key` safely replays; `dry_run` re-verifies without writing. " +
			"Writes source only — call publish_changes to build/deploy.",
		InputSchema:  tools.MustSchema[applyBundlePlanInput](),
		OutputSchema: tools.MustSchema[applyBundlePlanOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in applyBundlePlanInput) (*mcp.CallToolResult, applyBundlePlanOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		wrapErr := func(err error) error { return toolcontract.WithRequestContext(err, toolcontract.RequestContext{}) }
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		if strings.TrimSpace(in.PlanID) == "" {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: plan_id must not be empty"))
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(err)
		}
		if !in.DryRun && !limiter.Allow() {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(rateLimitExceededErr("apply_bundle_plan", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				PlanID string `json:"plan_id"`
			}{PlanID: in.PlanID})
			if hashErr != nil {
				return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached applyBundlePlanOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "apply_bundle_plan", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		// Keep retryable bundle conflicts from consuming the plan (#1001):
		// look the plan up without consuming it, and only consume it once
		// the revision check below has passed.
		entry, ok := plans.get(in.PlanID, isolationCallerKey(ctx))
		if !ok {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("plan_not_found: plan_id is unknown or has expired; call plan_bundle_change again"))
		}
		// From here on, errors carry the slug the plan resolved (#1001) — a
		// bundle spans multiple languages, so RequestedLang stays empty.
		wrapErr = func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: entry.Slug})
		}

		if !acquireContentLock("apply_bundle_plan") {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
		}
		defer releaseContentLock("apply_bundle_plan")

		// Whole-bundle optimistic-concurrency gate: reject if ANY translation
		// or shared asset changed since the plan was made (#854 AC2/AC3).
		currentRev, revErr := contentmodel.BundleRevision(entry.BundleDir)
		if revErr != nil {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to recompute bundle revision"))
		}
		if currentRev != entry.BundleRevision {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("bundle_conflict: bundle changed since the plan was created; call plan_bundle_change again"))
		}
		if !in.DryRun {
			if _, ok := plans.consume(in.PlanID, isolationCallerKey(ctx)); !ok {
				return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("plan_not_found: plan is no longer available; call plan_bundle_change again"))
			}
		}

		// Validate EVERY candidate before writing ANY — a single failure
		// rejects the whole bundle with nothing applied (#854 AC5).
		for _, tr := range entry.Translations {
			if valErr := validateFrontmatterRoundTrip(tr.Content); valErr != nil {
				return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("validation_error: translation %q: %w", tr.Lang, valErr))
			}
			if scErr := rejectDangerousShortcodes(tr.Content, cfg.BlockedShortcodes); scErr != nil {
				return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: translation %q: %w", tr.Lang, scErr))
			}
		}

		if in.DryRun {
			state := updatePageState(siteIdx != nil, bundleHasPublic(siteIdx, entry.Slug))
			return nil, newApplyBundlePlanOutput(applyBundlePlanData{
				Status: "unchanged", PlanID: in.PlanID, Slug: canonicalPublicSlug(entry.Slug), DryRun: true,
				BundleStatus: "valid", BeforeRevision: entry.BundleRevision,
				Translations: bundleDryRunOutcomes(cfg.ContentRoot, entry.Translations), State: &state,
				RateLimit: ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
			}, rateLimitRemaining(limiter)), nil
		}

		files := make([]struct {
			Path    string
			Content string
		}, len(entry.Translations))
		for i, tr := range entry.Translations {
			files[i] = struct {
				Path    string
				Content string
			}{Path: tr.FilePath, Content: tr.Content}
		}
		priorContent, writeErr := writeBundleTranslations(pg, files)
		if writeErr != nil {
			return nil, applyBundlePlanOutput{}, wrapErrWithLimiter(writeErr)
		}
		// Snapshot the whole pre-apply bundle, keyed by the revision it is
		// about to stop being, so rollback_bundle can restore it (#854 AC).
		snapshots.put(entry.BundleDir, entry.BundleRevision, isolationCallerKey(ctx), priorContent)

		var warnings []string
		outcomes := make([]bundleTranslationOutcomeDTO, 0, len(entry.Translations))
		for _, tr := range entry.Translations {
			if w := indexBundleTranslation(idx, siteIdx, siteDB, entry.Slug, tr.Lang, tr.FilePath, tr.Content); w != "" {
				warnings = append(warnings, w)
			}
			outcomes = append(outcomes, bundleTranslationOutcomeDTO{
				Lang: tr.Lang, SourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, tr.FilePath), Status: "applied",
				BeforeRevision: tr.Revision, AfterRevision: contentmodel.SourceRevisionBytes([]byte(tr.Content)),
				OperationsApplied: tr.Applied, OperationsRejected: tr.Rejected,
			})
		}

		afterRev, _ := contentmodel.BundleRevision(entry.BundleDir)
		bundleStatus := "applied"
		status := "updated"
		warning := ""
		if len(warnings) > 0 {
			status = "partial_success"
			warning = strings.Join(warnings, "; ")
		}
		state := updatePageState(siteIdx != nil, bundleHasPublic(siteIdx, entry.Slug))
		out := newApplyBundlePlanOutput(applyBundlePlanData{
			Status: status, PlanID: in.PlanID, Slug: canonicalPublicSlug(entry.Slug),
			BundleStatus: bundleStatus, BeforeRevision: entry.BundleRevision, AfterRevision: afterRev,
			Translations: outcomes, Warning: appendLastBuildWarning(warning), State: &state,
			RateLimit: ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "apply_bundle_plan", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("apply_bundle_plan: could not persist idempotency result", "plan_id", in.PlanID, "error", err)
			}
		}
		return nil, out, nil
	}))
}

func registerRollbackBundle(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteDB *db.DB,
	siteIdx *site.Index,
	mutationMu *sync.Mutex,
	mutationLimiters map[string]*rate.Limiter,
	idem *idempotencyStore,
	snapshots *bundleSnapshotStore,
) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "rollback_bundle",
		Title: "Rollback bundle",
		Description: "Restore every translation of a page bundle to a prior bundle revision a previous apply_bundle_plan produced, atomically. " +
			"`to_bundle_revision` must be a revision apply_bundle_plan snapshotted (24-hour retention); otherwise `snapshot_not_found`. " +
			"Non-dry-run calls require `expected_bundle_revision` (the bundle's current revision) — a stale value fails `bundle_conflict`, so this can never silently undo a newer change. " +
			"All translations restore together or none do. `idempotency_key` safely replays; `dry_run` previews. Writes source only. " +
			"Named rollback_bundle (not rollback_bundle_change) to stay within the 20-char connector tool-name budget (#329).",
		InputSchema:  tools.MustSchema[rollbackBundleInput](),
		OutputSchema: tools.MustSchema[rollbackBundleOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in rollbackBundleInput) (*mcp.CallToolResult, rollbackBundleOutput, error) {
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug})
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		if in.Slug == "" {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(err)
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(err)
		}
		if strings.TrimSpace(in.ToBundleRevision) == "" {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: to_bundle_revision must not be empty"))
		}
		if !in.DryRun && !limiter.Allow() {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(rateLimitExceededErr("rollback_bundle", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}
		dir, _, resErr := resolveBundleDir(pg, cfg.ContentRoot, in.Slug)
		if resErr != nil {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(resErr)
		}

		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				Slug             string `json:"slug"`
				ToBundleRevision string `json:"to_bundle_revision"`
				ExpectedRevision string `json:"expected_bundle_revision"`
			}{Slug: in.Slug, ToBundleRevision: in.ToBundleRevision, ExpectedRevision: in.ExpectedRevision})
			if hashErr != nil {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached rollbackBundleOutput
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "rollback_bundle", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		if !acquireContentLock("rollback_bundle") {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
		}
		defer releaseContentLock("rollback_bundle")

		currentRev, revErr := contentmodel.BundleRevision(dir)
		if revErr != nil {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to compute bundle revision"))
		}
		if !in.DryRun {
			if strings.TrimSpace(in.ExpectedRevision) == "" {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: expected_bundle_revision is required for non-dry-run rollback_bundle"))
			}
			if in.ExpectedRevision != currentRev {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("bundle_conflict: bundle changed since it was read; read the latest bundle revision and retry"))
			}
		}

		restore, ok := snapshots.get(dir, in.ToBundleRevision, isolationCallerKey(ctx))
		if !ok {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("snapshot_not_found: no bundle snapshot recorded for revision %q — only revisions produced by a prior apply_bundle_plan (last 24h) can be rolled back to", in.ToBundleRevision))
		}

		// Validate every snapshot file before touching disk (#636: a snapshot
		// predating a denylist extension must not reintroduce blocked content).
		paths := make([]string, 0, len(restore))
		for p := range restore {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if valErr := validateFrontmatterRoundTrip(restore[p]); valErr != nil {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("validation_error: snapshot %s: %w", filepath.Base(p), valErr))
			}
			if scErr := rejectDangerousShortcodes(restore[p], cfg.BlockedShortcodes); scErr != nil {
				return nil, rollbackBundleOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: snapshot %s: %w", filepath.Base(p), scErr))
			}
		}

		outcomes := make([]bundleTranslationOutcomeDTO, 0, len(paths))
		if in.DryRun {
			for _, p := range paths {
				outcomes = append(outcomes, bundleTranslationOutcomeDTO{
					Lang: inferLangFromIndexFile(p), SourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, p), Status: "would_restore",
				})
			}
			return nil, newRollbackBundleOutput(rollbackBundleData{
				Status: "unchanged", Slug: canonicalPublicSlug(in.Slug), DryRun: true, BundleStatus: "restorable",
				BeforeRevision: currentRev, Translations: outcomes,
				RateLimit: ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
			}, rateLimitRemaining(limiter)), nil
		}

		files := make([]struct {
			Path    string
			Content string
		}, len(paths))
		for i, p := range paths {
			files[i] = struct {
				Path    string
				Content string
			}{Path: p, Content: restore[p]}
		}
		if _, writeErr := writeBundleTranslations(pg, files); writeErr != nil {
			return nil, rollbackBundleOutput{}, wrapErrWithLimiter(writeErr)
		}

		var warnings []string
		for _, p := range paths {
			lang := inferLangFromIndexFile(p)
			if w := indexBundleTranslation(idx, siteIdx, siteDB, in.Slug, lang, p, restore[p]); w != "" {
				warnings = append(warnings, w)
			}
			outcomes = append(outcomes, bundleTranslationOutcomeDTO{
				Lang: lang, SourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, p), Status: "restored",
				AfterRevision: contentmodel.SourceRevisionBytes([]byte(restore[p])),
			})
		}
		afterRev, _ := contentmodel.BundleRevision(dir)
		status := "restored"
		warning := ""
		if len(warnings) > 0 {
			status = "partial_success"
			warning = strings.Join(warnings, "; ")
		}
		state := updatePageState(siteIdx != nil, bundleHasPublic(siteIdx, in.Slug))
		out := newRollbackBundleOutput(rollbackBundleData{
			Status: status, Slug: canonicalPublicSlug(in.Slug), BundleStatus: "restored",
			BeforeRevision: currentRev, AfterRevision: afterRev, Translations: outcomes,
			Warning: appendLastBuildWarning(warning), State: &state,
			RateLimit: ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
		}, rateLimitRemaining(limiter))
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "rollback_bundle", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("rollback_bundle: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))
}

// bundleDryRunOutcomes projects a plan's translations into dry-run outcome DTOs.
func bundleDryRunOutcomes(contentRoot string, translations []bundleTranslationPlan) []bundleTranslationOutcomeDTO {
	out := make([]bundleTranslationOutcomeDTO, 0, len(translations))
	for _, tr := range translations {
		out = append(out, bundleTranslationOutcomeDTO{
			Lang: tr.Lang, SourcePath: fileutil.LogicalContentPath(contentRoot, tr.FilePath), Status: "valid",
			BeforeRevision: tr.Revision, OperationsApplied: tr.Applied, OperationsRejected: tr.Rejected, Diff: tr.Diff,
		})
	}
	return out
}

func bundleHasPublic(siteIdx *site.Index, slug string) bool {
	if siteIdx == nil {
		return false
	}
	_, ok := siteIdx.GetBySlug(slug)
	return ok
}

// acquireContentLock / releaseContentLock centralise the TryLock-with-deadline
// pattern the single-page mutation tools inline, so bundle apply/rollback take
// the *same* hugosite.ContentMu once for the whole multi-file operation.
func acquireContentLock(tool string) bool {
	const lockWait = 10 * time.Second
	deadline := time.Now().Add(lockWait)
	for {
		if hugosite.ContentMu.TryLock() {
			slog.Debug(tool + ": lock_acquired")
			return true
		}
		if time.Now().After(deadline) {
			slog.Error(tool+": lock_timeout", "timeout_s", lockWait.Seconds())
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func releaseContentLock(tool string) {
	hugosite.ContentMu.Unlock()
	slog.Debug(tool + ": lock_released")
}
