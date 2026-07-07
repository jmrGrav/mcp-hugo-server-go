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
