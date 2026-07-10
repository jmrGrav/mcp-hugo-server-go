// Package taxonomy defines the canonical taxonomy normalization API.
//
// All MCP tools and site packages must route taxonomy processing through this package.
// No ad-hoc tag/category normalization may exist outside this package.
//
// # Convention
//
//   - Raw (Source): the original string as written in the content frontmatter.
//     Only leading/trailing whitespace is trimmed; case and spelling are preserved.
//
//   - Slug: canonical identifier used for comparison, deduplication, and filtering.
//     Rules: lowercase, underscores → hyphens, consecutive whitespace → single hyphen.
//     Example: "Post Mortem" → "post-mortem"; "security_tools" → "security-tools".
//
//   - Label: human-readable display name derived from the slug.
//     Rules: each hyphen-separated word is title-cased (first rune uppercased).
//     Example: "post-mortem" → "Post Mortem"; "sécurité" → "Sécurité".
//
// # Multilingual policy
//
// Terms in different languages are treated as distinct, even when semantically
// equivalent. "security" (English) and "sécurité" (French) produce different
// slugs and are never automatically merged. Only case, whitespace, and underscore
// variants of the same string are merged via their common slug.
package taxonomy

import (
	"sort"
	"strings"
	"unicode"
)

// TaxonomyTerm is the canonical representation of a single tag or category.
type TaxonomyTerm struct {
	Source string `json:"source"` // original string, trimmed
	Slug   string `json:"slug"`   // canonical identifier
	Label  string `json:"label"`  // display name
}

// Normalize converts raw tag/category strings into deduplicated TaxonomyTerms.
// Deduplication is by Slug. Order of first appearance is preserved.
// Empty and whitespace-only values are dropped.
func Normalize(values []string) []TaxonomyTerm {
	seen := map[string]bool{}
	out := make([]TaxonomyTerm, 0, len(values))
	for _, raw := range values {
		source := strings.TrimSpace(raw)
		if source == "" {
			continue
		}
		slug := Slug(source)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, TaxonomyTerm{
			Source: source,
			Slug:   slug,
			Label:  Label(slug),
		})
	}
	return out
}

// Slug returns the canonical slug for a raw term string.
// Lowercases the input, replaces underscores with hyphens, and collapses
// whitespace runs to a single hyphen.
func Slug(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.ReplaceAll(raw, "_", "-")
	fields := strings.Fields(raw)
	return strings.Join(fields, "-")
}

// Label returns the display name for a slug.
// Each hyphen-separated word has its first Unicode rune uppercased.
func Label(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, word := range words {
		words[i] = titleWord(word)
	}
	return strings.Join(words, " ")
}

// Merge returns the union of a and b, deduplicated by Slug.
// The first occurrence of each slug is kept.
func Merge(a, b []TaxonomyTerm) []TaxonomyTerm {
	seen := map[string]bool{}
	out := make([]TaxonomyTerm, 0, len(a)+len(b))
	for _, t := range a {
		if !seen[t.Slug] {
			seen[t.Slug] = true
			out = append(out, t)
		}
	}
	for _, t := range b {
		if !seen[t.Slug] {
			seen[t.Slug] = true
			out = append(out, t)
		}
	}
	return out
}

// Slugs returns the slug strings for a term slice, in order.
func Slugs(terms []TaxonomyTerm) []string {
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = t.Slug
	}
	return out
}

// MatchesSlug reports whether any value in rawValues has a slug equal to targetSlug.
// Use this for case/whitespace-insensitive tag and category filtering.
func MatchesSlug(rawValues []string, targetSlug string) bool {
	for _, raw := range rawValues {
		if Slug(raw) == targetSlug {
			return true
		}
	}
	return false
}

// SharedTerms returns TaxonomyTerms whose slugs appear in both a and b.
// The returned terms carry the Source value from a.
func SharedTerms(a, b []string) []TaxonomyTerm {
	bSlugs := make(map[string]bool, len(b))
	for _, raw := range b {
		if s := Slug(raw); s != "" {
			bSlugs[s] = true
		}
	}
	seen := map[string]bool{}
	var out []TaxonomyTerm
	for _, raw := range a {
		s := Slug(raw)
		if s == "" || seen[s] {
			continue
		}
		if bSlugs[s] {
			seen[s] = true
			out = append(out, TaxonomyTerm{
				Source: strings.TrimSpace(raw),
				Slug:   s,
				Label:  Label(s),
			})
		}
	}
	return out
}

// DeduplicateRaw deduplicates raw strings by slug, preserving first occurrence.
// Returns sorted output. Use this in place of ad-hoc lowercase-keyed dedup loops.
func DeduplicateRaw(values []string) []string {
	seen := map[string]string{}
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := Slug(v)
		if _, ok := seen[key]; !ok {
			seen[key] = v
		}
	}
	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func titleWord(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// ResolveAlias returns the canonical slug for slug using the operator-defined alias
// map. If slug is not a key in aliases, it is returned unchanged. aliases maps
// non-canonical slugs to canonical ones (e.g. "sécurité" → "security").
func ResolveAlias(slug string, aliases map[string]string) string {
	if canonical, ok := aliases[slug]; ok {
		return canonical
	}
	return slug
}

// NormalizeAliasMap returns a new map with both keys and values run through Slug,
// making operator config robust to casing, whitespace, and underscores.
func NormalizeAliasMap(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return raw
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		ks := Slug(k)
		vs := Slug(v)
		if ks != "" && vs != "" && ks != vs {
			out[ks] = vs
		}
	}
	return out
}

// ApplyAliases replaces each raw term whose slug is an alias key with the canonical
// slug, then deduplicates. Terms not in the alias map are returned as-is.
// Use this when building tag/category lists for tool responses.
func ApplyAliases(rawValues []string, aliases map[string]string) []string {
	if len(aliases) == 0 {
		return rawValues
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		s := Slug(v)
		if canonical, ok := aliases[s]; ok {
			if !seen[canonical] {
				seen[canonical] = true
				out = append(out, canonical)
			}
		} else {
			if !seen[s] {
				seen[s] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// MatchesSlugWithAliases reports whether any value in rawValues has a slug equal
// to targetSlug, or an alias that resolves to targetSlug.
func MatchesSlugWithAliases(rawValues []string, targetSlug string, aliases map[string]string) bool {
	for _, raw := range rawValues {
		s := Slug(raw)
		if s == targetSlug {
			return true
		}
		if len(aliases) > 0 {
			if canonical, ok := aliases[s]; ok && canonical == targetSlug {
				return true
			}
		}
	}
	return false
}

// FindSimilarPairs returns pairs of slugs whose Levenshtein distance is in
// [1, maxDist] and whose length is at least minLen. Pairs where one slug is
// already an alias of the other are excluded. The returned pairs are sorted
// lexicographically within each pair.
func FindSimilarPairs(slugs []string, maxDist, minLen int, aliases map[string]string) [][2]string {
	var out [][2]string
	for i := 0; i < len(slugs); i++ {
		if len([]rune(slugs[i])) < minLen {
			continue
		}
		for j := i + 1; j < len(slugs); j++ {
			if len([]rune(slugs[j])) < minLen {
				continue
			}
			a, b := slugs[i], slugs[j]
			// Skip pairs already connected via the alias map.
			if ResolveAlias(a, aliases) == b || ResolveAlias(b, aliases) == a {
				continue
			}
			if d := levenshtein(a, b); d >= 1 && d <= maxDist {
				if a > b {
					a, b = b, a
				}
				out = append(out, [2]string{a, b})
			}
		}
	}
	return out
}

// levenshtein computes the Levenshtein edit distance between a and b (rune-aware).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	m, n := len(ra), len(rb)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
