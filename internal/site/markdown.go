package site

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// ExtractArticleHTML extracts the page's actual article body, preferring the
// narrowest reliable boundary and falling back to progressively coarser
// ones: an element with id="content" (the convention used by this site's
// theme — and common generally — for the body wrapper, deliberately
// excluding sibling chrome like the title, table of contents, post
// metadata, share buttons, and prev/next navigation that themes commonly
// place alongside it, not inside it, #432), then the first <article>, then
// <main>, then <body> with script/style/nav/header/footer tags stripped.
// Input is the body-level HTML already extracted from a full page.
func ExtractArticleHTML(rawHTML string) string {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return rawHTML
	}
	if content := findElementByID(doc, "content"); content != nil {
		return renderChildrenHTML(content)
	}
	if article := findElement(doc, "article"); article != nil {
		return renderChildrenHTML(article)
	}
	if main := findElement(doc, "main"); main != nil {
		return renderChildrenHTML(main)
	}
	body := findElement(doc, "body")
	if body == nil {
		return rawHTML
	}
	removeDescendants(body, "script", "style", "nav", "header", "footer")
	return renderChildrenHTML(body)
}

// findElementByID returns the first element in the tree whose id attribute
// equals id, or nil if none matches.
func findElementByID(n *html.Node, id string) *html.Node {
	var out *html.Node
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil || out != nil {
			return
		}
		if cur.Type == html.ElementNode && nodeAttr(cur, "id") == id {
			out = cur
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func renderChildrenHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var buf bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&buf, c)
	}
	return strings.TrimSpace(buf.String())
}

func removeDescendants(n *html.Node, tags ...string) {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}
	var prune func(*html.Node)
	prune = func(cur *html.Node) {
		for c := cur.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && tagSet[c.Data] {
				cur.RemoveChild(c)
			} else {
				prune(c)
			}
			c = next
		}
	}
	prune(n)
}

func ExtractMarkdown(rawHTML string) string {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return rawHTML
	}
	body := findElement(doc, "body")
	if body == nil {
		return rawHTML
	}
	return strings.TrimSpace(htmlBodyToMarkdown(body))
}

func htmlBodyToMarkdown(body *html.Node) string {
	if body == nil {
		return ""
	}
	var b strings.Builder
	walkMarkdown(&b, body)
	return strings.TrimSpace(b.String())
}

func walkMarkdown(b *strings.Builder, n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		switch n.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(n.Data[1] - '0')
			b.WriteString("\n\n" + strings.Repeat("#", level) + " ")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("\n\n")
			return
		case "p":
			b.WriteString("\n\n")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("\n\n")
			return
		case "br":
			b.WriteString("\n")
			return
		case "strong", "b":
			b.WriteString("**")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("**")
			return
		case "em", "i":
			b.WriteString("*")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("*")
			return
		case "code":
			if n.Parent != nil && n.Parent.Data == "pre" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walkMarkdown(b, c)
				}
				return
			}
			b.WriteString("`")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("`")
			return
		case "pre":
			b.WriteString("\n\n```\n")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("\n```\n\n")
			return
		case "a":
			href := nodeAttr(n, "href")
			b.WriteString("[")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("](" + href + ")")
			return
		case "ul":
			b.WriteString("\n\n")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "li" {
					b.WriteString("- ")
					for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
						walkMarkdown(b, gc)
					}
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
			return
		case "ol":
			b.WriteString("\n\n")
			i := 1
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "li" {
					fmt.Fprintf(b, "%d. ", i)
					for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
						walkMarkdown(b, gc)
					}
					b.WriteString("\n")
					i++
				}
			}
			b.WriteString("\n")
			return
		case "blockquote":
			b.WriteString("\n\n> ")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkMarkdown(b, c)
			}
			b.WriteString("\n\n")
			return
		case "hr":
			b.WriteString("\n\n---\n\n")
			return
		case "script", "style", "nav", "header", "footer":
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkMarkdown(b, c)
	}
}
