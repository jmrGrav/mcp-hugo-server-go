package write

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// slugPattern is the unified slug format for create_page/update_page,
// per #380. Lowercase alphanumeric segments joined by "/", "_", or "-";
// single-character slugs are allowed. This is checked in addition to (not
// instead of) pg.SafeJoin's path-traversal/hidden-component guard and the
// reservedSlugs check — those remain the security boundary, this is a
// content-convention boundary that rejects slugs no legitimate Hugo section
// would use (spaces, uppercase, punctuation) before they ever reach disk.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9/_-]*[a-z0-9])?$`)

const (
	// maxTitleRunes and maxBodyBytes bound create_page/update_page input,
	// per #380. Values match the issue's own proposal (title 255 chars,
	// body 1MB) — generous enough that no legitimate blog post/article hits
	// them, tight enough to reject pathological input before it reaches a
	// file write.
	maxTitleRunes = 255
	maxBodyBytes  = 1 << 20
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
	if n := utf8.RuneCountInString(title); n > maxTitleRunes {
		return fmt.Errorf("invalid_params: title exceeds %d characters (got %d)", maxTitleRunes, n)
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

// rejectUnsafeText rejects null bytes and C0/C1 control characters other
// than \n, \r, \t, per #380. Content is validated as UTF-8 by Go's JSON
// decoding already (invalid UTF-8 in a JSON string fails to decode), so
// this only needs to police the control-character range within otherwise
// valid text — a null byte or raw control code has no legitimate place in
// a Markdown body or frontmatter title and can corrupt downstream parsing
// (YAML, HTML rendering) in ways that are hard to diagnose after the fact.
func rejectUnsafeText(s string) error {
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
