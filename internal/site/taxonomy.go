package site

import (
	"strings"
	"unicode"
)

type TaxonomyTerm struct {
	Source string `json:"source"`
	Slug   string `json:"slug"`
	Label  string `json:"label"`
}

func NormalizeTaxonomyTerms(values []string) []TaxonomyTerm {
	seen := map[string]bool{}
	out := make([]TaxonomyTerm, 0, len(values))
	for _, raw := range values {
		source := strings.TrimSpace(raw)
		if source == "" {
			continue
		}
		slug := taxonomySlug(source)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, TaxonomyTerm{
			Source: source,
			Slug:   slug,
			Label:  taxonomyLabel(slug),
		})
	}
	return out
}

func taxonomySlug(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.ReplaceAll(raw, "_", "-")
	fields := strings.Fields(raw)
	return strings.Join(fields, "-")
}

func taxonomyLabel(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, word := range words {
		words[i] = titleWord(word)
	}
	return strings.Join(words, " ")
}

func titleWord(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
