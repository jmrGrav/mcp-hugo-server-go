package read

import (
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestPageIdentityFromPage(t *testing.T) {
	page := site.Page{
		Slug:       "/posts/hello/",
		Title:      "Hello",
		URL:        "https://example.test/posts/hello/",
		Lang:       "en",
		Tags:       []string{"hugo"},
		Categories: []string{"tutorials"},
	}

	got := pageIdentityFromPage(page, "content/posts/hello.md", 7)
	if got.Slug != "/posts/hello/" {
		t.Fatalf("pageIdentityFromPage().Slug = %q", got.Slug)
	}
	if got.ReadingTime != 7 {
		t.Fatalf("pageIdentityFromPage().ReadingTime = %d", got.ReadingTime)
	}
	if len(got.Tags) != 1 || got.Tags[0].Slug != "hugo" {
		t.Fatalf("pageIdentityFromPage().Tags = %#v", got.Tags)
	}
	if len(got.Categories) != 1 || got.Categories[0].Label != "Tutorials" {
		t.Fatalf("pageIdentityFromPage().Categories = %#v", got.Categories)
	}
}

func TestLegacyEnvelopeUsesToolcontractMeta(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 30, 0, 0, time.UTC)
	got := legacyEnvelope(getBacklinksData{Slug: "/posts/hello/"}, now)

	if got.Success != true {
		t.Fatalf("legacyEnvelope().Success = %v, want true", got.Success)
	}
	if got.Version != toolResultVersion {
		t.Fatalf("legacyEnvelope().Version = %q, want %q", got.Version, toolResultVersion)
	}
	if got.GeneratedAt != now.Format(time.RFC3339) {
		t.Fatalf("legacyEnvelope().GeneratedAt = %q", got.GeneratedAt)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("legacyEnvelope().Warnings = %#v, want empty", got.Warnings)
	}
	if len(got.Errors) != 0 {
		t.Fatalf("legacyEnvelope().Errors = %#v, want empty", got.Errors)
	}
}
