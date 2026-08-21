package read

import (
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
)

func pageIdentityFromPage(p site.Page, sourcePath, revision string, readingTime int) contentmodel.PageIdentity {
	return contentmodel.PageIdentity{
		Slug:        p.Slug,
		SourceKey:   contentmodel.SourceKeyFromLogicalPath(sourcePath),
		Lang:        p.Lang,
		URL:         p.URL,
		SourcePath:  sourcePath,
		Revision:    revision,
		Title:       p.Title,
		Tags:        toContentmodelTerms(site.NormalizeTaxonomyTerms(p.Tags)),
		Categories:  toContentmodelTerms(site.NormalizeTaxonomyTerms(p.Categories)),
		ReadingTime: readingTime,
	}
}

func successEnvelope[T any](data T, now time.Time) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, now))
}

const contentProvenanceSiteSourceUntrusted = "site_source_untrusted"

// contentProvenanceSiteRenderedPublicUntrusted marks a payload built from
// the site's rendered public HTML output (as opposed to raw source) —
// still site-authored, still untrusted, distinct only in which build
// stage produced the text. Mirrors the anonymous-scope tools' own
// "site_rendered_public_untrusted" literal (internal/tools/anonymous/tools.go).
const contentProvenanceSiteRenderedPublicUntrusted = "site_rendered_public_untrusted"

func successEnvelopeWithContentProvenance[T any](data T, now time.Time, provenance string) toolcontract.ToolResponse[T] {
	meta := toolcontract.NewMeta(buildinfo.Version, now)
	meta.ContentProvenance = provenance
	return toolcontract.Success(data, meta)
}

// toContentmodelTerms converts site-package taxonomy terms to the contentmodel
// equivalent. The two types are structurally identical; the conversion exists
// to keep contentmodel free of site-package imports during the migration.
func toContentmodelTerms(terms []site.TaxonomyTerm) []contentmodel.TaxonomyTerm {
	out := make([]contentmodel.TaxonomyTerm, len(terms))
	for i, t := range terms {
		out[i] = contentmodel.TaxonomyTerm{
			Source: t.Source,
			Slug:   t.Slug,
			Label:  t.Label,
		}
	}
	return out
}

// canonicalTaxonomyStrings is the stable plain-array representation used by
// read tools. Rich tag_terms/category_terms retain source and label; the
// legacy string arrays are normalized slugs so agents can compare them across
// tools without casing-dependent mismatches (#970).
func canonicalTaxonomyStrings(values []string) []string {
	return taxonomy.Slugs(taxonomy.Normalize(values))
}
