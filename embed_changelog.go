// Package mcphugoserver exists solely to embed CHANGELOG.md at build time
// (#612), so the deployed binary can serve it via get_changelog without any
// runtime dependency on CHANGELOG.md being present on disk — production
// deployments ship only the compiled binary (see deploy.yml), not a working
// tree. Go's //go:embed cannot reach outside its own package directory, so
// this file has to live at the module root, alongside CHANGELOG.md itself,
// rather than in an internal/ package.
package mcphugoserver

import _ "embed"

// ChangelogMarkdown is the exact contents of CHANGELOG.md as of the commit
// this binary was built from — captured at build time, so it always
// matches the running binary exactly (zero drift), which is the whole
// point of a version-diff tool.
//
//go:embed CHANGELOG.md
var ChangelogMarkdown string
