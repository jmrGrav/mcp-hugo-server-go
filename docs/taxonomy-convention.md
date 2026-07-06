# Taxonomy Convention

This document defines the official taxonomy normalization convention for mcp-hugo-server-go.
All MCP tools must route taxonomy processing through `internal/taxonomy`. No ad-hoc
tag or category normalization may exist outside that package.

## Source of truth

`internal/taxonomy` is the single source of truth. All other packages (`internal/site`,
`internal/hugosite`, `internal/tools/…`) must import it. The package is a leaf: it
imports no other internal packages.

## Three representations

Every tag or category has exactly three representations:

| Name | Field | Description |
|------|-------|-------------|
| Raw / Source | `source`, `tags`, `categories` | Original string as written in frontmatter. Trimmed only — case and spelling preserved. |
| Slug | `slug` | Canonical identifier. Lowercase, underscores → hyphens, whitespace → hyphens. Used for comparison, deduplication, and filtering. |
| Label | `label` | Display name. Title-cased (first rune uppercased per word). Derived from slug. |

### Examples

| Raw (frontmatter) | Slug | Label |
|---|---|---|
| `Security` | `security` | `Security` |
| `SECURITY` | `security` | `Security` |
| `Post Mortem` | `post-mortem` | `Post Mortem` |
| `post_mortem` | `post-mortem` | `Post Mortem` |
| `sécurité` | `sécurité` | `Sécurité` |
| `Read-only` | `read-only` | `Read Only` |

## Multilingual policy

Terms in different languages are **distinct**. `"security"` (English) and `"sécurité"` (French)
produce different slugs and are never automatically merged. Only case, whitespace, and underscore
variants of the same string are merged via their common slug. Automatic cross-language aliasing
is deliberately out of scope.

## MCP tool output fields

Every authenticated MCP tool that returns page data includes:

- `tags` / `categories` — raw strings exactly as written in the content file
- `tag_terms` / `category_terms` — `[]TaxonomyTerm{Source, Slug, Label}`, normalized

`get_page` (anonymous, uses the source resolver) also includes `tag_terms` / `category_terms`.

Listing/search anonymous tools (`list_pages`, `search_pages`, `get_recent_posts`) expose only
`tags` / `categories` because they iterate the public HTML index without source resolution.
In a real Hugo deployment the HTML is rendered from source, so the fields would agree; in
the test fixtures they may differ by design. The slug-based normalization within each tool
is always consistent — use `get_page` or any authenticated tool when the resolved view matters.

`get_related_content` additionally returns:
- `shared_tags` / `shared_categories` — slug strings of the shared terms
- `shared_tag_terms` / `shared_category_terms` — full `TaxonomyTerm` objects

## Filtering and comparison

All tag/category filters use **slug-based matching**. A filter `tag=Security` matches a page
whose tags contain any of `Security`, `security`, `SECURITY`, `security tools` (→ slug
`security-tools` would not match, but `security` would). This is implemented via
`taxonomy.MatchesSlug(rawValues, targetSlug)`.

## Public API (`internal/taxonomy`)

```go
// Core types
type TaxonomyTerm struct { Source, Slug, Label string }

// Normalize raw strings into deduplicated TaxonomyTerms (order preserved).
func Normalize(values []string) []TaxonomyTerm

// Single-term operations
func Slug(raw string) string
func Label(slug string) string

// Collection helpers
func Merge(a, b []TaxonomyTerm) []TaxonomyTerm
func Slugs(terms []TaxonomyTerm) []string

// Matching / filtering
func MatchesSlug(rawValues []string, targetSlug string) bool
func SharedTerms(a, b []string) []TaxonomyTerm

// Deduplication
func DeduplicateRaw(values []string) []string
```

## Invariants guaranteed by tests

- `internal/taxonomy/taxonomy_test.go` — unit tests: accents, casing, unicode, multilingual,
  whitespace, underscores, order, empty values
- `internal/tools/read/cross_tool_taxonomy_test.go` — cross-tool consistency: every MCP tool
  that emits taxonomy for the same page must return identical `tag_terms` slugs and labels
- `internal/site/taxonomy_test.go` — backward compatibility of the `site` package wrapper

## Adding a new tool

If you add an MCP tool that returns page data:

1. Include `tags []string` and `categories []string` (raw, for backward compat)
2. Include `tag_terms []taxonomy.TaxonomyTerm` and `category_terms []taxonomy.TaxonomyTerm`
3. Populate both fields from the same `site.Page` value using `taxonomy.Normalize(p.Tags)`
4. For filtering parameters, always compare via `taxonomy.MatchesSlug` or `taxonomy.Slug`
5. Add the new tool to the cross-tool consistency test
