package read

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type inspectPreviewRenderedInput struct {
	Slug      string `json:"slug"`
	PreviewID string `json:"preview_id"`
}

type inspectPreviewRenderedData struct {
	InspectionScope  string              `json:"inspection_scope"`
	PreviewID        string              `json:"preview_id"`
	PreviewBuild     string              `json:"preview_build"`
	PreviewExpiresAt string              `json:"preview_expires_at"`
	Slug             string              `json:"slug"`
	URL              string              `json:"url"`
	Lang             string              `json:"lang"`
	OutputPath       string              `json:"output_path"`
	State            site.LifecycleState `json:"state"`
	Status           string              `json:"status"`
	Checks           []renderCheckResult `json:"checks"`
}

type inspectPreviewRenderedOutput struct {
	toolcontract.ToolResponse[inspectPreviewRenderedData]
}

func newInspectPreviewRenderedOutput(data inspectPreviewRenderedData, now time.Time) inspectPreviewRenderedOutput {
	return inspectPreviewRenderedOutput{ToolResponse: successEnvelope(data, now)}
}

// RegisterInspectPreviewRenderedPage wires a preview-aware rendered inspection
// tool onto the write/admin server only. It intentionally does not alter the
// public inspect_rendered reader surface: this variant reads isolated preview
// output for draft/test_content pages that do not exist in the public build.
func RegisterInspectPreviewRenderedPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, store *previewstore.Store, baseURL string) {
	if s == nil || store == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:         "inspect_preview",
		Title:        "Inspect preview rendered page",
		Description:  "Inspect rendered HTML/SEO/security checks against an isolated preview build for an unpublished or draft page. Requires preview_id from create_preview and a page slug/source key. Returns explicit preview-scoped context so callers do not confuse preview inspection with public availability. Requires write.",
		InputSchema:  tools.MustSchema[inspectPreviewRenderedInput](),
		OutputSchema: tools.MustSchema[inspectPreviewRenderedOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in inspectPreviewRenderedInput) (*mcp.CallToolResult, inspectPreviewRenderedOutput, error) {
		slug := strings.TrimSpace(in.Slug)
		if slug == "" {
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		previewID := strings.TrimSpace(in.PreviewID)
		if previewID == "" {
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("invalid_params: preview_id must not be empty")
		}

		entry, state := store.Lookup(previewID)
		switch state {
		case previewstore.LookupMissing:
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("preview_not_found: preview %q not found", previewID)
		case previewstore.LookupExpired:
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("preview_expired: preview %q has expired", previewID)
		}
		if ownerKey := caller.TokenKey(ctx); ownerKey != "" && entry.Owner != "" && entry.Owner != ownerKey {
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("preview_not_found: preview %q not found", previewID)
		}

		resolver := site.NewPageResolver(idx, srcIdx, cfg)
		resolved, ok := resolver.Resolve(slug)
		if !ok || resolved.Source == nil {
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("content_not_found: no source page found for slug %q", slug)
		}

		page := site.Page{
			Slug:       "/" + strings.Trim(resolved.Source.Slug, "/") + "/",
			Title:      resolved.Source.Title,
			URL:        strings.TrimRight(baseURL, "/") + previewURLPath(previewID, resolved.Source.Slug),
			Lang:       resolved.Source.Lang,
			OutputPath: filepath.ToSlash(filepath.Join(resolved.Source.Slug, "index.html")),
		}
		previewCfg := cfg
		previewCfg.SiteRoot = entry.Dir
		previewCfg.SiteURL = strings.TrimRight(baseURL, "/") + previewstore.CleanPath(previewID, "")
		doc, raw, err := loadRenderedHTML(previewCfg, page)
		if err != nil {
			return nil, inspectPreviewRenderedOutput{}, fmt.Errorf("render_output_unavailable: %v", err)
		}

		checks := []renderCheckResult{
			checkTitle(doc),
			checkMetaDescription(doc),
			checkCanonical(doc, previewCfg.SiteURL, page.Slug),
			checkHreflang(doc, idx, page),
			checkInternalLinks(idx, page, doc),
			checkMissingImages(previewCfg, page, doc),
			checkFeaturedImage(previewCfg, resolved, doc),
			checkRenderedTitleMarkup(raw),
			checkRenderedInlineEventHandlers(doc),
			checkRenderedUnsafeURLs(doc),
			checkRenderedPreviewTokenLeak(raw),
			checkRenderErrors(raw),
		}

		overall := "ok"
		for _, c := range checks {
			if c.Status == "fail" {
				overall = "issues_found"
				break
			}
			if c.Status == "warn" && overall == "ok" {
				overall = "warnings_found"
			}
		}

		return nil, newInspectPreviewRenderedOutput(inspectPreviewRenderedData{
			InspectionScope:  "preview",
			PreviewID:        previewID,
			PreviewBuild:     entry.BuildStatus,
			PreviewExpiresAt: entry.ExpiresAt.UTC().Format(time.RFC3339),
			Slug:             page.Slug,
			URL:              page.URL,
			Lang:             page.Lang,
			OutputPath:       page.OutputPath,
			State:            resolvedState(resolved, cfg.SiteRoot),
			Status:           overall,
			Checks:           checks,
		}, time.Now().UTC()), nil
	}))
}

func previewURLPath(previewID, sourceSlug string) string {
	p := previewstore.CleanPath(previewID, strings.Trim(sourceSlug, "/"))
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}
