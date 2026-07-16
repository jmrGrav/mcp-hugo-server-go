package read

import (
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
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

	got := pageIdentityFromPage(page, "content/posts/hello.md", "sha256:test", 7)
	if got.Slug != "/posts/hello/" {
		t.Fatalf("pageIdentityFromPage().Slug = %q", got.Slug)
	}
	if got.SourcePath != "content/posts/hello.md" {
		t.Fatalf("pageIdentityFromPage().SourcePath = %q", got.SourcePath)
	}
	if got.Revision != "sha256:test" {
		t.Fatalf("pageIdentityFromPage().Revision = %q", got.Revision)
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

func TestSuccessEnvelopePopulatesCompatibilityFields(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 30, 0, 0, time.UTC)
	buildinfo.Version = "v1.4.3-test"
	got := successEnvelope(getBacklinksData{Slug: "/posts/hello/"}, now)

	if got.Success != true {
		t.Fatalf("successEnvelope().Success = %v, want true", got.Success)
	}
	if got.Version != toolcontract.ToolResultVersion {
		t.Fatalf("successEnvelope().Version = %q, want %q", got.Version, toolcontract.ToolResultVersion)
	}
	if got.Meta.ServerVersion != buildinfo.Version {
		t.Fatalf("successEnvelope().Meta.ServerVersion = %q, want %q", got.Meta.ServerVersion, buildinfo.Version)
	}
	if got.GeneratedAt != now.Format(time.RFC3339) {
		t.Fatalf("successEnvelope().GeneratedAt = %q", got.GeneratedAt)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("successEnvelope().Warnings = %#v, want empty", got.Warnings)
	}
	if len(got.Errors) != 0 {
		t.Fatalf("successEnvelope().Errors = %#v, want empty", got.Errors)
	}
}
