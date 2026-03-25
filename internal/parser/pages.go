// Package parser contains functions for processing and extracting structured information from raw text documents, such as splitting text into pages and extracting table of contents entries.
package parser //nolint:revive

import "strings"

// SplitPages takes the full text of a document and splits it into pages based on the form feed character (\f), which is commonly used as a page delimiter in extracted text.
func SplitPages(text string) []string {
	return strings.Split(text, "\f")
}
