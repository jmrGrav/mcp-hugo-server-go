package contentmodel

import (
	"path/filepath"
	"strings"
)

// SourceKeyFromLogicalPath derives the canonical source-oriented page key
// from a logical Hugo content path such as:
//   - content/posts/hello.md              -> posts/hello
//   - content/posts/hello/index.fr.md     -> posts/hello
//   - content/posts/_index.en.md          -> posts
//   - content/_index.md                   -> ""
func SourceKeyFromLogicalPath(logicalPath string) string {
	p := filepath.ToSlash(strings.TrimSpace(logicalPath))
	p = strings.TrimPrefix(p, "content/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ""
	}
	base := filepath.Base(p)
	dir := filepath.Dir(p)
	switch {
	case base == "index.md":
		if dir == "." {
			return ""
		}
		return dir
	case strings.HasPrefix(base, "index.") && strings.HasSuffix(base, ".md"):
		if dir == "." {
			return ""
		}
		return dir
	case base == "_index.md":
		if dir == "." {
			return ""
		}
		return dir
	case strings.HasPrefix(base, "_index.") && strings.HasSuffix(base, ".md"):
		if dir == "." {
			return ""
		}
		return dir
	case strings.HasSuffix(base, ".md"):
		p = strings.TrimSuffix(p, ".md")
		return strings.TrimPrefix(p, "./")
	default:
		return strings.TrimPrefix(p, "./")
	}
}
