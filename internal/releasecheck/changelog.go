package releasecheck

import (
	"fmt"
	"regexp"
	"strings"
)

// CheckChangelogVersion verifies that changelog contains a top-level release
// entry for version. Versions may be provided with or without the leading "v".
func CheckChangelogVersion(changelog, version string) error {
	version = normalizeVersion(version)
	if version == "" {
		return fmt.Errorf("version is required")
	}
	pattern := `(?m)^## \[` + regexp.QuoteMeta(version) + `\](?:\s+-\s+.+)?$`
	if regexp.MustCompile(pattern).FindStringIndex(changelog) == nil {
		return fmt.Errorf("CHANGELOG.md is missing release heading for %s", version)
	}
	return nil
}

// ExtractChangelogReleaseNotes returns the exact release section for version,
// including its heading and body, without adjacent versions.
func ExtractChangelogReleaseNotes(changelog, version string) (string, error) {
	version = normalizeVersion(version)
	if version == "" {
		return "", fmt.Errorf("version is required")
	}
	headingPattern := `(?m)^## \[` + regexp.QuoteMeta(version) + `\](?:\s+-\s+.+)?$`
	start := regexp.MustCompile(headingPattern).FindStringIndex(changelog)
	if start == nil {
		return "", fmt.Errorf("CHANGELOG.md is missing release heading for %s", version)
	}
	rest := changelog[start[1]:]
	nextRel := regexp.MustCompile(`(?m)^## \[`).FindStringIndex(rest)
	end := len(changelog)
	if nextRel != nil {
		end = start[1] + nextRel[0]
	}
	return strings.TrimSpace(changelog[start[0]:end]), nil
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version
}
