package web

import (
	"strings"

	"golang.org/x/net/html"
)

// dropped elements never contribute text: script and style hold code, and
// nav, header, footer, and aside hold the same boilerplate on every page of
// a site.
var dropped = map[string]bool{
	"aside": true, "footer": true, "header": true, "nav": true,
	"noscript": true, "script": true, "style": true, "svg": true, "template": true,
}

// blocks end a line when they close, so a document's structure survives as
// paragraphs rather than arriving as one run-on line.
var blocks = map[string]bool{
	"article": true, "blockquote": true, "br": true, "div": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "li": true,
	"p": true, "pre": true, "section": true, "table": true, "tr": true,
}

// ToText reduces a document to its title and readable text. Anything that
// is not HTML is returned as it arrived, since a plain-text or JSON
// response is already the thing the caller wanted.
func ToText(body, contentType string) (string, string) {
	if !strings.Contains(strings.ToLower(contentType), "html") {
		return "", strings.TrimSpace(body)
	}

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return "", strings.TrimSpace(body)
	}

	var (
		title string
		b     strings.Builder
	)

	var walk func(*html.Node)

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && dropped[n.Data] {
			return
		}

		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			title = strings.TrimSpace(textOf(n))

			return
		}

		if n.Type == html.TextNode {
			if trimmed := strings.TrimSpace(n.Data); trimmed != "" {
				b.WriteString(trimmed + " ")
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		if n.Type == html.ElementNode && blocks[n.Data] {
			b.WriteString("\n")
		}
	}

	walk(doc)

	return title, collapse(b.String())
}

func textOf(n *html.Node) string {
	var b strings.Builder

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}

	return b.String()
}

// collapse squeezes the runs of blank lines and trailing spaces the walk
// leaves behind, so the text costs what it says rather than what the markup
// did.
func collapse(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))

	blank := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blank = true

			continue
		}

		if blank && len(out) > 0 {
			out = append(out, "")
		}

		blank = false

		out = append(out, trimmed)
	}

	return strings.Join(out, "\n")
}
