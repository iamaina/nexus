// Package layout provides functions to build a hierarchical structure of sections
// from the detected headings and their associated paragraphs.
package layout

import (
	"fmt"
	"strings"
)

// BuildSections takes a list of Nodes (which represent the hierarchical
// structure of headings and their associated paragraphs) and builds a list of
// Sections. Each Section contains the title (heading text), content (joined
// paragraphs), heading level, and any child sections. This function is crucial
// for organizing the document's content into a structured format that can be
// easily navigated and analyzed.
func BuildSections(nodes []*Node) []Section {
	var sections []Section

	for _, n := range nodes {
		sections = append(sections, buildSectionRecursive(n))
	}

	return sections
}

func buildSectionRecursive(n *Node) Section {
	section := Section{
		Title:   n.Heading.Text,
		Content: n.Blocks,
		Level:   n.Heading.Level,
		Page:    n.Heading.Page,
	}

	for _, child := range n.Children {
		section.Children = append(section.Children, buildSectionRecursive(child))
	}

	return section
}

// PrintSections prints a debug representation of sections to stdout.
func PrintSections(sections []Section, indent, startPage, endPage int) {

	for _, s := range sections {
		inRange := true
		if startPage > 0 && s.Page < startPage {
			inRange = false
		}
		if endPage > 0 && s.Page > endPage {
			inRange = false
		}

		if inRange {
			prefix := strings.Repeat("  ", indent) + "="
			fmt.Println(prefix + " " + s.Title)

			for _, b := range s.Content {
				linePrefix := strings.Repeat("  ", indent+1)
				for _, l := range RenderBlock(b, linePrefix) {
					fmt.Println(l)
				}
			}
		}

		// always recurse into children (they may be in range even if parent isn't)
		PrintSections(s.Children, indent+1, startPage, endPage)
	}
}
