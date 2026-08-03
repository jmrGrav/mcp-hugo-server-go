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
	}
	for _, s := range invalid {
		if err := validateFeaturedImagePath(s); err == nil {
			t.Fatalf("validateFeaturedImagePath(%q) = nil, want error", s)
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
