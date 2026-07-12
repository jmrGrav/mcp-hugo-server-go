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
	f.Add("Sécurité", "Security")
	f.Add("postmortem", "post-mortems")
	f.Add("loop", "loop")
	f.Add("", "security")

	f.Fuzz(func(t *testing.T, k, v string) {
		got := taxonomy.NormalizeAliasMap(map[string]string{k: v})
		for nk, nv := range got {
			if nk == "" || nv == "" {
				t.Fatalf("NormalizeAliasMap produced empty key/value: %v", got)
			}
			if nk != taxonomy.Slug(nk) || nv != taxonomy.Slug(nv) {
				t.Fatalf("NormalizeAliasMap produced non-canonical entry: %v", got)
			}
			if nk == nv {
				t.Fatalf("NormalizeAliasMap kept self-alias: %v", got)
			}
		}
	})
}
