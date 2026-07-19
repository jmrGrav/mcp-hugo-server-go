package read

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"golang.org/x/net/html"
)

// premutationBacklinks builds the backlink facet reused by get_backlinks,
// get_related_content, and get_page_for_edit. Keeping this mapping in one
// place prevents the later aggregated edit bundle (#527) from forking its
// own backlink shape from the standalone tool.
func premutationBacklinks(idx *site.Index, slug string) []backlinkDTO {
	if idx == nil || strings.TrimSpace(slug) == "" {
		return []backlinkDTO{}
	}
	entries := idx.GetBacklinks(slug)
	out := make([]backlinkDTO, len(entries))
	for i, e := range entries {
		out[i] = backlinkDTO{Slug: e.FromSlug, Title: e.FromTitle, URL: e.FromURL}
	}
	return out
}

// premutationImpact builds get_related_content's opt-in impact facet (#434):
// a pre-mutation summary of what changing ref would affect. Advisory only —
// never blocks a mutation, same posture as get_broken_links.
func premutationImpact(idx *site.Index, resolved site.ResolvedPage, ref site.Page, aliases map[string]string) impactDTO {
	impact := impactDTO{
		TaxonomyOrphans: []string{},
		Aliases:         []string{},
	}

	if idx != nil {
		// Hoisted out of the per-term loop below (design budget: one
		// ContentPages() scan per call, not one per term).
		contentPages := idx.ContentPages()
		for _, term := range append(append([]string{}, ref.Tags...), ref.Categories...) {
			target := taxonomy.ResolveAlias(taxonomy.Slug(term), aliases)
			if target == "" {
				continue
			}
			orphaned := true
			for _, pg := range contentPages {
				if pg.Slug == ref.Slug {
					continue
				}
				if taxonomy.MatchesSlugWithAliases(pg.Tags, target, aliases) ||
					taxonomy.MatchesSlugWithAliases(pg.Categories, target, aliases) {
					orphaned = false
					break
				}
			}
			if orphaned {
				impact.TaxonomyOrphans = append(impact.TaxonomyOrphans, term)
			}
		}
		for _, pg := range idx.Sitemap() {
			if pg.Slug == ref.Slug {
				impact.SitemapPresent = true
				break
			}
		}
		for _, pg := range idx.GetFeed(0) {
			if pg.Slug == ref.Slug {
				impact.FeedPresent = true
				break
			}
		}
	}

	if resolved.Source != nil {
		if raw, ok := resolved.Source.FrontmatterRaw["aliases"]; ok {
			impact.Aliases = frontmatterStringSlice(raw)
			if impact.Aliases == nil {
				impact.Aliases = []string{}
			}
		}
	}

	return impact
}

// premutationPreview builds inspect_rendered's opt-in include_preview facet
// (#435) by composing diff_page's git-diff logic, the same
// brokenInternalLinksFromDoc scan checkInternalLinks already ran against
// this same freshly-parsed doc (not brokenLinksForPage's cached, possibly
// stale RawHTML — using a different source here would let this response
// contradict its own checks[].internal_links result under build drift),
// and validate_frontmatter's per-page checks (validateFrontMatterPage) —
// rather than re-deriving any of their logic. Advisory only: never fails
// the call, never blocks a mutation.
func premutationPreview(ctx context.Context, idx *site.Index, cfg config.Config, resolved site.ResolvedPage, page site.Page, doc *html.Node) previewDTO {
	preview := previewDTO{Risks: []string{}}

	diffStatus, diffSummary := premutationDiffPreviewSummary(ctx, resolved, cfg)
	preview.DiffStatus = diffStatus
	preview.DiffSummary = diffSummary
	if diffStatus == "modified" {
		preview.Risks = append(preview.Risks, "uncommitted source changes: "+diffSummary)
	}

	broken, _ := brokenInternalLinksFromDoc(idx, page, doc)
	preview.BrokenLinksCount = len(broken)
	if len(broken) > 0 {
		preview.Risks = append(preview.Risks, fmt.Sprintf("%d broken internal link(s) on this page", len(broken)))
	}

	preview.FrontmatterValid = true
	if resolved.Source != nil {
		aliases := taxonomy.NormalizeAliasMap(cfg.TaxonomyAliases)
		issues := validateFrontMatterPage(*resolved.Source, aliases)
		if len(issues) > 0 {
			preview.FrontmatterValid = false
			preview.FrontmatterIssues = issues
			preview.Risks = append(preview.Risks, fmt.Sprintf("%d front-matter issue(s)", len(issues)))
		}
	}

	return preview
}

// premutationDiffPreviewSummary mirrors diff_page's own git-diff resolution
// (findGitRoot/gitShowFile/diffStatus/unifiedDiff) but returns a compact
// line-count summary instead of the full diff text — preview only needs
// enough to flag "this page has uncommitted changes," not the diff itself.
func premutationDiffPreviewSummary(ctx context.Context, resolved site.ResolvedPage, cfg config.Config) (status, summary string) {
	contentRoot := strings.TrimSpace(cfg.ContentRoot)
	if resolved.Source == nil || contentRoot == "" || cfg.GitBaseline.Mode == "disabled" {
		return "git_unavailable", "diff unavailable"
	}
	gitRoot, err := findGitRoot(ctx, contentRoot)
	if err != nil {
		return "git_unavailable", "diff unavailable: git repository not found"
	}
	absPath := resolved.SourcePath
	if absPath == "" {
		return "git_unavailable", "diff unavailable"
	}
	relRepoPath, err := filepath.Rel(gitRoot, absPath)
	if err != nil || strings.HasPrefix(relRepoPath, "..") {
		return "git_unavailable", "diff unavailable: source page is outside the repository root"
	}
	baseContent, baseExists, err := gitShowFile(ctx, gitRoot, relRepoPath)
	if err != nil && !isGitPathMissing(err) {
		return "git_unavailable", "diff unavailable"
	}
	if !baseExists {
		baseContent = nil
	}
	currentContent, err := os.ReadFile(absPath)
	if err != nil {
		return "git_unavailable", "diff unavailable"
	}
	dStatus := diffStatus(baseExists, currentContent, baseContent)
	if dStatus == "git_untracked" {
		return dStatus, "file is new and not yet tracked by git"
	}
	if dStatus == "unchanged" {
		return dStatus, "no uncommitted changes"
	}
	diffText, err := unifiedDiff(relRepoPath, baseContent, currentContent)
	if err != nil {
		return "git_unavailable", "diff unavailable"
	}
	added, removed := countDiffLines(diffText)
	return dStatus, fmt.Sprintf("%d line(s) added, %d removed", added, removed)
}

// countDiffLines counts +/- content lines in a unified diff, excluding the
// +++/--- file-header lines (which also start with +/-).
func countDiffLines(diffText string) (added, removed int) {
	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}
