package read

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listPageRevisionsInput struct {
	Slug         string `json:"slug"`
	Lang         string `json:"lang,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type pageRevisionDTO struct {
	RevisionKind string `json:"revision_kind"`
	Commit       string `json:"commit"`
	ShortCommit  string `json:"short_commit"`
	Date         string `json:"date"`
	Subject      string `json:"subject"`
}

type listPageRevisionsData struct {
	Slug      string            `json:"slug"`
	SourceKey string            `json:"source_key,omitempty"`
	Status    string            `json:"status"`
	Revisions []pageRevisionDTO `json:"revisions"`
	Total     int               `json:"total"`
}

type listPageRevisionsOutput struct {
	toolcontract.ToolResponse[listPageRevisionsData]
}

func newListPageRevisionsOutput(data listPageRevisionsData, now time.Time) listPageRevisionsOutput {
	return listPageRevisionsOutput{ToolResponse: successEnvelope(data, now)}
}

const listPageRevisionsDefaultLimit = 20
const listPageRevisionsMaxLimit = 100

// RegisterListPageRevisions registers list_page_revisions (#615): a
// read-only tool returning the prior git commits touching a page's source
// file, establishing the "what could I revert to" answer before any
// write-path rollback tool is designed. Deliberately conservative and
// read-only, per the issue's own explicit scoping — a real rollback_page
// write tool is a much larger, separate design question (a second mutation
// path that would need to interoperate with expected_revision/idempotency/
// the in-memory index/rate limits the same way update_page does), not
// something this tool attempts.
func RegisterListPageRevisions(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "list_page_revisions", "List page revisions",
		"List the prior git commits that touched a page's source file, most recent first. For multilingual bundles, pass `lang` to select a translation explicitly; a bare slug that resolves among several translations returns an explicit warning. Each row is explicitly `revision_kind:\"git_commit\"`: it is suitable for git history and diff_page, but is not an apply_content_plan snapshot and cannot be passed to rollback_change. Requires a local Git repository and a configured content root, same as diff_page; when git metadata is unavailable, `status: \"git_unavailable\"` is returned with an empty `revisions` list and an explanatory warning rather than failing outright. `limit` caps how many commits are returned (default 20, max 100) — `--follow` is used so renames are tracked across history. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPageRevisionsInput) (*mcp.CallToolResult, listPageRevisionsOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source git diagnostics")
			}
			if srcIdx == nil {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("git_metadata_unavailable: source index not initialized")
			}
			if strings.TrimSpace(in.Slug) == "" {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			if err := validateSlugLangConsistency(in.Slug, in.Lang); err != nil {
				return nil, listPageRevisionsOutput{}, err
			}
			if err := negativeLimitError(in.Limit); err != nil {
				return nil, listPageRevisionsOutput{}, err
			}
			limit := clampLimit(in.Limit, listPageRevisionsDefaultLimit, listPageRevisionsMaxLimit)

			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.ResolveWithLang(in.Slug, strings.TrimSpace(in.Lang))
			if !ok || resolved.Source == nil {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			languageWarning := implicitMultilingualResolutionWarning(in.Slug, in.Lang, resolved, srcIdx, cfg)
			contentRoot := strings.TrimSpace(cfg.ContentRoot)
			if contentRoot == "" {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("git_metadata_unavailable: content root not configured")
			}
			slug := canonicalResolvedSlug(resolved)
			sourceKey := contentmodel.SourceKeyFromLogicalPath(resolvedLogicalPath(contentRoot, resolved.SourcePath, resolved.Source.Slug))

			unavailable := func(reason string) listPageRevisionsOutput {
				resp := newListPageRevisionsOutput(listPageRevisionsData{
					Slug:      slug,
					SourceKey: sourceKey,
					Status:    "git_unavailable",
					Revisions: []pageRevisionDTO{},
				}, time.Now().UTC())
				resp.Warnings = []string{fmt.Sprintf("Git repository metadata is unavailable (%s); no revision history to list.", reason)}
				if languageWarning != "" {
					resp.Warnings = append(resp.Warnings, languageWarning)
				}
				return resp
			}
			if cfg.GitBaseline.Mode == "disabled" {
				return nil, unavailable("git baseline is disabled by configuration"), nil
			}
			gitRoot, err := findGitRoot(ctx, contentRoot)
			if err != nil {
				return nil, unavailable(strings.TrimSpace(err.Error())), nil
			}
			absPath := resolved.SourcePath
			if absPath == "" {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("content_not_found: page not readable for slug %q", in.Slug)
			}
			relRepoPath, err := filepath.Rel(gitRoot, absPath)
			if err != nil || strings.HasPrefix(relRepoPath, "..") {
				return nil, listPageRevisionsOutput{}, fmt.Errorf("git_metadata_unavailable: source page is outside the repository root")
			}

			const fieldSep = "\x1f" // ASCII unit separator: not expected in a commit subject
			logFormat := "%H" + fieldSep + "%h" + fieldSep + "%aI" + fieldSep + "%s"
			raw, err := gitutil.Output(ctx, gitRoot, "log", "--follow", "--max-count="+fmt.Sprint(limit), "--format="+logFormat, "--", filepath.ToSlash(relRepoPath))
			if err != nil {
				return nil, unavailable(strings.TrimSpace(err.Error())), nil
			}

			revisions := []pageRevisionDTO{}
			if strings.TrimSpace(raw) != "" {
				for _, line := range strings.Split(raw, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					parts := strings.SplitN(line, fieldSep, 4)
					if len(parts) != 4 {
						continue
					}
					revisions = append(revisions, pageRevisionDTO{
						RevisionKind: "git_commit",
						Commit:       parts[0],
						ShortCommit:  parts[1],
						Date:         parts[2],
						Subject:      parts[3],
					})
				}
			}

			resp := newListPageRevisionsOutput(listPageRevisionsData{
				Slug:      slug,
				SourceKey: sourceKey,
				Status:    "ok",
				Revisions: revisions,
				Total:     len(revisions),
			}, time.Now().UTC())
			if languageWarning != "" {
				resp.Warnings = []string{languageWarning}
			}
			return nil, resp, nil
		}, func(schema any) any { return tools.WithMaxLimit(schema, "limit", listPageRevisionsMaxLimit) })
}
