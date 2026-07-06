package releasecheck

import (
	"fmt"
	"strings"
)

const (
	latestReleaseBadge = "https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go"
	latestReleaseLink  = "https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest"
)

// CheckReadmeReleasePolicy verifies the README keeps release metadata dynamic
// instead of pinning a historical tag in the badge/link.
func CheckReadmeReleasePolicy(readme string) error {
	if !strings.Contains(readme, latestReleaseBadge) {
		return fmt.Errorf("README.md is missing the dynamic Latest Release badge")
	}
	if !strings.Contains(readme, latestReleaseLink) {
		return fmt.Errorf("README.md must link Latest Release to %s", latestReleaseLink)
	}
	return nil
}
