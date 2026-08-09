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
