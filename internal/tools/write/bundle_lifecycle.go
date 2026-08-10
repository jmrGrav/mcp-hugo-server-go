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
	"os"
	"path/filepath"
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
	Lang       string   `json:"lang"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

type createBundleInput struct {
	Slug   string            `json:"slug"`
	Pages  []bundlePageInput `json:"pages"`
	DryRun bool              `json:"dry_run,omitempty"`
}

type deleteBundleInput struct {
	Slug              string            `json:"slug"`
	Languages         []string          `json:"languages"`
	ExpectedRevisions map[string]string `json:"expected_revisions,omitempty"`
	DryRun            bool              `json:"dry_run,omitempty"`
}

type bundleLifecycleData struct {
	Status    string            `json:"status"`
	Slug      string            `json:"slug"`
	Languages []string          `json:"languages"`
	DryRun    bool              `json:"dry_run,omitempty"`
	Revisions map[string]string `json:"revisions,omitempty"`
}

type bundleLifecycleOutput struct {
	toolcontract.ToolResponse[bundleLifecycleData]
	RequestContext *toolcontract.RequestContext `json:"request_context,omitempty"`
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
	mcp.AddTool(s, &mcp.Tool{Name: "create_bundle", Title: "Create multilingual bundle", Description: "Atomically create all translations of a new Hugo page bundle. Every page is validated before any file is written; a failure leaves no partial bundle. dry_run previews the files without writing.", InputSchema: tools.MustSchema[createBundleInput](), OutputSchema: tools.MustSchema[bundleLifecycleOutput](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false)}}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createBundleInput) (*mcp.CallToolResult, bundleLifecycleOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrap := func(e error) error {
			return toolcontract.WithRequestContext(e, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: slug must not be empty"))
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
			if e = validateTaxonomyTerms("tag", p.Tags); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			if e = validateTaxonomyTerms("category", p.Categories); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			content, _ := buildFrontmatter(p.Title, p.Tags, p.Categories, p.Body, nil)
			if e = validateFrontmatterRoundTrip(content); e != nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("validation_error: %w", e))
			}
			path, e := bundleLangFile(dir, lang)
			if e != nil {
				return nil, bundleLifecycleOutput{}, wrap(e)
			}
			files = append(files, struct {
				path, content string
				page          bundlePageInput
			}{path, content, p})
			langs = append(langs, lang)
		}
		for _, f := range files {
			if _, e := os.Stat(f.path); e == nil {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("already_exists: translation %q already exists", filepath.Base(f.path)))
			} else if !os.IsNotExist(e) {
				return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("read_error: failed to inspect bundle"))
			}
		}
		if in.DryRun || cfg.ForceDryRunAll {
			return nil, bundleLifecycleSuccess(bundleLifecycleData{Status: "unchanged", Slug: canonicalPublicSlug(in.Slug), Languages: langs, DryRun: true}), nil
		}
		createLimiter := callerLimiter(&rt.mutationMu, rt.mutationLimiters, mutationCallerKey(ctx), cfg.RateLimit.CreateUpdatePerMin)
		if !createLimiter.Allow() {
			return nil, bundleLifecycleOutput{}, wrap(rateLimitExceededErr("create_bundle", cfg.RateLimit.CreateUpdatePerMin, createLimiter))
		}
		hugosite.ContentMu.Lock()
		defer hugosite.ContentMu.Unlock()
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
		for _, f := range files {
			raw, _ := os.ReadFile(f.path)
			rev := contentmodel.SourceRevisionBytes(raw)
			revs[f.page.Lang] = rev
			now := time.Now().UTC().Format(time.RFC3339)
			idx.Upsert(hugosite.SourcePage{Slug: in.Slug, FilePath: f.path, Lang: f.page.Lang, Title: f.page.Title, Date: now, Tags: f.page.Tags, Categories: f.page.Categories, Body: f.page.Body, FrontmatterRaw: map[string]any{"title": f.page.Title, "date": now}, BuildPending: true})
		}
		if siteDB != nil {
			for _, f := range files {
				if page, ok := idx.GetBySlugLang(in.Slug, f.page.Lang); ok {
					_ = siteDB.SyncSourcePage(*page)
				}
			}
		}
		return nil, bundleLifecycleSuccess(bundleLifecycleData{Status: "created", Slug: canonicalPublicSlug(in.Slug), Languages: langs, Revisions: revs}), nil
	}))

	mcp.AddTool(s, &mcp.Tool{Name: "delete_bundle", Title: "Delete multilingual bundle", Description: "Atomically delete selected translations of a Hugo page bundle. All revisions are checked before the first unlink; a failure leaves the bundle unchanged. Pass every language to remove the bundle completely.", InputSchema: tools.MustSchema[deleteBundleInput](), OutputSchema: tools.MustSchema[bundleLifecycleOutput](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(true)}}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in deleteBundleInput) (*mcp.CallToolResult, bundleLifecycleOutput, error) {
		in.Slug = normalizeInputSlug(in.Slug)
		wrap := func(e error) error {
			return toolcontract.WithRequestContext(e, toolcontract.RequestContext{Slug: in.Slug})
		}
		if in.Slug == "" {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: slug must not be empty"))
		}
		if len(in.Languages) == 0 {
			return nil, bundleLifecycleOutput{}, wrap(fmt.Errorf("invalid_params: languages must not be empty"))
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
		hugosite.ContentMu.Lock()
		defer hugosite.ContentMu.Unlock()
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
		if siteDB != nil && !bundleHasRemainingLangFiles(dir) {
			_ = siteDB.DeletePage(in.Slug)
		}
		revs := map[string]string{}
		for _, p := range paths {
			revs[p.lang] = p.rev
		}
		return nil, bundleLifecycleSuccess(bundleLifecycleData{Status: "deleted", Slug: canonicalPublicSlug(in.Slug), Languages: langs, Revisions: revs}), nil
	}))
}
