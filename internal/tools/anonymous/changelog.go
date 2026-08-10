package anonymous

import (
	"context"
	"fmt"

	mcphugoserver "github.com/jmrGrav/mcp-hugo-server-go"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/releasecheck"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getChangelogInput struct {
	SinceVersion string `json:"since_version,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type changelogEntryDTO struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	Body    string `json:"body,omitempty"`
}

type getChangelogData struct {
	Entries []changelogEntryDTO `json:"entries"`
	Total   int                 `json:"total"`
}

type getChangelogOutput struct {
	toolcontract.ToolResponse[getChangelogData]
}

func newGetChangelogOutput(data getChangelogData) getChangelogOutput {
	return getChangelogOutput{ToolResponse: success(data)}
}

const getChangelogDefaultLimit = 5
const getChangelogMaxLimit = 20

// RegisterGetChangelog registers get_changelog (#612): an agent auditing
// the live server today has no way to ask "what changed since vX.Y.Z"
// without either already knowing to fetch the raw CHANGELOG.md from GitHub,
// or blindly re-testing the entire tool surface on every audit. This is
// exactly the expensive per-audit cost the issue describes.
//
// CHANGELOG.md is not shipped to disk in production (deploy.yml deploys
// only the compiled binary), so the file content is embedded at build time
// (embed_changelog.go, module root) rather than read from a runtime path.
// Release CI additionally verifies that the requested release version is the
// first versioned heading in CHANGELOG.md before deployment, closing the drift
// class where a binary could announce vX.Y.Z while still embedding an older
// top section.
//
// Anonymous tier, matching list_tags/list_categories: the changelog is
// already public on GitHub, so gating it behind a read/write scope adds no
// real confidentiality, only friction for exactly the kind of unauthenticated
// audit session this tool exists to help.
func RegisterGetChangelog(s *mcp.Server) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "get_changelog", "Get changelog",
		"Return CHANGELOG.md entries from the copy embedded in this running binary at build time. Release CI verifies that the deployed release version is the first versioned heading before shipping, so release builds expose the matching top entry rather than silently drifting to an older section. Without arguments, returns the 5 most recent versioned releases (bounded by default rather than dumping the entire file). Pass `since_version` (e.g. \"v1.5.9\", with or without the leading \"v\") to get every release strictly newer than that version instead — an agent that last audited at v1.5.9 can request exactly what changed since then rather than re-testing the full tool surface. `limit` caps how many entries are returned either way (default 5, max 20). `response_mode:\"compact\"` is audit-oriented: when `limit` is omitted it defaults to 1 entry instead of 5, and omits each entry's raw Markdown `body`. Standard mode keeps each entry's `body` as the release section's raw Markdown (its own `###` subsections like Added/Fixed/Security, verbatim) rather than further parsed into structured fields — CHANGELOG.md's own formatting is the source of truth. Fails with `invalid_params` if `since_version` doesn't match any release heading in the file. No additional business scope is required beyond the read/anonymous-tier permission; on OAuth-enabled deployments, a Bearer token is still required for every `/mcp` call, including this tool.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getChangelogInput) (*mcp.CallToolResult, getChangelogOutput, error) {
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, getChangelogOutput{}, err
			}
			mode, err := toolcontract.ResolveResponseMode(in.ResponseMode)
			if err != nil {
				return nil, getChangelogOutput{}, err
			}
			defaultLimit := getChangelogDefaultLimit
			includeBody := true
			if mode == toolcontract.ResponseModeCompact {
				defaultLimit = 1
				includeBody = false
			}
			limit := clampLimit(in.Limit, defaultLimit, getChangelogMaxLimit)

			entries, err := releasecheck.ListReleaseEntriesSince(mcphugoserver.ChangelogMarkdown, in.SinceVersion, limit)
			if err != nil {
				return nil, getChangelogOutput{}, fmt.Errorf("invalid_params: %w", err)
			}

			dtos := make([]changelogEntryDTO, 0, len(entries))
			for _, e := range entries {
				dto := changelogEntryDTO{Version: e.Version, Date: e.Date}
				if includeBody {
					dto.Body = e.Body
				}
				dtos = append(dtos, dto)
			}
			return nil, newGetChangelogOutput(getChangelogData{Entries: dtos, Total: len(dtos)}), nil
		}, func(schema any) any { return tools.WithMaxLimit(schema, "limit", getChangelogMaxLimit) })
}
