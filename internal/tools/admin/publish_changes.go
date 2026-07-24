package admin

// publish_changes (#340, #438) sits one layer above apply_content_plan
// (internal/tools/write/content_plan.go) — it does not know or care whether
// the source it's publishing came from apply_content_plan or a plain
// update_page call. It drives the existing build_site + verify_publication
// pipeline as one explicit, separately-confirmed step: never auto-chained
// onto apply_content_plan (docs/transactional-edit-design.md §4's answer to
// #340's confirmation question — MCP has no in-band "are you sure," so
// confirmation here means a distinct call an agent/human must deliberately
// make). A publish_changes call is not considered fully successful unless
// verify_publication's own status comes back "fresh" — that result is
// surfaced directly in data.publication, not just summarized.

import (
	"context"
	"fmt"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type publishChangesInput struct {
	Slug string `json:"slug"`
	// WaitSeconds is forwarded to verify_publication's own wait_seconds
	// (#421) — see that tool's doc for the local-only settle-then-check
	// semantics and the server-side clamp.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

type publishChangesBuildDTO struct {
	BuildID        string `json:"build_id"`
	DurationMs     int64  `json:"duration_ms"`
	OutputRevision string `json:"output_revision,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

type publishChangesData struct {
	// Status is "published" only when the build succeeded *and*
	// verify_publication's own status came back "fresh". Any other
	// verify_publication status (e.g. "stale", "http_error",
	// "not_yet_published") reports "build_succeeded_unverified" here — the
	// build itself did not fail, but publication is not confirmed to match
	// the intended source yet. A failed build surfaces as a tool error
	// (build_error/build_in_progress), identical to build_site's own
	// behavior — it never reaches this data shape.
	Status      string                 `json:"status"`
	Build       publishChangesBuildDTO `json:"build"`
	Publication verifyPublicationData  `json:"publication"`
}

type publishChangesOutput struct {
	toolcontract.ToolResponse[publishChangesData]
}

func newPublishChangesOutput(data publishChangesData) publishChangesOutput {
	return publishChangesOutput{
		ToolResponse: toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC())),
	}
}

// RegisterPublishChanges wires publish_changes (write scope). Like
// verify_publication, it needs the public site index and (optionally) the
// source index to resolve a page's lifecycle state, so it's registered
// separately from admin.Register, alongside RegisterVerifyPublication.
func RegisterPublishChanges(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, siteReload ...func() error) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "publish_changes",
		Title: "Publish changes",
		Description: "Build the site and confirm a page is actually live — bundles build_site + verify_publication into one explicit, separately-confirmed step. " +
			"Never auto-chained onto apply_content_plan/update_page; publishing is always its own deliberate call (#340). " +
			"`data.status` is \"published\" only when the build succeeds cleanly (no failed post-build callback — e.g. a CDN purge failure could leave stale bytes cached at the edge even though local files are fresh) and verify_publication's own check comes back \"fresh\" (source/build/public/index all agree and, if `site_url` is configured, the live HTTP response confirms it); otherwise it's \"build_succeeded_unverified\" — the build did not fail outright, but publication isn't confirmed clean yet (see `data.build.warning` for a callback failure and `data.publication.status`/`data.publication.explanation` for which publication stage is behind). " +
			"A failed build surfaces as a tool error (`build_error`/`build_in_progress`), identical to `build_site`'s own behavior — it never reaches `data.status`. " +
			"Optional `wait_seconds` is forwarded to verify_publication's own local settle-then-check wait (bounded server-side). " +
			"Writes only build output and derived indexes — never touches page source; that's `apply_content_plan`/`update_page`'s layer.",
		InputSchema:  tools.MustSchema[publishChangesInput](),
		OutputSchema: tools.MustSchema[publishChangesOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in publishChangesInput) (*mcp.CallToolResult, publishChangesOutput, error) {
		if idx == nil {
			return nil, publishChangesOutput{}, fmt.Errorf("index not initialized")
		}
		if in.Slug == "" {
			return nil, publishChangesOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		buildData, err := runBuild(ctx, cfg, siteReload...)
		if err != nil {
			return nil, publishChangesOutput{}, err
		}

		pub, err := pollForFreshPublication(ctx, idx, srcIdx, cfg, in.Slug, in.WaitSeconds)
		if err != nil {
			return nil, publishChangesOutput{}, err
		}

		// A partial_success build (an index-reload/DB-reindex/CDN-purge
		// callback failed — see runBuild) never counts as "published" even
		// if verify_publication happens to read "fresh": that check only
		// looks at source/build/public/index file state and an optional
		// HTTP probe, none of which would catch a failed Cloudflare purge
		// leaving stale bytes cached at the edge. data.build.warning always
		// carries the specific callback failure either way.
		status := "build_succeeded_unverified"
		if buildData.Status == "ok" && pub.Status == "fresh" {
			status = "published"
		}

		return nil, newPublishChangesOutput(publishChangesData{
			Status: status,
			Build: publishChangesBuildDTO{
				BuildID:        buildData.BuildID,
				DurationMs:     buildData.DurationMs,
				OutputRevision: buildData.OutputRevision,
				Warning:        buildData.Warning,
			},
			Publication: pub,
		}), nil
	}))
}
