package releasecheck

import (
	"fmt"
	"regexp"
	"strings"
)

// CheckChangelogVersion verifies that changelog contains a top-level release
// entry for version. Versions may be provided with or without the leading "v".
func CheckChangelogVersion(changelog, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("version is required")
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	pattern := `(?m)^## \[` + regexp.QuoteMeta(version) + `\](?:\s+-\s+.+)?$`
	if regexp.MustCompile(pattern).FindStringIndex(changelog) == nil {
		return fmt.Errorf("CHANGELOG.md is missing release heading for %s", version)
	}
	return nil
}
