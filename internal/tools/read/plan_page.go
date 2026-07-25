package read

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type planPageInput struct {
	Topic        string   `json:"topic,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	ResponseMode string   `json:"response_mode,omitempty"`
}

type planPageData struct {
	ContentTypes       []contentTypeDTO           `json:"content_types"`
	SpecialFiles       []specialFileDTO           `json:"special_files,omitempty"`
	RelevantTags       []string                   `json:"relevant_tags,omitempty"`
	RelevantCategories []string                   `json:"relevant_categories,omitempty"`
	SuggestedLinks     []linkSuggestionDTO        `json:"suggested_links,omitempty"`
	EmptyLinksReason   *emptyResultExplanationDTO `json:"empty_links_reason,omitempty"`
}

type planPageOutput struct {
	toolcontract.ToolResponse[planPageData]
}

func newPlanPageOutput(data planPageData, now time.Time) planPageOutput {
	return planPageOutput{ToolResponse: successEnvelope(data, now)}
}

// RegisterPlanPage registers plan_page (#622): a pre-writing scaffold that
// bundles three calls an agent otherwise makes separately before writing the
// first line of a new article — list_content_types (expected archetype/
// fields), suggest_links (internal-linking candidates), and a filtered view
// of the site's existing tags/categories relevant to the topic — into one.
// Each of the three underlying mechanisms is reused verbatim (computeContentTypes,
// scoreLinkSuggestions), not reimplemented, so plan_page's output for each
// facet is identical to calling the standalone tool directly.
func RegisterPlanPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "plan_page", "Plan a new page",
		"Pre-writing scaffold bundling three calls into one, before you write the first line of a new article: `content_types`/`special_files` (identical to list_content_types — the expected archetype and front matter fields for the site's content types), `relevant_tags`/`relevant_categories` (the subset of the site's existing tag/category vocabulary matching `topic` or any of the submitted `tags`/`categories`, via a case-insensitive substring match in either direction — this also surfaces an existing differently-cased spelling for a tag/category you're about to introduce, e.g. submitting `tags: [\"Debug\"]` when the index already has \"debug\" returns \"debug\" in `relevant_tags\"`), and `suggested_links` (identical to suggest_links — internal-linking candidates scored by shared tags/categories, using `topic` as the body-mention text) when at least one of `tags`/`categories` is provided (omitted with `empty_links_reason` when neither is set, since suggest_links itself requires at least one to score anything). All three facets are read-only and reuse the exact same underlying logic as their standalone tools — nothing here is a new heuristic beyond the tag/category substring match. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in planPageInput) (*mcp.CallToolResult, planPageOutput, error) {
			contentTypes, specialFiles := computeContentTypes(ctx, srcIdx, cfg)

			searchTerms := make([]string, 0, 1+len(in.Tags)+len(in.Categories))
			if strings.TrimSpace(in.Topic) != "" {
				searchTerms = append(searchTerms, in.Topic)
			}
			searchTerms = append(searchTerms, in.Tags...)
			searchTerms = append(searchTerms, in.Categories...)

			var relevantTags, relevantCategories []string
			var suggestedLinks []linkSuggestionDTO
			var emptyLinksReason *emptyResultExplanationDTO
			if idx != nil {
				relevantTags = matchVocabulary(idx.AllTags(), searchTerms)
				relevantCategories = matchVocabulary(idx.AllCategories(), searchTerms)

				if len(in.Tags) > 0 || len(in.Categories) > 0 {
					suggestions, evaluated := scoreLinkSuggestions(idx, "", in.Tags, in.Categories, in.Topic, 10)
					suggestedLinks = suggestions
					if len(suggestions) == 0 {
						emptyLinksReason = newEmptyResultExplanation(evaluated, minTaxonomyAffinityScore)
					}
				}
			}

			return nil, newPlanPageOutput(planPageData{
				ContentTypes:       contentTypes,
				SpecialFiles:       specialFiles,
				RelevantTags:       relevantTags,
				RelevantCategories: relevantCategories,
				SuggestedLinks:     suggestedLinks,
				EmptyLinksReason:   emptyLinksReason,
			}, time.Now().UTC()), nil
		})
}

// matchVocabulary returns the subset of vocab whose value case-insensitively
// contains, or is contained by, any of terms — a single simple substring
// heuristic that covers both "topic mentions an existing tag" and "the
// caller's own submitted tag already exists under a different casing"
// (#622). Returns nil (not just empty) when terms is empty, so an
// omitted topic/tags/categories input cleanly omits the field via
// json:",omitempty" rather than reporting an empty array.
func matchVocabulary(vocab []string, terms []string) []string {
	if len(terms) == 0 {
		return nil
	}
	var lowerTerms []string
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			lowerTerms = append(lowerTerms, t)
		}
	}
	if len(lowerTerms) == 0 {
		return nil
	}
	var out []string
	for _, v := range vocab {
		vl := strings.ToLower(v)
		for _, t := range lowerTerms {
			if strings.Contains(vl, t) || strings.Contains(t, vl) {
				out = append(out, v)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
