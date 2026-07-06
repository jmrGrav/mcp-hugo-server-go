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

func (c *ContentClassifier) IsArticle(p Page) bool {
	return c.Classify(p) == KindArticle
}

func (c *ContentClassifier) IsTechnical(p Page) bool {
	return c.Classify(p) == KindTechnical
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

func (idx *Index) ContentPages() []Page {
	if idx == nil {
		return nil
	}
	classifier := idx.classifier()
	out := make([]Page, 0, len(idx.entries))
	for _, e := range idx.entries {
		if classifier.IsContent(e.page) {
			out = append(out, e.page)
		}
	}
	return out
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
