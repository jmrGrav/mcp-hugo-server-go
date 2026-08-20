package write

import (
	"strings"
	"testing"
	"time"
)

func TestFrontmatterTimeParsesKnownLayoutsAndFallsBack(t *testing.T) {
	fixed := time.Date(2026, 3, 5, 10, 30, 0, 0, time.UTC)
	if got := frontmatterTime(fixed); !got.Equal(fixed) {
		t.Fatalf("frontmatterTime(time.Time) = %v, want %v unchanged", got, fixed)
	}
	if got := frontmatterTime(""); !got.IsZero() {
		t.Fatalf("frontmatterTime(\"\") = %v, want zero time", got)
	}
	if got := frontmatterTime("2026-03-05T10:30:00Z"); got.IsZero() || got.Year() != 2026 {
		t.Fatalf("frontmatterTime(RFC3339) = %v, want a parsed 2026 date", got)
	}
	if got := frontmatterTime("2026-03-05T10:30:00"); got.IsZero() || got.Year() != 2026 {
		t.Fatalf("frontmatterTime(no-offset layout) = %v, want a parsed 2026 date", got)
	}
	if got := frontmatterTime("2026-03-05"); got.IsZero() || got.Year() != 2026 {
		t.Fatalf("frontmatterTime(date-only layout) = %v, want a parsed 2026 date", got)
	}
	if got := frontmatterTime("not a date"); !got.IsZero() {
		t.Fatalf("frontmatterTime(unparseable string) = %v, want zero time", got)
	}
	if got := frontmatterTime(42); !got.IsZero() {
		t.Fatalf("frontmatterTime(unsupported type) = %v, want zero time", got)
	}
}

func TestParseFrontmatterHelpers(t *testing.T) {
	raw := []byte("---\ntitle: Demo\ntags:\n  - alpha\ncategories:\n  - beta\n---\n\nBody here.\n")

	fm := parseFrontmatterMap(raw)
	if fm == nil {
		t.Fatal("parseFrontmatterMap() = nil, want decoded map")
	}
	if got := fm["title"]; got != "Demo" {
		t.Fatalf("parseFrontmatterMap()[title] = %v, want Demo", got)
	}

	if got := bodyFromRaw(raw); got != "Body here." {
		t.Fatalf("bodyFromRaw() = %q, want %q", got, "Body here.")
	}

	tags, categories := currentTaxonomyFromRaw(raw)
	if len(tags) != 1 || tags[0] != "alpha" {
		t.Fatalf("currentTaxonomyFromRaw() tags = %#v, want [alpha]", tags)
	}
	if len(categories) != 1 || categories[0] != "beta" {
		t.Fatalf("currentTaxonomyFromRaw() categories = %#v, want [beta]", categories)
	}

	if got := toStringSlice([]any{"x", 7, true}); len(got) != 3 || got[0] != "x" || got[1] != "7" || got[2] != "true" {
		t.Fatalf("toStringSlice([]any) = %#v, want [x 7 true]", got)
	}
	if got := toStringSlice([]string{"a", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("toStringSlice([]string) = %#v, want [a b]", got)
	}
	if got := toStringSlice(42); got != nil {
		t.Fatalf("toStringSlice(non-slice) = %#v, want nil", got)
	}
}

func TestParseFrontmatterHelpersMalformedInputs(t *testing.T) {
	if got := parseFrontmatterMap([]byte("plain body")); got != nil {
		t.Fatalf("parseFrontmatterMap(no frontmatter) = %#v, want nil", got)
	}
	if got := parseFrontmatterMap([]byte("---\ntitle: [\n---\nbody")); got != nil {
		t.Fatalf("parseFrontmatterMap(invalid yaml) = %#v, want nil", got)
	}
	if got := bodyFromRaw([]byte("plain body")); got != "plain body" {
		t.Fatalf("bodyFromRaw(no frontmatter) = %q, want plain body", got)
	}
	tags, categories := currentTaxonomyFromRaw([]byte("plain body"))
	if tags != nil || categories != nil {
		t.Fatalf("currentTaxonomyFromRaw(no frontmatter) = (%#v, %#v), want (nil, nil)", tags, categories)
	}
}

func TestResolvePlanOperationsValidations(t *testing.T) {
	draftTrue := true
	tests := []struct {
		name    string
		ops     []planOperationInput
		wantErr string
	}{
		{
			name:    "update_body requires non-empty body",
			ops:     []planOperationInput{{Op: "update_body", Body: "   "}},
			wantErr: "update_body operation requires a non-empty body",
		},
		{
			name:    "update_body blocks dangerous shortcode",
			ops:     []planOperationInput{{Op: "update_body", Body: "{{< unsafe >}}"}},
			wantErr: "blocked shortcode",
		},
		{
			name:    "set_title requires non-empty value",
			ops:     []planOperationInput{{Op: "set_title", Value: " "}},
			wantErr: "set_title operation requires a non-empty value",
		},
		{
			name:    "add_tag requires value",
			ops:     []planOperationInput{{Op: "add_tag"}},
			wantErr: "add_tag operation requires value",
		},
		{
			name:    "remove_category requires value",
			ops:     []planOperationInput{{Op: "remove_category"}},
			wantErr: "remove_category operation requires value",
		},
		{
			name:    "set_draft requires draft value",
			ops:     []planOperationInput{{Op: "set_draft"}},
			wantErr: "set_draft operation requires draft_value",
		},
		{
			name:    "set_field only supports description",
			ops:     []planOperationInput{{Op: "set_field", Field: "layout", Value: "x"}},
			wantErr: "set_field only supports field \"description\"",
		},
		{
			name:    "set_field rejects unsafe description",
			ops:     []planOperationInput{{Op: "set_field", Field: "description", Value: "bad\x00desc"}},
			wantErr: "description",
		},
		{
			name:    "empty op rejected",
			ops:     []planOperationInput{{Op: ""}},
			wantErr: "operations[].op must not be empty",
		},
		{
			name:    "unknown op rejected",
			ops:     []planOperationInput{{Op: "set_layout"}},
			wantErr: "unknown operation",
		},
		{
			name:    "set_draft accepts explicit bool",
			ops:     []planOperationInput{{Op: "set_draft", DraftValue: &draftTrue}},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePlanOperations([]string{"alpha"}, []string{"beta"}, tt.ops, []string{"unsafe"})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("resolvePlanOperations() error = %v, want nil", err)
				}
				if got.Draft == nil || *got.Draft != draftTrue {
					t.Fatalf("resolvePlanOperations() draft = %#v, want true", got.Draft)
				}
				return
			}
			if err == nil {
				t.Fatal("resolvePlanOperations() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolvePlanOperations() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestResolvePlanOperationsAppliesDeltasAndReportsRejections(t *testing.T) {
	draftFalse := false
	got, err := resolvePlanOperations(
		[]string{"alpha", "beta"},
		[]string{"guides"},
		[]planOperationInput{
			{Op: "add_tag", Value: "beta"},
			{Op: "add_tag", Value: "gamma"},
			{Op: "remove_tag", Value: "missing"},
			{Op: "remove_tag", Value: "alpha"},
			{Op: "add_category", Value: "guides"},
			{Op: "add_category", Value: "news"},
			{Op: "remove_category", Value: "missing"},
			{Op: "remove_category", Value: "guides"},
			{Op: "set_title", Value: "New title"},
			{Op: "update_body", Body: "Fresh body"},
			{Op: "set_draft", DraftValue: &draftFalse},
			{Op: "set_field", Field: "description", Value: "Clean description"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("resolvePlanOperations() error = %v", err)
	}

	if got.Title != "New title" || got.Body != "Fresh body" || got.Description != "Clean description" {
		t.Fatalf("resolvePlanOperations() text fields = %#v, want title/body/description populated", got)
	}
	if got.Draft == nil || *got.Draft {
		t.Fatalf("resolvePlanOperations() draft = %#v, want false", got.Draft)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "beta" || got.Tags[1] != "gamma" {
		t.Fatalf("resolvePlanOperations() tags = %#v, want [beta gamma]", got.Tags)
	}
	if len(got.Categories) != 1 || got.Categories[0] != "news" {
		t.Fatalf("resolvePlanOperations() categories = %#v, want [news]", got.Categories)
	}
	if len(got.Rejected) != 4 {
		t.Fatalf("resolvePlanOperations() rejected = %#v, want 4 entries", got.Rejected)
	}
	if len(got.Applied) != 8 {
		t.Fatalf("resolvePlanOperations() applied = %#v, want 8 entries", got.Applied)
	}
}
