package server

import (
	"sort"
	"testing"
)

// TestToolExposureTierCoversEveryRegisteredTool is the completeness
// invariant #1137's design relies on: toolExposureTier is a hand-curated
// table for the reader/editorial/admin tiers with "advanced" as an
// implicit catch-all in toolsToHideForExposureProfile. That catch-all
// means a newly added tool with no explicit entry silently becomes
// "advanced" rather than failing loudly — acceptable as a safe default,
// but only if every CURRENTLY known tool that isn't meant to be
// "advanced" actually has an entry naming its real tier. This test
// catches drift the other direction too: an entry for a tool that no
// longer exists in the registry (renamed/removed) would silently stop
// mattering rather than erroring — flag that as well so the table stays
// honest.
func TestToolExposureTierCoversEveryRegisteredTool(t *testing.T) {
	reg := buildRegistry()
	registered := make(map[string]bool, len(reg.All()))
	for _, d := range reg.All() {
		registered[d.Name] = true
	}

	var staleEntries []string
	for name := range toolExposureTier {
		if !registered[name] {
			staleEntries = append(staleEntries, name)
		}
	}
	sort.Strings(staleEntries)
	if len(staleEntries) > 0 {
		t.Fatalf("toolExposureTier has entries for tools no longer in the registry: %v", staleEntries)
	}

	// Every registered tool must resolve to exactly one of the four known
	// tiers (curated entry, or the "advanced" default) — never an unknown
	// value, which would break exposureProfileRank silently.
	for _, d := range reg.All() {
		tier, ok := toolExposureTier[d.Name]
		if !ok {
			tier = ExposureProfileAdvanced
		}
		if exposureProfileRank(tier) < 0 {
			t.Fatalf("tool %q resolved to unknown exposure tier %q", d.Name, tier)
		}
	}
}

func TestExposureProfileRankOrdering(t *testing.T) {
	if !(exposureProfileRank(ExposureProfileReader) < exposureProfileRank(ExposureProfileEditorial) &&
		exposureProfileRank(ExposureProfileEditorial) < exposureProfileRank(ExposureProfileAdvanced) &&
		exposureProfileRank(ExposureProfileAdvanced) < exposureProfileRank(ExposureProfileAdmin)) {
		t.Fatalf("exposure profile ranks are not strictly increasing reader<editorial<advanced<admin")
	}
	if exposureProfileRank("bogus") >= 0 {
		t.Fatalf("exposureProfileRank(bogus) = %d, want negative", exposureProfileRank("bogus"))
	}
}

func TestIsKnownExposureProfileRejectsUnknownValues(t *testing.T) {
	for _, p := range []string{ExposureProfileReader, ExposureProfileEditorial, ExposureProfileAdvanced, ExposureProfileAdmin} {
		if !isKnownExposureProfile(p) {
			t.Fatalf("isKnownExposureProfile(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "Reader", "READER", "reader ", "superadmin", "bogus"} {
		if isKnownExposureProfile(p) {
			t.Fatalf("isKnownExposureProfile(%q) = true, want false", p)
		}
	}
}

func TestToolsToHideForExposureProfileReaderHidesEverythingElse(t *testing.T) {
	all := []string{"get_page", "create_page", "bootstrap_hugo", "get_backlinks"}
	hidden := toolsToHideForExposureProfile(all, ExposureProfileReader)
	hiddenSet := map[string]bool{}
	for _, n := range hidden {
		hiddenSet[n] = true
	}
	if hiddenSet["get_page"] {
		t.Fatalf("get_page (reader tier) must not be hidden under the reader profile")
	}
	for _, want := range []string{"create_page", "bootstrap_hugo", "get_backlinks"} {
		if !hiddenSet[want] {
			t.Fatalf("%q must be hidden under the reader profile, hidden=%v", want, hidden)
		}
	}
}

func TestToolsToHideForExposureProfileAdminHidesNothing(t *testing.T) {
	all := []string{"get_page", "create_page", "bootstrap_hugo", "get_backlinks"}
	hidden := toolsToHideForExposureProfile(all, ExposureProfileAdmin)
	if len(hidden) != 0 {
		t.Fatalf("admin profile must hide nothing, got %v", hidden)
	}
}

func TestToolsToHideForExposureProfileUnknownToolDefaultsToAdvanced(t *testing.T) {
	all := []string{"totally_new_tool_not_in_the_table"}
	// Hidden under reader (advanced > reader rank)...
	if hidden := toolsToHideForExposureProfile(all, ExposureProfileReader); len(hidden) != 1 {
		t.Fatalf("unknown tool should default to advanced tier and be hidden under reader, got %v", hidden)
	}
	// ...but visible under advanced itself.
	if hidden := toolsToHideForExposureProfile(all, ExposureProfileAdvanced); len(hidden) != 0 {
		t.Fatalf("unknown tool defaulting to advanced tier must be visible under the advanced profile, got hidden=%v", hidden)
	}
}

// TestGetCapabilitiesStaysAboveEditorialTier guards the invariant
// documented in toolExposureTier's own doc comment: get_capabilities'
// masked_tools field is scope-only, not profile-aware, and that gap is
// only safe because get_capabilities itself is unreachable at
// reader/editorial profiles. If someone moves get_capabilities to a lower
// tier without adding profile-awareness to maskedCapabilityTools, this
// test fails loudly instead of letting the field silently under-report.
func TestGetCapabilitiesStaysAboveEditorialTier(t *testing.T) {
	tier, ok := toolExposureTier["get_capabilities"]
	if !ok {
		t.Fatal("get_capabilities has no explicit tier entry")
	}
	if exposureProfileRank(tier) <= exposureProfileRank(ExposureProfileEditorial) {
		t.Fatalf("get_capabilities tier = %q, want advanced or admin (masked_tools is scope-only, not profile-aware — see toolExposureTier's doc comment)", tier)
	}
}

func TestScopeNameForRank(t *testing.T) {
	cases := []struct {
		rank int
		want string
	}{
		{0, ""},
		{1, "write"},
		{2, "admin"},
		{3, "admin"},
	}
	for _, tc := range cases {
		if got := scopeNameForRank(tc.rank); got != tc.want {
			t.Fatalf("scopeNameForRank(%d) = %q, want %q", tc.rank, got, tc.want)
		}
	}
}
