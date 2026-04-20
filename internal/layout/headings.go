package layout

import (
	"regexp"
	"strings"
	"unicode"
)

// DetectHeadings identifies heading lines based on font size relative to bodyFont and bold styling.
func DetectHeadings(lines []Line, bodyFont float64, _ []float64) []Heading {
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
			regexp.MustCompile(`\s\d+$`).MatchString(text) ||
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

// MergeWrappedHeadings merges consecutive headings that are line-wrapped
// continuations of the same heading. A heading is considered a continuation
// when it shares the same level and page as the previous heading, is within
// a small Y distance (line-wrap), and the previous heading's text does not
// end with sentence-terminating punctuation.
func MergeWrappedHeadings(headings []Heading) []Heading {
	if len(headings) == 0 {
		return headings
	}

	merged := make([]Heading, 0, len(headings))
	merged = append(merged, headings[0])

	for i := 1; i < len(headings); i++ {
		prev := &merged[len(merged)-1]
		cur := headings[i]

		if cur.Level == prev.Level &&
			cur.Page == prev.Page &&
			cur.Y-prev.Y < 40 && // within ~40pt vertically (generous for large fonts)
			!endsWithTerminator(prev.Text) {
			prev.Text = prev.Text + " " + cur.Text
			continue
		}

		merged = append(merged, cur)
	}

	return merged
}

func endsWithTerminator(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	last := s[len(s)-1]
	return last == '.' || last == '!' || last == '?' || last == ':' || last == ';'
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
