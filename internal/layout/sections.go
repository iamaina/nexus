// Package layout provides functions to build a hierarchical structure of sections
// from the detected headings and their associated paragraphs.
package layout

import "strings"

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
	var parts []string

	for _, b := range n.Blocks {
		switch b.Type {
		case BlockParagraph:
			parts = append(parts, b.Text)

		case BlockCode:
			parts = append(parts, "\n[code]\n"+b.Text+"\n")
		case BlockImage:
			parts = append(parts, "\n[image]\n")
		}
	}

	content := strings.Join(parts, "\n")

	section := Section{
		Title:   n.Heading.Text,
		Content: content,
		Level:   n.Heading.Level,
		Page:    n.Heading.Page,
	}

	for _, child := range n.Children {
		section.Children = append(section.Children, buildSectionRecursive(child))
	}

	return section
}

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
			println(prefix + " " + s.Title)

			content := strings.TrimSpace(s.Content)
			if content != "" {
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					println(strings.Repeat("  ", indent+1) + truncate(line, len(line)))
				}
			}
		}

		// always recurse into children (they may be in range even if parent isn't)
		PrintSections(s.Children, indent+1, startPage, endPage)
	}
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
