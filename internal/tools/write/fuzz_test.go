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
