package site

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

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
