package write

import "testing"

func TestRejectUnsafeTextRejectsNullBytes(t *testing.T) {
	if err := rejectUnsafeText("hello\x00world"); err == nil {
		t.Fatal("rejectUnsafeText: want error for null byte, got nil")
	}
}

func TestRejectUnsafeTextRejectsControlChars(t *testing.T) {
	if err := rejectUnsafeText("hello\x07world"); err == nil {
		t.Fatal("rejectUnsafeText: want error for BEL control char, got nil")
	}
}

func TestRejectUnsafeTextRejectsC1Controls(t *testing.T) {
	if err := rejectUnsafeText("hello\u0085world"); err == nil {
		t.Fatal("rejectUnsafeText: want error for U+0085 NEL (C1 control), got nil")
	}
}

func TestRejectUnsafeTextAllowsNewlinesTabsCarriageReturns(t *testing.T) {
	if err := rejectUnsafeText("line one\nline two\ttabbed\r\n"); err != nil {
		t.Fatalf("rejectUnsafeText: want nil for \\n\\t\\r, got %v", err)
	}
}

func TestRejectUnsafeTextAllowsMultibyteUTF8(t *testing.T) {
	if err := rejectUnsafeText("héllo wörld 日本語 \U0001F389"); err != nil {
		t.Fatalf("rejectUnsafeText: want nil for valid multibyte UTF-8, got %v", err)
	}
}

func TestValidateSlugFormat(t *testing.T) {
	valid := []string{"a", "posts/hello", "my-post_2026", "a/b/c"}
	for _, s := range valid {
		if err := validateSlugFormat(s); err != nil {
			t.Errorf("validateSlugFormat(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{"Posts/Hello", "has space", "has.dot", "trailing-/", "/leading", "emoji\U0001F389"}
	for _, s := range invalid {
		if err := validateSlugFormat(s); err == nil {
			t.Errorf("validateSlugFormat(%q) = nil, want error", s)
		}
	}
}

func TestValidateTitleFormatRejectsOverLength(t *testing.T) {
	long := make([]byte, maxTitleRunes+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateTitleFormat(string(long)); err == nil {
		t.Fatal("validateTitleFormat: want error for over-length title, got nil")
	}
}

func TestValidateTitleFormatAllowsMaxLength(t *testing.T) {
	exact := make([]byte, maxTitleRunes)
	for i := range exact {
		exact[i] = 'a'
	}
	if err := validateTitleFormat(string(exact)); err != nil {
		t.Fatalf("validateTitleFormat: want nil at exactly max length, got %v", err)
	}
}

func TestValidateTitleFormatRejectsHTMLMarkup(t *testing.T) {
	if err := validateTitleFormat(`<img src=x onerror=alert(1)>`); err == nil {
		t.Fatal("validateTitleFormat: want error for raw HTML markup in title, got nil")
	}
}

func TestValidateTitleFormatAllowsPlainAngleBracketText(t *testing.T) {
	for _, title := range []string{"3 < 5", "A > B", "Rust < Go?"} {
		if err := validateTitleFormat(title); err != nil {
			t.Fatalf("validateTitleFormat(%q) = %v, want nil", title, err)
		}
	}
}

func TestValidateFeaturedImagePath(t *testing.T) {
	valid := []string{"/images/posts/hero.jpg", "/images/posts/hero-featured.jpg"}
	for _, s := range valid {
		if err := validateFeaturedImagePath(s); err != nil {
			t.Fatalf("validateFeaturedImagePath(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{
		"images/posts/hero.jpg",
		"//cdn.example.test/img.jpg",
		"/images/../etc/passwd",
		`/images\evil.jpg`,
		"data:text/html;base64,AAAA",
		"http://example.test/hero.jpg",
		"https://example.test/hero.jpg",
		"javascript:alert(1)",
		"file:///etc/passwd",
		// #835: a value that's a normalized, local, non-traversal path with
		// no disallowed scheme prefix still must not carry HTML/attribute-
		// breakout metacharacters, since featuredImage frontmatter is
		// rendered into HTML attributes/CSS url() by the site theme without
		// re-validating.
		`/img.jpg" onerror="alert(1)`,
		"/images/<script>.jpg",
		"/images/hero.jpg onload=alert(1)",
		"/images/hero'.jpg",
	}
	for _, s := range invalid {
		if err := validateFeaturedImagePath(s); err == nil {
			t.Fatalf("validateFeaturedImagePath(%q) = nil, want error", s)
		}
	}
}

// TestValidateFeaturedImagePathCharacterization pins the EXACT error string
// (or nil) validateFeaturedImagePath returns for a battery of inputs. It was
// written BEFORE the #855 migration onto the shared urlpolicy validator and
// must stay byte-for-byte green afterwards — this is the regression guard
// proving the migration does not alter featured_image's observable behavior,
// including the check ORDER that makes the #835 fix effective (e.g. a
// scheme-looking value with no leading "/" must report the leading-slash
// error, not a scheme error, because that gate runs first).
func TestValidateFeaturedImagePathCharacterization(t *testing.T) {
	cases := []struct {
		in  string
		err string // "" means nil (accepted)
	}{
		{"", ""},
		{"/images/posts/hero.jpg", ""},
		{"/images/posts/hero-featured.jpg", ""},
		{"/images/x-featured.jpg", ""},
		{"/a_b/c~d/e.jpg", ""},
		{"images/posts/hero.jpg", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"javascript:alert(1)", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"data:text/html;base64,AAAA", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"vbscript:msgbox(1)", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"http://example.test/hero.jpg", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"https://example.test/hero.jpg", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"file:///etc/passwd", "invalid_params: featured_image must be a site-root-relative path starting with /"},
		{"//cdn.example.test/img.jpg", "invalid_params: featured_image must not be protocol-relative"},
		{`/images\evil.jpg`, "invalid_params: featured_image must not contain backslashes"},
		{"/images/../etc/passwd", "invalid_params: featured_image must not contain dot path segments"},
		{"/a/../b", "invalid_params: featured_image must not contain dot path segments"},
		{"/./a.jpg", "invalid_params: featured_image must not contain dot path segments"},
		{`/img.jpg" onerror="alert(1)`, "invalid_params: featured_image must contain only letters, digits, and ._~/- characters"},
		{"/images/<script>.jpg", "invalid_params: featured_image must contain only letters, digits, and ._~/- characters"},
		{"/images/hero.jpg onload=alert(1)", "invalid_params: featured_image must contain only letters, digits, and ._~/- characters"},
		{"/images/hero'.jpg", "invalid_params: featured_image must contain only letters, digits, and ._~/- characters"},
		{"/img\x01.jpg", "invalid_params: featured_image must not contain control characters (found U+0001)"},
		{"/img\x00.jpg", "invalid_params: featured_image must not contain null bytes"},
	}
	for _, tc := range cases {
		err := validateFeaturedImagePath(tc.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.err {
			t.Errorf("validateFeaturedImagePath(%q) = %q, want %q", tc.in, got, tc.err)
		}
	}
}

func TestValidateBodyFormatRejectsOverLength(t *testing.T) {
	long := make([]byte, maxBodyBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateBodyFormat(string(long), nil); err == nil {
		t.Fatal("validateBodyFormat: want error for over-length body, got nil")
	}
}

func TestRejectDangerousShortcodesBlocksConfiguredName(t *testing.T) {
	if err := rejectDangerousShortcodes("before {{< script >}}x{{< /script >}} after", []string{"raw", "script"}); err == nil {
		t.Fatal("rejectDangerousShortcodes: want error for blocked \"script\" shortcode, got nil")
	}
}

func TestRejectDangerousShortcodesIsCaseInsensitive(t *testing.T) {
	if err := rejectDangerousShortcodes("{{< SCRIPT >}}x{{< /SCRIPT >}}", []string{"script"}); err == nil {
		t.Fatal("rejectDangerousShortcodes: want error for differently-cased blocked shortcode, got nil")
	}
}

func TestRejectDangerousShortcodesAllowsUnlistedName(t *testing.T) {
	if err := rejectDangerousShortcodes("{{< figure src=\"x.png\" >}}", []string{"raw", "script"}); err != nil {
		t.Fatalf("rejectDangerousShortcodes: want nil for an unlisted shortcode, got %v", err)
	}
}

func TestRejectDangerousShortcodesAllowsPlainTextMentioningName(t *testing.T) {
	if err := rejectDangerousShortcodes("This post explains Hugo's raw and script shortcodes.", []string{"raw", "script"}); err != nil {
		t.Fatalf("rejectDangerousShortcodes: want nil for plain-text mention without {{ }} delimiters, got %v", err)
	}
}

func TestRejectDangerousShortcodesEmptyBlocklistAllowsEverything(t *testing.T) {
	if err := rejectDangerousShortcodes("{{< script >}}x{{< /script >}}", nil); err != nil {
		t.Fatalf("rejectDangerousShortcodes: want nil for an empty blocklist, got %v", err)
	}
}
