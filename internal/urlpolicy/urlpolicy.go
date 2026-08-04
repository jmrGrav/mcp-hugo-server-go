// Package urlpolicy is the single shared validator for URL-like frontmatter
// values written by this server (issue #855). Before it existed, each
// URL-like field grew its own bespoke shape/scheme/charset checks, and the
// #835 stored-XSS defect (a value like `/img.jpg" onerror="alert(1)` passing
// every existing shape check because nothing bounded the character set) is
// exactly the class of bug that a per-field, copy-pasted validator lets slip
// through the next time a new field is added. Consolidating the policy here
// means a field opts into a policy mode rather than reimplementing parsing.
//
// # Policy modes
//
//   - LocalOnly: the value must be a site-root-relative path — a single
//     leading "/", no protocol-relative "//", no backslashes, no dot ("." /
//     "..") path segments, already path.Clean-normalized, and drawn only from
//     the allowlisted charset [A-Za-z0-9._~/-]. No scheme (http/https/data/
//     javascript/…) is ever acceptable. This reproduces validateFeaturedImagePath's
//     post-#835 strictness exactly.
//   - ExternalAllowed: the value may EITHER be a LocalOnly path (above) OR an
//     absolute http:// / https:// URL. Every other scheme — javascript:,
//     data:, vbscript:, file:, mailto:, and protocol-relative "//" — is
//     forbidden, and no embedded control characters are permitted. This mode
//     exists for fields that legitimately need an absolute URL (a canonical
//     override, an external link, or a future preview/CDN image URL).
//
// # Which fields use which mode (AC5 documentation)
//
// LocalOnly (site-relative image/redirect paths — must never become an open
// redirect or an absolute-URL injection vector):
//
//   - featured_image  — the only URL-like field currently accepted as write
//     input (update_page). Migrated onto this package by #855.
//   - aliases / redirects, social image paths — LocalOnly is the correct mode
//     for these Hugo constructs *when* they become writable; they are not
//     accepted as write input today, so nothing wires them yet. They are
//     documented here so the mode is chosen, not reinvented, when they are.
//
// ExternalAllowed (may hold an absolute URL):
//
//   - canonical overrides, external links, future preview/image URL fields.
//     Hugo's `canonical`/`url` overrides are frequently absolute URLs, so
//     forbidding external URLs outright would be wrong for them. None are
//     accepted as write input today; when one is, it opts into this mode
//     rather than growing a fresh parser. When genuinely ambiguous whether a
//     new field needs external URLs, default to LocalOnly (the stricter mode).
package urlpolicy

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Policy selects how a URL-like value is validated. The zero value is
// LocalOnly, the stricter mode, so a field that forgets to choose fails safe.
type Policy int

const (
	// LocalOnly permits only site-root-relative paths (see package doc).
	LocalOnly Policy = iota
	// ExternalAllowed permits a LocalOnly path OR an absolute http/https URL.
	ExternalAllowed
)

// localCharsetPattern allowlists the character set for a site-root-relative
// path. The shape/scheme/traversal checks reject specific bad shapes but do
// not bound the character set itself; without this a value like
// `/img.jpg" onerror="alert(1)` or `/images/<script>.jpg` is a normalized,
// local, non-traversal path with no disallowed scheme prefix and would be
// written verbatim into frontmatter, which the theme renders into HTML
// attributes / CSS url() without re-validating — the #835 stored-injection
// class.
var localCharsetPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`)

// forbiddenLocalSchemePrefixes are scheme-looking prefixes explicitly named
// in a LocalOnly error when they appear after a leading "/". In practice the
// leading-"/" gate already rejects every bare `scheme:` value first, so this
// list only fires for a pathological `/data:...`-style input; it is retained
// to preserve validateFeaturedImagePath's pre-#855 message set byte-for-byte.
var forbiddenLocalSchemePrefixes = []string{"data:", "javascript:", "file:", "http://", "https://"}

// Validate checks value against policy. It returns a nil error on success and
// a lowercase, field-agnostic message on failure (e.g. "must be a
// site-root-relative path starting with /") so callers can wrap it with their
// own field name — e.g. fmt.Errorf("invalid_params: featured_image %w", err) —
// and keep messages consistent across fields. An empty value is accepted
// (callers use "" to clear the field); reject empties at the call site if a
// field is required.
func Validate(value string, policy Policy) error {
	if err := rejectControlChars(value); err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	if policy == ExternalAllowed {
		if isAbsoluteHTTPURL(value) {
			return validateAbsoluteHTTPURL(value)
		}
		// Not an http(s) URL: it must be a valid local path, or a forbidden
		// scheme. Fall through to the local check, whose leading-"/" gate
		// rejects javascript:/data:/vbscript:/etc. with a clear message.
	}
	return validateLocalPath(value)
}

// validateLocalPath enforces the LocalOnly policy. The check order is load-
// bearing and must not be reordered: the leading-"/" gate runs before the
// scheme-prefix loop, so a bare `javascript:alert(1)` (no slash) reports the
// leading-slash error rather than a scheme error — the exact observable
// behavior the #835 characterization test pins.
func validateLocalPath(v string) error {
	if !strings.HasPrefix(v, "/") {
		return fmt.Errorf("must be a site-root-relative path starting with /")
	}
	if strings.HasPrefix(v, "//") {
		return fmt.Errorf("must not be protocol-relative")
	}
	if strings.Contains(v, `\`) {
		return fmt.Errorf("must not contain backslashes")
	}
	lower := strings.ToLower(v)
	for _, prefix := range forbiddenLocalSchemePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return fmt.Errorf("must be a local site path, not %q", prefix)
		}
	}
	for _, seg := range strings.Split(strings.TrimPrefix(v, "/"), "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("must not contain dot path segments")
		}
	}
	if cleaned := path.Clean(v); cleaned != v {
		return fmt.Errorf("must be a normalized local path")
	}
	if !localCharsetPattern.MatchString(v) {
		return fmt.Errorf("must contain only letters, digits, and ._~/- characters")
	}
	return nil
}

// isAbsoluteHTTPURL reports whether v begins with an http:// or https://
// scheme (case-insensitively). Anything else — including protocol-relative
// "//host" and other schemes — is not treated as an external URL and is
// routed to the local-path check, which rejects it.
func isAbsoluteHTTPURL(v string) bool {
	lower := strings.ToLower(v)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// externalUnsafeChars are HTML/attribute-breakout metacharacters and raw
// whitespace that must never appear in an ExternalAllowed URL. LocalOnly's
// charset allowlist ([A-Za-z0-9._~/-]) already blocks these, but the http(s)
// branch has no such allowlist, so a value like `https://h/a" onerror="b` or
// `https://h/"><script>…` — a well-formed http(s) URL that the scheme check
// accepts — would otherwise be written verbatim into frontmatter the theme
// renders into an HTML attribute / CSS url() without re-escaping. That is the
// exact #835 stored-injection class, so it must be closed in this branch too.
// net/url.Parse is intentionally NOT relied on here: it happily accepts a raw
// double-quote in the path, so it would not catch the breakout. The \n \r \t
// that rejectControlChars deliberately exempts are also rejected here — they
// have no legitimate place in a URL and enable attribute/CSS breakout.
const externalUnsafeChars = "\"'<>` \t\n\r"

// validateAbsoluteHTTPURL enforces the ExternalAllowed policy for a value
// already known (by isAbsoluteHTTPURL) to carry an http(s) scheme. It rejects
// backslashes, HTML-unsafe metacharacters/whitespace (see externalUnsafeChars),
// and requires a non-empty host after the scheme; the scheme itself is the
// allowlist, so no javascript:/data:/vbscript:/file: value can reach here
// (they are not http(s) and go to the local check instead). Control characters
// (other than \n\r\t) were already rejected by Validate before this point.
func validateAbsoluteHTTPURL(v string) error {
	if strings.Contains(v, `\`) {
		return fmt.Errorf("must not contain backslashes")
	}
	if strings.ContainsAny(v, externalUnsafeChars) {
		return fmt.Errorf("must not contain spaces or HTML-unsafe characters")
	}
	rest := v[strings.Index(v, "//")+2:]
	host := rest
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		host = rest[:i]
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("must be an absolute http(s) URL with a host")
	}
	return nil
}

// rejectControlChars rejects null bytes and C0/C1 control characters other
// than \n, \r, \t. It mirrors the write package's rejectUnsafeText so that a
// field migrated onto this package (e.g. featured_image) returns the identical
// message it did before, and so ExternalAllowed's "no embedded control
// characters" requirement is enforced in one place. A raw control code has no
// legitimate place in a URL or path and can corrupt downstream YAML/HTML
// parsing in hard-to-diagnose ways.
func rejectControlChars(s string) error {
	for _, r := range s {
		if r == 0 {
			return fmt.Errorf("must not contain null bytes")
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return fmt.Errorf("must not contain control characters (found U+%04X)", r)
		}
		if r >= 0x7F && r <= 0x9F {
			return fmt.Errorf("must not contain C1 control characters (found U+%04X)", r)
		}
	}
	return nil
}
