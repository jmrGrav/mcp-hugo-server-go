package taxonomy_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"security", "security"},
		{"Security", "security"},
		{"SECURITY", "security"},
		{"Post Mortem", "post-mortem"},
		{"post_mortem", "post-mortem"},
		{"post-mortem", "post-mortem"},
		{" postmortem ", "postmortem"},
		{"sécurité", "sécurité"},
		{"Sécurité", "sécurité"},
		{"security tools", "security-tools"},
		{"", ""},
		{"   ", ""},
		{"Go", "go"},
		{"Read-only", "read-only"},
		{"CI/CD", "ci/cd"},
	}
	for _, tc := range cases {
		if got := taxonomy.Slug(tc.raw); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"security", "Security"},
		{"post-mortem", "Post Mortem"},
		{"post-mortems", "Post Mortems"},
		{"sécurité", "Sécurité"},
		{"security-tools", "Security Tools"},
		{"go", "Go"},
		{"read-only", "Read Only"},
		{"postmortem", "Postmortem"},
	}
	for _, tc := range cases {
		if got := taxonomy.Label(tc.slug); got != tc.want {
			t.Errorf("Label(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Run("basic dedup and normalization", func(t *testing.T) {
		got := taxonomy.Normalize([]string{" postmortem ", "Post-mortems", "security", "sécurité", "", "security"})
		if len(got) != 4 {
			t.Fatalf("Normalize() len=%d, want 4: %#v", len(got), got)
		}
		want := map[string]string{
			"postmortem":   "Postmortem",
			"post-mortems": "Post Mortems",
			"security":     "Security",
			"sécurité":     "Sécurité",
		}
		for _, term := range got {
			wantLabel, ok := want[term.Slug]
			if !ok {
				t.Fatalf("unexpected slug %q", term.Slug)
			}
			if term.Label != wantLabel {
				t.Errorf("Label for %q = %q, want %q", term.Slug, term.Label, wantLabel)
			}
			if term.Source == "" {
				t.Errorf("Source is empty for slug %q", term.Slug)
			}
		}
	})

	t.Run("case variants are the same term", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"Security", "security", "SECURITY"})
		if len(got) != 1 {
			t.Fatalf("want 1 term for case variants, got %d: %#v", len(got), got)
		}
		if got[0].Slug != "security" {
			t.Errorf("slug = %q, want security", got[0].Slug)
		}
		if got[0].Source != "Security" {
			t.Errorf("Source = %q, want Security (first occurrence preserved)", got[0].Source)
		}
	})

	t.Run("whitespace variants are the same term", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"post mortem", "Post Mortem", "post-mortem"})
		if len(got) != 1 {
			t.Fatalf("want 1 term for whitespace variants, got %d: %#v", len(got), got)
		}
		if got[0].Slug != "post-mortem" {
			t.Errorf("slug = %q, want post-mortem", got[0].Slug)
		}
	})

	t.Run("underscore variants are the same term", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"security_tools", "security-tools"})
		if len(got) != 1 {
			t.Fatalf("want 1 term, got %d: %#v", len(got), got)
		}
		if got[0].Slug != "security-tools" {
			t.Errorf("slug = %q, want security-tools", got[0].Slug)
		}
	})

	t.Run("cross-language terms are distinct", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"security", "sécurité", "Sicherheit"})
		if len(got) != 3 {
			t.Fatalf("cross-language terms must not merge, got %d: %#v", len(got), got)
		}
	})

	t.Run("empty and blank values dropped", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"", "   ", "tag"})
		if len(got) != 1 {
			t.Fatalf("want 1 term, got %d", len(got))
		}
	})

	t.Run("order preserved", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"zebra", "alpha", "middle"})
		if len(got) != 3 {
			t.Fatalf("want 3 terms, got %d", len(got))
		}
		if got[0].Slug != "zebra" || got[1].Slug != "alpha" || got[2].Slug != "middle" {
			t.Errorf("order not preserved: %v", taxonomy.Slugs(got))
		}
	})

	t.Run("unicode accents", func(t *testing.T) {
		got := taxonomy.Normalize([]string{"Éducation", "éducation"})
		if len(got) != 1 {
			t.Fatalf("want 1 term for accent variants, got %d: %#v", len(got), got)
		}
		if got[0].Slug != "éducation" {
			t.Errorf("slug = %q, want éducation", got[0].Slug)
		}
		if got[0].Label != "Éducation" {
			t.Errorf("label = %q, want Éducation", got[0].Label)
		}
	})
}

func TestMatchesSlug(t *testing.T) {
	tags := []string{"Security", "Go", "Hugo"}
	if !taxonomy.MatchesSlug(tags, "security") {
		t.Error("MatchesSlug(security) should match Security")
	}
	if !taxonomy.MatchesSlug(tags, "go") {
		t.Error("MatchesSlug(go) should match Go")
	}
	if taxonomy.MatchesSlug(tags, "rust") {
		t.Error("MatchesSlug(rust) should not match")
	}
	if !taxonomy.MatchesSlug(tags, "hugo") {
		t.Error("MatchesSlug(hugo) should match Hugo")
	}
}

func TestSharedTerms(t *testing.T) {
	a := []string{"Security", "Go", "Hugo"}
	b := []string{"security", "python", "hugo"}
	shared := taxonomy.SharedTerms(a, b)
	if len(shared) != 2 {
		t.Fatalf("want 2 shared terms, got %d: %#v", len(shared), shared)
	}
	slugs := taxonomy.Slugs(shared)
	hasSlug := func(s string) bool {
		for _, sl := range slugs {
			if sl == s {
				return true
			}
		}
		return false
	}
	if !hasSlug("security") || !hasSlug("hugo") {
		t.Errorf("expected security and hugo in shared slugs, got %v", slugs)
	}
}

func TestMerge(t *testing.T) {
	a := taxonomy.Normalize([]string{"go", "security"})
	b := taxonomy.Normalize([]string{"security", "python"})
	merged := taxonomy.Merge(a, b)
	if len(merged) != 3 {
		t.Fatalf("want 3 terms after merge, got %d: %#v", len(merged), merged)
	}
}

func TestDeduplicateRaw(t *testing.T) {
	got := taxonomy.DeduplicateRaw([]string{"Security", "security", "Go", "go", "", "  "})
	// Two unique slugs: security, go → 2 results
	if len(got) != 2 {
		t.Fatalf("want 2 unique terms, got %d: %v", len(got), got)
	}
}

func TestSlugs(t *testing.T) {
	terms := taxonomy.Normalize([]string{"Hugo", "go", "security"})
	slugs := taxonomy.Slugs(terms)
	if len(slugs) != 3 {
		t.Fatalf("want 3 slugs, got %d", len(slugs))
	}
	if slugs[0] != "hugo" || slugs[1] != "go" || slugs[2] != "security" {
		t.Errorf("slugs = %v, unexpected order or values", slugs)
	}
}
