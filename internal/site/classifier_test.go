package site

import "testing"

func TestContentClassifierClassifiesHugoGeneratedPages(t *testing.T) {
	idx := &Index{
		entries: []entry{
			{page: Page{Slug: "/"}},
			{page: Page{Slug: "/posts/"}},
			{page: Page{Slug: "/posts/foo/"}},
			{page: Page{Slug: "/about/"}},
			{page: Page{Slug: "/tags/go/"}},
			{page: Page{Slug: "/categories/security/"}},
			{page: Page{Slug: "/en/tags/webhook/"}},
			{page: Page{Slug: "/fr/categories/securite/"}},
			{page: Page{Slug: "/fr/posts/bonjour/"}},
			{page: Page{Slug: "/page/2/"}},
			{page: Page{Slug: "/en/page/2/"}},
			{page: Page{Slug: "/posts/page/2/"}},
			{page: Page{Slug: "/robots.txt"}},
			{page: Page{Slug: "/security.txt"}},
			{page: Page{Slug: "/.well-known/security.txt"}},
		},
	}
	classifier := NewClassifier(idx)

	tests := []struct {
		name      string
		slug      string
		wantKind  PageKind
		content   bool
		article   bool
		technical bool
	}{
		{name: "home", slug: "/", wantKind: KindHome},
		{name: "posts section", slug: "/posts/", wantKind: KindSection},
		{name: "article", slug: "/posts/foo/", wantKind: KindArticle, content: true, article: true},
		{name: "normal page", slug: "/about/", wantKind: KindPage, content: true},
		{name: "tag taxonomy", slug: "/tags/go/", wantKind: KindTaxonomy},
		{name: "category taxonomy", slug: "/categories/security/", wantKind: KindTaxonomy},
		{name: "language-prefixed tag taxonomy", slug: "/en/tags/webhook/", wantKind: KindTaxonomy},
		{name: "language-prefixed category taxonomy", slug: "/fr/categories/securite/", wantKind: KindTaxonomy},
		{name: "language-prefixed article", slug: "/fr/posts/bonjour/", wantKind: KindArticle, content: true, article: true},
		{name: "root pagination", slug: "/page/2/", wantKind: KindPagination},
		{name: "language-prefixed pagination", slug: "/en/page/2/", wantKind: KindPagination},
		{name: "section pagination", slug: "/posts/page/2/", wantKind: KindPagination},
		{name: "robots", slug: "/robots.txt", wantKind: KindTechnical, technical: true},
		{name: "security", slug: "/security.txt", wantKind: KindTechnical, technical: true},
		{name: "well-known", slug: "/.well-known/security.txt", wantKind: KindTechnical, technical: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := Page{Slug: tt.slug}
			if got := classifier.Classify(page); got != tt.wantKind {
				t.Fatalf("Classify(%q) = %v, want %v", tt.slug, got, tt.wantKind)
			}
			if got := classifier.IsContent(page); got != tt.content {
				t.Fatalf("IsContent(%q) = %v, want %v", tt.slug, got, tt.content)
			}
			if got := classifier.IsArticle(page); got != tt.article {
				t.Fatalf("IsArticle(%q) = %v, want %v", tt.slug, got, tt.article)
			}
			if got := classifier.IsTechnical(page); got != tt.technical {
				t.Fatalf("IsTechnical(%q) = %v, want %v", tt.slug, got, tt.technical)
			}
		})
	}
}

func TestContentPagesExcludeTaxonomyPaginationSectionsAndTechnicalFiles(t *testing.T) {
	idx := &Index{
		entries: []entry{
			{page: Page{Slug: "/", Date: "2026-07-01"}},
			{page: Page{Slug: "/posts/", Date: "2026-07-02"}},
			{page: Page{Slug: "/posts/foo/", Date: "2026-07-03"}},
			{page: Page{Slug: "/about/", Date: "2026-07-04"}},
			{page: Page{Slug: "/tags/go/", Date: "2026-07-05"}},
			{page: Page{Slug: "/categories/security/", Date: "2026-07-06"}},
			{page: Page{Slug: "/en/tags/webhook/", Date: "2026-07-06"}},
			{page: Page{Slug: "/fr/categories/securite/", Date: "2026-07-06"}},
			{page: Page{Slug: "/fr/posts/bonjour/", Date: "2026-07-06"}},
			{page: Page{Slug: "/page/2/", Date: "2026-07-07"}},
			{page: Page{Slug: "/en/page/2/", Date: "2026-07-07"}},
			{page: Page{Slug: "/robots.txt", Date: "2026-07-08"}},
		},
	}

	got := idx.ContentPages()
	if len(got) != 3 {
		t.Fatalf("ContentPages() len = %d, want 3: %#v", len(got), got)
	}
	if got[0].Slug != "/posts/foo/" || got[1].Slug != "/about/" || got[2].Slug != "/fr/posts/bonjour/" {
		t.Fatalf("ContentPages() = %#v", got)
	}
}
