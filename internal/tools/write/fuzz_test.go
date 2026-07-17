package write

import (
	"strings"
	"testing"
)

func FuzzApplyPageUpdatesRoundTrip(f *testing.F) {
	f.Add("---\ntitle: Page\ndate: 2026-01-01T00:00:00Z\ntags:\n  - old\ncategories:\n  - Docs\ndraft: false\n---\n\nBody.", "New Title", "New body", "go,hugo", "docs,infra", true, "desc")
	f.Add("---\ntitle: Page\n---\n\n---\n\nBody after rule.", "", "", "", "", false, "")
	f.Add("not-frontmatter", "Title", "Body", "", "", false, "")

	f.Fuzz(func(t *testing.T, input, newTitle, newBody, tagsCSV, categoriesCSV string, draft bool, description string) {
		if len(input) > 2048 || len(newTitle) > 256 || len(newBody) > 1024 || len(tagsCSV) > 256 || len(categoriesCSV) > 256 || len(description) > 256 {
			return
		}
		opts := pageUpdateOpts{
			Tags:        splitCSV(tagsCSV),
			Categories:  splitCSV(categoriesCSV),
			Draft:       &draft,
			Description: description,
		}

		got, err := applyPageUpdates(input, newTitle, newBody, opts)
		if err != nil {
			return
		}
		if !strings.HasPrefix(got, "---\n") {
			t.Fatalf("applyPageUpdates returned content without frontmatter delimiter: %q", got)
		}
		if err := validateFrontmatterRoundTrip(got); err != nil {
			t.Fatalf("applyPageUpdates returned invalid round-trip content: %v\noutput=%q", err, got)
		}
	})
}

func FuzzValidateFrontmatterRoundTrip(f *testing.F) {
	f.Add("---\ntitle: T\n---\nBody\n")
	f.Add("---\ntitle: T\n---\n---\ntitle: U\n---\nBody\n")
	f.Add("not-frontmatter")
	f.Add("---\ntitle: [\n---\nBody\n")

	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 2048 {
			return
		}
		_ = validateFrontmatterRoundTrip(content)
	})
}

// FuzzRejectUnsafeText covers #380's null-byte/control-char/UTF-8-boundary
// edge cases. It never asserts a specific accept/reject outcome (both are
// valid depending on input) — it only asserts rejectUnsafeText never panics
// and, when it does reject, the returned error is non-nil and non-empty.
func FuzzRejectUnsafeText(f *testing.F) {
	f.Add("")
	f.Add("hello world")
	f.Add("line one\nline two\ttabbed\r\n")
	f.Add("\x00null byte")
	f.Add("bell\x07control")
	f.Add("héllo wörld") // multibyte UTF-8, must not be misread as control chars
	f.Add("emoji 🎉 boundary")
	f.Add("c1 control \x85 (NEL)")
	f.Add(string([]byte{0xff, 0xfe})) // invalid UTF-8 byte sequence

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			return
		}
		err := rejectUnsafeText(s)
		if err != nil && err.Error() == "" {
			t.Fatalf("rejectUnsafeText returned a non-nil error with empty message for input %q", s)
		}
	})
}

func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
