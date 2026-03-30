// This file contains the logic for building paragraphs from lines of text
// extracted from a PDF document.
package layout

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// BuildBlocks takes a list of lines and the body font size, and builds a list
// of blocks, which can be either paragraphs or code snippets. The function uses
// heuristics to determine whether a line is part of a code block (e.g., based
// on font, presence of code-like characters) or a regular paragraph. It then
// groups lines into blocks based on their proximity in the Y coordinate and
// page number, allowing us to reconstruct the logical structure of the
// document's content.
func BuildBlocks(lines []Line, bodyFont float64) []Block {
	var blocks []Block
	var paragraphBuffer *Block
	var codeBuffer *Block

	for i := range lines {
		l := &lines[i]

		text := strings.TrimSpace(l.Text)
		if text == "" ||
			isTOCLine(text) ||
			isPageNumber(text) {
			continue
		}

		// 🟦 CODE DETECTION
		if isCodeLine(*l) {
			// flush paragraph BEFORE entering code
			if paragraphBuffer != nil {
				blocks = append(blocks, *paragraphBuffer)
				paragraphBuffer = nil
			}

			if codeBuffer == nil {
				codeBuffer = &Block{
					Type: BlockCode,
					Text: text,
					Page: l.Page,
					Y:    l.Y,
				}
			} else {
				// same block → append with newline
				if l.Page == codeBuffer.Page && math.Abs(l.Y-codeBuffer.Y) < 14 {
					codeBuffer.Text += "\n" + text
					codeBuffer.Y = l.Y
				} else {
					// new code block
					blocks = append(blocks, *codeBuffer)
					codeBuffer = &Block{
						Type: BlockCode,
						Text: text,
						Page: l.Page,
						Y:    l.Y,
					}
				}
			}

			continue
		}

		// leaving code → flush it
		if codeBuffer != nil {
			blocks = append(blocks, *codeBuffer)
			codeBuffer = nil
		}

		// skip headings
		if l.FontSize > bodyFont {
			continue
		}

		// 🟩 PARAGRAPH
		if paragraphBuffer == nil {
			paragraphBuffer = &Block{
				Type: BlockParagraph,
				Text: text,
				Page: l.Page,
				Y:    l.Y,
			}
			continue
		}

		samePage := l.Page == paragraphBuffer.Page
		closeY := math.Abs(l.Y-paragraphBuffer.Y) < 12

		if samePage && closeY {
			paragraphBuffer.Text += " " + text
			paragraphBuffer.Y = l.Y
		} else {
			// flush current paragraph and start new one
			blocks = append(blocks, *paragraphBuffer)
			paragraphBuffer = &Block{
				Type: BlockParagraph,
				Text: text,
				Page: l.Page,
				Y:    l.Y,
			}
		}
	}

	if paragraphBuffer != nil {
		blocks = append(blocks, *paragraphBuffer)
	}

	if codeBuffer != nil {
		blocks = append(blocks, *codeBuffer)
	}

	return blocks
}

// AttachBlocks takes a tree of headings and a list of blocks and
// attaches each block to the nearest preceding heading based on page number
// and Y coordinate. This function first flattens the tree of headings into a
// list, sorts the headings by their position in the document, and then iterates
// through the blocks to find the appropriate heading to attach each
// block to. This allows us to build a more complete representation of the
// document's structure, including both its hierarchy and content.
func AttachBlocks(tree []*Node, blocks []Block) {
	var flat []*Node

	// flatten
	var walk func(nodes []*Node)
	walk = func(nodes []*Node) {
		for _, n := range nodes {
			flat = append(flat, n)
			walk(n.Children)
		}
	}
	walk(tree)

	// sort headings (FIXED ORDER)
	sort.Slice(flat, func(i, j int) bool {
		if flat[i].Heading.Page == flat[j].Heading.Page {
			return flat[i].Heading.Y < flat[j].Heading.Y
		}
		return flat[i].Heading.Page < flat[j].Heading.Page
	})

	// attach blocks
	for _, b := range blocks {
		var target *Node

		for i := range flat {
			h := flat[i].Heading

			if h.Page > b.Page {
				break
			}

			if h.Page == b.Page && h.Y > b.Y {
				break
			}

			target = flat[i]
		}

		if target != nil {
			target.Blocks = append(target.Blocks, b)
		}
	}
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

func isCodeLine(l Line) bool {
	font := strings.ToLower(l.FontName)

	return strings.Contains(font, "mono") ||
		strings.Contains(font, "courier") ||
		strings.Contains(font, "code") ||
		strings.Contains(font, "mn")
}

func isPageNumber(text string) bool {
	text = strings.TrimSpace(text)

	// pure number
	if regexp.MustCompile(`^\d+$`).MatchString(text) {
		return true
	}

	return false
}
