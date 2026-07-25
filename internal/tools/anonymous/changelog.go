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
}

type changelogEntryDTO struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	Body    string `json:"body"`
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
// (embed_changelog.go, module root) rather than read from a runtime path —
// this also means the served changelog always matches the running binary
// exactly, with zero drift.
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
		"Return CHANGELOG.md entries — the exact content embedded in this running binary at build time, so it always matches the deployed version with zero drift. Without arguments, returns the 5 most recent versioned releases (bounded by default rather than dumping the entire file). Pass `since_version` (e.g. \"v1.5.9\", with or without the leading \"v\") to get every release strictly newer than that version instead — an agent that last audited at v1.5.9 can request exactly what changed since then rather than re-testing the full tool surface. `limit` caps how many entries are returned either way (default 5, max 20). Each entry's `body` is the release section's raw Markdown (its own `###` subsections like Added/Fixed/Security, verbatim) rather than further parsed into structured fields — CHANGELOG.md's own formatting is the source of truth. Fails with `invalid_params` if `since_version` doesn't match any release heading in the file. Usable without authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in getChangelogInput) (*mcp.CallToolResult, getChangelogOutput, error) {
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, getChangelogOutput{}, err
			}
			limit := clampLimit(in.Limit, getChangelogDefaultLimit, getChangelogMaxLimit)

			entries, err := releasecheck.ListReleaseEntriesSince(mcphugoserver.ChangelogMarkdown, in.SinceVersion, limit)
			if err != nil {
				return nil, getChangelogOutput{}, fmt.Errorf("invalid_params: %w", err)
			}

			dtos := make([]changelogEntryDTO, 0, len(entries))
			for _, e := range entries {
				dtos = append(dtos, changelogEntryDTO{Version: e.Version, Date: e.Date, Body: e.Body})
			}
			return nil, newGetChangelogOutput(getChangelogData{Entries: dtos, Total: len(dtos)}), nil
		}, func(schema any) any { return tools.WithMaxLimit(schema, "limit", getChangelogMaxLimit) })
}
