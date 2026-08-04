package urlpolicy

import (
	"strings"
	"testing"
)

func TestValidateLocalOnly(t *testing.T) {
	accepted := []string{
		"",                       // empty clears the field
		"/images/posts/hero.jpg", // valid local public path
		"/images/x-featured.jpg", // generate_hero_image output shape
		"/a_b/c~d/e-f.jpg",       // full allowlisted charset
		"/images/2026/07/pic.png",
	}
	for _, s := range accepted {
		if err := Validate(s, LocalOnly); err != nil {
			t.Errorf("Validate(%q, LocalOnly) = %v, want nil", s, err)
		}
	}

	// AC5 vectors: javascript:, unsafe data:, vbscript:, protocol-relative,
	// path-traversal-ish local paths, and #835 attribute-breakout charset.
	rejected := []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"http://evil.example/x.jpg",  // external URL forbidden in LocalOnly
		"https://evil.example/x.jpg", // external URL forbidden in LocalOnly
		"//cdn.example/x.jpg",        // protocol-relative
		"/images/../../etc/passwd",   // traversal
		"/a/../b",                    // traversal
		"images/no-leading-slash.jpg",
		`/images\evil.jpg`,            // backslash
		`/img.jpg" onerror="alert(1)`, // #835 attribute breakout
		"/images/<script>.jpg",        // #835 tag injection
		"/img\x01.jpg",                // control char
		"/img\x00.jpg",                // null byte
	}
	for _, s := range rejected {
		if err := Validate(s, LocalOnly); err == nil {
			t.Errorf("Validate(%q, LocalOnly) = nil, want error", s)
		}
	}
}

func TestValidateExternalAllowed(t *testing.T) {
	accepted := []string{
		"",                               // empty
		"/images/local-still-ok.jpg",     // a local path is also valid here
		"http://example.test/hero.jpg",   // valid external URL
		"https://example.test/a/b?c=d#e", // query + fragment
		"HTTPS://Example.Test/Hero.JPG",  // scheme/host case-insensitive
	}
	for _, s := range accepted {
		if err := Validate(s, ExternalAllowed); err != nil {
			t.Errorf("Validate(%q, ExternalAllowed) = %v, want nil", s, err)
		}
	}

	// Forbidden schemes remain forbidden even where external URLs are allowed:
	// only http/https are the allowlist.
	rejected := []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"//cdn.example/x.jpg", // protocol-relative is not http(s)
		"http://",             // no host
		"https://\x01evil",    // embedded control char
		"mailto:a@b.test",     // not an image/URL scheme we allow

		// #835 attribute-breakout class, in the http(s) branch: these are
		// well-formed enough that the scheme+host check accepts them, but the
		// raw quote / angle brackets / whitespace break out of an HTML
		// attribute the theme renders the value into. LocalOnly blocks these
		// via its charset allowlist; ExternalAllowed must too. Each of these
		// returned nil (accepted) before the metacharacter denylist was added.
		`https://good.example/a" onerror="alert(1)`,   // attribute breakout
		`https://x.example/"><script>alert(1)</script>`, // tag injection
		"https://good.example/a b",                    // raw space
		"https://good.example/a\tb",                   // tab (exempt from control-char check)
		"https://good.example/a\nb",                   // newline
		"https://good.example/a'b",                    // single quote
		"https://good.example/a`b",                    // backtick
	}
	for _, s := range rejected {
		if err := Validate(s, ExternalAllowed); err == nil {
			t.Errorf("Validate(%q, ExternalAllowed) = nil, want error", s)
		}
	}
}

// baselinePreFixLocalCheck models the PRE-#835 featured_image validator: a
// shape-only check (leading "/", no traversal, no scheme prefix) that never
// bounded the character set. AC5 asks that each new regression genuinely fails
// against pre-fix code; since current HEAD already blocks these vectors, this
// self-contained differential shows the vectors the shared validator now
// rejects that the pre-fix shape check accepted.
func baselinePreFixLocalCheck(v string) error {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return errShape
	}
	for _, seg := range strings.Split(strings.TrimPrefix(v, "/"), "/") {
		if seg == "." || seg == ".." {
			return errShape
		}
	}
	return nil // NOTE: no charset bound — the #835 gap.
}

type shapeErr struct{}

func (shapeErr) Error() string { return "bad shape" }

var errShape = shapeErr{}

func TestSharedValidatorClosesPreFixGap(t *testing.T) {
	// These pass the pre-#835 shape check but must be rejected by the shared
	// validator — the byte-for-byte proof that the migration preserves the
	// #835 fix rather than regressing it.
	gapVectors := []string{
		`/img.jpg" onerror="alert(1)`,
		"/images/<script>.jpg",
		"/images/hero.jpg onload=alert(1)",
	}
	for _, s := range gapVectors {
		if err := baselinePreFixLocalCheck(s); err != nil {
			t.Fatalf("baseline pre-fix check unexpectedly rejected %q (%v); test premise is wrong", s, err)
		}
		if err := Validate(s, LocalOnly); err == nil {
			t.Errorf("Validate(%q, LocalOnly) = nil, want error (pre-fix gap not closed)", s)
		}
	}
}
