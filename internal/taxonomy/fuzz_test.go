package taxonomy_test

import (
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
)

func FuzzTaxonomyNormalization(f *testing.F) {
	f.Add("Security|security|sécurité|post_mortem")
	f.Add(" Go |go|GO|read-only")
	f.Add("|||")
	f.Add("éducation|Éducation|education")

	f.Fuzz(func(t *testing.T, raw string) {
		values := strings.Split(raw, "|")
		terms := taxonomy.Normalize(values)
		seen := map[string]bool{}
		for _, term := range terms {
			if term.Source == "" || term.Slug == "" {
				t.Fatalf("Normalize produced empty field: %#v from %q", term, raw)
			}
			if seen[term.Slug] {
				t.Fatalf("Normalize produced duplicate slug %q from %q", term.Slug, raw)
			}
			seen[term.Slug] = true
			if taxonomy.Slug(term.Source) != term.Slug {
				t.Fatalf("Source/Slug divergence: source=%q slug=%q raw=%q", term.Source, term.Slug, raw)
			}
		}

		dedup := taxonomy.DeduplicateRaw(values)
		dedupSeen := map[string]bool{}
		for _, v := range dedup {
			s := taxonomy.Slug(v)
			if s == "" {
				t.Fatalf("DeduplicateRaw returned empty-slug value %q from %q", v, raw)
			}
			if dedupSeen[s] {
				t.Fatalf("DeduplicateRaw returned duplicate slug %q from %q", s, raw)
			}
			dedupSeen[s] = true
		}
	})
}

func FuzzNormalizeAliasMap(f *testing.F) {
	// Two key-value pairs to exercise multi-entry collision and merge behavior.
	f.Add("Sécurité", "Security", "postmortem", "post-mortems")
	f.Add("loop", "loop", "", "security")
	f.Add("GO", "go", "Go", "golang")
	f.Add("a", "b", "a", "c") // same key, different values

	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		input := map[string]string{k1: v1, k2: v2}
		got := taxonomy.NormalizeAliasMap(input)
		for nk, nv := range got {
			if nk == "" || nv == "" {
				t.Fatalf("NormalizeAliasMap produced empty key/value: %v from %v", got, input)
			}
			if nk != taxonomy.Slug(nk) || nv != taxonomy.Slug(nv) {
				t.Fatalf("NormalizeAliasMap produced non-canonical entry: %v from %v", got, input)
			}
			if nk == nv {
				t.Fatalf("NormalizeAliasMap kept self-alias: %v from %v", got, input)
			}
		}
	})
}
