package admin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
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
	Detail     string `json:"detail"`
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
			"NEVER deletes anything: `data.auto_delete` is always false; use delete_page_asset (scope=generated) or revoke_preview to act on a finding. Host-safe: generated assets report a hugo_root-relative logical path, preview residue reports only an opaque directory basename — never an absolute host path. Requires site.admin.",
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

// scanOrphanedGeneratedAssets walks {HugoRoot}/static/images for generated
// hero images (files ending in HeroImageSuffix) and flags any whose derived
// source slug has no owning page in the source index.
func scanOrphanedGeneratedAssets(cfg config.Config, srcIdx *hugosite.SourceIndex) []storageFinding {
	hugoRoot := strings.TrimSpace(cfg.HugoRoot)
	if hugoRoot == "" || srcIdx == nil {
		return nil
	}
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	var out []storageFinding
	_ = filepath.WalkDir(imagesRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable subtrees rather than fail the whole check
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), HeroImageSuffix) {
			return nil
		}
		rel, relErr := filepath.Rel(imagesRoot, path)
		if relErr != nil {
			return nil
		}
		slug := strings.TrimSuffix(filepath.ToSlash(rel), HeroImageSuffix)
		if slug == "" {
			return nil
		}
		if _, ok := srcIdx.GetBySlug(slug); ok {
			return nil // has an owning page — not orphaned
		}
		out = append(out, storageFinding{
			Code:          storageFindingOrphanedGeneratedAsset,
			Severity:      storageSeverityWarning,
			ResourceClass: storageResourceGeneratedAsset,
			LogicalPath:   logicalHugoRootPath(hugoRoot, path),
			Slug:          slug,
			Detail:        "generated hero image has no owning page in the source index; remove with delete_page_asset (scope=generated) if the page was deleted",
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].LogicalPath < out[j].LogicalPath })
	return out
}

// scanExpiredPreviewResidue enumerates on-disk mcp-preview-* directories and
// flags any not backed by a live preview entry. It reports only the opaque
// basename and age, never the absolute temp path.
func scanExpiredPreviewResidue(previews *previewstore.Store) []storageFinding {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "mcp-preview-*"))
	if err != nil || len(matches) == 0 {
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
