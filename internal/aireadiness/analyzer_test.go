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

// TestAnalyzeIgnoresHTMLCommentAsParagraphText is a regression test for
// #1183: a long single-line HTML comment (e.g. a base64-encoded
// mermaid-source marker) was previously accumulated as ordinary paragraph
// text, producing a false-positive "paragraph exceeds threshold" warning
// for internal bookkeeping that was never editorial content.
func TestAnalyzeIgnoresHTMLCommentAsParagraphText(t *testing.T) {
	comment := "<!-- mermaid-source:" + strings.Repeat("A", DefaultParagraphLengthThreshold+50) + " -->"
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: "# Intro\n" + comment + "\nActual short paragraph.\n",
	})

	if got := report.Checks.ParagraphLengths.Status; got != StatusPass {
		t.Fatalf("paragraph_lengths status = %q, want %q (comment must not count as a paragraph)", got, StatusPass)
	}
	if len(report.Checks.ParagraphLengths.OffendingParagraphs) != 0 {
		t.Fatalf("offending_paragraphs = %d, want 0", len(report.Checks.ParagraphLengths.OffendingParagraphs))
	}
}

// TestAnalyzeExcludesHTMLCommentFromBodyCharacters covers the other half of
// #1183's report: an HTML comment must not inflate body_characters/
// section-length accumulation either, not just paragraph_lengths.
func TestAnalyzeExcludesHTMLCommentFromBodyCharacters(t *testing.T) {
	prose := "Short body text."
	withoutComment := Analyze(Document{
		Title: "Hello", Date: "2026-07-19", Summary: "summary",
		Markdown: "# Intro\n" + prose + "\n",
	})
	comment := "<!-- mermaid-source:" + strings.Repeat("A", 500) + " -->"
	withComment := Analyze(Document{
		Title: "Hello", Date: "2026-07-19", Summary: "summary",
		Markdown: "# Intro\n" + comment + "\n" + prose + "\n",
	})

	got := withComment.Checks.InternalLinkDensity.BodyCharacters
	want := withoutComment.Checks.InternalLinkDensity.BodyCharacters
	if got != want {
		t.Fatalf("body_characters with HTML comment = %d, want %d (same as without the comment)", got, want)
	}
}

// TestAnalyzeIgnoresMultiLineHTMLComment covers a <!-- ... --> block split
// across lines, not just the single-line case #1183 reported.
func TestAnalyzeIgnoresMultiLineHTMLComment(t *testing.T) {
	md := "# Intro\n<!--\n" + strings.Repeat("A", DefaultParagraphLengthThreshold+50) + "\n-->\nActual short paragraph.\n"
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: md,
	})

	if got := report.Checks.ParagraphLengths.Status; got != StatusPass {
		t.Fatalf("paragraph_lengths status = %q, want %q (multi-line comment must not count as a paragraph)", got, StatusPass)
	}
}

// TestAnalyzeHTMLCommentWithTrailingProseDoesNotSwallowRestOfDocument
// guards against over-correcting #1183: a comment-and-prose line like
// "<!-- marker --> Trailing prose." has its own closing "-->" and must not
// be treated as an unterminated block-comment opener — doing so would drop
// every following line (headings included) up to the next "-->" or EOF.
func TestAnalyzeHTMLCommentWithTrailingProseDoesNotSwallowRestOfDocument(t *testing.T) {
	md := "# Intro\n<!-- marker --> Trailing prose.\n## Second\n" + strings.Repeat("c", 100) + "\n"
	report := Analyze(Document{
		Title:    "Hello",
		Date:     "2026-07-19",
		Summary:  "summary",
		Markdown: md,
	})

	if got := report.Checks.HeadingHierarchy.HeadingCount; got != 2 {
		t.Fatalf("heading_count = %d, want 2 (## Second must still be seen)", got)
	}
}
