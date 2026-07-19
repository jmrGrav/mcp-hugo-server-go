package aireadiness

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"

	DefaultSectionLengthThreshold   = 2500
	DefaultParagraphLengthThreshold = 900
	DefaultLinkDensityBodyThreshold = 2000
	DefaultCitationBodyThreshold    = 3000
	DefaultCitationMinimumHeadings  = 2
)

type Document struct {
	Title       string
	Date        string
	Summary     string
	Description string
	Tags        []string
	Categories  []string
	Markdown    string
}

type HeadingJump struct {
	FromLevel int    `json:"from_level"`
	ToLevel   int    `json:"to_level"`
	FromText  string `json:"from_text"`
	ToText    string `json:"to_text"`
	Line      int    `json:"line"`
}

type HeadingHierarchyCheck struct {
	Status         string        `json:"status"`
	HeadingCount   int           `json:"heading_count"`
	MalformedLines []int         `json:"malformed_lines,omitempty"`
	Jumps          []HeadingJump `json:"jumps,omitempty"`
}

type SectionLengthFinding struct {
	Heading    string `json:"heading"`
	Characters int    `json:"characters"`
}

type SectionLengthsCheck struct {
	Status              string                 `json:"status"`
	ThresholdCharacters int                    `json:"threshold_characters"`
	LongestSectionChars int                    `json:"longest_section_characters"`
	OffendingSections   []SectionLengthFinding `json:"offending_sections,omitempty"`
}

type ParagraphLengthFinding struct {
	Preview    string `json:"preview"`
	Characters int    `json:"characters"`
}

type ParagraphLengthsCheck struct {
	Status                string                   `json:"status"`
	ThresholdCharacters   int                      `json:"threshold_characters"`
	LongestParagraphChars int                      `json:"longest_paragraph_characters"`
	OffendingParagraphs   []ParagraphLengthFinding `json:"offending_paragraphs,omitempty"`
}

type MetadataPresenceCheck struct {
	Status             string   `json:"status"`
	TitlePresent       bool     `json:"title_present"`
	DatePresent        bool     `json:"date_present"`
	SummaryPresent     bool     `json:"summary_present"`
	DescriptionPresent bool     `json:"description_present"`
	TagsCount          int      `json:"tags_count"`
	CategoriesCount    int      `json:"categories_count"`
	MissingRequired    []string `json:"missing_required,omitempty"`
	MissingRecommended []string `json:"missing_recommended,omitempty"`
}

type InternalLinkDensityCheck struct {
	Status             string `json:"status"`
	Evaluated          bool   `json:"evaluated"`
	BodyCharacters     int    `json:"body_characters"`
	ThresholdBodyChars int    `json:"threshold_body_characters"`
	InternalLinkCount  int    `json:"internal_link_count"`
}

type CitationStructureCheck struct {
	Status              string `json:"status"`
	BodyCharacters      int    `json:"body_characters"`
	ThresholdBodyChars  int    `json:"threshold_body_characters"`
	HeadingCount        int    `json:"heading_count"`
	MinimumHeadingCount int    `json:"minimum_heading_count"`
}

type Checks struct {
	HeadingHierarchy    HeadingHierarchyCheck    `json:"heading_hierarchy"`
	SectionLengths      SectionLengthsCheck      `json:"section_lengths"`
	ParagraphLengths    ParagraphLengthsCheck    `json:"paragraph_lengths"`
	MetadataPresence    MetadataPresenceCheck    `json:"metadata_presence"`
	InternalLinkDensity InternalLinkDensityCheck `json:"internal_link_density"`
	CitationStructure   CitationStructureCheck   `json:"citation_structure"`
}

type Report struct {
	Status      string   `json:"status"`
	Checks      Checks   `json:"checks"`
	Warnings    []string `json:"warnings"`
	Suggestions []string `json:"suggestions"`
}

type headingInfo struct {
	Level int
	Text  string
	Line  int
}

type sectionInfo struct {
	Heading string
	Chars   int
}

type paragraphInfo struct {
	Text  string
	Chars int
}

type markdownStats struct {
	Headings       []headingInfo
	MalformedLines []int
	Sections       []sectionInfo
	Paragraphs     []paragraphInfo
	InternalLinks  int
	BodyChars      int
}

var (
	atxHeadingRE       = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+[ \t]*)?$`)
	markdownLinkRE     = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	hugoRefShortcodeRE = regexp.MustCompile(`\{\{[%<]\s*(?:relref|ref)\b[^}]*[%>]\}\}`)
)

func Analyze(doc Document) Report {
	stats := scanMarkdown(doc.Markdown)
	checks := Checks{
		HeadingHierarchy:    analyzeHeadingHierarchy(stats),
		SectionLengths:      analyzeSectionLengths(stats),
		ParagraphLengths:    analyzeParagraphLengths(stats),
		MetadataPresence:    analyzeMetadataPresence(doc),
		InternalLinkDensity: analyzeInternalLinkDensity(stats),
		CitationStructure:   analyzeCitationStructure(stats),
	}
	report := Report{
		Status: StatusPass,
		Checks: checks,
	}
	for _, status := range []string{
		checks.HeadingHierarchy.Status,
		checks.SectionLengths.Status,
		checks.ParagraphLengths.Status,
		checks.MetadataPresence.Status,
		checks.InternalLinkDensity.Status,
		checks.CitationStructure.Status,
	} {
		report.Status = mergeStatus(report.Status, status)
	}
	addCheckMessages(&report, checks)
	if report.Warnings == nil {
		report.Warnings = []string{}
	}
	if report.Suggestions == nil {
		report.Suggestions = []string{}
	}
	return report
}

func analyzeHeadingHierarchy(stats markdownStats) HeadingHierarchyCheck {
	check := HeadingHierarchyCheck{
		Status:       StatusPass,
		HeadingCount: len(stats.Headings),
	}
	if len(stats.MalformedLines) > 0 {
		check.Status = StatusFail
		check.MalformedLines = append([]int(nil), stats.MalformedLines...)
	}
	for i := 1; i < len(stats.Headings); i++ {
		prev := stats.Headings[i-1]
		cur := stats.Headings[i]
		if cur.Level-prev.Level > 1 {
			check.Status = mergeStatus(check.Status, StatusWarn)
			check.Jumps = append(check.Jumps, HeadingJump{
				FromLevel: prev.Level,
				ToLevel:   cur.Level,
				FromText:  prev.Text,
				ToText:    cur.Text,
				Line:      cur.Line,
			})
		}
	}
	return check
}

func analyzeSectionLengths(stats markdownStats) SectionLengthsCheck {
	check := SectionLengthsCheck{
		Status:              StatusPass,
		ThresholdCharacters: DefaultSectionLengthThreshold,
	}
	for _, section := range stats.Sections {
		if section.Chars > check.LongestSectionChars {
			check.LongestSectionChars = section.Chars
		}
		if section.Chars > DefaultSectionLengthThreshold {
			check.Status = mergeStatus(check.Status, StatusWarn)
			check.OffendingSections = append(check.OffendingSections, SectionLengthFinding{
				Heading:    section.Heading,
				Characters: section.Chars,
			})
		}
	}
	return check
}

func analyzeParagraphLengths(stats markdownStats) ParagraphLengthsCheck {
	check := ParagraphLengthsCheck{
		Status:              StatusPass,
		ThresholdCharacters: DefaultParagraphLengthThreshold,
	}
	for _, paragraph := range stats.Paragraphs {
		if paragraph.Chars > check.LongestParagraphChars {
			check.LongestParagraphChars = paragraph.Chars
		}
		if paragraph.Chars > DefaultParagraphLengthThreshold {
			check.Status = mergeStatus(check.Status, StatusWarn)
			check.OffendingParagraphs = append(check.OffendingParagraphs, ParagraphLengthFinding{
				Preview:    preview(paragraph.Text, 96),
				Characters: paragraph.Chars,
			})
		}
	}
	return check
}

func analyzeMetadataPresence(doc Document) MetadataPresenceCheck {
	check := MetadataPresenceCheck{
		Status:             StatusPass,
		TitlePresent:       strings.TrimSpace(doc.Title) != "",
		DatePresent:        strings.TrimSpace(doc.Date) != "",
		SummaryPresent:     strings.TrimSpace(doc.Summary) != "",
		DescriptionPresent: strings.TrimSpace(doc.Description) != "",
		TagsCount:          len(doc.Tags),
		CategoriesCount:    len(doc.Categories),
	}
	if !check.TitlePresent {
		check.MissingRequired = append(check.MissingRequired, "title")
	}
	if !check.DatePresent {
		check.MissingRequired = append(check.MissingRequired, "date")
	}
	if len(check.MissingRequired) > 0 {
		check.Status = StatusFail
	}
	if !check.SummaryPresent && !check.DescriptionPresent {
		check.MissingRecommended = append(check.MissingRecommended, "summary_or_description")
		check.Status = mergeStatus(check.Status, StatusWarn)
	}
	return check
}

func analyzeInternalLinkDensity(stats markdownStats) InternalLinkDensityCheck {
	check := InternalLinkDensityCheck{
		Status:             StatusPass,
		ThresholdBodyChars: DefaultLinkDensityBodyThreshold,
		BodyCharacters:     stats.BodyChars,
		InternalLinkCount:  stats.InternalLinks,
	}
	if stats.BodyChars < DefaultLinkDensityBodyThreshold {
		return check
	}
	check.Evaluated = true
	if stats.InternalLinks == 0 {
		check.Status = StatusWarn
	}
	return check
}

func analyzeCitationStructure(stats markdownStats) CitationStructureCheck {
	check := CitationStructureCheck{
		Status:              StatusPass,
		BodyCharacters:      stats.BodyChars,
		ThresholdBodyChars:  DefaultCitationBodyThreshold,
		HeadingCount:        len(stats.Headings),
		MinimumHeadingCount: DefaultCitationMinimumHeadings,
	}
	if stats.BodyChars >= DefaultCitationBodyThreshold && len(stats.Headings) < DefaultCitationMinimumHeadings {
		check.Status = StatusWarn
	}
	return check
}

func addCheckMessages(report *Report, checks Checks) {
	if checks.HeadingHierarchy.Status == StatusFail {
		report.Warnings = append(report.Warnings, "Malformed heading syntax prevents reliable section extraction.")
		report.Suggestions = append(report.Suggestions, "Normalize Markdown headings to valid ATX syntax before running agent transformations.")
	} else if checks.HeadingHierarchy.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "Heading hierarchy contains skipped levels.")
		report.Suggestions = append(report.Suggestions, "Insert intermediate headings instead of jumping levels, for example H2 directly to H4.")
	}
	if checks.SectionLengths.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "At least one section exceeds the stable section-length threshold without subdivision.")
		report.Suggestions = append(report.Suggestions, "Split long sections with subheadings before they exceed 2500 characters.")
	}
	if checks.ParagraphLengths.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "At least one paragraph exceeds the stable paragraph-length threshold.")
		report.Suggestions = append(report.Suggestions, "Split oversized paragraphs into smaller blocks that are easier to segment and cite.")
	}
	if checks.MetadataPresence.Status == StatusFail {
		report.Warnings = append(report.Warnings, "Required source metadata is missing.")
		report.Suggestions = append(report.Suggestions, "Add the required title/date fields before relying on this page in autonomous edit workflows.")
	} else if checks.MetadataPresence.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "Recommended summary metadata is missing.")
		report.Suggestions = append(report.Suggestions, "Add a summary or description so downstream agents do not have to infer one from the body.")
	}
	if checks.InternalLinkDensity.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "This long page has no internal links.")
		report.Suggestions = append(report.Suggestions, "Add at least one internal link so the page connects back into the site graph.")
	}
	if checks.CitationStructure.Status == StatusWarn {
		report.Warnings = append(report.Warnings, "The page is long but exposes too few headings for stable subsection citation.")
		report.Suggestions = append(report.Suggestions, "Add more section headings so agents can cite and transform smaller subsections safely.")
	}
}

func scanMarkdown(md string) markdownStats {
	lines := strings.Split(md, "\n")
	var (
		stats             markdownStats
		inFence           bool
		fenceMarker       string
		currentSection    = "(lead)"
		currentSectionLen int
		paragraphLines    []string
		plainBuilder      strings.Builder
	)

	flushSection := func() {
		stats.Sections = append(stats.Sections, sectionInfo{
			Heading: currentSection,
			Chars:   currentSectionLen,
		})
		currentSectionLen = 0
	}
	flushParagraph := func() {
		if len(paragraphLines) == 0 {
			return
		}
		text := strings.Join(paragraphLines, " ")
		text = strings.TrimSpace(text)
		if text != "" {
			stats.Paragraphs = append(stats.Paragraphs, paragraphInfo{
				Text:  text,
				Chars: utf8.RuneCountInString(text),
			})
		}
		paragraphLines = paragraphLines[:0]
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, ok := fenceBoundary(trimmed); ok {
			flushParagraph()
			if inFence && marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			} else if !inFence {
				inFence = true
				fenceMarker = marker
			}
			continue
		}
		if inFence {
			continue
		}

		if trimmed != "" {
			plainBuilder.WriteString(trimmed)
			plainBuilder.WriteByte('\n')
			currentSectionLen += utf8.RuneCountInString(trimmed)
		}

		if heading, ok, malformed := parseHeading(trimmed, i+1); ok {
			flushParagraph()
			flushSection()
			stats.Headings = append(stats.Headings, heading)
			currentSection = heading.Text
			continue
		} else if malformed {
			stats.MalformedLines = append(stats.MalformedLines, i+1)
			flushParagraph()
			continue
		}

		if trimmed == "" || isNonParagraphLine(trimmed) {
			flushParagraph()
			continue
		}
		paragraphLines = append(paragraphLines, trimmed)
	}

	flushParagraph()
	flushSection()

	plain := plainBuilder.String()
	stats.BodyChars = utf8.RuneCountInString(strings.TrimSpace(plain))
	stats.InternalLinks = countInternalLinks(plain)
	return stats
}

func parseHeading(trimmed string, line int) (headingInfo, bool, bool) {
	if trimmed == "" || !strings.HasPrefix(trimmed, "#") {
		return headingInfo{}, false, false
	}
	m := atxHeadingRE.FindStringSubmatch(trimmed)
	if m == nil {
		return headingInfo{}, false, true
	}
	return headingInfo{
		Level: len(m[1]),
		Text:  strings.TrimSpace(m[2]),
		Line:  line,
	}, true, false
}

func fenceBoundary(trimmed string) (string, bool) {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```", true
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~", true
	default:
		return "", false
	}
}

func isNonParagraphLine(trimmed string) bool {
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "|") {
		return true
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return true
	}
	if len(trimmed) >= 3 && (strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "***")) {
		return true
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			break
		}
		if i+1 < len(trimmed) && trimmed[i+1] == '.' {
			return true
		}
	}
	return false
}

func countInternalLinks(md string) int {
	count := 0
	for _, match := range markdownLinkRE.FindAllStringSubmatch(md, -1) {
		target := strings.TrimSpace(match[1])
		if isInternalLinkTarget(target) {
			count++
		}
	}
	count += len(hugoRefShortcodeRE.FindAllString(md, -1))
	return count
}

func isInternalLinkTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "#") {
		return false
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") {
		return true
	}
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "tel:") {
		return false
	}
	return false
}

func preview(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes]) + "..."
}

func mergeStatus(current, next string) string {
	if current == StatusFail || next == StatusPass || next == "" {
		return current
	}
	if next == StatusFail {
		return StatusFail
	}
	if current == StatusPass && next == StatusWarn {
		return StatusWarn
	}
	return current
}
