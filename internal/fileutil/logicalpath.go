package fileutil

import (
	"path/filepath"
	"strings"
)

// LogicalContentPath projects an absolute or repository-local source file path
// into the agent-facing logical content path under content/.
//
// Examples:
//
//	/srv/site/content/posts/hello/index.fr.md -> content/posts/hello/index.fr.md
//	posts/hello/index.fr.md                   -> posts/hello/index.fr.md
func LogicalContentPath(contentRoot, sourcePath string) string {
	contentRoot = strings.TrimSpace(contentRoot)
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return ""
	}
	if contentRoot != "" {
		if rel, err := filepath.Rel(contentRoot, sourcePath); err == nil && rel != "" && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(filepath.Join("content", rel))
		}
	}
	return filepath.ToSlash(sourcePath)
}
