// This file contains the logic for building a hierarchical tree of headings
// based on their levels.
package layout

import (
	"fmt"
	"strings"
)

// BuildHeadingTree takes a list of detected headings and constructs a tree
// structure based on their levels. It uses a stack to keep track of the current
// hierarchy and attaches child nodes to their respective parents. The resulting
// tree can be used to represent the document's structure and for further
// processing or visualization.
func BuildHeadingTree(headings []Heading) []*Node {
	var roots []*Node

	// index = level-1
	stack := make([]*Node, 10)

	for _, h := range headings {
		node := &Node{Heading: h}
		level := h.Level

		// normalize (just in case)
		if level < 1 {
			level = 1
		}
		if level > len(stack) {
			level = len(stack)
		}

		// root level
		if level == 1 {
			roots = append(roots, node)
			stack[0] = node
			continue
		}

		// find parent
		parent := stack[level-2]

		// fallback if parent missing (important for messy PDFs)
		if parent == nil {
			// attach to last valid upper level
			for i := level - 2; i >= 0; i-- {
				if stack[i] != nil {
					parent = stack[i]
					break
				}
			}
		}

		if parent != nil {
			parent.Children = append(parent.Children, node)
		} else {
			// worst case → treat as root
			roots = append(roots, node)
		}

		// update stack
		stack[level-1] = node

		// clear deeper levels (important!)
		for i := level; i < len(stack); i++ {
			stack[i] = nil
		}
	}

	return roots
}

func PrintTree(nodes []*Node, indent int) {
	for _, n := range nodes {
		if n.Heading.Page > 0 && n.Heading.Page <= 20 {
			fmt.Printf("%s- %s (L%d)\n",
				strings.Repeat("  ", indent),
				n.Heading.Text,
				n.Heading.Level,
			)
			for _, p := range n.Paragraphs {
				fmt.Printf("%s  ¶ %s\n",
					strings.Repeat("  ", indent),
					p,
				)
			}
			PrintTree(n.Children, indent+1)
		}
	}
}
