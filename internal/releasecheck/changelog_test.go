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
