package read

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/aireadiness"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type validateAIReadinessInput struct {
	Slug string `json:"slug"`
	// Lang optionally selects one translation of a multilingual page bundle to
	// audit explicitly (e.g. "fr", "en"), matching the other read tools
	// (get_page_markdown, get_page_frontmatter, ...). When omitted, the
	// translation is resolved implicitly from the slug shape as before.
	Lang string `json:"lang,omitempty"`
}

type validateAIReadinessData struct {
	Slug               string              `json:"slug"`
	ResolvedLang       string              `json:"resolved_lang"`
	ResolvedSourcePath string              `json:"resolved_source_path"`
	Revision           string              `json:"revision,omitempty"`
	State              site.LifecycleState `json:"state"`
	Status             string              `json:"status"`
	Checks             aireadiness.Checks  `json:"checks"`
	Warnings           []string            `json:"warnings"`
	Suggestions        []string            `json:"suggestions"`
}

type validateAIReadinessOutput struct {
	toolcontract.ToolResponse[validateAIReadinessData]
}

func RegisterAIReadiness(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "check_ai_readiness", "Validate AI readiness",
		"Run a deterministic source-structure audit over one Hugo page's front matter and Markdown body. Checks only heading hierarchy, section/paragraph length outliers, metadata presence, internal-link density, and citation structure. This tool is intentionally source-oriented: it does not score SEO, rendered HTML, build freshness, or broken-link correctness. Scope caveat (#865): the name is broader than the contract — a `pass` means the page is *structurally* well-formed, NOT that it is substantive, complete, or high-quality. Very short content can pass because the checks are structural, not editorial; do not read a `pass` as an editorial go-ahead. For multilingual content, pass `lang` to resolve one translation explicitly; URL-based language selection remains accepted as a compatibility path. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in validateAIReadinessInput) (*mcp.CallToolResult, validateAIReadinessOutput, error) {
			if srcIdx == nil {
				return nil, validateAIReadinessOutput{}, fmt.Errorf("source index not initialized")
			}
			slug := strings.TrimSpace(in.Slug)
			if slug == "" {
				return nil, validateAIReadinessOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}

			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.ResolveWithLang(slug, strings.TrimSpace(in.Lang))
			if !ok || resolved.Source == nil {
				return nil, validateAIReadinessOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}

			report := aireadiness.Analyze(aireadiness.Document{
				Title:       resolved.Source.Title,
				Date:        resolved.Source.Date,
				Summary:     frontmatterStringValue(resolved.Source.FrontmatterRaw["summary"]),
				Description: frontmatterStringValue(resolved.Source.FrontmatterRaw["description"]),
				Tags:        append([]string(nil), resolved.Source.Tags...),
				Categories:  append([]string(nil), resolved.Source.Categories...),
				Markdown:    resolved.Source.Body,
			})

			relPath := resolved.Source.Slug
			if resolved.SourcePath != "" {
				relPath = fileutil.LogicalContentPath(cfg.ContentRoot, resolved.SourcePath)
			}
			data := validateAIReadinessData{
				Slug:               canonicalResolvedSlug(resolved),
				ResolvedLang:       resolvedLang(resolved),
				ResolvedSourcePath: relPath,
				Revision:           resolvedRevision(resolved),
				State:              resolvedState(resolved, cfg.SiteRoot),
				Status:             report.Status,
				Checks:             report.Checks,
				Warnings:           report.Warnings,
				Suggestions:        report.Suggestions,
			}
			return nil, newValidateAIReadinessOutput(data, time.Now().UTC()), nil
		})
}

func newValidateAIReadinessOutput(data validateAIReadinessData, now time.Time) validateAIReadinessOutput {
	return validateAIReadinessOutput{ToolResponse: successEnvelope(data, now)}
}

func frontmatterStringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
