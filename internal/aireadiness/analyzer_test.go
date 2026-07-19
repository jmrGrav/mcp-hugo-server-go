package aireadiness

import (
	"strings"
	"testing"
)

func TestAnalyzeHeadingHierarchyWarnsOnSkippedLevels(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: "## Top\ntext\n#### Deep\nmore\n",
	})

	if got := report.Checks.HeadingHierarchy.Status; got != StatusWarn {
		t.Fatalf("heading_hierarchy status = %q, want %q", got, StatusWarn)
	}
	if len(report.Checks.HeadingHierarchy.Jumps) != 1 {
		t.Fatalf("heading_hierarchy jumps = %d, want 1", len(report.Checks.HeadingHierarchy.Jumps))
	}
	if got := report.Status; got != StatusWarn {
		t.Fatalf("report status = %q, want %q", got, StatusWarn)
	}
}

func TestAnalyzeHeadingHierarchyFailsOnMalformedSyntax(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: "##Good heading?\nbody\n",
	})

	if got := report.Checks.HeadingHierarchy.Status; got != StatusFail {
		t.Fatalf("heading_hierarchy status = %q, want %q", got, StatusFail)
	}
	if len(report.Checks.HeadingHierarchy.MalformedLines) != 1 || report.Checks.HeadingHierarchy.MalformedLines[0] != 1 {
		t.Fatalf("malformed_lines = %#v, want [1]", report.Checks.HeadingHierarchy.MalformedLines)
	}
}

func TestAnalyzeSectionLengthsWarnsOnOversizedSection(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: "# Intro\n" + strings.Repeat("a", DefaultSectionLengthThreshold+50),
	})

	if got := report.Checks.SectionLengths.Status; got != StatusWarn {
		t.Fatalf("section_lengths status = %q, want %q", got, StatusWarn)
	}
	if len(report.Checks.SectionLengths.OffendingSections) != 1 {
		t.Fatalf("offending_sections = %d, want 1", len(report.Checks.SectionLengths.OffendingSections))
	}
}

func TestAnalyzeParagraphLengthsWarnsOnOversizedParagraph(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: strings.Repeat("b", DefaultParagraphLengthThreshold+25),
	})

	if got := report.Checks.ParagraphLengths.Status; got != StatusWarn {
		t.Fatalf("paragraph_lengths status = %q, want %q", got, StatusWarn)
	}
	if len(report.Checks.ParagraphLengths.OffendingParagraphs) != 1 {
		t.Fatalf("offending_paragraphs = %d, want 1", len(report.Checks.ParagraphLengths.OffendingParagraphs))
	}
}

func TestAnalyzeMetadataPresenceFailsWithoutTitleOrDate(t *testing.T) {
	report := Analyze(Document{
		Markdown: "Body",
	})

	if got := report.Checks.MetadataPresence.Status; got != StatusFail {
		t.Fatalf("metadata_presence status = %q, want %q", got, StatusFail)
	}
	if got := report.Status; got != StatusFail {
		t.Fatalf("report status = %q, want %q", got, StatusFail)
	}
}

func TestAnalyzeMetadataPresenceWarnsWhenSummaryMissing(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Markdown: "Body",
	})

	if got := report.Checks.MetadataPresence.Status; got != StatusWarn {
		t.Fatalf("metadata_presence status = %q, want %q", got, StatusWarn)
	}
}

func TestAnalyzeInternalLinkDensityWarnsForLongPageWithoutLinks(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: strings.Repeat("long body ", 260),
	})

	if got := report.Checks.InternalLinkDensity.Status; got != StatusWarn {
		t.Fatalf("internal_link_density status = %q, want %q", got, StatusWarn)
	}
	if !report.Checks.InternalLinkDensity.Evaluated {
		t.Fatal("internal_link_density evaluated = false, want true")
	}
}

func TestAnalyzeInternalLinkDensityCountsMarkdownAndRelrefLinks(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: strings.Repeat("body ", 450) + "\n[doc](/posts/hello/)\n{{< relref \"posts/other\" >}}\n",
	})

	if got := report.Checks.InternalLinkDensity.InternalLinkCount; got != 2 {
		t.Fatalf("internal_link_count = %d, want 2", got)
	}
	if got := report.Checks.InternalLinkDensity.Status; got != StatusPass {
		t.Fatalf("internal_link_density status = %q, want %q", got, StatusPass)
	}
}

func TestAnalyzeCitationStructureWarnsWhenLongPageHasTooFewHeadings(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: strings.Repeat("section words ", 260),
	})

	if got := report.Checks.CitationStructure.Status; got != StatusWarn {
		t.Fatalf("citation_structure status = %q, want %q", got, StatusWarn)
	}
}

func TestAnalyzeIgnoresHeadingsInsideCodeFences(t *testing.T) {
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: "```md\n#### not a heading\n```\n## Real\nBody\n",
	})

	if got := report.Checks.HeadingHierarchy.HeadingCount; got != 1 {
		t.Fatalf("heading_count = %d, want 1", got)
	}
	if got := report.Checks.HeadingHierarchy.Status; got != StatusPass {
		t.Fatalf("heading_hierarchy status = %q, want %q", got, StatusPass)
	}
}
