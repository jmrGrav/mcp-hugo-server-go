package releasecheck

import "testing"

func TestReadmeAllowsDynamicLatestReleaseBadgeAndLink(t *testing.T) {
	readme := `# repo

[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/latest)
`
	if err := CheckReadmeReleasePolicy(readme); err != nil {
		t.Fatalf("CheckReadmeReleasePolicy() error = %v", err)
	}
}

func TestReadmeRejectsMissingLatestReleaseBadge(t *testing.T) {
	readme := `# repo

[![CI](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml/badge.svg)](https://github.com/jmrGrav/mcp-hugo-server-go/actions/workflows/ci.yml)
`
	if err := CheckReadmeReleasePolicy(readme); err == nil {
		t.Fatal("CheckReadmeReleasePolicy() error = nil, want missing latest release badge error")
	}
}

func TestReadmeRejectsPinnedReleaseLink(t *testing.T) {
	readme := `# repo

[![Latest Release](https://img.shields.io/github/v/release/jmrGrav/mcp-hugo-server-go)](https://github.com/jmrGrav/mcp-hugo-server-go/releases/tag/v1.3.4)
`
	if err := CheckReadmeReleasePolicy(readme); err == nil {
		t.Fatal("CheckReadmeReleasePolicy() error = nil, want pinned release link error")
	}
}
