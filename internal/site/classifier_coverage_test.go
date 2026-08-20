package site

import "testing"

// ShouldIgnoreBrokenLinkTarget/CountKinds were previously untested directly
// despite being real production code paths: ShouldIgnoreBrokenLinkTarget
// gates internal/db's broken-link reconciliation and
// internal/tools/read/extended.go's inspect_rendered link check (a
// false-positive there wrongly flags a legitimate pagination/technical/home
// route as a broken link); CountKinds feeds get_sitemap/get_site_information's
// content-vs-taxonomy/section/other breakdown.

func TestShouldIgnoreBrokenLinkTargetIgnoresPaginationAndTechnicalAndHome(t *testing.T) {
	cases := []struct {
		name       string
		targetSlug string
		wantIgnore bool
	}{
		{"home route", "", true},
		{"pagination route", "posts/page/2", true},
		{"well-known technical route", ".well-known/security.txt", true},
		{"robots.txt", "robots.txt", true},
		{"single-segment page (no known roots, classifies as content)", "about", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldIgnoreBrokenLinkTarget(tc.targetSlug); got != tc.wantIgnore {
				t.Fatalf("ShouldIgnoreBrokenLinkTarget(%q) = %v, want %v", tc.targetSlug, got, tc.wantIgnore)
			}
		})
	}
}

func TestCountKindsBucketsEveryPageKind(t *testing.T) {
	pages := []Page{
		{Slug: "posts/hello"},  // KindArticle -> ContentPages
		{Slug: "about"},        // KindPage (no known section root) -> ContentPages
		{Slug: "tags"},         // KindTaxonomy -> TaxonomyPages
		{Slug: "posts"},        // KindSection (has children below) -> SectionPages
		{Slug: "posts/page/2"}, // KindPagination -> OtherDocuments
		{Slug: "robots.txt"},   // KindTechnical -> OtherDocuments
		{Slug: ""},             // KindHome -> OtherDocuments
	}
	c := NewClassifierFromPages(pages)
	counts := c.CountKinds(pages)

	if counts.ContentPages != 2 {
		t.Fatalf("ContentPages = %d, want 2 (posts/hello, about)", counts.ContentPages)
	}
	if counts.TaxonomyPages != 1 {
		t.Fatalf("TaxonomyPages = %d, want 1 (tags)", counts.TaxonomyPages)
	}
	if counts.SectionPages != 1 {
		t.Fatalf("SectionPages = %d, want 1 (posts)", counts.SectionPages)
	}
	if counts.OtherDocuments != 3 {
		t.Fatalf("OtherDocuments = %d, want 3 (pagination, technical, home)", counts.OtherDocuments)
	}
}

func TestCountKindsOnNilClassifier(t *testing.T) {
	var c *ContentClassifier
	counts := c.CountKinds([]Page{{Slug: "about"}, {Slug: "robots.txt"}})
	if counts.ContentPages != 1 || counts.OtherDocuments != 1 {
		t.Fatalf("CountKinds on nil classifier = %+v, want ContentPages=1 OtherDocuments=1", counts)
	}
}
