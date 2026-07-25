package releasecheck

import (
	"fmt"
	"regexp"
	"strings"
)

// CheckChangelogVersion verifies that changelog contains a top-level release
// entry for version. Versions may be provided with or without the leading "v".
func CheckChangelogVersion(changelog, version string) error {
	version = normalizeVersion(version)
	if version == "" {
		return fmt.Errorf("version is required")
	}
	pattern := `(?m)^## \[` + regexp.QuoteMeta(version) + `\](?:\s+-\s+.+)?$`
	if regexp.MustCompile(pattern).FindStringIndex(changelog) == nil {
		return fmt.Errorf("CHANGELOG.md is missing release heading for %s", version)
	}
	return nil
}

// ExtractChangelogReleaseNotes returns the exact release section for version,
// including its heading and body, without adjacent versions.
func ExtractChangelogReleaseNotes(changelog, version string) (string, error) {
	version = normalizeVersion(version)
	if version == "" {
		return "", fmt.Errorf("version is required")
	}
	headingPattern := `(?m)^## \[` + regexp.QuoteMeta(version) + `\](?:\s+-\s+.+)?$`
	start := regexp.MustCompile(headingPattern).FindStringIndex(changelog)
	if start == nil {
		return "", fmt.Errorf("CHANGELOG.md is missing release heading for %s", version)
	}
	rest := changelog[start[1]:]
	nextRel := regexp.MustCompile(`(?m)^## \[`).FindStringIndex(rest)
	end := len(changelog)
	if nextRel != nil {
		end = start[1] + nextRel[0]
	}
	return strings.TrimSpace(changelog[start[0]:end]), nil
}

// ReleaseEntry is one `## [vX.Y.Z] - date` section of CHANGELOG.md: the
// version, its release date (empty if the heading omits one — matches the
// optional `(?:\s+-\s+.+)?` suffix CheckChangelogVersion/
// ExtractChangelogReleaseNotes already tolerate), and the section body
// (everything after the heading up to, but not including, the next
// heading), trimmed.
type ReleaseEntry struct {
	Version string
	Date    string
	Body    string
}

// releaseHeadingPattern matches any `## [vX.Y.Z]` or `## [vX.Y.Z] - date`
// heading, capturing the version and (optionally) the date — deliberately
// excludes `## [Unreleased]`, which never matches the vX.Y.Z shape.
var releaseHeadingPattern = regexp.MustCompile(`(?m)^## \[(v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)\](?:\s+-\s+(.+))?$`)

// ListReleaseEntries returns every versioned release section in changelog,
// most-recent-first (CHANGELOG.md's own chronological order, #612) — used
// by get_changelog to serve version-diff history without any runtime
// dependency on git or a working tree. limit <= 0 returns every entry;
// otherwise only the first (most recent) limit entries are returned,
// keeping the default response bounded rather than dumping the entire
// file.
func ListReleaseEntries(changelog string, limit int) []ReleaseEntry {
	matches := releaseHeadingPattern.FindAllStringSubmatchIndex(changelog, -1)
	if len(matches) == 0 {
		return nil
	}
	entries := make([]ReleaseEntry, 0, len(matches))
	for i, m := range matches {
		version := changelog[m[2]:m[3]]
		date := ""
		if m[4] != -1 {
			date = strings.TrimSpace(changelog[m[4]:m[5]])
		}
		bodyStart := m[1]
		bodyEnd := len(changelog)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		entries = append(entries, ReleaseEntry{
			Version: version,
			Date:    date,
			Body:    strings.TrimSpace(changelog[bodyStart:bodyEnd]),
		})
		if limit > 0 && len(entries) >= limit {
			break
		}
	}
	return entries
}

// ListReleaseEntriesSince returns every versioned release section strictly
// newer than sinceVersion (i.e. everything up to, but not including, that
// version's own heading), most-recent-first, capped by limit the same way
// ListReleaseEntries is. Returns an error if sinceVersion has no matching
// heading in changelog. Passing an empty sinceVersion is equivalent to
// calling ListReleaseEntries directly (no cutoff).
func ListReleaseEntriesSince(changelog, sinceVersion string, limit int) ([]ReleaseEntry, error) {
	sinceVersion = normalizeVersion(sinceVersion)
	if sinceVersion == "" {
		return ListReleaseEntries(changelog, limit), nil
	}
	// Validate sinceVersion is a real heading before scanning — distinguishes
	// "since_version is the most recent entry, nothing newer" (a valid,
	// empty result) from "since_version was never a real heading at all"
	// (a caller-input error). Checking membership up front, rather than
	// inferring it from an empty result, also handles the case where
	// sinceVersion never appears at all: the scan below would otherwise
	// silently collect every entry (never finding a match to stop at)
	// instead of erroring.
	if err := CheckChangelogVersion(changelog, sinceVersion); err != nil {
		return nil, err
	}
	all := ListReleaseEntries(changelog, 0)
	var out []ReleaseEntry
	for _, e := range all {
		if e.Version == sinceVersion {
			break
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version
}
