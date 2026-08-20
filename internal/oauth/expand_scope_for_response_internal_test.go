package oauth

import "testing"

// expandScopeForResponse only affects the OAuth token response body — it
// must never change what a scope string means for authorization purposes.
// tools.ScopeRank (the actual enforcement check) does an exact-match switch
// on the single-token canonical form, so a multi-token response string like
// "read write" must never be what gets persisted/verified — these tests
// pin the display-only mapping directly.

func TestExpandScopeForResponse(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"read", "read"},
		{"write", "read write"},
		{"admin", "read write admin"},
		{"content.read", "read"},
		{"content.write", "read write"},
		{"site.admin", "read write admin"},
		{"reader", "read"},
		{LegacyScopeAlias, "read"},
		{"", ""},
		{"not-a-real-scope", "not-a-real-scope"},
	}
	for _, tt := range tests {
		if got := expandScopeForResponse(tt.in); got != tt.want {
			t.Errorf("expandScopeForResponse(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
