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

func TestNormalizeAliasMap(t *testing.T) {
	aliases := taxonomy.NormalizeAliasMap(map[string]string{
		"Sécurité":   "Security",
		"postmortem": "post-mortems",
		"loop":       "loop", // key == value: must be dropped
	})
	if aliases["sécurité"] != "security" {
		t.Errorf("NormalizeAliasMap: key Sécurité→sécurité not normalized, got %v", aliases)
	}
	if aliases["postmortem"] != "post-mortems" {
		t.Errorf("NormalizeAliasMap: postmortem entry missing, got %v", aliases)
	}
	if _, ok := aliases["loop"]; ok {
		t.Error("NormalizeAliasMap: self-alias loop:loop should be dropped")
	}
}

func TestApplyAliases(t *testing.T) {
	aliases := map[string]string{"sécurité": "security", "postmortem": "post-mortems"}
	got := taxonomy.ApplyAliases([]string{"sécurité", "docker", "sécurité", "Sécurité"}, aliases)
	// sécurité → security (merged); docker unchanged; Sécurité slug = sécurité → also security, deduplicated
	if len(got) != 2 {
		t.Fatalf("ApplyAliases: got %v, want [security docker]", got)
	}
	if got[0] != "security" || got[1] != "docker" {
		t.Errorf("ApplyAliases: got %v, want [security docker]", got)
	}

	// Empty aliases: return input unchanged
	plain := taxonomy.ApplyAliases([]string{"a", "b"}, nil)
	if len(plain) != 2 {
		t.Errorf("ApplyAliases(nil aliases): got %v", plain)
	}
}

func TestMatchesSlugWithAliases(t *testing.T) {
	aliases := map[string]string{"sécurité": "security"}
	// Direct slug match
	if !taxonomy.MatchesSlugWithAliases([]string{"security"}, "security", aliases) {
		t.Error("MatchesSlugWithAliases: direct match failed")
	}
	// Alias match: page has sécurité, filter by canonical security → should match
	if !taxonomy.MatchesSlugWithAliases([]string{"sécurité"}, "security", aliases) {
		t.Error("MatchesSlugWithAliases: alias match failed for sécurité→security")
	}
	// No match
	if taxonomy.MatchesSlugWithAliases([]string{"docker"}, "security", aliases) {
		t.Error("MatchesSlugWithAliases: should not match docker for security")
	}
}

func TestResolveAlias(t *testing.T) {
	aliases := map[string]string{
		"sécurité":   "security",
		"postmortem": "post-mortems",
	}
	if got := taxonomy.ResolveAlias("sécurité", aliases); got != "security" {
		t.Errorf("ResolveAlias: got %q, want %q", got, "security")
	}
	if got := taxonomy.ResolveAlias("security", aliases); got != "security" {
		t.Errorf("ResolveAlias for non-alias: got %q, want %q", got, "security")
	}
	if got := taxonomy.ResolveAlias("other", nil); got != "other" {
		t.Errorf("ResolveAlias nil map: got %q, want %q", got, "other")
	}
}

func TestFindSimilarPairs(t *testing.T) {
	slugs := []string{"security", "sécurité", "postmortem", "post-mortems", "go", "docker"}
	aliases := map[string]string{"sécurité": "security"}

	// security/sécurité: levenshtein distance=2, but sécurité is aliased to security → excluded
	// postmortem/post-mortems: distance=2, not aliased → should appear
	pairs := taxonomy.FindSimilarPairs(slugs, 2, 5, aliases)
	found := false
	for _, p := range pairs {
		if (p[0] == "post-mortems" && p[1] == "postmortem") || (p[0] == "postmortem" && p[1] == "post-mortems") {
			found = true
		}
		// sécurité/security pair must NOT appear (aliased)
		if (p[0] == "sécurité" && p[1] == "security") || (p[0] == "security" && p[1] == "sécurité") {
			t.Errorf("aliased pair sécurité/security should not appear in similar pairs")
		}
	}
	if !found {
		t.Errorf("expected postmortem/post-mortems similar pair, pairs=%v", pairs)
	}
}
