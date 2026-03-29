// This file defines the DocumentType type and a function to detect the document
// type based on its content. The DocumentType can be used to categorize
// documents as either "slides", "book", or "unknown". The detection is based on
// a simple heuristic that looks at the average number of words per paragraph,
// which can help downstream processing apply different strategies for parsing
// or analyzing the document.
package layout

import "strings"

// DocumentType is a simple string type that can be used to categorize documents based on their content or structure. This can be useful for
// downstream processing, such as applying different parsing or analysis
// strategies based on the document type.
func DetectDocumentType(paragraphs []Line) DocumentType {
	if len(paragraphs) == 0 {
		return DocumentUnknown
	}

	totalWords := 0

	for _, p := range paragraphs {
		totalWords += len(strings.Fields(p.Text))
	}

	avgWords := totalWords / len(paragraphs)

	// Heuristic:
	// Slides → very short text blocks
	// Books → longer flowing text
	if avgWords < 12 {
		return DocumentSlides
	}

	return DocumentBook
}
