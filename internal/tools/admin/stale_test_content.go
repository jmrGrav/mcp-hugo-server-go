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

type StaleTestContentEntry struct {
	Slug           string `json:"slug"`
	Owner          string `json:"owner,omitempty"`
	ExpiresAt      string `json:"expires_at"`
	OverdueSeconds int64  `json:"overdue_seconds"`
	Reason         string `json:"reason"`
}

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
	stale := CollectStaleTestContent(srcIdx, thresholdHours, time.Now())
	if len(stale) == 0 {
		return nil
	}
	slugs := make([]string, 0, len(stale))
	for _, entry := range stale {
		slugs = append(slugs, entry.Slug)
	}
	return fmt.Errorf("%d stale test/audit content slug(s) past their expiry or the %dh threshold: %s — confirm these are safe to remove with delete_page", len(slugs), thresholdHours, strings.Join(slugs, ", "))
}

// CollectStaleTestContent returns structured, machine-readable stale
// test-content entries for operator-facing tools such as get_runtime_status.
// It is read-only and never mutates the site. now is injected for stable
// tests.
func CollectStaleTestContent(srcIdx *hugosite.SourceIndex, thresholdHours int, now time.Time) []StaleTestContentEntry {
	if srcIdx == nil {
		return nil
	}
	seen := make(map[string]bool)
	var stale []StaleTestContentEntry

	for _, p := range srcIdx.ListPages(0, 0) {
		if seen[p.Slug] {
			continue
		}
		if expiresAt, ok := testContentExpiry(p.FrontmatterRaw); ok {
			if now.After(expiresAt) {
				seen[p.Slug] = true
				stale = append(stale, StaleTestContentEntry{
					Slug:           canonicalSourceSlug(p.Slug),
					Owner:          testContentOwner(p.FrontmatterRaw),
					ExpiresAt:      expiresAt.UTC().Format(time.RFC3339),
					OverdueSeconds: int64(now.Sub(expiresAt).Seconds()),
					Reason:         "test_content_expired",
				})
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
		effectiveExpiry := info.ModTime().Add(time.Duration(thresholdHours) * time.Hour)
		if !now.Before(effectiveExpiry) {
			seen[p.Slug] = true
			stale = append(stale, StaleTestContentEntry{
				Slug:           canonicalSourceSlug(p.Slug),
				ExpiresAt:      effectiveExpiry.UTC().Format(time.RFC3339),
				OverdueSeconds: int64(now.Sub(effectiveExpiry).Seconds()),
				Reason:         "reserved_test_slug_threshold",
			})
		}
	}
	sort.SliceStable(stale, func(i, j int) bool { return stale[i].Slug < stale[j].Slug })
	return stale
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

func testContentOwner(frontmatterRaw map[string]any) string {
	if frontmatterRaw == nil {
		return ""
	}
	if raw, ok := frontmatterRaw["test_content_owner"]; ok {
		if owner, ok := raw.(string); ok {
			return strings.TrimSpace(owner)
		}
	}
	return ""
}

func canonicalSourceSlug(slug string) string {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return "/"
	}
	return "/" + slug + "/"
}
