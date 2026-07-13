package read

import (
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
)

// legacyEnvelopeDTO preserves the existing JSON contract for tools not yet
// migrated to ToolResponse. The Errors field carries only the human-readable
// message; the machine-readable Code is intentionally dropped here to keep
// backward compatibility. Migrate a tool to ToolResponse to expose Code.
type legacyEnvelopeDTO[T any] struct {
	Success     bool     `json:"success"`
	Version     string   `json:"version"`
	GeneratedAt string   `json:"generated_at"`
	Data        T        `json:"data"`
	Warnings    []string `json:"warnings"`
	Errors      []string `json:"errors"`
}

func pageIdentityFromPage(p site.Page, sourcePath string, readingTime int) contentmodel.PageIdentity {
	return contentmodel.PageIdentity{
		Slug:        p.Slug,
		Lang:        p.Lang,
		URL:         p.URL,
		SourcePath:  sourcePath,
		Title:       p.Title,
		Tags:        toContentmodelTerms(site.NormalizeTaxonomyTerms(p.Tags)),
		Categories:  toContentmodelTerms(site.NormalizeTaxonomyTerms(p.Categories)),
		ReadingTime: readingTime,
	}
}

func legacyEnvelope[T any](data T, now time.Time) legacyEnvelopeDTO[T] {
	resp := toolcontract.Success(data, toolcontract.NewMeta(toolResultVersion, now))
	return legacyEnvelopeDTO[T]{
		Success:     resp.Success,
		Version:     resp.Meta.ServerVersion,
		GeneratedAt: resp.Meta.GeneratedAt,
		Data:        resp.Data,
		Warnings:    resp.Warnings,
		Errors:      toolErrorMessages(resp.Errors),
	}
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

func toolErrorMessages(errs []toolcontract.ToolError) []string {
	if len(errs) == 0 {
		return []string{}
	}
	out := make([]string, len(errs))
	for i, err := range errs {
		out[i] = err.Message
	}
	return out
}
