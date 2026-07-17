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
	Slug           string   `json:"slug"`
	Lang           string   `json:"lang,omitempty"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	Tags           []string `json:"tags"`
	Categories     []string `json:"categories"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	DryRun         bool     `json:"dry_run,omitempty"`
}

type createPageOutput struct {
	toolcontract.ToolResponse[map[string]any]
	Status             string               `json:"status,omitempty"`
	Slug               string               `json:"slug"`
	Path               string               `json:"path,omitempty"`
	ResolvedLang       string               `json:"resolved_lang"`
	ResolvedSourcePath string               `json:"resolved_source_path"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	Content            string               `json:"content,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
}

type updatePageInput struct {
	Slug             string   `json:"slug"`
	Lang             string   `json:"lang,omitempty"`
	Title            string   `json:"title,omitempty"`
	Body             string   `json:"body,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Categories       []string `json:"categories,omitempty"`
	Draft            *bool    `json:"draft,omitempty"`
	Description      string   `json:"description,omitempty"`
	ExpectedRevision string   `json:"expected_revision,omitempty"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty"`
	DryRun           bool     `json:"dry_run,omitempty"`
}

type updatePageOutput struct {
	toolcontract.ToolResponse[map[string]any]
	Status             string               `json:"status,omitempty"`
	Slug               string               `json:"slug"`
	ResolvedLang       string               `json:"resolved_lang"`
	ResolvedSourcePath string               `json:"resolved_source_path"`
	DryRun             bool                 `json:"dry_run,omitempty"`
	Diff               string               `json:"diff,omitempty"`
	Warning            string               `json:"warning,omitempty"`
	State              *site.LifecycleState `json:"state,omitempty"`
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
	toolcontract.ToolResponse[map[string]any]
	Status             string                   `json:"status,omitempty"`
	Slug               string                   `json:"slug"`
	ResolvedLang       string                   `json:"resolved_lang"`
	ResolvedSourcePath string                   `json:"resolved_source_path"`
	DryRun             bool                     `json:"dry_run,omitempty"`
	Content            string                   `json:"content,omitempty"`
	Backlinks          *[]deletePageBacklinkDTO `json:"backlinks,omitempty"`
	Warning            string                   `json:"warning,omitempty"`
	State              *site.LifecycleState     `json:"state,omitempty"`
}

func writeSuccessEnvelope() toolcontract.ToolResponse[map[string]any] {
	return toolcontract.Success(map[string]any{}, toolcontract.NewMeta(toolcontract.ToolResultVersion, time.Now().UTC()))
}

// normalizeInputSlug strips leading and trailing slashes so agents that pass
// /posts/foo/ and posts/foo reach the same content directory and source-index
// entry (#265).
func normalizeInputSlug(s string) string { return strings.Trim(s, "/") }

var reservedSlugs = map[string]bool{
	"_index": true,
	"index":  true,
}

var validLangPattern = regexp.MustCompile(`^[A-Za-z0-9-]{2,5}$`)

// deleteCallerKey builds a rate-limit key that isolates delete budgets by caller IP.
// Falls back to "unknown" when context carries no IP (e.g. in tests).
func deleteCallerKey(ctx context.Context) string {
	ip, _ := ctx.Value(oauth.CtxCallerIP).(string)
	if ip == "" {
		ip = "unknown"
	}
	return ip
}

// deleteCallerLimiter returns (or creates) a per-caller rate.Limiter for deletions.
func deleteCallerLimiter(mu *sync.Mutex, m map[string]*rate.Limiter, key string) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := m[key]; ok {
		return l
	}
	l := rate.NewLimiter(rate.Every(time.Minute/5), 5)
	m[key] = l
	return l
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
	idem := newIdempotencyStore(15*time.Minute, 256)

	mcp.AddTool(s, &mcp.Tool{
		Name:         "create_page",
		Title:        "Publish page",
		Description:  "Create a new Hugo content page at {slug}/index.md with front matter and body content. Fails with `already_exists` if the destination already exists; use update_page for edits. Repeating the same non-dry-run request normally fails once the page exists, but callers may provide `idempotency_key` to safely replay the exact same create attempt after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether the page only exists in source so far or is already publicly available.",
		InputSchema:  tools.MustSchema[createPageInput](),
		OutputSchema: tools.MustSchema[createPageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(_ context.Context, _ *mcp.CallToolRequest, in createPageInput) (*mcp.CallToolResult, createPageOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		if in.Slug == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		resolvedLang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, createPageOutput{}, err
		}
		if in.Title == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: title must not be empty")
		}
		if reservedSlugs[in.Slug] {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug is reserved")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("create_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		filePath := filepath.Join(dir, "index.md")
		if resolvedLang != "" {
			filePath = filepath.Join(dir, "index."+resolvedLang+".md")
		}
		content := buildFrontmatter(in.Title, in.Tags, in.Categories, in.Body)

		// Round-trip guard: verify the generated content parses correctly.
		if err := validateFrontmatterRoundTrip(content); err != nil {
			return nil, createPageOutput{}, fmt.Errorf("validation_error: %w", err)
		}

		if in.DryRun {
			if _, err := os.Stat(filePath); err == nil {
				return nil, createPageOutput{}, fmt.Errorf("already_exists: page already exists at slug %q", in.Slug)
			} else if !os.IsNotExist(err) {
				slog.Error("create_page: dry-run stat failed", "slug", in.Slug, "error", err)
				return nil, createPageOutput{}, fmt.Errorf("read_error: failed to inspect destination path")
			}
			logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
			return nil, createPageOutput{
				ToolResponse:       writeSuccessEnvelope(),
				Status:             "ok",
				Slug:               in.Slug,
				ResolvedLang:       resolvedLang,
				ResolvedSourcePath: logicalPath,
				DryRun:             true,
				Content:            content,
			}, nil
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
				return nil, createPageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
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
				return nil, createPageOutput{}, fmt.Errorf("internal_error: failed to hash idempotency request")
			}
			idemHash = hash
			var cached createPageOutput
			hit, replayErr := idem.replay("create_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, createPageOutput{}, replayErr
			}
			if hit {
				return nil, cached, nil
			}
		}

		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("create_page: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("security_error: symlink detected in write path")
		}
		if err := fileutil.AtomicCreateChecked(filePath, content, pg); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, createPageOutput{}, fmt.Errorf("already_exists: page already exists at slug %q", in.Slug)
			}
			slog.Error("create_page: write failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("write_error: failed to write page")
		}
		now := time.Now().UTC().Format(time.RFC3339)
		created := hugosite.SourcePage{
			Slug:           in.Slug,
			FilePath:       filePath,
			Lang:           resolvedLang,
			Title:          in.Title,
			Date:           now,
			Tags:           in.Tags,
			Categories:     in.Categories,
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
		out := createPageOutput{
			ToolResponse:       writeSuccessEnvelope(),
			Status:             status,
			Slug:               in.Slug,
			Path:               logicalPath,
			ResolvedLang:       resolvedLang,
			ResolvedSourcePath: logicalPath,
			Warning:            warning,
			State:              &state,
		}
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
			"Successful non-dry-run responses include a `state` object that tells agents whether the source changed ahead of the public build/index state.",
		InputSchema:  tools.MustSchema[updatePageInput](),
		OutputSchema: tools.MustSchema[updatePageOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(_ context.Context, _ *mcp.CallToolRequest, in updatePageInput) (*mcp.CallToolResult, updatePageOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		if in.Slug == "" {
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		lang, err := validateLangParam(in.Lang)
		if err != nil {
			return nil, updatePageOutput{}, err
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
				return nil, updatePageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("update_page: lock_released")
		}()

		existing, ok := idx.GetBySlug(in.Slug)
		if !ok {
			return nil, updatePageOutput{}, fmt.Errorf("not_found: page not found")
		}

		if _, err := pg.SafeJoin(in.Slug); err != nil {
			slog.Warn("update_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		resolvedSource, langErr := resolveExistingSource(cfg.ContentRoot, in.Slug, lang)
		if langErr != nil {
			return nil, updatePageOutput{}, langErr
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
				return nil, updatePageOutput{}, fmt.Errorf("internal_error: failed to hash idempotency request")
			}
			idemHash = hash
			var cached updatePageOutput
			hit, replayErr := idem.replay("update_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, updatePageOutput{}, replayErr
			}
			if hit {
				return nil, cached, nil
			}
		}

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("update_page: read failed", "slug", in.Slug, "path", filePath, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("read_error: failed to read page")
		}
		currentRevision := contentmodel.SourceRevisionBytes(raw)
		if !in.DryRun {
			if strings.TrimSpace(in.ExpectedRevision) == "" {
				return nil, updatePageOutput{}, fmt.Errorf("invalid_params: expected_revision is required for non-dry-run update_page")
			}
			if in.ExpectedRevision != currentRevision {
				return nil, updatePageOutput{}, fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan")
			}
		}
		opts := pageUpdateOpts{
			Tags:        in.Tags,
			Categories:  in.Categories,
			Draft:       in.Draft,
			Description: in.Description,
		}
		content, err := applyPageUpdates(string(raw), in.Title, in.Body, opts)
		if err != nil {
			slog.Error("update_page: frontmatter update failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("parse_error: failed to update frontmatter")
		}
		// Round-trip guard: reject content with malformed/duplicated frontmatter.
		if err := validateFrontmatterRoundTrip(content); err != nil {
			slog.Error("update_page: round-trip guard failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("validation_error: %w", err)
		}
		if in.DryRun {
			// Use the resolved filename (e.g. index.fr.md) so the diff header
			// matches the file that a real write would touch.
			diffLabel := in.Slug + "/" + filepath.Base(filePath)
			diff := simpleDiff(diffLabel, string(raw), content)
			logicalPath := fileutil.LogicalContentPath(cfg.ContentRoot, filePath)
			return nil, updatePageOutput{
				ToolResponse:       writeSuccessEnvelope(),
				Status:             "ok",
				Slug:               in.Slug,
				ResolvedLang:       resolvedSource.Lang,
				ResolvedSourcePath: logicalPath,
				DryRun:             true,
				Diff:               diff,
			}, nil
		}
		if err := pg.RevalidateForWrite(filePath); err != nil {
			slog.Warn("update_page: symlink-swap detected before write", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("security_error: symlink detected in write path")
		}
		if err := fileutil.AtomicWriteChecked(filePath, content, pg); err != nil {
			slog.Error("update_page: write failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("write_error: failed to write page")
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
		if in.Tags != nil {
			updated.Tags = in.Tags
		}
		if in.Categories != nil {
			updated.Categories = in.Categories
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
				if in.Tags != nil {
					pubUpdated.Tags = in.Tags
				}
				if in.Categories != nil {
					pubUpdated.Categories = in.Categories
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
		out := updatePageOutput{
			ToolResponse:       writeSuccessEnvelope(),
			Status:             status,
			Slug:               in.Slug,
			ResolvedLang:       resolvedSource.Lang,
			ResolvedSourcePath: logicalPath,
			Warning:            warning,
			State:              &state,
		}
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
		Description:  "Delete a Hugo content page. This is destructive and rate limited to 5 deletions per minute. Non-dry-run calls require `expected_revision`, the `revision` value from a prior read of this page (e.g. get_page), unless the page has no source file to protect; a stale value fails with `revision_conflict`, telling the agent to re-read and replan. Callers may provide `idempotency_key` to safely replay the exact same non-dry-run delete after a timeout or uncertain delivery. Successful non-dry-run responses include a `state` object that tells agents whether source, public output, and derived indexes were all removed cleanly.",
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
		if in.Slug == "" {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("delete_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		// Return not_found when the source directory does not exist (#266).
		// Check this before the rate limiter to avoid burning the budget on client errors.
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return nil, deletePageOutput{}, fmt.Errorf("not_found: page not found for slug %q", in.Slug)
		}
		resolvedSource := inspectDeleteSource(dir)

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
			return nil, deletePageOutput{
				ToolResponse:       writeSuccessEnvelope(),
				Status:             "ok",
				Slug:               in.Slug,
				ResolvedLang:       resolvedSource.Lang,
				ResolvedSourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, resolvedSource.SourcePath),
				DryRun:             true,
				Content:            content,
				Backlinks:          &bls,
			}, nil
		}
		if resolvedSource.SourcePath != "" && strings.TrimSpace(in.ExpectedRevision) == "" {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: expected_revision is required for non-dry-run delete_page")
		}

		callerKey := deleteCallerKey(ctx)
		if !deleteCallerLimiter(&deleteMu, deleteLimiters, callerKey).Allow() {
			return nil, deletePageOutput{}, fmt.Errorf("rate_limit_exceeded: delete_page is limited to 5 per minute")
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
				return nil, deletePageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
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
				return nil, deletePageOutput{}, fmt.Errorf("internal_error: failed to hash idempotency request")
			}
			idemHash = hash
			var cached deletePageOutput
			hit, replayErr := idem.replay("delete_page", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, deletePageOutput{}, replayErr
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
				return nil, deletePageOutput{}, fmt.Errorf("read_error: failed to read page revision")
			}
		}
		if in.ExpectedRevision != currentRevision {
			return nil, deletePageOutput{}, fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan")
		}

		if err := os.RemoveAll(dir); err != nil {
			slog.Error("delete_page: remove failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("delete_error: failed to delete page")
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
		out := deletePageOutput{
			ToolResponse:       writeSuccessEnvelope(),
			Status:             status,
			Slug:               in.Slug,
			ResolvedLang:       resolvedSource.Lang,
			ResolvedSourcePath: fileutil.LogicalContentPath(cfg.ContentRoot, resolvedSource.SourcePath),
			Warning:            deleteWarning,
			State:              &state,
		}
		if idemHash != "" {
			if err := idem.remember("delete_page", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("delete_page: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	registerUploadPageAsset(s, pg, idx, cfg, idem)
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
		{Name: "create_page", RequiredScope: "content.write"},
		{Name: "update_page", RequiredScope: "content.write"},
		{Name: "delete_page", RequiredScope: "content.write"},
		{Name: "upload_page_asset", RequiredScope: "content.write"},
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
