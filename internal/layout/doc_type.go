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
	shortParagraphs := 0

	for _, p := range paragraphs {
		words := len(strings.Fields(p.Text))
		totalWords += words

		if words < 8 {
			shortParagraphs++
		}
	}

	avgWords := totalWords / len(paragraphs)

	// 🔥 KEY SIGNALS
	isMostlyShort := shortParagraphs > len(paragraphs)/2
	isVeryShortAvg := avgWords < 10
	isTooFewParagraphs := len(paragraphs) < 30

	if isMostlyShort && isVeryShortAvg && isTooFewParagraphs {
		return DocumentSlides
	}

	return DocumentBook
}
