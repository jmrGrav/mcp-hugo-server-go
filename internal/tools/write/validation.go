package write

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/urlpolicy"
)

// slugPattern is the unified slug format for create_page/update_page,
// per #380. Lowercase alphanumeric segments joined by "/", "_", or "-";
// single-character slugs are allowed. This is checked in addition to (not
// instead of) pg.SafeJoin's path-traversal/hidden-component guard and the
// reservedSlugs check — those remain the security boundary, this is a
// content-convention boundary that rejects slugs no legitimate Hugo section
// would use (spaces, uppercase, punctuation) before they ever reach disk.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9/_-]*[a-z0-9])?$`)

// htmlTagPattern matches actual tag-like markup while still allowing plain
// comparison text such as "3 < 5" in titles.
var htmlTagPattern = regexp.MustCompile(`(?i)<\s*/?\s*[a-z!][^>]*>`)

const (
	// maxTitleRunes and maxBodyBytes bound create_page/update_page input,
	// per #380. Values match the issue's own proposal (title 255 chars,
	// body 1MB) — generous enough that no legitimate blog post/article hits
	// them, tight enough to reject pathological input before it reaches a
	// file write.
	maxTitleRunes = 255
	maxBodyBytes  = 1 << 20
	// maxTaxonomyTermRunes bounds a single tag/category value on
	// create_page/update_page (#886). A taxonomy term is a short label — a
	// word or a few words — that becomes a taxonomy listing/archive page;
	// 100 runes is generous for any legitimate term while rejecting the
	// pathological ~300-character value the audit found silently accepted,
	// which would pollute list_tags/list_categories and the archive pages.
	// Rejected explicitly rather than truncated, per this codebase's
	// "explicit rejection over silent coercion" convention (#833/#836/#837/
	// #838) and matching maxTitleRunes' own rune-count bound.
	maxTaxonomyTermRunes = 100
)

func validateSlugFormat(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("invalid_params: slug must match ^[a-z0-9]([a-z0-9/_-]*[a-z0-9])?$ (lowercase, alphanumeric, /_- separators)")
	}
	return nil
}

func validateTitleFormat(title string) error {
	if err := rejectUnsafeText(title); err != nil {
		return fmt.Errorf("invalid_params: title %w", err)
	}
	if htmlTagPattern.MatchString(title) {
		return fmt.Errorf("invalid_params: title must not contain HTML markup")
	}
	if n := utf8.RuneCountInString(title); n > maxTitleRunes {
		return fmt.Errorf("invalid_params: title exceeds %d characters (got %d)", maxTitleRunes, n)
	}
	return nil
}

// validateTaxonomyTerms rejects any tag/category value exceeding
// maxTaxonomyTermRunes (#886). kind is "tag" or "category" and names the
// offending field in the error. An empty slice (the common "leave taxonomy
// unchanged" shape on update_page) validates trivially.
func validateTaxonomyTerms(kind string, terms []string) error {
	for _, t := range terms {
		if n := utf8.RuneCountInString(t); n > maxTaxonomyTermRunes {
			return fmt.Errorf("invalid_params: %s value exceeds %d characters (got %d)", kind, maxTaxonomyTermRunes, n)
		}
	}
	return nil
}

// validateFeaturedImagePath validates update_page's featured_image parameter.
// featured_image is a site-root-relative image path — the theme renders it
// into HTML attributes / CSS url() without re-validating — so it opts into
// urlpolicy.LocalOnly, the strictest mode: a single leading "/", no
// protocol-relative "//", no backslashes, no dot path segments, path.Clean-
// normalized, and drawn only from the [A-Za-z0-9._~/-] charset (the #835
// stored-XSS fix). An empty value is accepted and clears the frontmatter key
// (#825). Delegating to the shared urlpolicy package (#855) replaces the
// former per-field implementation; the message set is preserved byte-for-byte
// (see TestValidateFeaturedImagePathCharacterization) so this is not a
// behavior change for featured_image.
func validateFeaturedImagePath(v string) error {
	if err := urlpolicy.Validate(v, urlpolicy.LocalOnly); err != nil {
		return fmt.Errorf("invalid_params: featured_image %w", err)
	}
	return nil
}

func validateBodyFormat(body string, blockedShortcodes []string) error {
	if err := rejectUnsafeText(body); err != nil {
		return fmt.Errorf("invalid_params: body %w", err)
	}
	if n := len(body); n > maxBodyBytes {
		return fmt.Errorf("invalid_params: body exceeds %d bytes (got %d)", maxBodyBytes, n)
	}
	if err := rejectDangerousShortcodes(body, blockedShortcodes); err != nil {
		return fmt.Errorf("invalid_params: %w", err)
	}
	return nil
}

// shortcodeInvocationPattern matches the opening delimiter of a Hugo
// shortcode invocation — {{< name or {{% name, with an optional trailing
// "-" (Hugo's whitespace-trim marker) and an optional leading "/" (a
// closing tag, e.g. {{< /raw >}}, still names the same shortcode) — and
// captures the shortcode name that follows.
var shortcodeInvocationPattern = regexp.MustCompile(`\{\{[%<]-?\s*/?\s*([A-Za-z0-9_-]+)`)

// rejectDangerousShortcodes rejects a body that invokes any shortcode named
// in blocked (#590) — a deliberate hardening of create_page/update_page
// against the confirmed live gap that Hugo's own markup.goldmark.renderer.unsafe=false
// setting only blocks raw HTML typed directly into Markdown, not a
// theme-provided shortcode built specifically to bypass that protection
// (e.g. LoveIt's own "raw"/"script" shortcodes, which store or emit a
// shortcode's inner content as literal, unescaped HTML/JavaScript on the
// public page). blocked is server-configured (config.Config.BlockedShortcodes),
// not a per-call tool input — there is deliberately no way for a caller to
// opt out of this check on a single request.
func rejectDangerousShortcodes(body string, blocked []string) error {
	if len(blocked) == 0 {
		return nil
	}
	blockedSet := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		blockedSet[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, match := range shortcodeInvocationPattern.FindAllStringSubmatch(body, -1) {
		if blockedSet[strings.ToLower(match[1])] {
			return fmt.Errorf("body invokes a blocked shortcode (%q); this server rejects shortcodes known to render unescaped HTML/JavaScript on the public page", match[1])
		}
	}
	return nil
}

// isBidiControlRune reports whether r is one of the Unicode bidirectional
// control code points (#1158): the explicit embedding/override pair
// LRE/RLE/PDF (U+202A-U+202C), the explicit override pair LRO/RLO (U+202D-
// U+202E), and the isolate set LRI/RLI/FSI/PDI (U+2066-U+2069). These have
// no legitimate place in a page title — they exist to change how following
// text is displayed (e.g. RLO can make "gpj.exe" render as "exe.jpg"), which
// is a known spoofing technique (bidi-override / "Trojan Source" attacks)
// and title/list_pages output has no rendering context that would ever
// justify one.
func isBidiControlRune(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

const (
	emojiTagBase  = '\U0001F3F4' // WAVING BLACK FLAG
	tagLetterA    = '\U000E0061'
	tagLetterZ    = '\U000E007A'
	cancelTagRune = '\U000E007F'
	tagBlockStart = '\U000E0000'
	tagBlockEnd   = '\U000E007F'
)

// validEmojiTagSpecs is the complete RGI emoji-tag-sequence repertoire in
// Unicode Emoji 17.0: the subdivision flags for England, Scotland, and Wales.
// Keeping the allowlist explicit rejects invisible TAG payloads that merely
// have the right framing but do not encode a real emoji.
var validEmojiTagSpecs = map[string]struct{}{
	"gbeng": {},
	"gbsct": {},
	"gbwls": {},
}

// rejectMalformedEmojiTags permits only complete RGI subdivision-flag emoji
// sequences. Every other TAG-block code point is invisible formatting data,
// not editorial text, so accepting it would allow hidden ASCII-like payloads
// to survive in Markdown. Content is rejected, never silently rewritten.
func rejectMalformedEmojiTags(s string) error {
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == emojiTagBase && i+1 < len(runes) && runes[i+1] >= tagBlockStart && runes[i+1] <= tagBlockEnd {
			start := i + 1
			j := start
			var spec strings.Builder
			for ; j < len(runes) && runes[j] >= tagLetterA && runes[j] <= tagLetterZ; j++ {
				spec.WriteRune(runes[j] - 0xE0000)
			}
			if j == start || j >= len(runes) || runes[j] != cancelTagRune {
				return fmt.Errorf("must not contain malformed Unicode TAG sequences")
			}
			if _, ok := validEmojiTagSpecs[spec.String()]; !ok {
				return fmt.Errorf("must not contain non-RGI Unicode TAG sequences")
			}
			i = j
			continue
		}
		if runes[i] >= tagBlockStart && runes[i] <= tagBlockEnd {
			return fmt.Errorf("must not contain standalone Unicode TAG characters (found U+%04X)", runes[i])
		}
	}
	return nil
}

// rejectUnsafeText rejects null bytes, C0/C1 control characters other than
// \n, \r, \t, Unicode bidirectional control characters, and malformed or
// standalone Unicode TAG characters, per #380, #1158, and #1239. Content is
// validated as UTF-8 by Go's JSON decoding already
// (invalid UTF-8 in a JSON string fails to decode), so this only needs to
// police the control-character range within otherwise valid text — a null
// byte or raw control code has no legitimate place in a Markdown body or
// frontmatter title and can corrupt downstream parsing (YAML, HTML
// rendering) in ways that are hard to diagnose after the fact.
func rejectUnsafeText(s string) error {
	if err := rejectMalformedEmojiTags(s); err != nil {
		return err
	}
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
		if isBidiControlRune(r) {
			return fmt.Errorf("must not contain bidirectional control characters (found U+%04X)", r)
		}
	}
	return nil
}
