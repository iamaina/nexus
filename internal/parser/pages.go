// Extracts the page numbers from the text. It looks for lines that match the pattern of a page number.
package parser

import "strings"

func SplitPages(text string) []string {
	return strings.Split(text, "\f")
}
