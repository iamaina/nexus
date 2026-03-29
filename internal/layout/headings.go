// This file contains the logic for detecting headings in a PDF document based
// on font size, font name, and other heuristics. The DetectHeadings function
// takes a list of lines, the body font size, and predefined heading levels to
// identify potential headings in the document.
package layout

import (
	"regexp"
	"strings"
	"unicode"
)

func DetectHeadings(lines []Line, bodyFont float64, levels []float64) []Heading {
	var headings []Heading

	for _, line := range lines {

		text := strings.TrimSpace(line.Text)
		isBold := strings.Contains(strings.ToLower(line.FontName), "bold")

		// filter out non-heading candidates

		// --- STRUCTURAL FILTERS (FIRST) ---
		if hasIconFont(line) ||
			hasMixedFonts(line) ||
			hasCodeSpan(line) {
			continue
		}
		// --- TEXT FILTERS ---
		if !isMeaningfulText(text) ||
			strings.Contains(text, ". . .") ||
			regexp.MustCompile(`\d+$`).MatchString(text) ||
			line.FontSize <= bodyFont && isLikelySentence(text) {
			continue
		}
		// --- STYLE FILTER ---
		if !isBold && line.FontSize < bodyFont*1.3 {
			continue
		}

		// candidate heading
		if line.FontSize > bodyFont {
			level := classifyHeading(line.FontSize, bodyFont)

			if level == 0 {
				continue
			}

			headings = append(headings, Heading{
				Text:     strings.TrimSpace(line.Text),
				Level:    level,
				FontSize: line.FontSize,
				FontName: line.FontName,
				Page:     line.Page,
				Y:        line.Y,
			})
		}
	}

	return headings
}

func isMeaningfulText(s string) bool {

	if s == "" {
		return false
	}

	// reject very short (but keep things like "Go")
	if len(s) == 1 {
		// allow letters/digits only
		r := []rune(s)[0]
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}

	// reject if mostly symbols
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}

	ratio := float64(letters) / float64(len([]rune(s)))

	return ratio > 0.5
}

func isLikelySentence(s string) bool {
	// long text → likely paragraph, not heading
	if len(s) > 80 {
		return true
	}

	// contains punctuation typical of sentences
	if strings.ContainsAny(s, ".:,;") {
		return true
	}

	// starts lowercase (very strong signal)
	r := []rune(strings.TrimSpace(s))
	if len(r) > 0 && unicode.IsLower(r[0]) {
		return true
	}

	return false
}

func hasIconFont(line Line) bool {
	for _, s := range line.Spans {
		if strings.Contains(s.FontName, "FontAwesome") {
			return true
		}
	}
	return false
}

func hasMixedFonts(line Line) bool {
	fonts := make(map[string]struct{})

	for _, s := range line.Spans {
		fonts[s.FontName] = struct{}{}
	}

	return len(fonts) > 1
}

func hasCodeSpan(line Line) bool {
	for _, s := range line.Spans {
		if strings.Contains(s.FontName, "Mono") ||
			strings.Contains(s.FontName, "Courier") {
			return true
		}
	}
	return false
}

func classifyHeading(fs float64, body float64) int {
	ratio := fs / body

	switch {
	case ratio >= 1.8:
		return 1
	case ratio >= 1.3:
		return 2
	case ratio >= 1.1:
		return 3
	default:
		return 0
	}
}
