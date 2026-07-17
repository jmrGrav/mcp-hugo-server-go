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

func TestValidateBodyFormatRejectsOverLength(t *testing.T) {
	long := make([]byte, maxBodyBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateBodyFormat(string(long)); err == nil {
		t.Fatal("validateBodyFormat: want error for over-length body, got nil")
	}
}
