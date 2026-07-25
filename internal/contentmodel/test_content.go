package contentmodel

import "strings"

// ReservedTestSlugPrefixes are slug-segment prefixes observed in this
// project's own audit history (content/.mcp-audit.log) for throwaway
// content created during live testing (#584) — narrow and specific on
// purpose. Deliberately excludes bare "test-"/"audit-": this site publishes
// real articles about security audits (e.g. "audit-securite-..."), and a
// generic prefix would misclassify legitimate content as leftover test
// cruft. Detection based on this list is advisory only — it never flags a
// page as frontmatter-invalid, and (per #608) never deletes anything on its
// own; it only ever surfaces as a signal for a human or agent to act on.
var ReservedTestSlugPrefixes = []string{"mcp-audit-", "test-audit-", "codex-"}

// IsReservedTestSlug reports whether slug's final path segment matches one
// of ReservedTestSlugPrefixes, case-insensitively. Shared between
// validate_frontmatter/validate_site's test_content_slugs advisory (#584)
// and the post-build stale-test-content check (#608), so both sides of
// "detect" and "periodically remind an operator" agree on exactly the same
// definition of "test content."
func IsReservedTestSlug(slug string) bool {
	last := slug
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		last = slug[i+1:]
	}
	last = strings.ToLower(last)
	for _, prefix := range ReservedTestSlugPrefixes {
		if strings.HasPrefix(last, prefix) {
			return true
		}
	}
	return false
}
