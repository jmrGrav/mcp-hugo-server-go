package site

import (
	"strconv"
	"strings"
)

type PageKind int8

const (
	KindArticle PageKind = iota
	KindSection
	KindTaxonomy
	KindPagination
	KindHome
	KindPage
	KindTechnical
)

type ContentClassifier struct {
	sectionRoots  map[string]struct{}
	taxonomyRoots map[string]struct{}
}

type PageKindCounts struct {
	ContentPages   int
	TaxonomyPages  int
	SectionPages   int
	OtherDocuments int
}

func NewClassifier(idx *Index) *ContentClassifier {
	var pages []Page
	if idx != nil {
		pages = make([]Page, 0, len(idx.entries))
		for _, e := range idx.entries {
			pages = append(pages, e.page)
		}
	}
	return NewClassifierFromPages(pages)
}

func NewClassifierFromPages(pages []Page) *ContentClassifier {
	c := &ContentClassifier{
		sectionRoots:  map[string]struct{}{},
		taxonomyRoots: map[string]struct{}{},
	}
	for _, root := range []string{"tags", "categories", "series"} {
		c.taxonomyRoots[root] = struct{}{}
	}
	if len(pages) > 0 {
		childRoots := map[string]struct{}{}
		for _, page := range pages {
			parts := slugParts(page.Slug)
			if len(parts) > 1 {
				childRoots[parts[0]] = struct{}{}
			}
		}
		for _, page := range pages {
			parts := slugParts(page.Slug)
			if len(parts) == 1 {
				switch parts[0] {
				case "tags", "categories", "series":
					c.taxonomyRoots[parts[0]] = struct{}{}
				default:
					if _, hasChildren := childRoots[parts[0]]; hasChildren {
						c.sectionRoots[parts[0]] = struct{}{}
					}
				}
			}
		}
	}
	c.sectionRoots["posts"] = struct{}{}
	return c
}

func (c *ContentClassifier) Classify(p Page) PageKind {
	parts := slugParts(p.Slug)
	if len(parts) == 0 {
		return KindHome
	}
	parts = stripLanguagePrefix(parts)
	if isTechnicalSlugParts(parts) {
		return KindTechnical
	}
	if isPaginationParts(parts) {
		return KindPagination
	}
	if c == nil {
		c = NewClassifier(nil)
	}
	if _, ok := c.taxonomyRoots[parts[0]]; ok {
		return KindTaxonomy
	}
	if len(parts) == 1 {
		if _, ok := c.sectionRoots[parts[0]]; ok {
			return KindSection
		}
		return KindPage
	}
	if parts[0] == "posts" {
		return KindArticle
	}
	return KindPage
}

func (c *ContentClassifier) IsContent(p Page) bool {
	switch c.Classify(p) {
	case KindArticle, KindPage:
		return true
	default:
		return false
	}
}

// ShouldIgnoreBrokenLinkTarget reports whether a link to targetSlug should
// never be flagged as broken, regardless of whether the index has a record
// of it. Hugo's paginated listing pages (/en/page/2/, /en/page/3/, ...)
// legitimately canonicalize back to page 1 for SEO, which makes the
// indexer's own duplicate-slug collapse (NewIndex, #1099's alias-dedup
// mechanism) drop them from bySlug even though they are real, independently
// servable URLs (#1101) — so a link-target existence check must never treat
// a pagination-shaped target as "missing" in the first place, the same way
// it already skips taxonomy/section/technical targets that this index
// doesn't track individually. Two independent broken-link implementations
// (internal/tools/read's in-memory scan and internal/db's SQL-backed link
// graph) both need this exact policy; #1104 was a live incident caused by
// those two implementations drifting apart, so this is the single source of
// truth for both from now on.
func ShouldIgnoreBrokenLinkTarget(targetSlug string) bool {
	return !NewClassifier(nil).IsContent(Page{Slug: targetSlug})
}

func (c *ContentClassifier) IsArticle(p Page) bool {
	return c.Classify(p) == KindArticle
}

func (c *ContentClassifier) IsTechnical(p Page) bool {
	return c.Classify(p) == KindTechnical
}

func (c *ContentClassifier) CountKinds(pages []Page) PageKindCounts {
	var counts PageKindCounts
	for _, p := range pages {
		switch c.Classify(p) {
		case KindArticle, KindPage:
			counts.ContentPages++
		case KindTaxonomy:
			counts.TaxonomyPages++
		case KindSection:
			counts.SectionPages++
		default:
			counts.OtherDocuments++
		}
	}
	return counts
}

func (idx *Index) classifier() *ContentClassifier {
	if idx == nil {
		return NewClassifier(nil)
	}
	if idx.contentClassifier != nil {
		return idx.contentClassifier
	}
	// Fallback for zero-value Index (tests that construct Index{} directly).
	return NewClassifier(idx)
}

// Classifier returns idx's cached content classifier (rebuilt automatically
// on Reload — see index.go). Prefer this over calling NewClassifier(idx)
// directly when a caller already has an *Index: NewClassifier walks every
// indexed page to build its section/taxonomy maps, so reconstructing it
// per-call is O(pages) on every use; this accessor is O(1) after the first
// build.
func (idx *Index) Classifier() *ContentClassifier {
	if idx == nil {
		return NewClassifier(nil)
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.classifier()
}

// contentPagesLocked builds the content page list. Callers must hold idx.mu.RLock.
func (idx *Index) contentPagesLocked() []Page {
	classifier := idx.classifier()
	out := make([]Page, 0, len(idx.entries))
	for _, e := range idx.entries {
		if classifier.IsContent(e.page) {
			out = append(out, e.page)
		}
	}
	return out
}

func (idx *Index) ContentPages() []Page {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.contentPagesLocked()
}

func slugParts(slug string) []string {
	slug = normalizeSlug(slug)
	slug = strings.Trim(slug, "/")
	if slug == "" {
		return nil
	}
	return strings.Split(slug, "/")
}

func stripLanguagePrefix(parts []string) []string {
	if len(parts) < 2 {
		return parts
	}
	if !looksLikeLanguageCode(parts[0]) {
		return parts
	}
	return parts[1:]
}

func looksLikeLanguageCode(v string) bool {
	if len(v) != 2 && len(v) != 5 {
		return false
	}
	for i, r := range v {
		if i == 2 {
			if r != '-' && r != '_' {
				return false
			}
			continue
		}
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isTechnicalSlugParts(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	if parts[0] == ".well-known" {
		return true
	}
	if len(parts) != 1 {
		return false
	}
	switch parts[0] {
	case "robots.txt", "security.txt", "llms.txt", "humans.txt", "ai.txt",
		"404.html", "404", "500.html", "500":
		return true
	default:
		return false
	}
}

func isPaginationParts(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	if parts[len(parts)-2] != "page" {
		return false
	}
	_, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil
}
