// Package layout provides the PDF layout analysis pipeline.
package layout

import "strings"

// DetectDocumentType classifies a document as book, slides, or unknown based
// on paragraph length heuristics.
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
