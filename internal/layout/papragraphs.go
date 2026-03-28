// This file contains the logic for building paragraphs from lines of text
// extracted from a PDF document.
package layout

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// BuildParagraphs takes a list of lines and the body font size to group lines
// into paragraphs based on their vertical proximity. It skips lines that are
// likely to be headings or code snippets and merges lines that are close to
// each other in the Y coordinate into a single paragraph. The resulting
// paragraphs can then be used for further analysis or output.
func BuildParagraphs(lines []Line, bodyFont float64) []Line {
	var paragraphs []Line

	var current *Line

	for i := range lines {
		l := &lines[i]

		if strings.TrimSpace(l.Text) == "" {
			continue
		}

		// skip headings
		if l.FontSize > bodyFont {
			continue
		}

		// skip code
		if hasCodeSpan(*l) {
			continue
		}

		// skip TOC lines
		if isTOCLine(l.Text) {
			continue
		}

		// skip likely TOC sections
		if isLikelyTOCSection(l, bodyFont) {
			continue
		}

		// start new paragraph
		if current == nil {
			copy := *l
			current = &copy
			continue
		}

		// merge if close vertically
		if math.Abs(l.Y-current.Y) < 12 {
			current.Text += " " + l.Text
			current.Y = l.Y
		} else {
			if strings.TrimSpace(current.Text) != "" {
				paragraphs = append(paragraphs, *current)
			}
			copy := *l
			current = &copy
		}
	}

	if current != nil && strings.TrimSpace(current.Text) != "" {
		paragraphs = append(paragraphs, *current)
	}

	return paragraphs
}

func AttachParagraphs(root []*Node, paragraphs []Line) {
	var flat []*Node

	// flatten tree (DFS)
	var walk func(nodes []*Node)
	walk = func(nodes []*Node) {
		for _, n := range nodes {
			flat = append(flat, n)
			walk(n.Children)
		}
	}
	walk(root)

	// sort by page + Y (top → bottom)
	sort.Slice(flat, func(i, j int) bool {
		if flat[i].Heading.Page == flat[j].Heading.Page {
			return flat[i].Heading.Y < flat[j].Heading.Y
		}
		return flat[i].Heading.Page < flat[j].Heading.Page
	})

	for _, p := range paragraphs {
		var target *Node

		for i := range flat {
			h := flat[i].Heading

			if h.Page > p.Page {
				break
			}

			if h.Page == p.Page && h.Y > p.Y {
				break
			}

			target = flat[i]
		}

		if target != nil {
			target.Paragraphs = append(target.Paragraphs, p.Text)
		}
	}
}

func isLikelyTOCSection(l *Line, bodyFont float64) bool {
	text := strings.TrimSpace(l.Text)

	// only care about early pages
	if l.Page > 5 {
		return false
	}

	// numbered pattern (1. Something, 2.3 Something)
	if regexp.MustCompile(`^\d+(\.\d+)*\.\s`).MatchString(text) {
		return true
	}

	// short lines that look like titles
	if len(text) < 80 && l.FontSize <= bodyFont {
		return true
	}

	return false
}

func isTOCLine(text string) bool {
	text = strings.TrimSpace(text)

	// dotted leaders + page number
	if strings.Contains(text, ". .") || strings.Contains(text, " . .") {
		return true
	}

	// ends with number (page reference)
	if regexp.MustCompile(`\s\d+$`).MatchString(text) {
		return true
	}

	return false
}
