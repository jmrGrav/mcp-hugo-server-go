package toolcontract

import (
	"testing"
	"unicode/utf8"
)

func TestResolveResponseMode(t *testing.T) {
	tests := []struct {
		raw     string
		want    ResponseMode
		wantErr bool
	}{
		{"", ResponseModeStandard, false},
		{"standard", ResponseModeStandard, false},
		{"compact", ResponseModeCompact, false},
		{"full", "", true},
		{"ids_only", "", true},
		{"bogus", "", true},
	}
	for _, tt := range tests {
		got, err := ResolveResponseMode(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ResolveResponseMode(%q): expected error, got nil", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveResponseMode(%q): unexpected error %v", tt.raw, err)
		}
		if got != tt.want {
			t.Errorf("ResolveResponseMode(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestResolveIncludeBody(t *testing.T) {
	if !ResolveIncludeBody(nil) {
		t.Error("ResolveIncludeBody(nil) = false, want true (default)")
	}
	tru, fls := true, false
	if !ResolveIncludeBody(&tru) {
		t.Error("ResolveIncludeBody(&true) = false")
	}
	if ResolveIncludeBody(&fls) {
		t.Error("ResolveIncludeBody(&false) = true")
	}
}

func TestTruncateBody(t *testing.T) {
	s, truncated := TruncateBody("hello world", 5)
	if s != "hello" || !truncated {
		t.Errorf("TruncateBody(11 chars, 5) = (%q, %v), want (\"hello\", true)", s, truncated)
	}
	s, truncated = TruncateBody("hi", 10)
	if s != "hi" || truncated {
		t.Errorf("TruncateBody(2 chars, 10) = (%q, %v), want (\"hi\", false)", s, truncated)
	}
	s, truncated = TruncateBody("hi", 0)
	if s != "hi" || truncated {
		t.Errorf("TruncateBody(maxChars=0) = (%q, %v), want unchanged", s, truncated)
	}

	// Multibyte runes must not be split mid-character (byte-slicing would
	// corrupt the boundary and produce invalid UTF-8).
	s, truncated = TruncateBody("héllo wörld", 5)
	wantRunes := []rune("héllo wörld")[:5]
	if s != string(wantRunes) || !truncated {
		t.Errorf("TruncateBody(multibyte, 5) = (%q, %v), want (%q, true)", s, truncated, string(wantRunes))
	}
	if !utf8.ValidString(s) {
		t.Errorf("TruncateBody(multibyte, 5) produced invalid UTF-8: %q", s)
	}
}

func TestSelectFields(t *testing.T) {
	row := map[string]any{"slug": "/a/", "title": "A", "summary": "long text"}
	got := SelectFields(row, []string{"slug", "title", "nonexistent"})
	if len(got) != 2 {
		t.Fatalf("SelectFields: got %d keys, want 2 (unknown field silently dropped): %v", len(got), got)
	}
	if got["slug"] != "/a/" || got["title"] != "A" {
		t.Errorf("SelectFields = %v, want slug+title preserved", got)
	}
	if _, ok := got["summary"]; ok {
		t.Error("SelectFields: summary should have been excluded")
	}
}
