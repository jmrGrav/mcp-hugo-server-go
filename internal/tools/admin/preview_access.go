package admin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listPreviewsInput struct{}

type listPreviewItem struct {
	PreviewID   string `json:"preview_id"`
	URL         string `json:"url"`
	Owner       string `json:"owner,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	BuildStatus string `json:"build_status"`
}

type listPreviewsData struct {
	ConfiguredCount int               `json:"configured_count"`
	Previews        []listPreviewItem `json:"previews"`
}

type listPreviewsOutput struct {
	toolcontract.ToolResponse[listPreviewsData]
}

type revokePreviewInput struct {
	PreviewID string `json:"preview_id"`
}

type revokePreviewData struct {
	PreviewID string `json:"preview_id"`
	Status    string `json:"status"`
}

type revokePreviewOutput struct {
	toolcontract.ToolResponse[revokePreviewData]
}

type revokeAllPreviewsInput struct{}

type revokeAllPreviewsData struct {
	Status       string `json:"status"`
	RevokedCount int    `json:"revoked_count"`
}

type revokeAllPreviewsOutput struct {
	toolcontract.ToolResponse[revokeAllPreviewsData]
}

func previewAccessSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func RegisterPreviewAccessTools(s *mcp.Server, store *previewstore.Store, baseURL string) {
	if s == nil || store == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:         "list_previews",
		Title:        "List previews",
		Description:  "List currently active preview sessions with owner and expiry metadata. Returns clean preview URLs without re-emitting the entry token — the entry token is not currently invalidated after first exchange, so it remains valid (like any bearer secret) for the preview's remaining TTL; use revoke_preview if a URL has been exposed and access must be cut off immediately (#853 follow-up: enforce single-use). Requires site.admin.",
		InputSchema:  tools.MustSchema[listPreviewsInput](),
		OutputSchema: tools.MustSchema[listPreviewsOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ listPreviewsInput) (*mcp.CallToolResult, listPreviewsOutput, error) {
		_ = ctx
		snaps := store.List()
		slices.SortFunc(snaps, func(a, b previewstore.Snapshot) int {
			return a.CreatedAt.Compare(b.CreatedAt)
		})
		items := make([]listPreviewItem, 0, len(snaps))
		for _, snap := range snaps {
			items = append(items, listPreviewItem{
				PreviewID:   snap.ID,
				URL:         strings.TrimRight(baseURL, "/") + previewstore.CleanPath(snap.ID, ""),
				Owner:       snap.Owner,
				CreatedAt:   snap.CreatedAt.UTC().Format(time.RFC3339),
				ExpiresAt:   snap.ExpiresAt.UTC().Format(time.RFC3339),
				BuildStatus: snap.BuildStatus,
			})
		}
		return nil, listPreviewsOutput{
			ToolResponse: previewAccessSuccessEnvelope(listPreviewsData{
				ConfiguredCount: len(items),
				Previews:        items,
			}),
		}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:         "revoke_preview",
		Title:        "Revoke preview",
		Description:  "Revoke one active preview by preview_id and delete its isolated build directory. Requires site.admin.",
		InputSchema:  tools.MustSchema[revokePreviewInput](),
		OutputSchema: tools.MustSchema[revokePreviewOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in revokePreviewInput) (*mcp.CallToolResult, revokePreviewOutput, error) {
		_ = ctx
		id := strings.TrimSpace(in.PreviewID)
		if id == "" {
			return nil, revokePreviewOutput{}, fmt.Errorf("invalid_params: preview_id must not be empty")
		}
		if !store.Revoke(id) {
			return nil, revokePreviewOutput{}, fmt.Errorf("preview_not_found: preview %q not found or already expired", id)
		}
		return nil, revokePreviewOutput{
			ToolResponse: previewAccessSuccessEnvelope(revokePreviewData{
				PreviewID: id,
				Status:    "revoked",
			}),
		}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:         "revoke_all_previews",
		Title:        "Revoke all previews",
		Description:  "Revoke every active preview and delete all isolated preview directories. Requires site.admin.",
		InputSchema:  tools.MustSchema[revokeAllPreviewsInput](),
		OutputSchema: tools.MustSchema[revokeAllPreviewsOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ revokeAllPreviewsInput) (*mcp.CallToolResult, revokeAllPreviewsOutput, error) {
		_ = ctx
		count := store.RevokeAll()
		return nil, revokeAllPreviewsOutput{
			ToolResponse: previewAccessSuccessEnvelope(revokeAllPreviewsData{
				Status:       "revoked",
				RevokedCount: count,
			}),
		}, nil
	}))
}
