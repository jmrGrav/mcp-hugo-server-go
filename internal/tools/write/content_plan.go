package write

// plan_content_change / apply_content_plan (#438, design anchor #338, see
// docs/transactional-edit-design.md). A plan is a server-held, TTL'd,
// single-use preview: plan_content_change never writes, apply_content_plan
// replays exactly the content a plan already computed, nothing re-derived
// from fresh input. rollback_change and publish_changes are deliberately not
// part of this file — rollback_change stays blocked on this deployment
// having no controlled git-commit capability (see docs/git-baseline-model.md
// and #379's invariant that only a real commit is a valid rollback target,
// never "the state before the last apply"); publish_changes is a separate,
// later layer per the design doc's own sequencing.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	"gopkg.in/yaml.v3"
)

const planTTL = 5 * time.Minute
const planMaxEntries = 128

// planEntry is the server-held, single-use record a plan_id resolves to.
// Mirrors idempotencyStore's shape (map + mutex + TTL prune + max-entries
// eviction) per the design doc, deliberately a separate store instance since
// plans and idempotency results have different lifetimes and replay
// semantics.
type planEntry struct {
	Slug       string
	Lang       string
	FilePath   string
	Revision   string // the pinned baseline apply_content_plan re-checks
	Content    string // exact candidate bytes apply_content_plan will write
	Title      string
	Body       string
	Tags       []string
	Categories []string
	CreatedAt  time.Time
}

type planStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]planEntry
}

func newPlanStore(ttl time.Duration, maxEntries int) *planStore {
	return &planStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]planEntry),
	}
}

func (s *planStore) put(id string, entry planEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	s.entries[id] = entry
	s.trimLocked()
}

// get looks up a plan without consuming it (used for a dry-run apply, which
// re-verifies but must not remove the plan).
func (s *planStore) get(id string) (planEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	return entry, ok
}

// consume looks up and atomically removes a plan. Per the design doc, a plan
// is single-use: applying it (successfully or not) removes it from the
// store, so it can never be replayed against a page that has since changed
// without a fresh plan_content_change call.
func (s *planStore) consume(id string) (planEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	if ok {
		delete(s.entries, id)
	}
	return entry, ok
}

func (s *planStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for id, entry := range s.entries {
		if now.Sub(entry.CreatedAt) > s.ttl {
			delete(s.entries, id)
		}
	}
}

func (s *planStore) trimLocked() {
	if s.maxEntries <= 0 || len(s.entries) <= s.maxEntries {
		return
	}
	for len(s.entries) > s.maxEntries {
		var oldestID string
		var oldest time.Time
		first := true
		for id, entry := range s.entries {
			if first || entry.CreatedAt.Before(oldest) {
				oldestID = id
				oldest = entry.CreatedAt
				first = false
			}
		}
		delete(s.entries, oldestID)
	}
}

func newPlanID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "plan_" + hex.EncodeToString(b), nil
}

type planOperationInput struct {
	// Op is one of: update_body, set_title, add_tag, remove_tag,
	// add_category, remove_category, set_draft, set_field.
	Op string `json:"op"`
	// Body is required for update_body.
	Body string `json:"body,omitempty"`
	// Value is required for set_title, add_tag, remove_tag, add_category,
	// remove_category, and (paired with Field) set_field.
	Value string `json:"value,omitempty"`
	// Field is required for set_field; only "description" is supported.
	Field string `json:"field,omitempty"`
	// DraftValue is required for set_draft.
	DraftValue *bool `json:"draft_value,omitempty"`
}

type planContentChangeInput struct {
	Slug       string               `json:"slug"`
	Lang       string               `json:"lang,omitempty"`
	Operations []planOperationInput `json:"operations"`
}

type planTargetDTO struct {
	Slug               string              `json:"slug"`
	ResolvedSourcePath string              `json:"resolved_source_path"`
	Revision           string              `json:"revision"`
	State              site.LifecycleState `json:"state"`
}

type planRejectedOperationDTO struct {
	Op     string `json:"op"`
	Reason string `json:"reason"`
}

type planEstimatedDiffDTO struct {
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

type planContentChangeData struct {
	Target               planTargetDTO              `json:"target"`
	OperationsApplied    []string                   `json:"operations_applied,omitempty"`
	OperationsRejected   []planRejectedOperationDTO `json:"operations_rejected,omitempty"`
	Diff                 string                     `json:"diff,omitempty"`
	EstimatedDiff        planEstimatedDiffDTO       `json:"estimated_diff"`
	PlanID               string                     `json:"plan_id,omitempty"`
	PlanExpiresAt        string                     `json:"plan_expires_at,omitempty"`
	RequiresConfirmation bool                       `json:"requires_confirmation"`
}

type planContentChangeOutput struct {
	toolcontract.ToolResponse[planContentChangeData]
	// RequestContext — see the comment on createPageOutput.RequestContext.
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
}

func newPlanContentChangeOutput(data planContentChangeData) planContentChangeOutput {
	return planContentChangeOutput{ToolResponse: writeSuccessEnvelope(data)}
}

type applyContentPlanInput struct {
	PlanID         string `json:"plan_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type applyContentPlanData struct {
	Status             string               `json:"status,omitempty"`
	PlanID             string               `json:"plan_id,omitempty"`
	Slug               string               `json:"slug,omitempty"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	BeforeRevision     string               `json:"before_revision,omitempty"`
	AfterRevision      string               `json:"after_revision,omitempty"`
	Validation         string               `json:"validation,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
	RateLimitRemaining int                  `json:"rate_limit_remaining"`
}

type applyContentPlanOutput struct {
	toolcontract.ToolResponse[applyContentPlanData]
	RequestContext     *toolcontract.RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int                          `json:"rate_limit_remaining"`
}

func newApplyContentPlanOutput(data applyContentPlanData) applyContentPlanOutput {
	return applyContentPlanOutput{
		ToolResponse:       writeSuccessEnvelope(data),
		RateLimitRemaining: data.RateLimitRemaining,
	}
}

type resolvedPlanOperations struct {
	Title       string
	Body        string
	Tags        []string
	Categories  []string
	Draft       *bool
	Description string
	Applied     []string
	Rejected    []planRejectedOperationDTO
}

// parseFrontmatterMap decodes a source file's YAML frontmatter into a plain
// map, independent of the (not language-aware) source index — used wherever
// a handler needs a page's *current* on-disk fields without trusting
// idx.GetBySlug's possibly-wrong-language or stale cache. See the comment at
// plan_content_change's call site for why that matters.
func parseFrontmatterMap(raw []byte) map[string]any {
	content := string(raw)
	if !strings.HasPrefix(content, "---") {
		return nil
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return nil
	}
	fm := map[string]any{}
	if err := yaml.NewDecoder(strings.NewReader(parts[1])).Decode(&fm); err != nil {
		return nil
	}
	return fm
}

// currentTaxonomyFromRaw reads tags/categories straight out of a source
// file's own frontmatter bytes. See parseFrontmatterMap's comment.
func currentTaxonomyFromRaw(raw []byte) (tags, categories []string) {
	fm := parseFrontmatterMap(raw)
	if fm == nil {
		return nil, nil
	}
	return toStringSlice(fm["tags"]), toStringSlice(fm["categories"])
}

// resolvePlanOperations turns the small, deliberately non-general operation
// vocabulary (docs/transactional-edit-design.md §2) into the same
// pageUpdateOpts shape update_page already consumes. add_tag/remove_tag/
// add_category/remove_category compute a delta against the page's current
// tags/categories (from the source index) rather than requiring the caller
// to resend the full list — the one place this tool's contract genuinely
// diverges from update_page's "always send the full list" contract.

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprint(item))
			}
		}
		return out
	default:
		return nil
	}
}

func resolvePlanOperations(existingTags, existingCategories []string, ops []planOperationInput, blockedShortcodes []string) (resolvedPlanOperations, error) {
	var out resolvedPlanOperations
	tags := slices.Clone(existingTags)
	categories := slices.Clone(existingCategories)
	tagsChanged := false
	categoriesChanged := false

	for _, op := range ops {
		switch op.Op {
		case "update_body":
			if strings.TrimSpace(op.Body) == "" {
				return out, fmt.Errorf("invalid_params: update_body operation requires a non-empty body")
			}
			if err := validateBodyFormat(op.Body, blockedShortcodes); err != nil {
				return out, err
			}
			out.Body = op.Body
			out.Applied = append(out.Applied, "update_body")
		case "set_title":
			if strings.TrimSpace(op.Value) == "" {
				return out, fmt.Errorf("invalid_params: set_title operation requires a non-empty value")
			}
			if err := validateTitleFormat(op.Value); err != nil {
				return out, err
			}
			out.Title = op.Value
			out.Applied = append(out.Applied, "set_title")
		case "add_tag":
			if op.Value == "" {
				return out, fmt.Errorf("invalid_params: add_tag operation requires value")
			}
			if slices.Contains(tags, op.Value) {
				out.Rejected = append(out.Rejected, planRejectedOperationDTO{Op: "add_tag:" + op.Value, Reason: "tag already present"})
			} else {
				tags = append(tags, op.Value)
				tagsChanged = true
				out.Applied = append(out.Applied, "add_tag:"+op.Value)
			}
		case "remove_tag":
			if op.Value == "" {
				return out, fmt.Errorf("invalid_params: remove_tag operation requires value")
			}
			if i := slices.Index(tags, op.Value); i < 0 {
				out.Rejected = append(out.Rejected, planRejectedOperationDTO{Op: "remove_tag:" + op.Value, Reason: "tag not present"})
			} else {
				tags = slices.Delete(tags, i, i+1)
				tagsChanged = true
				out.Applied = append(out.Applied, "remove_tag:"+op.Value)
			}
		case "add_category":
			if op.Value == "" {
				return out, fmt.Errorf("invalid_params: add_category operation requires value")
			}
			if slices.Contains(categories, op.Value) {
				out.Rejected = append(out.Rejected, planRejectedOperationDTO{Op: "add_category:" + op.Value, Reason: "category already present"})
			} else {
				categories = append(categories, op.Value)
				categoriesChanged = true
				out.Applied = append(out.Applied, "add_category:"+op.Value)
			}
		case "remove_category":
			if op.Value == "" {
				return out, fmt.Errorf("invalid_params: remove_category operation requires value")
			}
			if i := slices.Index(categories, op.Value); i < 0 {
				out.Rejected = append(out.Rejected, planRejectedOperationDTO{Op: "remove_category:" + op.Value, Reason: "category not present"})
			} else {
				categories = slices.Delete(categories, i, i+1)
				categoriesChanged = true
				out.Applied = append(out.Applied, "remove_category:"+op.Value)
			}
		case "set_draft":
			if op.DraftValue == nil {
				return out, fmt.Errorf("invalid_params: set_draft operation requires draft_value")
			}
			out.Draft = op.DraftValue
			out.Applied = append(out.Applied, "set_draft")
		case "set_field":
			if op.Field != "description" {
				return out, fmt.Errorf("invalid_params: set_field only supports field \"description\" in this version")
			}
			if err := rejectUnsafeText(op.Value); err != nil {
				return out, fmt.Errorf("invalid_params: description %w", err)
			}
			out.Description = op.Value
			out.Applied = append(out.Applied, "set_field:description")
		case "":
			return out, fmt.Errorf("invalid_params: operations[].op must not be empty")
		default:
			return out, fmt.Errorf("invalid_params: unknown operation %q", op.Op)
		}
	}

	if tagsChanged {
		out.Tags = tags
	}
	if categoriesChanged {
		out.Categories = categories
	}
	return out, nil
}

// diffLineCounts counts +/- lines in a unified diff produced by simpleDiff,
// skipping the "+++ "/"--- " header lines.
func diffLineCounts(diff string) (added, removed int) {
	for _, line := range strings.Split(diff, "\n") {
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

// planConfirmationLineThreshold is the diff size (added+removed lines) above
// which plan_content_change flags requires_confirmation. Informational only
// — see docs/transactional-edit-design.md §7: apply_content_plan requiring
// a separate call is the actual enforcement, not this field.
const planConfirmationLineThreshold = 20

func registerContentPlanTools(
	s *mcp.Server,
	pg *security.PathGuard,
	idx *hugosite.SourceIndex,
	cfg config.Config,
	siteDB *db.DB,
	siteIdx *site.Index,
	mutationMu *sync.Mutex,
	mutationLimiters map[string]*rate.Limiter,
	idem *idempotencyStore,
	plans *planStore,
	snapshots *snapshotStore,
) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "plan_content_change",
		Title: "Plan content change",
		Description: "Preview a set of discrete edits to an existing page — update_body, set_title, add_tag/remove_tag, add_category/remove_category, set_draft, set_field (field: \"description\" only) — without writing anything. " +
			"add_tag/remove_tag/add_category/remove_category compute a delta against the page's current tags/categories, so you only send what's changing, not the full list. " +
			"Operations that don't apply cleanly (e.g. remove_tag for a tag the page doesn't have) are reported in `data.operations_rejected` without failing the whole plan. " +
			"Returns `data.plan_id`, a server-held, single-use preview that expires after 5 minutes (`data.plan_expires_at`); pass it to apply_content_plan to write exactly what was previewed, nothing re-derived. " +
			"`data.diff`/`data.estimated_diff` show exactly what would change, computed the same way update_page's dry_run does. " +
			"Requires no scope — planning never writes (#450).",
		InputSchema:  tools.MustSchema[planContentChangeInput](),
		OutputSchema: tools.MustSchema[planContentChangeOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in planContentChangeInput) (*mcp.CallToolResult, planContentChangeOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: in.Slug, RequestedLang: in.Lang})
		}
		if in.Slug == "" {
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		lang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, planContentChangeOutput{}, wrapErr(err)
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, planContentChangeOutput{}, wrapErr(err)
		}
		if len(in.Operations) == 0 {
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: operations must not be empty"))
		}
		if _, err := pg.SafeJoin(in.Slug); err != nil {
			slog.Warn("plan_content_change: path validation failed", "slug", in.Slug, "error", err)
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("invalid_params: path validation failed"))
		}

		resolvedSource, langErr := resolveExistingSource(cfg.ContentRoot, in.Slug, lang)
		if langErr != nil {
			return nil, planContentChangeOutput{}, wrapErr(langErr)
		}
		filePath := resolvedSource.SourcePath

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("plan_content_change: read failed", "slug", in.Slug, "path", filePath, "error", err)
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("read_error: failed to read page"))
		}
		revision := contentmodel.SourceRevisionBytes(raw)

		// add_tag/remove_tag/add_category/remove_category compute a delta
		// against the page's *current* tags/categories — read from the
		// resolved file's own frontmatter, not idx.GetBySlug. The source
		// index's bySlug lookup is not language-aware (for a bilingual page
		// it returns whichever language happened to be indexed last), so
		// using it here would compute the delta against the wrong
		// language's tags and then overwrite the correct file's tags with
		// that wrong-language-derived list (setYAMLSeq replaces, it doesn't
		// merge). Reading straight from raw also can't be stale relative to
		// the file this plan is about to pin its revision against.
		currentTags, currentCategories := currentTaxonomyFromRaw(raw)

		resolved, err := resolvePlanOperations(currentTags, currentCategories, in.Operations, cfg.BlockedShortcodes)
		if err != nil {
			return nil, planContentChangeOutput{}, wrapErr(err)
		}

		opts := pageUpdateOpts{
			Tags:        resolved.Tags,
			Categories:  resolved.Categories,
			Draft:       resolved.Draft,
			Description: resolved.Description,
		}
		content, err := applyPageUpdates(string(raw), resolved.Title, resolved.Body, opts)
		if err != nil {
			slog.Error("plan_content_change: frontmatter update failed", "slug", in.Slug, "error", err)
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("parse_error: failed to update frontmatter"))
		}
		if err := validateFrontmatterRoundTrip(content); err != nil {
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("validation_error: %w", err))
		}

		diffLabel := in.Slug + "/" + filepath.Base(filePath)
		diff := simpleDiff(diffLabel, string(raw), content)
		added, removed := diffLineCounts(diff)

		hadPublic := false
		if siteIdx != nil {
			_, hadPublic = siteIdx.GetBySlug(in.Slug)
		}
		state := updatePageState(siteIdx != nil, hadPublic)

		planID, err := newPlanID()
		if err != nil {
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("internal_error: failed to allocate plan id"))
		}
		now := time.Now().UTC()
		plans.put(planID, planEntry{
			Slug:       in.Slug,
			Lang:       resolvedSource.Lang,
			FilePath:   filePath,
			Revision:   revision,
			Content:    content,
			Title:      resolved.Title,
			Body:       resolved.Body,
			Tags:       resolved.Tags,
			Categories: resolved.Categories,
			CreatedAt:  now,
		})

		logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
		return nil, newPlanContentChangeOutput(planContentChangeData{
			Target: planTargetDTO{
				Slug:               canonicalPublicSlug(in.Slug),
				ResolvedSourcePath: logicalPath,
				Revision:           revision,
				State:              state,
			},
			OperationsApplied:    resolved.Applied,
			OperationsRejected:   resolved.Rejected,
			Diff:                 diff,
			EstimatedDiff:        planEstimatedDiffDTO{LinesAdded: added, LinesRemoved: removed},
			PlanID:               planID,
			PlanExpiresAt:        now.Add(planTTL).Format(time.RFC3339),
			RequiresConfirmation: added+removed > planConfirmationLineThreshold,
		}), nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:  "apply_content_plan",
		Title: "Apply content plan",
		Description: "Write exactly what a prior plan_content_change call previewed — no body/tags/title are resent, apply executes the plan's frozen content verbatim. " +
			"Fails with `plan_not_found` if `plan_id` is unknown, already applied, or its 5-minute TTL expired (call plan_content_change again); fails with `revision_conflict` if the page changed since the plan was created. " +
			"A plan is single-use: this call consumes it whether the write succeeds or fails. " +
			"Callers may provide `idempotency_key` to safely replay the exact same non-dry-run apply after a timeout or uncertain delivery. " +
			"`dry_run` re-verifies the plan without writing or consuming it. " +
			"Deliberately writes source only — no build/publish/index-freshness fields in the response; that is publish_changes's layer, a separate, later, explicitly-confirmed step. " +
			"`rate_limit_remaining` reports the caller's remaining budget on the shared create/update/upload quota (#466).",
		InputSchema:  tools.MustSchema[applyContentPlanInput](),
		OutputSchema: tools.MustSchema[applyContentPlanOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in applyContentPlanInput) (*mcp.CallToolResult, applyContentPlanOutput, error) {
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{})
		}
		if strings.TrimSpace(in.PlanID) == "" {
			return nil, applyContentPlanOutput{}, wrapErr(fmt.Errorf("invalid_params: plan_id must not be empty"))
		}

		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			fields := map[string]any{"rate_limit_remaining": rateLimitRemaining(limiter)}
			return toolcontract.WithDataFields(toolcontract.WithRootFields(wrapErr(err), fields), fields)
		}
		if !in.DryRun && !limiter.Allow() {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(rateLimitExceededErr("apply_content_plan", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		// Idempotency replay is checked before the plan lookup: a plan is
		// single-use and deleted the moment a real (non-dry-run) apply
		// attempt is made, successful or not. A genuine retry of an
		// already-applied request must not depend on the plan still
		// existing, or replay would be indistinguishable from
		// plan_not_found on the second call — deliberately reordered from
		// the design doc's literal listing (which checked plan existence
		// first) once implementing surfaced that gap.
		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(struct {
				PlanID string `json:"plan_id"`
			}{PlanID: in.PlanID})
			if hashErr != nil {
				return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached applyContentPlanOutput
			hit, replayErr := idem.replay("apply_content_plan", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, applyContentPlanOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		var entry planEntry
		var ok bool
		if in.DryRun {
			entry, ok = plans.get(in.PlanID)
		} else {
			entry, ok = plans.consume(in.PlanID)
		}
		if !ok {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("plan_not_found: plan_id is unknown or has expired; call plan_content_change again"))
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("apply_content_plan: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("apply_content_plan: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("apply_content_plan: lock_released")
		}()

		raw, err := os.ReadFile(entry.FilePath)
		if err != nil {
			slog.Error("apply_content_plan: read failed", "plan_id", in.PlanID, "path", entry.FilePath, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("read_error: failed to read page"))
		}
		currentRevision := contentmodel.SourceRevisionBytes(raw)
		if entry.Revision != currentRevision {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("revision_conflict: page changed since the plan was created; call plan_content_change again"))
		}

		if err := validateFrontmatterRoundTrip(entry.Content); err != nil {
			slog.Error("apply_content_plan: round-trip guard failed", "plan_id", in.PlanID, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("validation_error: %w", err))
		}

		if in.DryRun {
			hadPublic := false
			if siteIdx != nil {
				_, hadPublic = siteIdx.GetBySlug(entry.Slug)
			}
			state := updatePageState(siteIdx != nil, hadPublic)
			return nil, newApplyContentPlanOutput(applyContentPlanData{
				Status:             "ok",
				PlanID:             in.PlanID,
				Slug:               canonicalPublicSlug(entry.Slug),
				DryRun:             true,
				BeforeRevision:     entry.Revision,
				Validation:         "passed",
				State:              &state,
				RateLimitRemaining: rateLimitRemaining(limiter),
			}), nil
		}

		if err := pg.RevalidateForWrite(entry.FilePath); err != nil {
			slog.Warn("apply_content_plan: symlink-swap detected before write", "plan_id", in.PlanID, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		if err := fileutil.AtomicWriteChecked(entry.FilePath, entry.Content, pg); err != nil {
			slog.Error("apply_content_plan: write failed", "plan_id", in.PlanID, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}
		// Snapshot the pre-write content, keyed by the revision it's about
		// to stop being, so rollback_change can restore exactly this state
		// later (#379's amended invariant — see docs/transactional-edit-
		// design.md §4). Only captured on a successful write: a failed
		// write never changed the file, so there's nothing new to roll
		// back from.
		snapshots.put(entry.FilePath, entry.Revision, string(raw))

		var updated hugosite.SourcePage
		if existing, hasExisting := idx.GetBySlug(entry.Slug); hasExisting {
			updated = *existing
		} else {
			updated = hugosite.SourcePage{Slug: entry.Slug}
		}
		updated.FilePath = entry.FilePath
		updated.Lang = entry.Lang
		if entry.Title != "" {
			updated.Title = entry.Title
			if updated.FrontmatterRaw == nil {
				updated.FrontmatterRaw = make(map[string]any)
			}
			updated.FrontmatterRaw["title"] = entry.Title
		}
		if entry.Body != "" {
			updated.Body = entry.Body
		}
		if entry.Tags != nil {
			updated.Tags = entry.Tags
		}
		if entry.Categories != nil {
			updated.Categories = entry.Categories
		}
		updated.BuildPending = true
		idx.Upsert(updated)

		hadPublic := false
		if siteIdx != nil {
			if pub, ok := siteIdx.GetBySlug(entry.Slug); ok {
				hadPublic = true
				pubUpdated := *pub
				if entry.Title != "" {
					pubUpdated.Title = entry.Title
				}
				if entry.Tags != nil {
					pubUpdated.Tags = entry.Tags
				}
				if entry.Categories != nil {
					pubUpdated.Categories = entry.Categories
				}
				siteIdx.UpsertPage(pubUpdated)
			}
		}

		status := "ok"
		warning := ""
		if siteDB != nil {
			if err := siteDB.SyncSourcePage(updated); err != nil {
				slog.Warn("apply_content_plan: db sync failed", "plan_id", in.PlanID, "error", err)
				status = "partial_success"
				warning = fmt.Sprintf("source updated but derived DB could not be updated: %v", err)
			}
		}

		state := updatePageState(siteIdx != nil, hadPublic)
		out := newApplyContentPlanOutput(applyContentPlanData{
			Status:             status,
			PlanID:             in.PlanID,
			Slug:               canonicalPublicSlug(entry.Slug),
			BeforeRevision:     entry.Revision,
			AfterRevision:      contentmodel.SourceRevisionBytes([]byte(entry.Content)),
			Validation:         "passed",
			Warning:            appendLastBuildWarning(warning),
			State:              &state,
			RateLimitRemaining: rateLimitRemaining(limiter),
		})
		if idemHash != "" {
			if err := idem.remember("apply_content_plan", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("apply_content_plan: could not persist idempotency result", "plan_id", in.PlanID, "error", err)
			}
		}
		return nil, out, nil
	}))
}
