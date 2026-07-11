package site

import (
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// BacklinkEntry describes a page that links to a target page.
type BacklinkEntry struct {
	FromSlug  string `json:"from_slug"`
	FromTitle string `json:"from_title"`
	FromURL   string `json:"from_url"`
}

// backlinkCache lazily builds and caches the reverse-link map for a site index.
// Call invalidate() whenever the index is reloaded or mutated.
type backlinkCache struct {
	mu    sync.Mutex
	index map[string][]BacklinkEntry // normalized target slug → source pages
}

func (c *backlinkCache) getOrBuild(idx *Index) map[string][]BacklinkEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index != nil {
		return c.index
	}
	c.index = buildReverseMap(idx)
	return c.index
}

func (c *backlinkCache) invalidate() {
	c.mu.Lock()
	c.index = nil
	c.mu.Unlock()
}

func buildReverseMap(idx *Index) map[string][]BacklinkEntry {
	result := make(map[string][]BacklinkEntry)
	if idx == nil {
		return result
	}
	classifier := NewClassifier(idx)
	for _, page := range idx.ContentPages() {
		base, err := url.Parse(page.URL)
		if err != nil || base == nil {
			continue
		}
		seen := make(map[string]bool)
		for _, href := range extractLinksHTML(page.RawHTML) {
			ref, err := url.Parse(href)
			if err != nil {
				continue
			}
			if ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https" {
				continue
			}
			if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
				continue
			}
			target := base.ResolveReference(ref)
			if target.Host != "" && target.Host != base.Host {
				continue
			}
			targetSlug := normalizeSlug(target.Path)
			if seen[targetSlug] || targetSlug == page.Slug {
				continue
			}
			if targetPage, found := idx.GetBySlug(targetSlug); found && classifier.IsContent(*targetPage) {
				seen[targetSlug] = true
				result[targetSlug] = append(result[targetSlug], BacklinkEntry{
					FromSlug:  page.Slug,
					FromTitle: page.Title,
					FromURL:   page.URL,
				})
			}
		}
	}
	return result
}

func extractLinksHTML(rawHTML string) []string {
	if strings.TrimSpace(rawHTML) == "" {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil
	}
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if strings.EqualFold(a.Key, "href") {
					if v := strings.TrimSpace(a.Val); v != "" {
						links = append(links, v)
					}
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links
}
