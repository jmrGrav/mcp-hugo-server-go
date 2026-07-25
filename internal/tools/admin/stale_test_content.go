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

// CheckStaleTestContent scans srcIdx for two independent categories of
// stale test/audit content and never deletes or modifies anything — this
// is strictly an advisory signal, surfaced via the returned error (which
// build_site/publish_changes's existing post-build-callback-warning
// mechanism, #644, already turns into a visible data.warning) rather than
// requiring an operator to think to call validate_frontmatter/validate_site
// themselves:
//
//  1. Pages whose slug matches the reserved test-content prefix convention
//     (contentmodel.IsReservedTestSlug, #584) and have sat on disk longer
//     than thresholdHours (#608). thresholdHours <= 0 disables this
//     category entirely — off by default.
//  2. Pages explicitly marked via create_page's opt-in test_content
//     parameter (#661) whose own test_content_expires_at frontmatter has
//     passed. This category is checked unconditionally, independent of
//     thresholdHours — the caller explicitly asked for TTL tracking on
//     that specific page, so it must keep working even when the
//     server-wide sweep is disabled.
func CheckStaleTestContent(srcIdx *hugosite.SourceIndex, thresholdHours int) error {
	if srcIdx == nil {
		return nil
	}
	now := time.Now()
	seen := make(map[string]bool)
	var stale []string

	for _, p := range srcIdx.ListPages(0, 0) {
		if seen[p.Slug] {
			continue
		}
		if expiresAt, ok := testContentExpiry(p.FrontmatterRaw); ok {
			if now.After(expiresAt) {
				seen[p.Slug] = true
				stale = append(stale, p.Slug)
			}
			continue
		}
		if thresholdHours <= 0 || !contentmodel.IsReservedTestSlug(p.Slug) {
			continue
		}
		info, err := os.Stat(p.FilePath)
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= time.Duration(thresholdHours)*time.Hour {
			seen[p.Slug] = true
			stale = append(stale, p.Slug)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return fmt.Errorf("%d stale test/audit content slug(s) past their expiry or the %dh threshold: %s — confirm these are safe to remove with delete_page", len(stale), thresholdHours, strings.Join(stale, ", "))
}

// testContentExpiry extracts test_content_expires_at from a page's raw
// frontmatter (#661), returning ok=false if the page never opted into
// create_page's test_content parameter or the value can't be parsed. A
// freshly create_page'd entry (still only in the in-memory index, not yet
// re-parsed from disk) carries this as a plain Go string; the same value
// read back from an on-disk YAML frontmatter block is auto-parsed by the
// YAML library into a native time.Time (RFC3339-looking scalars are
// recognized as timestamps), so both representations must be accepted.
func testContentExpiry(frontmatterRaw map[string]any) (time.Time, bool) {
	raw, ok := frontmatterRaw["test_content_expires_at"]
	if !ok {
		return time.Time{}, false
	}
	switch v := raw.(type) {
	case time.Time:
		return v, true
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	default:
		return time.Time{}, false
	}
}
