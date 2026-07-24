package write

// rollback_change (#438, #340, amended #379 — see docs/transactional-edit-
// design.md §4 and the comment thread on #379). Restores a page's source to
// a state this server's own apply_content_plan captured, guarded by the
// same expected_revision optimistic-concurrency check every other write
// tool uses. Deliberately narrower than git-commit-based rollback: only
// revisions apply_content_plan itself produced and snapshotted are
// rollback-able, not arbitrary history.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// snapshotTTL/snapshotMaxEntries are deliberately more generous than the
// plan store's: a plan is a short-lived preview handle, but a snapshot is
// the actual undo history for a page — a caller may reasonably want to roll
// back something applied hours ago, not just seconds ago.
const snapshotTTL = 24 * time.Hour
const snapshotMaxEntries = 512

type snapshotEntry struct {
	Content   string
	CreatedAt time.Time
}

// snapshotStore mirrors idempotencyStore/planStore's shape (map + mutex +
// TTL prune + max-entries eviction). Unlike planStore, get does not consume
// — a snapshot is idempotent-safe to roll back to more than once (matching
// rollback_change's IdempotentHint), not a single-use preview.
type snapshotStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]snapshotEntry
}

func newSnapshotStore(ttl time.Duration, maxEntries int) *snapshotStore {
	return &snapshotStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]snapshotEntry),
	}
}

func snapshotKey(filePath, revision string) string {
	return filePath + "\x00" + revision
}

func (s *snapshotStore) put(filePath, revision, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneLocked(now)
	s.entries[snapshotKey(filePath, revision)] = snapshotEntry{Content: content, CreatedAt: now}
	s.trimLocked()
}

func (s *snapshotStore) get(filePath, revision string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[snapshotKey(filePath, revision)]
	if !ok {
		return "", false
	}
	return entry.Content, true
}

func (s *snapshotStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for key, entry := range s.entries {
		if now.Sub(entry.CreatedAt) > s.ttl {
			delete(s.entries, key)
		}
	}
}

func (s *snapshotStore) trimLocked() {
	if s.maxEntries <= 0 || len(s.entries) <= s.maxEntries {
		return
	}
	for len(s.entries) > s.maxEntries {
		var oldestKey string
		var oldest time.Time
		first := true
		for key, entry := range s.entries {
			if first || entry.CreatedAt.Before(oldest) {
				oldestKey = key
				oldest = entry.CreatedAt
				first = false
			}
		}
		delete(s.entries, oldestKey)
	}
}

type rollbackChangeInput struct {
	Slug             string `json:"slug"`
	Lang             string `json:"lang,omitempty"`
	ToRevision       string `json:"to_revision"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	DryRun           bool   `json:"dry_run,omitempty"`
}

type rollbackChangeData struct {
	Status             string               `json:"status,omitempty"`
	Slug               string               `json:"slug,omitempty"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	Diff               string               `json:"diff,omitempty"`
	BeforeRevision     string               `json:"before_revision,omitempty"`
	AfterRevision      string               `json:"after_revision,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
	RateLimitRemaining int                  `json:"rate_limit_remaining"`
}

type rollbackChangeOutput struct {
	toolcontract.ToolResponse[rollbackChangeData]
	RequestContext     *toolcontract.RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int                          `json:"rate_limit_remaining"`
}

func newRollbackChangeOutput(data rollbackChangeData) rollbackChangeOutput {
	return rollbackChangeOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: data.RateLimitRemaining,
	}
}

func registerRollbackChange(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteDB *db.DB,
	siteIdx *site.Index,
	mutationMu *sync.Mutex,
	mutationLimiters map[string]*rate.Limiter,
	idem *idempotencyStore,
	snapshots *snapshotStore,
) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "rollback_change",
		Title: "Rollback change",
		Description: "Restore a page's source to a prior revision this server's own apply_content_plan produced. " +
			"`to_revision` must be a revision apply_content_plan previously wrote — not arbitrary git history (this deployment has no controlled git-commit capability; see #379). " +
			"Fails with `snapshot_not_found` if no snapshot was captured for that revision of this page (only revisions produced by apply_content_plan are ever snapshotted, with a 24-hour retention) or the resolved file/language doesn't match what the snapshot was captured for. " +
			"Non-dry-run calls require `expected_revision`, the page's *current* revision — a stale value fails with `revision_conflict`, the same optimistic-concurrency guard every other write tool uses, so this can never silently undo a newer, unrelated change. " +
			"Callers may provide `idempotency_key` to safely replay the exact same non-dry-run rollback after a timeout or uncertain delivery. " +
			"`dry_run` previews the diff without writing. " +
			"Writes source only — like apply_content_plan, does not build/publish; call publish_changes afterward. " +
			"`rate_limit_remaining` reports the caller's remaining budget on the shared create/update/upload quota (#466).",
		InputSchema:  tools.MustSchema[rollbackChangeInput](),
		OutputSchema: tools.MustSchema[rollbackChangeOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in rollbackChangeInput) (*mcp.CallToolResult, rollbackChangeOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug, RequestedLang: in.Lang})
		}
		if in.Slug == "" {
			return nil, rollbackChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		lang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, rollbackChangeOutput{}, wrapErr(err)
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, rollbackChangeOutput{}, wrapErr(err)
		}
		if strings.TrimSpace(in.ToRevision) == "" {
			return nil, rollbackChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: to_revision must not be empty"))
		}

		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			fields := map[string]any{"rate_limit_remaining": rateLimitRemaining(limiter)}
			return toolcontract.WithDataFields(toolcontract.WithRootFields(wrapErr(err), fields), fields)
		}
		if !in.DryRun && !limiter.Allow() {
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(rateLimitExceededErr("rollback_change", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		if _, err := pg.SafeJoin(in.Slug); err != nil {
			slog.Warn("rollback_change: path validation failed", "slug", in.Slug, "error", err)
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: path validation failed"))
		}

		resolvedSource, langErr := resolveExistingSource(cfg.ContentRoot, in.Slug, lang)
		if langErr != nil {
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(langErr)
		}
		filePath := resolvedSource.SourcePath

		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				Slug             string `json:"slug"`
				Lang             string `json:"lang,omitempty"`
				ToRevision       string `json:"to_revision"`
				ExpectedRevision string `json:"expected_revision"`
			}{Slug: in.Slug, Lang: lang, ToRevision: in.ToRevision, ExpectedRevision: in.ExpectedRevision})
			if hashErr != nil {
				return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached rollbackChangeOutput
			hit, replayErr := idem.replay("rollback_change", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, rollbackChangeOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("rollback_change: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("rollback_change: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("rollback_change: lock_released")
		}()

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("rollback_change: read failed", "slug", in.Slug, "path", filePath, "error", err)
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read page"))
		}
		currentRevision := contentmodel.SourceRevisionBytes(raw)
		if !in.DryRun {
			if strings.TrimSpace(in.ExpectedRevision) == "" {
				return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: expected_revision is required for non-dry-run rollback_change"))
			}
			if in.ExpectedRevision != currentRevision {
				return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and retry"))
			}
		}

		snapshotContent, ok := snapshots.get(filePath, in.ToRevision)
		if !ok {
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("snapshot_not_found: no snapshot recorded for revision %q of this page — only revisions produced by a prior apply_content_plan call, within the last 24 hours, can be rolled back to", in.ToRevision))
		}
		if err := validateFrontmatterRoundTrip(snapshotContent); err != nil {
			slog.Error("rollback_change: round-trip guard failed on stored snapshot", "slug", in.Slug, "error", err)
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("validation_error: %w", err))
		}

		if in.DryRun {
			diffLabel := in.Slug + "/" + filepath.Base(filePath)
			diff := simpleDiff(diffLabel, string(raw), snapshotContent)
			return nil, newRollbackChangeOutput(rollbackChangeData{
				Status:             "ok",
				Slug:               canonicalPublicSlug(in.Slug),
				DryRun:             true,
				Diff:               diff,
				BeforeRevision:     currentRevision,
				RateLimitRemaining: rateLimitRemaining(limiter),
			}), nil
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("rollback_change: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if err := fileutil.AtomicWriteChecked(filePath, snapshotContent, pg); err != nil {
			slog.Error("rollback_change: write failed", "slug", in.Slug, "error", err)
			return nil, rollbackChangeOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}

		restoredTags, restoredCategories := currentTaxonomyFromRaw([]byte(snapshotContent))
		restoredFM := parseFrontmatterMap([]byte(snapshotContent))
		var restoredTitle string
		if restoredFM != nil {
			if t, ok := restoredFM["title"].(string); ok {
				restoredTitle = t
			}
		}

		var updated hugosite.SourcePage
		if existing, hasExisting := idx.GetBySlug(in.Slug); hasExisting {
			updated = *existing
		} else {
			updated = hugosite.SourcePage{Slug: in.Slug}
		}
		updated.FilePath = filePath
		updated.Lang = resolvedSource.Lang
		if restoredTitle != "" {
			updated.Title = restoredTitle
			if updated.FrontmatterRaw == nil {
				updated.FrontmatterRaw = make(map[string]any)
			}
			updated.FrontmatterRaw["title"] = restoredTitle
		}
		updated.Tags = restoredTags
		updated.Categories = restoredCategories
		updated.BuildPending = true
		idx.Upsert(updated)

		hadPublic := false
		if siteIdx != nil {
			if pub, ok := siteIdx.GetBySlug(in.Slug); ok {
				hadPublic = true
				pubUpdated := *pub
				if restoredTitle != "" {
					pubUpdated.Title = restoredTitle
				}
				pubUpdated.Tags = restoredTags
				pubUpdated.Categories = restoredCategories
				siteIdx.UpsertPage(pubUpdated)
			}
		}

		status := "ok"
		warning := ""
		if siteDB != nil {
			if err := siteDB.SyncSourcePage(updated); err != nil {
				slog.Warn("rollback_change: db sync failed", "slug", in.Slug, "error", err)
				status = "partial_success"
				warning = fmt.Sprintf("source rolled back but derived DB could not be updated: %v", err)
			}
		}

		state := updatePageState(siteIdx != nil, hadPublic)
		out := newRollbackChangeOutput(rollbackChangeData{
			Status:             status,
			Slug:               canonicalPublicSlug(in.Slug),
			BeforeRevision:     currentRevision,
			AfterRevision:      contentmodel.SourceRevisionBytes([]byte(snapshotContent)),
			Warning:            appendLastBuildWarning(warning),
			State:              &state,
			RateLimitRemaining: rateLimitRemaining(limiter),
		})
		if idemHash != "" {
			if err := idem.remember("rollback_change", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("rollback_change: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))
}
