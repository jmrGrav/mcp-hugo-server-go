package caller

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

func TestKey(t *testing.T) {
	tests := []struct {
		name     string
		tokenID  any
		callerIP any
		want     string
	}{
		{
			name:    "token id present returns token id",
			tokenID: "tok-abc123",
			want:    "tok-abc123",
		},
		{
			name:     "token id takes priority over caller ip",
			tokenID:  "tok-abc123",
			callerIP: "203.0.113.5",
			want:     "tok-abc123",
		},
		{
			name:     "empty token id falls back to caller ip",
			tokenID:  "",
			callerIP: "203.0.113.5",
			want:     "203.0.113.5",
		},
		{
			name:     "whitespace-only token id falls back to caller ip",
			tokenID:  "   ",
			callerIP: "203.0.113.5",
			want:     "203.0.113.5",
		},
		{
			name:     "non-string token id falls back to caller ip",
			tokenID:  42,
			callerIP: "203.0.113.5",
			want:     "203.0.113.5",
		},
		{
			name:     "caller ip present with no token id",
			callerIP: "203.0.113.5",
			want:     "203.0.113.5",
		},
		{
			name:     "whitespace-only caller ip yields empty key",
			callerIP: "   ",
			want:     "",
		},
		{
			name: "neither value present yields empty key",
			want: "",
		},
		{
			name:     "non-string caller ip with no token id yields empty key",
			callerIP: 203,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.tokenID != nil {
				ctx = context.WithValue(ctx, oauth.CtxTokenID, tt.tokenID)
			}
			if tt.callerIP != nil {
				ctx = context.WithValue(ctx, oauth.CtxCallerIP, tt.callerIP)
			}
			if got := Key(ctx); got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeyEmptyContextYieldsEmptyKey(t *testing.T) {
	if got := Key(context.Background()); got != "" {
		t.Errorf("Key(context.Background()) = %q, want empty string", got)
	}
}

// TestSourceMatchesKeyPrecedence is a regression test: Source must report
// exactly which context value Key actually resolved, in the same
// principal→token→ip precedence, so a diagnostic surface built on Source
// (get_rate_limits' identity_source) can never describe a different
// identity than the one the quota bucket was actually keyed on (#962).
func TestSourceMatchesKeyPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		principal  any
		tokenID    any
		callerIP   any
		wantSource string
	}{
		{name: "principal present wins over token and ip", principal: "client-abc", tokenID: "tok-1", callerIP: "203.0.113.5", wantSource: "principal"},
		{name: "empty principal falls back to token", principal: "", tokenID: "tok-1", callerIP: "203.0.113.5", wantSource: "token"},
		{name: "whitespace-only principal falls back to token", principal: "   ", tokenID: "tok-1", wantSource: "token"},
		{name: "no principal or token falls back to ip", callerIP: "203.0.113.5", wantSource: "ip"},
		{name: "nothing present yields unknown", wantSource: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.principal != nil {
				ctx = context.WithValue(ctx, oauth.CtxPrincipal, tt.principal)
			}
			if tt.tokenID != nil {
				ctx = context.WithValue(ctx, oauth.CtxTokenID, tt.tokenID)
			}
			if tt.callerIP != nil {
				ctx = context.WithValue(ctx, oauth.CtxCallerIP, tt.callerIP)
			}
			if got := Source(ctx); got != tt.wantSource {
				t.Errorf("Source() = %q, want %q", got, tt.wantSource)
			}

			// The identity Key() actually resolved must be non-empty
			// precisely when Source() isn't "unknown" — proving Source
			// tracks Key's real fallback outcome, not a parallel guess.
			key := Key(ctx)
			if tt.wantSource == "unknown" && key != "" {
				t.Errorf("Source()=unknown but Key()=%q (non-empty) — diverged", key)
			}
			if tt.wantSource != "unknown" && key == "" {
				t.Errorf("Source()=%q but Key()=\"\" — diverged", tt.wantSource)
			}
		})
	}
}
