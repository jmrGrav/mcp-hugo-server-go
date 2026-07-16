package read

import (
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
)

func pageIdentityFromPage(p site.Page, sourcePath, revision string, readingTime int) contentmodel.PageIdentity {
	return contentmodel.PageIdentity{
		Slug:        p.Slug,
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
