package site

import "testing"

func TestNormalizeTaxonomyTerms(t *testing.T) {
	got := NormalizeTaxonomyTerms([]string{" postmortem ", "Post-mortems", "security", "sécurité", "", "security"})
	if len(got) != 4 {
		t.Fatalf("NormalizeTaxonomyTerms() length = %d want 4: %#v", len(got), got)
	}
	cases := map[string]string{
		"postmortem":   "Postmortem",
		"post-mortems": "Post Mortems",
		"security":     "Security",
		"sécurité":     "Sécurité",
	}
	for _, term := range got {
		want, ok := cases[term.Slug]
		if !ok {
			t.Fatalf("unexpected slug %q in %#v", term.Slug, got)
		}
		if term.Label != want {
			t.Fatalf("label for %q = %q want %q", term.Slug, term.Label, want)
		}
		if term.Source == "" {
			t.Fatalf("term source is empty: %#v", term)
		}
	}
}
