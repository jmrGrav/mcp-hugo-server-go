package site

import "github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"

// TaxonomyTerm is an alias for taxonomy.TaxonomyTerm.
// All MCP tools should import internal/taxonomy directly; this alias exists
// for backward compatibility with existing callers in this package.
type TaxonomyTerm = taxonomy.TaxonomyTerm

// NormalizeTaxonomyTerms is a backward-compatible wrapper for taxonomy.Normalize.
func NormalizeTaxonomyTerms(values []string) []TaxonomyTerm {
	return taxonomy.Normalize(values)
}
