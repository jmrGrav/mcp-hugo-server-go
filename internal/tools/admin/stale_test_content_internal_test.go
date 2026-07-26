package admin

import (
	"testing"
	"time"
)

func TestTestContentExpiryAcceptsStringAndTime(t *testing.T) {
	expiresAt := time.Date(2026, 7, 26, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name          string
		frontmatter   map[string]any
		wantOK        bool
		wantExpiresAt time.Time
	}{
		{
			name: "string",
			frontmatter: map[string]any{
				"test_content_expires_at": expiresAt.Format(time.RFC3339),
			},
			wantOK:        true,
			wantExpiresAt: expiresAt,
		},
		{
			name: "time",
			frontmatter: map[string]any{
				"test_content_expires_at": expiresAt,
			},
			wantOK:        true,
			wantExpiresAt: expiresAt,
		},
		{
			name:        "missing",
			frontmatter: map[string]any{},
			wantOK:      false,
		},
		{
			name: "invalid-string",
			frontmatter: map[string]any{
				"test_content_expires_at": "not-a-timestamp",
			},
			wantOK: false,
		},
		{
			name: "unsupported-type",
			frontmatter: map[string]any{
				"test_content_expires_at": 123,
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := testContentExpiry(tt.frontmatter)
			if ok != tt.wantOK {
				t.Fatalf("testContentExpiry() ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if !got.Equal(tt.wantExpiresAt) {
				t.Fatalf("testContentExpiry() time = %s, want %s", got.Format(time.RFC3339), tt.wantExpiresAt.Format(time.RFC3339))
			}
		})
	}
}
