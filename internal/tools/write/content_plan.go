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
	"encoding/json"
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
	CallerKey  string
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
	persistent *db.DB
}

func newPlanStore(ttl time.Duration, maxEntries int, persistent ...*db.DB) *planStore {
	var journal *db.DB
	if len(persistent) > 0 {
		journal = persistent[0]
	}
	return &planStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]planEntry),
		persistent: journal,
	}
}

func (s *planStore) put(id string, entry planEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	if s.persistent != nil {
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if err := s.persistent.PutEphemeralRecord("content_plan", id, entry.CallerKey, raw, entry.CreatedAt); err != nil {
			return err
		}
	}
	s.entries[id] = entry
	s.trimLocked()
	return nil
}

// get looks up a plan without consuming it (used for a dry-run apply, which
// re-verifies but must not remove the plan).
func (s *planStore) get(id, callerKey string) (planEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	if !ok && s.persistent != nil {
		raw, found, err := s.persistent.GetEphemeralRecord("content_plan", id, callerKey, s.ttl)
		if err != nil {
			return planEntry{}, false, fmt.Errorf("read persisted content plan: %w", err)
		}
		if found {
			if err := json.Unmarshal(raw, &entry); err != nil {
				return planEntry{}, false, fmt.Errorf("decode persisted content plan: %w", err)
			}
			ok = true
			s.entries[id] = entry
		}
	}
	if ok && entry.CallerKey != "" && entry.CallerKey != callerKey {
		return planEntry{}, false, nil
	}
	return entry, ok, nil
}

// consume looks up and atomically removes a plan. Per the design doc, a plan
// is single-use: applying it (successfully or not) removes it from the
// store, so it can never be replayed against a page that has since changed
// without a fresh plan_content_change call.
func (s *planStore) consume(id, callerKey string) (planEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[id]
	if !ok && s.persistent != nil {
		raw, found, err := s.persistent.GetEphemeralRecord("content_plan", id, callerKey, s.ttl)
		if err != nil {
			return planEntry{}, false, fmt.Errorf("read persisted content plan: %w", err)
		}
		if found {
			if err := json.Unmarshal(raw, &entry); err != nil {
				return planEntry{}, false, fmt.Errorf("decode persisted content plan: %w", err)
			}
			ok = true
		}
	}
	if ok && entry.CallerKey != "" && entry.CallerKey != callerKey {
		return planEntry{}, false, nil
	}
	if ok {
		if s.persistent != nil {
			if err := s.persistent.DeleteEphemeralRecord("content_plan", id, callerKey); err != nil {
				return planEntry{}, false, err
			}
		}
		delete(s.entries, id)
	}
	return entry, ok, nil
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
	Status         string               `json:"status,omitempty"`
	PlanID         string               `json:"plan_id,omitempty"`
	Slug           string               `json:"slug,omitempty"`
	DryRun         bool                 `json:"dry_run,omitempty"`
	BeforeRevision string               `json:"before_revision,omitempty"`
	AfterRevision  string               `json:"after_revision,omitempty"`
	RevisionKind   string               `json:"revision_kind,omitempty"`
	Validation     string               `json:"validation,omitempty"`
	Warning        string               `json:"warning,omitempty"`
	State          *site.LifecycleState `json:"state,omitempty"`
	RateLimit      *rateLimitBucket     `json:"rate_limit,omitempty"`
	// RateLimitRemaining — see the comment on createPageData's field of the
	// same name (#520, #605).
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`
}

type applyContentPlanOutput struct {
	toolcontract.ToolResponse[applyContentPlanData]
	RequestContext     *toolcontract.RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int                          `json:"rate_limit_remaining"`
}

// newApplyContentPlanOutput — see the comment on newCreatePageOutput (#520,
// #605): rateLimitRemaining is an explicit parameter, not read off data.
func newApplyContentPlanOutput(data applyContentPlanData, rateLimitRemaining int) applyContentPlanOutput {
	return applyContentPlanOutput{
		ToolResponse:       writeSuccessEnvelopeWithWarning(data, data.Warning),
		RateLimitRemaining: rateLimitRemaining,
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

// frontmatterString, frontmatterBool, and frontmatterTime mirror
// hugosite.NewSourceIndex's own raw-frontmatter-value coercion (see
// stringVal/boolVal/timeVal in internal/hugosite/source_index.go) so a
// handler that rebuilds a hugosite.SourcePage from a parseFrontmatterMap
// result — rollback_change, specifically — can resync SourcePage's typed
// Date/Draft/PublishDate/ExpiryDate fields the same way NewSourceIndex
// populates them at initial parse time, instead of leaving them at whatever
// value the base entry the rebuild started from happened to hold. Kept
// package-local rather than exported from hugosite: this coercion has
// exactly one caller today, and hugosite is a package the whole server
// depends on.
func frontmatterString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func frontmatterBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func frontmatterTime(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case string:
		if x == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// bodyFromRaw extracts the Markdown body from a source file's raw bytes,
// matching hugosite.splitFrontmatter's own convention exactly
// (strings.TrimSpace of everything after the second "---" delimiter) since
// callers store the result into the same hugosite.SourcePage.Body field
// that function populates at index-build time (#643) — a mismatched
// convention here would silently desync in-memory source index bodies from
// what a fresh index rebuild would produce for the same file.
func bodyFromRaw(raw []byte) string {
	content := string(raw)
	if !strings.HasPrefix(content, "---") {
		return content
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return content
	}
	return strings.TrimSpace(parts[2])
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
			// #904: create_page/update_page reject an overlong tag via
			// validateTaxonomyTerms (#886); plan_content_change/
			// apply_content_plan and plan_bundle_change/apply_bundle_plan
			// (both funnel through this shared function) bypassed it
			// entirely, so a plan could still write an arbitrary-length tag.
			// Checked at plan time (fail-fast), not deferred to apply.
			if err := validateTaxonomyTerms("tag", []string{op.Value}); err != nil {
				return out, err
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
			// #904 — see the identical add_tag comment above.
			if err := validateTaxonomyTerms("category", []string{op.Value}); err != nil {
				return out, err
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
			"If the page still carries `test_content: true`, any planned `set_draft:false` is rejected during validation — test content must remain non-publishable while that marker is present (#728). " +
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
			Tags:       resolved.Tags,
			Categories: resolved.Categories,
			Draft:      resolved.Draft,
		}
		if resolved.Description != "" {
			opts.Description = strPtr(resolved.Description)
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
		if err := plans.put(planID, planEntry{
			CallerKey:  isolationCallerKey(ctx),
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
		}); err != nil {
			return nil, planContentChangeOutput{}, wrapErr(fmt.Errorf("persistence_error: failed to persist content plan"))
		}

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
			"`test_content` remains an ongoing safety invariant here too: content whose frontmatter still carries `test_content: true` cannot be applied in a `draft:false` state, even if an older or externally-crafted plan attempts it (#728). " +
			"A plan is single-use after a terminal apply attempt; retryable revision conflicts and transient build/content-lock failures preserve it so the caller can retry or re-plan safely. " +
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
		if cfg.ForceDryRunAll {
			in.DryRun = true
		}
		wrapErr := func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{})
		}
		callerKey := mutationCallerKey(ctx)
		limiter := callerLimiter(mutationMu, mutationLimiters, callerKey, cfg.RateLimit.CreateUpdatePerMin)
		wrapErrWithLimiter := func(err error) error {
			return toolcontract.WithDataFields(
				toolcontract.WithRootFields(wrapErr(err), rateLimitRootFields(limiter)),
				rateLimitDataFields(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC()),
			)
		}
		if strings.TrimSpace(in.PlanID) == "" {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("invalid_params: plan_id must not be empty"))
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(err)
		}
		if !in.DryRun && !limiter.Allow() {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(rateLimitExceededErr("apply_content_plan", cfg.RateLimit.CreateUpdatePerMin, limiter))
		}

		// Idempotency replay is checked before the plan lookup: a plan is
		// single-use and deleted once a real (non-dry-run) apply attempt
		// succeeds in passing the revision check — not merely attempted; a
		// revision_conflict or build_in_progress preserves it (#1001). A
		// genuine retry of an already-applied request must not depend on
		// the plan still existing, or replay would be indistinguishable
		// from plan_not_found on the second call — deliberately reordered
		// from the design doc's literal listing (which checked plan
		// existence first) once implementing surfaced that gap.
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
			hit, replayErr := idem.replay(idempotencyCallerKey(ctx), "apply_content_plan", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, applyContentPlanOutput{}, wrapErrWithLimiter(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		// Keep retryable revision conflicts from consuming the plan (#1001):
		// look the plan up without consuming it, and only consume it once
		// the revision check below has passed.
		entry, ok, planErr := plans.get(in.PlanID, isolationCallerKey(ctx))
		if planErr != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("persistence_error: failed to load content plan"))
		}
		if !ok {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("plan_not_found: plan_id is unknown or has expired; call plan_content_change again"))
		}
		// From here on, errors carry the slug/lang the plan resolved (#1001)
		// — a caller reading request_context on a revision_conflict/
		// build_in_progress/read_error no longer has to guess which page.
		wrapErr = func(err error) error {
			return toolcontract.WithRequestContext(err, toolcontract.RequestContext{Slug: entry.Slug, RequestedLang: entry.Lang})
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
				Status:         "unchanged",
				PlanID:         in.PlanID,
				Slug:           canonicalPublicSlug(entry.Slug),
				DryRun:         true,
				BeforeRevision: entry.Revision,
				Validation:     "passed",
				State:          &state,
				RateLimit:      ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
			}, rateLimitRemaining(limiter)), nil
		}
		if err := snapshots.put(entry.FilePath, entry.Revision, isolationCallerKey(ctx), string(raw)); err != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("persistence_error: failed to retain rollback snapshot: %w", err))
		}
		if err := pg.RevalidateForWrite(entry.FilePath); err != nil {
			slog.Warn("apply_content_plan: symlink-swap detected before write", "plan_id", in.PlanID, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("security_error: symlink detected in write path"))
		}
		afterRevision := contentmodel.SourceRevisionBytes([]byte(entry.Content))
		recoveryOp, err := beginSourceWriteRecovery(siteDB, entry.FilePath, currentRevision, afterRevision, recoveryIdempotencyFor(ctx, "apply_content_plan", in.IdempotencyKey, idemHash, map[string]any{
			"plan_id": in.PlanID, "slug": canonicalPublicSlug(entry.Slug),
			"before_revision": currentRevision, "after_revision": afterRevision, "revision_kind": "content_snapshot",
		}))
		if err != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("persistence_error: failed to record source write intent"))
		}
		if err := fileutil.AtomicWriteChecked(entry.FilePath, entry.Content, pg); err != nil {
			slog.Error("apply_content_plan: write failed", "plan_id", in.PlanID, "error", err)
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("write_error: failed to write page"))
		}
		if err := recoveryFilesystemBoundary("apply_content_plan", "after_source_write"); err != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("persistence_error: interrupted after source write"))
		}
		if err := recoveryOp.record(siteDB, "file_written"); err != nil {
			slog.Warn("apply_content_plan: could not advance recovery journal", "plan_id", in.PlanID, "error", err)
		}
		status := "updated"
		warning := ""
		_, ok, err = plans.consume(in.PlanID, isolationCallerKey(ctx))
		if err == nil {
			err = planConsumeFailure("apply_content_plan")
		}
		if err != nil {
			slog.Warn("apply_content_plan: plan consumption failed after write", "plan_id", in.PlanID, "error", err)
			status = "partial_success"
			warning = fmt.Sprintf("source updated but plan consumption could not be persisted: %v", err)
		} else if !ok {
			slog.Warn("apply_content_plan: plan consumption reported not-ok after write", "plan_id", in.PlanID)
			status = "partial_success"
			warning = "source updated but plan consumption could not be persisted"
		}
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
		// Re-parse FrontmatterRaw wholesale from the content just written —
		// see the identical fix/comment on update_page in tools.go (#810).
		if fm := parseFrontmatterMap([]byte(entry.Content)); fm != nil {
			updated.FrontmatterRaw = fm
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

		if siteDB != nil {
			if err := siteDB.SyncSourcePage(updated); err != nil {
				slog.Warn("apply_content_plan: db sync failed", "plan_id", in.PlanID, "error", err)
				status = "partial_success"
				dbWarning := fmt.Sprintf("source updated but derived DB could not be updated: %v", err)
				if warning != "" {
					warning = warning + "; " + dbWarning
				} else {
					warning = dbWarning
				}
			}
		}

		state := updatePageState(siteIdx != nil, hadPublic)
		out := newApplyContentPlanOutput(applyContentPlanData{
			Status:         status,
			PlanID:         in.PlanID,
			Slug:           canonicalPublicSlug(entry.Slug),
			BeforeRevision: entry.Revision,
			AfterRevision:  contentmodel.SourceRevisionBytes([]byte(entry.Content)),
			RevisionKind:   "content_snapshot",
			Validation:     "passed",
			Warning:        appendLastBuildWarning(warning),
			State:          &state,
			RateLimit:      ptrRateLimitBucket(newRateLimitBucket(limiter, cfg.RateLimit.CreateUpdatePerMin, rateLimitScopeCreateUpdateUpload, time.Now().UTC())),
		}, rateLimitRemaining(limiter))
		if err := recoveryOp.stageResult(siteDB, out); err != nil {
			return nil, applyContentPlanOutput{}, wrapErrWithLimiter(fmt.Errorf("persistence_error: failed to stage recoverable mutation result"))
		}
		if idemHash != "" {
			if err := idem.remember(idempotencyCallerKey(ctx), "apply_content_plan", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("apply_content_plan: could not persist idempotency result", "plan_id", in.PlanID, "error", err)
			}
		}
		if err := recoveryOp.record(siteDB, "committed"); err != nil {
			slog.Warn("apply_content_plan: could not commit recovery journal", "plan_id", in.PlanID, "error", err)
		}
		return nil, out, nil
	}))
}
