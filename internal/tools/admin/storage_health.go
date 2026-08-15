package admin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// get_storage_health (#861) is an advisory-only storage/integrity surface:
// it reports residue and lifecycle drift (orphaned generated hero images,
// expired/orphaned preview directories still on disk) as machine-readable
// findings with codes and severity, and NEVER deletes anything. It exists so
// the recurring "is this residue expected or unexpected drift?" question an
// audit keeps rediscovering can be answered with one read instead of manual
// filesystem archaeology. Path reporting is deliberately host-safe: generated
// assets use the hugo_root-relative logical path, and preview residue reports
// only the opaque directory basename (mcp-preview-<hex>), never an absolute
// host temp path.
//
// #894 note: this tool deliberately does NOT expose test_content_owner. Its
// two finding classes are `orphaned_generated_asset` (a generated hero image
// whose owning page is, by definition, gone from the index — so its frontmatter
// and any test_content_owner are unrecoverable) and `expired_preview_residue`
// (a preview directory with no page association at all). There is no finding
// here whose owner could be read from a live page, so an owner field/filter
// would be a permanently-empty footgun. test_content_owner exposure and the
// optional owner filter therefore live only on validate_site/validate_frontmatter
// (where the pages still exist); see internal/tools/read/extended.go.

const (
	storageFindingOrphanedGeneratedAsset = "orphaned_generated_asset"
	storageFindingExpiredPreviewResidue  = "expired_preview_residue"

	storageSeverityWarning = "warning"
	storageSeverityInfo    = "info"

	storageResourceGeneratedAsset = "generated_asset"
	storageResourcePreview        = "preview"
)

type getStorageHealthInput struct{}

// storageFinding is one advisory residue/integrity observation. It carries a
// stable machine-readable code and severity; humans/agents decide remediation
// (this tool never auto-deletes).
type storageFinding struct {
	Code          string `json:"code"`
	Severity      string `json:"severity"`
	ResourceClass string `json:"resource_class"`
	// LogicalPath is the hugo_root-relative path for generated assets; omitted
	// for previews, which report only Ref (an opaque basename) to avoid
	// leaking the host temp directory layout.
	LogicalPath string `json:"logical_path,omitempty"`
	Slug        string `json:"slug,omitempty"`
	// Ref is an opaque, non-path identifier (e.g. a preview directory
	// basename) safe to expose without revealing host paths.
	Ref        string `json:"ref,omitempty"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
	// ReferencedBy lists source pages whose frontmatter explicitly points at
	// this asset. Present only for explanatory/suspicious cases.
	ReferencedBy []string `json:"referenced_by,omitempty"`
	// Confidence explains how strong the orphan classification is.
	Confidence string `json:"confidence,omitempty"`
	// Reason is the machine-readable explanation for why this candidate was
	// or was not considered a real orphan.
	Reason            string                    `json:"reason,omitempty"`
	RecommendedAction *storageRecommendedAction `json:"recommended_action,omitempty"`
	Detail            string                    `json:"detail"`
}

type storageRecommendedAction struct {
	RecommendedTool string            `json:"recommended_tool"`
	Arguments       map[string]string `json:"arguments"`
}

type storageHealthSummary struct {
	TotalFindings           int  `json:"total_findings"`
	OrphanedGeneratedAssets int  `json:"orphaned_generated_assets"`
	ExpiredPreviewResidue   int  `json:"expired_preview_residue"`
	Scanned                 bool `json:"scanned"`
}

type getStorageHealthData struct {
	// AutoDelete is always false — this surface is advisory-first (#861); it
	// is reported explicitly so a caller never has to assume the posture.
	AutoDelete bool                 `json:"auto_delete"`
	Summary    storageHealthSummary `json:"summary"`
	Findings   []storageFinding     `json:"findings"`
}

type getStorageHealthOutput struct {
	toolcontract.ToolResponse[getStorageHealthData]
}

// RegisterStorageHealth wires get_storage_health (site.admin scope). It reads
// the source index (to tell which generated assets have an owning page) and
// the preview store (to tell live preview dirs from expired residue).
func RegisterStorageHealth(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, previews *previewstore.Store) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_storage_health",
		Title: "Get storage health",
		Description: "Advisory-only storage/integrity health check (#861). Returns machine-readable findings — each with a stable `code`, `severity`, and `resource_class` — for residue that accumulates outside a page's own content bundle: " +
			"`orphaned_generated_asset` (a generate_hero_image `{slug}-featured.jpg` under static/images whose owning page no longer exists in the index) and `expired_preview_residue` (an mcp-preview-* directory still on disk with no live preview backing it, e.g. after a restart). " +
			"NEVER deletes anything: `data.auto_delete` is always false; use delete_page_asset (scope=generated) or revoke_preview to act on a finding. Host-safe: generated assets report a hugo_root-relative logical path, preview residue reports only an opaque directory basename — never an absolute host path. Requires write.",
		InputSchema:  tools.MustSchema[getStorageHealthInput](),
		OutputSchema: tools.MustSchema[getStorageHealthOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(_ context.Context, _ *mcp.CallToolRequest, _ getStorageHealthInput) (*mcp.CallToolResult, getStorageHealthOutput, error) {
		findings := make([]storageFinding, 0)
		findings = append(findings, scanOrphanedGeneratedAssets(cfg, srcIdx)...)
		findings = append(findings, scanExpiredPreviewResidue(previews)...)

		summary := storageHealthSummary{TotalFindings: len(findings), Scanned: true}
		for _, f := range findings {
			switch f.Code {
			case storageFindingOrphanedGeneratedAsset:
				summary.OrphanedGeneratedAssets++
			case storageFindingExpiredPreviewResidue:
				summary.ExpiredPreviewResidue++
			}
		}

		return nil, getStorageHealthOutput{ToolResponse: imageSuccessEnvelope(getStorageHealthData{
			AutoDelete: false,
			Summary:    summary,
			Findings:   findings,
		})}, nil
	}))
}

type heroAssetSnapshot struct {
	knownSlugs   map[string]struct{}
	referencedBy map[string][]string
}

// scanOrphanedGeneratedAssets walks {HugoRoot}/static/images for generated
// hero images (historically jpg, but legacy hand-attached heroes can also be
// png) and flags only files that have neither an explicit frontmatter
// reference nor an owning page in the source index.
func scanOrphanedGeneratedAssets(cfg config.Config, srcIdx *hugosite.SourceIndex) []storageFinding {
	hugoRoot := strings.TrimSpace(cfg.HugoRoot)
	if hugoRoot == "" || srcIdx == nil {
		return nil
	}
	imagesRoot := filepath.Join(hugoRoot, "static", "images")

	snapshot := snapshotHeroAssetOwnership(srcIdx)

	var out []storageFinding
	_ = filepath.WalkDir(imagesRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable subtrees rather than fail the whole check
		}
		if d.IsDir() {
			return nil
		}
		slug, ok := heroSlugFromLogicalPath(filepath.ToSlash(strings.TrimPrefix(path, imagesRoot+string(filepath.Separator))))
		if !ok || slug == "" {
			return nil
		}
		logicalPath := logicalHugoRootPath(hugoRoot, path)
		referencedBy := snapshot.referencedBy[logicalPath]
		if len(referencedBy) > 0 {
			return nil
		}
		if heroSlugHasOwner(snapshot.knownSlugs, slug) {
			return nil // has an owning page — not orphaned
		}
		args := map[string]string{"scope": "generated", "slug": slug, "filename": filepath.Base(logicalPath)}
		// delete_page_asset requires expected_sha256 (or expected_revision) as
		// a concurrency guard on every non-dry-run call; without it here, this
		// recommended_action would only ever work as a dry_run preview, not the
		// directly executable remediation #1022 asked for. Best-effort: if the
		// file becomes unreadable between the walk and this read, the caller
		// falls back to list_page_assets for the current hash.
		if raw, readErr := os.ReadFile(path); readErr == nil {
			args["expected_sha256"] = contentmodel.SourceRevisionBytes(raw)
		}
		out = append(out, storageFinding{
			Code:          storageFindingOrphanedGeneratedAsset,
			Severity:      storageSeverityWarning,
			ResourceClass: storageResourceGeneratedAsset,
			LogicalPath:   logicalPath,
			Slug:          slug,
			Confidence:    "high",
			Reason:        "no_frontmatter_reference_or_source_owner",
			RecommendedAction: &storageRecommendedAction{
				RecommendedTool: "delete_page_asset",
				Arguments:       args,
			},
			Detail: "generated hero image has no explicit featured image reference and no owning page in the source index; remove with delete_page_asset (scope=generated) only after confirming the page was deleted",
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].LogicalPath < out[j].LogicalPath })
	return out
}

func snapshotHeroAssetOwnership(srcIdx *hugosite.SourceIndex) heroAssetSnapshot {
	hugosite.ContentMu.RLock()
	pages := srcIdx.ListPages(0, 0)
	knownSlugs := srcIdx.AllSlugs()
	hugosite.ContentMu.RUnlock()

	slugSet := make(map[string]struct{}, len(knownSlugs))
	for _, s := range knownSlugs {
		slugSet[s] = struct{}{}
	}
	referencedBy := make(map[string][]string)
	for _, p := range pages {
		for _, key := range []string{"featuredImage", "featuredImagePreview"} {
			logicalPath := heroLogicalPathFromFrontmatter(p.FrontmatterRaw[key])
			if logicalPath == "" {
				continue
			}
			referencedBy[logicalPath] = append(referencedBy[logicalPath], p.Slug)
		}
	}
	for logicalPath, slugs := range referencedBy {
		sort.Strings(slugs)
		referencedBy[logicalPath] = uniqueStrings(slugs)
	}
	return heroAssetSnapshot{knownSlugs: slugSet, referencedBy: referencedBy}
}

func heroLogicalPathFromFrontmatter(v any) string {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" || !strings.HasPrefix(s, "/images/") {
		return ""
	}
	return "static/" + strings.TrimPrefix(s, "/")
}

func heroSlugFromLogicalPath(rel string) (string, bool) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	switch {
	case strings.HasSuffix(rel, adminHeroPNGSuffix):
		return strings.TrimSuffix(rel, adminHeroPNGSuffix), true
	case strings.HasSuffix(rel, HeroImageSuffix):
		return strings.TrimSuffix(rel, HeroImageSuffix), true
	default:
		return "", false
	}
}

const adminHeroPNGSuffix = "-featured.png"

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// heroSlugHasOwner reports whether slug — derived from a hero image's path
// relative to static/images — has an owning page in knownSlugs, a snapshot
// of every SourcePage.Slug in the index. generate_hero_image historically
// wrote flat filenames keyed on the bare post slug
// (static/images/{slug}-featured.jpg) before it started keying on the full
// source slug, which nests section pages under a subdirectory (e.g.
// static/images/posts/{slug}-featured.jpg). A flat legacy filename for a
// page under a section (slug has no "/") therefore never matches the
// section-qualified slug directly; the fallback below checks whether any
// indexed slug ends in "/"+slug before concluding the asset is truly
// orphaned, so pre-existing flat hero images for real pages aren't
// misreported as residue.
func heroSlugHasOwner(knownSlugs map[string]struct{}, slug string) bool {
	if _, ok := knownSlugs[slug]; ok {
		return true
	}
	if strings.Contains(slug, "/") {
		return false
	}
	suffix := "/" + slug
	for full := range knownSlugs {
		if strings.HasSuffix(full, suffix) {
			return true
		}
	}
	return false
}

// scanExpiredPreviewResidue enumerates on-disk mcp-preview-* directories and
// flags any not backed by a live preview entry. It reports only the opaque
// basename and age, never the absolute temp path.
func scanExpiredPreviewResidue(previews *previewstore.Store) []storageFinding {
	roots := []string{os.TempDir()}
	if previews != nil {
		if managed := previews.ManagedRoot(); managed != "" && managed != os.TempDir() {
			roots = append(roots, managed)
		}
	}
	var matches []string
	for _, root := range roots {
		found, err := filepath.Glob(filepath.Join(root, "mcp-preview-*"))
		if err != nil {
			continue
		}
		matches = append(matches, found...)
	}
	if len(matches) == 0 {
		return nil
	}
	var active map[string]struct{}
	if previews != nil {
		active = previews.ActiveDirs()
	}
	now := time.Now()
	var out []storageFinding
	for _, dir := range matches {
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if _, live := active[dir]; live {
			continue // backed by a live preview — expected, not residue
		}
		age := int64(now.Sub(info.ModTime()).Seconds())
		if age < 0 {
			age = 0
		}
		out = append(out, storageFinding{
			Code:          storageFindingExpiredPreviewResidue,
			Severity:      storageSeverityInfo,
			ResourceClass: storageResourcePreview,
			Ref:           filepath.Base(dir),
			AgeSeconds:    age,
			Detail:        "preview directory on disk with no live preview backing it (expired or orphaned after restart); safe to remove — no live preview depends on it",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}
