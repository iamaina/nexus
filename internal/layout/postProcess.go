// postProcess.go contains functions to clean up the heading tree after initial construction.
package layout

import (
	"regexp"
	"strings"
)

func isRealContentStart(h Heading) bool {
	text := strings.TrimSpace(h.Text)

	// Strong signals only

	// 1. Chapter keyword
	if strings.HasPrefix(strings.ToLower(text), "chapter") {
		return true
	}

	// 2. Numbered sections (1. / 1.1 / 2.3 etc.)
	if regexp.MustCompile(`^\d+(\.\d+)*\.`).MatchString(text) {
		return true
	}

	return false
}

// TrimFrontMatter removes leading nodes that are likely part of front matter
// (e.g., title page, TOC). It checks for certain patterns in the heading text
// to determine where the main content likely starts. This is a heuristic
// approach and may not be perfect, but it can help improve the structure of the
// heading tree for many documents.
func TrimFrontMatter(nodes []*Node) []*Node {
	for i, n := range nodes {
		if isRealContentStart(n.Heading) {
			return nodes[i:]
		}
	}
	return nodes
}
