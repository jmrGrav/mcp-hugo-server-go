package releasecheck

import "testing"

func TestChangelogContainsVersionHeading(t *testing.T) {
	changelog := "# Changelog\n\n## [v1.2.10] - 2026-07-05\n\n- fixed\n"
	if err := CheckChangelogVersion(changelog, "v1.2.10"); err != nil {
		t.Fatalf("CheckChangelogVersion() error = %v", err)
	}
}

func TestChangelogRejectsMissingVersionHeading(t *testing.T) {
	changelog := "# Changelog\n\n## [v1.2.9] - 2026-07-05\n\n- fixed\n"
	if err := CheckChangelogVersion(changelog, "v1.2.10"); err == nil {
		t.Fatal("CheckChangelogVersion() error = nil, want missing version error")
	}
}

func TestNormalizeVersionAcceptsOptionalLeadingV(t *testing.T) {
	changelog := "# Changelog\n\n## [v1.2.10] - 2026-07-05\n\n- fixed\n"
	if err := CheckChangelogVersion(changelog, "1.2.10"); err != nil {
		t.Fatalf("CheckChangelogVersion() error = %v", err)
	}
}

func TestExtractReleaseNotesReturnsOnlyRequestedSection(t *testing.T) {
	changelog := "# Changelog\n\n## [Unreleased]\n\n- wip\n\n## [v1.2.10] - 2026-07-05\n\n### Fixed\n- shipped\n\n## [v1.2.9] - 2026-07-04\n\n- previous\n"
	got, err := ExtractChangelogReleaseNotes(changelog, "v1.2.10")
	if err != nil {
		t.Fatalf("ExtractChangelogReleaseNotes() error = %v", err)
	}
	want := "## [v1.2.10] - 2026-07-05\n\n### Fixed\n- shipped"
	if got != want {
		t.Fatalf("ExtractChangelogReleaseNotes() = %q, want %q", got, want)
	}
}

func TestExtractReleaseNotesRejectsMissingVersion(t *testing.T) {
	changelog := "# Changelog\n\n## [v1.2.9] - 2026-07-04\n\n- previous\n"
	if _, err := ExtractChangelogReleaseNotes(changelog, "v1.2.10"); err == nil {
		t.Fatal("ExtractChangelogReleaseNotes() error = nil, want missing version error")
	}
}

const sampleMultiReleaseChangelog = "# Changelog\n\n## [Unreleased]\n\n- wip\n\n## [v1.2.10] - 2026-07-05\n\n### Fixed\n- shipped\n\n## [v1.2.9] - 2026-07-04\n\n- previous\n\n## [v1.2.8] - 2026-07-01\n\n- older\n"

// TestListReleaseEntriesReturnsMostRecentFirst is the regression test for
// #612: entries must come back in CHANGELOG.md's own chronological order
// (most recent first), excluding the [Unreleased] heading.
func TestListReleaseEntriesReturnsMostRecentFirst(t *testing.T) {
	entries := ListReleaseEntries(sampleMultiReleaseChangelog, 0)
	if len(entries) != 3 {
		t.Fatalf("ListReleaseEntries() = %d entries, want 3 (Unreleased excluded): %#v", len(entries), entries)
	}
	wantVersions := []string{"v1.2.10", "v1.2.9", "v1.2.8"}
	for i, want := range wantVersions {
		if entries[i].Version != want {
			t.Errorf("entries[%d].Version = %q, want %q", i, entries[i].Version, want)
		}
	}
	if entries[0].Date != "2026-07-05" {
		t.Errorf("entries[0].Date = %q, want 2026-07-05", entries[0].Date)
	}
	if entries[0].Body != "### Fixed\n- shipped" {
		t.Errorf("entries[0].Body = %q, want the Fixed section body only", entries[0].Body)
	}
}

// TestListReleaseEntriesRespectsLimit confirms limit caps the number of
// entries returned, keeping the default response bounded rather than
// dumping the entire file (#612's own explicit token-cost concern).
func TestListReleaseEntriesRespectsLimit(t *testing.T) {
	entries := ListReleaseEntries(sampleMultiReleaseChangelog, 2)
	if len(entries) != 2 {
		t.Fatalf("ListReleaseEntries(limit=2) = %d entries, want 2", len(entries))
	}
	if entries[0].Version != "v1.2.10" || entries[1].Version != "v1.2.9" {
		t.Errorf("ListReleaseEntries(limit=2) = %#v, want the 2 most recent", entries)
	}
}

// TestListReleaseEntriesSinceExcludesRequestedVersionAndOlder confirms the
// cutoff is exclusive: since_version's own entry, and everything older, is
// omitted.
func TestListReleaseEntriesSinceExcludesRequestedVersionAndOlder(t *testing.T) {
	entries, err := ListReleaseEntriesSince(sampleMultiReleaseChangelog, "v1.2.9", 0)
	if err != nil {
		t.Fatalf("ListReleaseEntriesSince() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Version != "v1.2.10" {
		t.Fatalf("ListReleaseEntriesSince(v1.2.9) = %#v, want only v1.2.10", entries)
	}
}

// TestListReleaseEntriesSinceMostRecentReturnsEmptyNotError confirms
// asking "what's new since the most recent version" is a valid, empty
// result — not an error — distinguishing it from an unknown since_version.
func TestListReleaseEntriesSinceMostRecentReturnsEmptyNotError(t *testing.T) {
	entries, err := ListReleaseEntriesSince(sampleMultiReleaseChangelog, "v1.2.10", 0)
	if err != nil {
		t.Fatalf("ListReleaseEntriesSince(most recent) error = %v, want nil", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListReleaseEntriesSince(most recent) = %#v, want empty", entries)
	}
}

// TestListReleaseEntriesSinceUnknownVersionReturnsError confirms a
// since_version that never appears as a heading is rejected as a caller
// error, not silently treated as "everything."
func TestListReleaseEntriesSinceUnknownVersionReturnsError(t *testing.T) {
	if _, err := ListReleaseEntriesSince(sampleMultiReleaseChangelog, "v9.9.9", 0); err == nil {
		t.Fatal("ListReleaseEntriesSince(unknown version) error = nil, want an error")
	}
}
