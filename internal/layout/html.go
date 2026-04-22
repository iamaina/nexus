package layout

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// skipTags lists HTML elements whose content should not be extracted.
// These are structural/navigation elements that add noise, not signal.
var skipTags = map[string]bool{
	"nav": true, "header": true, "footer": true, "aside": true,
	"script": true, "style": true, "noscript": true, "iframe": true,
	"button": true, "form": true, "input": true, "select": true,
}

// headingSize maps HTML heading levels to synthetic font sizes, matching the
// convention used by ExtractMarkdown so the downstream pipeline treats them identically.
var headingSize = map[string]float64{
	"h1": 24.0,
	"h2": 16.0,
	"h3": 13.2,
	"h4": 12.0, // H4+ treated as body text — not detected as headings
	"h5": 12.0,
	"h6": 12.0,
}

const (
	htmlBodySize = 12.0
	htmlLineH    = 14.0
	htmlParaGap  = 30.0
	htmlCodeH    = 12.0
)

// ExtractHTML converts an HTML document into []Span, preserving heading
// structure as synthetic font sizes so the layout pipeline can detect
// sections and build a proper heading tree.
//
// Links are not followed here — see CrawlAndIngest in the ingestion package.
func ExtractHTML(r io.Reader) ([]Span, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	var spans []Span
	y := 50.0

	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inPre bool) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)

			// Skip navigation and non-content elements entirely.
			if skipTags[tag] {
				return
			}

			// Block-level elements that introduce a paragraph break before and after.
			switch tag {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := strings.TrimSpace(nodeText(n))
				if text == "" {
					break
				}
				size := headingSize[tag]
				bold := "Helvetica-Bold"
				if size == htmlBodySize {
					bold = "Helvetica"
				}
				spans = append(spans, Span{
					Type:     "text",
					Text:     text,
					FontSize: size,
					FontName: bold,
					Page:     1,
					Y:        y,
				})
				y += htmlParaGap
				return // children already consumed by nodeText

			case "pre", "code":
				text := strings.TrimSpace(nodeText(n))
				if text == "" {
					break
				}
				for _, line := range strings.Split(text, "\n") {
					line = strings.TrimRight(line, " \t")
					if line == "" {
						y += htmlCodeH
						continue
					}
					spans = append(spans, Span{
						Type:     "text",
						Text:     line,
						FontSize: htmlBodySize,
						FontName: "Courier",
						Page:     1,
						Y:        y,
					})
					y += htmlCodeH
				}
				y += htmlParaGap
				return

			case "p", "li", "dt", "dd", "blockquote", "th", "td":
				text := strings.TrimSpace(nodeText(n))
				if text == "" {
					break
				}
				spans = append(spans, Span{
					Type:     "text",
					Text:     text,
					FontSize: htmlBodySize,
					FontName: "Helvetica",
					Page:     1,
					Y:        y,
				})
				y += htmlLineH
				y += htmlParaGap
				return

			case "br":
				y += htmlLineH
				return
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inPre)
		}
	}

	walk(doc, false)
	return spans, nil
}

// ExtractLinks returns all href values found in <a> tags within the document.
// Used by CrawlAndIngest to discover pages to crawl.
func ExtractLinks(r io.Reader) ([]string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					links = append(links, attr.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links, nil
}

// nodeText extracts all text content from a node and its descendants,
// collapsing whitespace.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	// collapse internal whitespace runs to a single space
	return strings.Join(strings.Fields(sb.String()), " ")
}
