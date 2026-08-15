package write

// Atomic lifecycle operations for multilingual bundles (#1008). These tools
// deliberately operate on source files only: no build or public output is
// touched. All validation happens before the first filesystem mutation and a
// best-effort rollback restores files created/removed if a later operation
// fails while the content lock is held.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bundlePageInput struct {
	Lang          string            `json:"lang"`
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	Tags          []string          `json:"tags,omitempty"`
	Categories    []string          `json:"categories,omitempty"`
	Draft         *bool             `json:"draft,omitempty"`
	Description   *string           `json:"description,omitempty"`
	FeaturedImage *string           `json:"featured_image,omitempty"`
	TestContent   *testContentInput `json:"test_content,omitempty"`
}

type createBundleInput struct {
	Slug           string            `json:"slug"`
	Pages          []bundlePageInput `json:"pages"`
	DryRun         bool              `json:"dry_run,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

type deleteBundleInput struct {
	Slug              string            `json:"slug"`
	Languages         []string          `json:"languages"`
	ExpectedRevisions map[string]string `json:"expected_revisions"`
	DryRun            bool              `json:"dry_run,omitempty"`
	IdempotencyKey    string            `json:"idempotency_key,omitempty"`
}

type bundleLifecycleData struct {
	Status               string            `json:"status"`
	Slug                 string            `json:"slug"`
	Languages            []string          `json:"languages"`
	DryRun               bool              `json:"dry_run,omitempty"`
	Revisions            map[string]string `json:"revisions,omitempty"`
	TestContentExpiresAt map[string]string `json:"test_content_expires_at,omitempty"`
}

type bundleLifecycleOutput struct {
	toolcontract.ToolResponse[bundleLifecycleData]
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
}

func buildBundleFrontmatter(p bundlePageInput) (string, string) {
	return buildFrontmatterWithOptions(p.Title, p.Tags, p.Categories, p.Body, p.Draft, p.Description, p.FeaturedImage, p.TestContent)
}

func bundleLifecycleSuccess(d bundleLifecycleData) bundleLifecycleOutput {
	return bundleLifecycleOutput{ToolResponse: writeSuccessEnvelope(d)}
}

func bundleLangFile(dir, lang string) (string, error) {
	lang, err := validateLangParam(lang)
	if err != nil {
		return "", err
	}
	if lang == "" {
		return filepath.Join(dir, "index.md"), nil
	}
	return filepath.Join(dir, "index."+lang+".md"), nil
}

func registerBundleLifecycleTools(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config, siteDB *db.DB, rt *writeRegisterRuntime) {
	mcp.AddTool(s, &mcp.Tool{Name: "create_bundle", Title: "Create multilingual bundle", Description: "Atomically create all translations of a new Hugo page bundle. Every page is validated before any file is written; a failure leaves no partial bundle. Each page may set `draft`, `description`, `featured_image`, and the same explicit `test_content: {ttl_hours?, owner?}` safety marker as create_page; test_content forces draft:true for that translation and returns its effective expiry in `data.test_content_expires_at`. dry_run previews the files without writing. Repeating the same non-dry-run request normally fails once any translation exists, but callers may provide `idempotency_key` to safely replay the exact same create attempt after a timeout or uncertain delivery — recoverable afterward via get_mutation_status(tool=\"create_bundle\").", InputSchema: tools.MustSchema[createBundleInput](), OutputSchema: tools.MustSchema[bundleLifecycleOutput](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false)}}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createBundleInput) (*mcp.CallToolResult, bundleLifecycleOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrap := func(e error) error {
			return toolcontract.WithRequestContext(e, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, bundleLifecycleOutput{}, wrap(err)
		}
		if err := validateSlugFormat(in.Slug); err != nil {
			return nil, bundleLifecycleOutput{}, wrap(err)
		}
		if seg, ok := reservedSlugConflict(in.Slug); ok {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: slug segment %q is reserved", seg))
		}
		if len(in.Pages) == 0 {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: pages must not be empty"))
		}
		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: path validation failed"))
		}
		seen := map[string]bool{}
		files := make([]struct {
			path, content string
			expiresAt     string
			page          bundlePageInput
		}, 0, len(in.Pages))
		langs := make([]string, 0, len(in.Pages))
		for _, p := range in.Pages {
			lang, e := validateLangParam(p.Lang)
			if e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if seen[lang] {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: duplicate language %q", lang))
			}
			seen[lang] = true
			if e = rejectUnconfiguredLang(lang, cfg); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if p.Title == "" {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: title must not be empty"))
			}
			if e = validateTitleFormat(p.Title); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if e = validateBodyFormat(p.Body, cfg.BlockedShortcodes); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if p.Description != nil {
				if e = rejectUnsafeText(*p.Description); e != nil {
					return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: description %w", e))
				}
			}
			if p.FeaturedImage != nil {
				if e = validateFeaturedImagePath(*p.FeaturedImage); e != nil {
					return nil, bundleLifecycleOutput{}, wrap(e)
				}
			}
			if p.TestContent != nil && p.TestContent.TTLHours != nil {
				ttl := *p.TestContent.TTLHours
				if ttl <= 0 || ttl > testContentMaxTTLHours {
					return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: test_content.ttl_hours must be between 1 and %d hours when provided", testContentMaxTTLHours))
				}
			}
			if e = validateTaxonomyTerms("tag", p.Tags); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if e = validateTaxonomyTerms("category", p.Categories); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			content, expiresAt := buildBundleFrontmatter(p)
			if e = validateFrontmatterRoundTrip(content); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("validation_error: %w", e))
			}
			path, e := bundleLangFile(dir, lang)
			if e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			p.Lang = lang
			files = append(files, struct {
				path, content string
				expiresAt     string
				page          bundlePageInput
			}{path, content, expiresAt, p})
			langs = append(langs, lang)
		}
		mutating := !in.DryRun && !cfg.ForceDryRunAll

		lockHeld := false
		acquireLock := func() error {
			if lockHeld {
				return nil
			}
			const lockWait = 10 * time.Second
			deadline := time.Now().Add(lockWait)
			for {
				if hugosite.ContentMu.TryLock() {
					lockHeld = true
					return nil
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		if mutating && strings.TrimSpace(in.IdempotencyKey) != "" {
			if err := acquireLock(); err != nil {
				return nil, bundleLifecycleOutput{}, wrap(err)
			}
		}
		defer func() {
			if lockHeld {
				hugosite.ContentMu.Unlock()
			}
		}()

		// Idempotency replay must run before the already_exists preflight below:
		// a successful first create leaves the translations on disk, so checking
		// existence first would turn the exact same retry into already_exists
		// instead of replaying the original success (matches delete_page's #724
		// fix). It stays under the content lock so same-key concurrent retries
		// serialize correctly.
		idemHash := ""
		if mutating && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(in)
			if hashErr != nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached bundleLifecycleOutput
			hit, replayErr := rt.idem.replay(idempotencyCallerKey(ctx), "create_bundle", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, bundleLifecycleOutput{}, wrap(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		for _, f := range files {
			if _, e := os.Stat(f.path); e == nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("already_exists: translation %q already exists", filepath.Base(f.path)))
			} else if !os.IsNotExist(e) {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("read_error: failed to inspect bundle"))
			}
		}
		if !mutating {
			expires := map[string]string{}
			for _, f := range files {
				if f.expiresAt != "" {
					expires[f.page.Lang] = f.expiresAt
				}
			}
			return nil, bundleLifecycleSuccess(bundleLifecycleData{Status: "unchanged", Slug: canonicalPublicSlug(in.Slug), Languages: langs, DryRun: true, TestContentExpiresAt: expires}), nil
		}
		createLimiter := callerLimiter(&rt.mutationMu, rt.mutationLimiters, mutationCallerKey(ctx), cfg.RateLimit.CreateUpdatePerMin)
		if !createLimiter.Allow() {
			return nil, bundleLifecycleOutput{}, wrap(rateLimitExceededErr("create_bundle", cfg.RateLimit.CreateUpdatePerMin, createLimiter))
		}
		if err := acquireLock(); err != nil {
			return nil, bundleLifecycleOutput{}, wrap(err)
		}
		created := []string{}
		rollback := func() {
			for _, p := range created {
				_ = os.Remove(p)
			}
			_ = os.Remove(dir)
		}
		for _, f := range files {
			if e := fileutil.AtomicCreateChecked(f.path, f.content, pg); e != nil {
				rollback()
				if errors.Is(e, fs.ErrExist) {
					return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("already_exists: bundle changed during create"))
				}
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("write_error: bundle creation rolled back: %v", e))
			}
			created = append(created, f.path)
		}
		revs := map[string]string{}
		expires := map[string]string{}
		for _, f := range files {
			raw, _ := os.ReadFile(f.path)
			rev := contentmodel.SourceRevisionBytes(raw)
			revs[f.page.Lang] = rev
			if f.expiresAt != "" {
				expires[f.page.Lang] = f.expiresAt
			}
			now := time.Now().UTC().Format(time.RFC3339)
			effectiveDraft := f.page.TestContent != nil || (f.page.Draft != nil && *f.page.Draft)
			frontmatterRaw := map[string]any{"title": f.page.Title, "date": now, "draft": effectiveDraft}
			if f.page.Description != nil && *f.page.Description != "" {
				frontmatterRaw["description"] = *f.page.Description
			}
			if f.page.FeaturedImage != nil && *f.page.FeaturedImage != "" {
				frontmatterRaw["featuredImage"] = *f.page.FeaturedImage
			}
			if f.page.TestContent != nil {
				frontmatterRaw["test_content"] = true
				if f.page.TestContent.Owner != "" {
					frontmatterRaw["test_content_owner"] = f.page.TestContent.Owner
				}
				if f.expiresAt != "" {
					frontmatterRaw["test_content_expires_at"] = f.expiresAt
				}
			}
			idx.Upsert(hugosite.SourcePage{Slug: in.Slug, FilePath: f.path, Lang: f.page.Lang, Title: f.page.Title, Date: now, Draft: effectiveDraft, Tags: f.page.Tags, Categories: f.page.Categories, Body: f.page.Body, FrontmatterRaw: frontmatterRaw, BuildPending: true})
		}
		if siteDB != nil {
			for _, f := range files {
				if page, ok := idx.GetBySlugLang(in.Slug, f.page.Lang); ok {
					_ = siteDB.SyncSourcePage(*page)
				}
			}
		}
		out := bundleLifecycleSuccess(bundleLifecycleData{Status: "created", Slug: canonicalPublicSlug(in.Slug), Languages: langs, Revisions: revs, TestContentExpiresAt: expires})
		if idemHash != "" {
			if err := rt.idem.remember(idempotencyCallerKey(ctx), "create_bundle", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("create_bundle: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{Name: "delete_bundle", Title: "Delete multilingual bundle", Description: "Atomically delete selected translations of a Hugo page bundle. `expected_revisions` must supply the current revision (from get_page/create_bundle/delete_bundle output) for every language listed, including for dry_run; the call fails invalid_params otherwise. All revisions are checked before the first unlink; a failure leaves the bundle unchanged. Pass every language to remove the bundle completely. Callers may provide `idempotency_key` to safely replay the exact same non-dry-run delete after a timeout or uncertain delivery — recoverable afterward via get_mutation_status(tool=\"delete_bundle\").", InputSchema: tools.MustSchema[deleteBundleInput](), OutputSchema: tools.MustSchema[bundleLifecycleOutput](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(true)}}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in deleteBundleInput) (*mcp.CallToolResult, bundleLifecycleOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrap := func(e error) error {
			return toolcontract.WithRequestContext(e, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, bundleLifecycleOutput{}, wrap(err)
		}
		if len(in.Languages) == 0 {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: languages must not be empty"))
		}

		lockHeld := false
		acquireLock := func() error {
			if lockHeld {
				return nil
			}
			const lockWait = 10 * time.Second
			deadline := time.Now().Add(lockWait)
			for {
				if hugosite.ContentMu.TryLock() {
					lockHeld = true
					return nil
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			if err := acquireLock(); err != nil {
				return nil, bundleLifecycleOutput{}, wrap(err)
			}
		}
		defer func() {
			if lockHeld {
				hugosite.ContentMu.Unlock()
			}
		}()

		// Idempotency replay must run before any current-state existence/revision
		// checks: a successful first delete removes the translations, so
		// checking them first would turn the exact same retry into not_found
		// instead of replaying the original success (matches delete_page's #724
		// fix). It stays under the content lock so same-key concurrent retries
		// serialize correctly.
		idemHash := ""
		if !in.DryRun && strings.TrimSpace(in.IdempotencyKey) != "" {
			hash, hashErr := requestHash(in)
			if hashErr != nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("internal_error: failed to hash idempotency request"))
			}
			idemHash = hash
			var cached bundleLifecycleOutput
			hit, replayErr := rt.idem.replay(idempotencyCallerKey(ctx), "delete_bundle", in.IdempotencyKey, idemHash, &cached)
			if replayErr != nil {
				return nil, bundleLifecycleOutput{}, wrap(replayErr)
			}
			if hit {
				return nil, cached, nil
			}
		}

		dir, e := pg.SafeJoin(in.Slug)
		if e != nil {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: path validation failed"))
		}
		seen := map[string]bool{}
		paths := []struct{ lang, path, content, rev string }{}
		langs := []string{}
		for _, rawLang := range in.Languages {
			lang, ve := validateLangParam(rawLang)
			if ve != nil {
				return nil, bundleLifecycleOutput{}, wrap(ve)
			}
			if seen[lang] {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: duplicate language %q", lang))
			}
			seen[lang] = true
			path, ve := bundleLangFile(dir, lang)
			if ve != nil {
				return nil, bundleLifecycleOutput{}, wrap(ve)
			}
			raw, re := os.ReadFile(path)
			if re != nil {
				if os.IsNotExist(re) {
					return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("not_found: translation %q does not exist", lang))
				}
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("read_error: failed to read translation"))
			}
			rev := contentmodel.SourceRevisionBytes(raw)
			if expected := in.ExpectedRevisions[lang]; expected == "" {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: expected_revisions[%q] is required", lang))
			} else if expected != rev {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("revision_conflict: translation %q changed since it was read", lang))
			}
			paths = append(paths, struct{ lang, path, content, rev string }{lang, path, string(raw), rev})
			langs = append(langs, lang)
		}
		if in.DryRun || cfg.ForceDryRunAll {
			revs := map[string]string{}
			for _, p := range paths {
				revs[p.lang] = p.rev
			}
			return nil, bundleLifecycleSuccess(bundleLifecycleData{Status: "unchanged", Slug: canonicalPublicSlug(in.Slug), Languages: langs, DryRun: true, Revisions: revs}), nil
		}
		deleteLimiter := callerLimiter(&rt.deleteMu, rt.deleteLimiters, mutationCallerKey(ctx), cfg.RateLimit.DestructivePerMin)
		if !deleteLimiter.Allow() {
			return nil, bundleLifecycleOutput{}, wrap(rateLimitExceededErr("delete_bundle", cfg.RateLimit.DestructivePerMin, deleteLimiter))
		}
		if err := acquireLock(); err != nil {
			return nil, bundleLifecycleOutput{}, wrap(err)
		}
		for _, p := range paths {
			raw, re := os.ReadFile(p.path)
			if re != nil || contentmodel.SourceRevisionBytes(raw) != p.rev {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("revision_conflict: bundle changed during delete"))
			}
		}
		removed := []struct{ path, content string }{}
		rollback := func() {
			for _, p := range removed {
				_ = fileutil.AtomicWriteChecked(p.path, p.content, pg)
			}
		}
		for _, p := range paths {
			if re := os.Remove(p.path); re != nil {
				rollback()
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("delete_error: bundle deletion rolled back: %v", re))
			}
			removed = append(removed, struct{ path, content string }{p.path, p.content})
		}
		if !bundleHasRemainingLangFiles(dir) {
			_ = os.Remove(dir)
			idx.Delete(in.Slug)
		} else {
			for _, p := range paths {
				idx.DeleteLang(in.Slug, p.lang)
			}
		}
		if siteDB != nil {
			for _, p := range paths {
				_ = siteDB.DeleteContentRepresentation(in.Slug, p.lang, "source")
			}
			if !bundleHasRemainingLangFiles(dir) {
				_ = siteDB.DeleteBundleRepresentations(in.Slug, "source")
				_ = siteDB.DeletePage(in.Slug)
			}
		}
		revs := map[string]string{}
		for _, p := range paths {
			revs[p.lang] = p.rev
		}
		out := bundleLifecycleSuccess(bundleLifecycleData{Status: "deleted", Slug: canonicalPublicSlug(in.Slug), Languages: langs, Revisions: revs})
		if idemHash != "" {
			if err := rt.idem.remember(idempotencyCallerKey(ctx), "delete_bundle", in.IdempotencyKey, idemHash, out); err != nil {
				slog.Warn("delete_bundle: could not persist idempotency result", "slug", in.Slug, "error", err)
			}
		}
		return nil, out, nil
	}))
}
