package admin

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

// CheckStaleTestContent scans srcIdx for source pages whose slug matches
// the reserved test-content prefix convention (contentmodel.IsReservedTestSlug,
// #584) and have sat on disk longer than thresholdHours (#608). It never
// deletes or modifies anything — this is strictly an advisory signal,
// surfaced via the returned error (which build_site/publish_changes's
// existing post-build-callback-warning mechanism, #644, already turns into
// a visible data.warning) rather than requiring an operator to think to
// call validate_frontmatter/validate_site themselves.
//
// thresholdHours <= 0 disables the check entirely (returns nil
// unconditionally) — this feature is off by default (#608).
func CheckStaleTestContent(srcIdx *hugosite.SourceIndex, thresholdHours int) error {
	if srcIdx == nil || thresholdHours <= 0 {
		return nil
	}
	threshold := time.Duration(thresholdHours) * time.Hour
	now := time.Now()

	seen := make(map[string]bool)
	var stale []string
	for _, p := range srcIdx.ListPages(0, 0) {
		if !contentmodel.IsReservedTestSlug(p.Slug) || seen[p.Slug] {
			continue
		}
		info, err := os.Stat(p.FilePath)
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= threshold {
			seen[p.Slug] = true
			stale = append(stale, p.Slug)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return fmt.Errorf("%d stale test/audit content slug(s) still present past the %dh threshold: %s — confirm these are safe to remove with delete_page", len(stale), thresholdHours, strings.Join(stale, ", "))
}
