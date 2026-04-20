package layout

import (
	"sort"
	"strings"
)

// AnalyzeFonts returns the body font size and a frequency map of all font sizes found in lines.
func AnalyzeFonts(lines []Line) (float64, map[float64]int) {
	// freq counts all non-code lines — used by BuildFontLevels to find heading candidates.
	freq := make(map[float64]int)
	// bodyFreq counts only non-bold, non-code lines — used to detect body font.
	// Excluding bold prevents code-heavy docs (where bold headings outnumber body text)
	// from misidentifying a heading size as the body font.
	bodyFreq := make(map[float64]int)

	for _, l := range lines {
		isCode := strings.Contains(l.FontName, "Mono") ||
			strings.Contains(l.FontName, "Courier")
		isBold := strings.Contains(strings.ToLower(l.FontName), "bold")

		if isCode {
			continue
		}
		freq[l.FontSize]++
		if !isBold {
			bodyFreq[l.FontSize]++
		}
	}

	// Find body font from non-bold lines first.
	var body float64
	maxCount := 0
	for fs, count := range bodyFreq {
		if count > maxCount {
			maxCount = count
			body = fs
		}
	}

	// Fallback: if all non-code lines are bold (e.g. heading-only doc), use full freq.
	if body == 0 {
		for fs, count := range freq {
			if count > maxCount {
				maxCount = count
				body = fs
			}
		}
	}

	return body, freq
}

// BuildFontLevels returns font sizes larger than bodyFont that appear frequently
// enough to be heading candidates, sorted largest first.
func BuildFontLevels(fonts map[float64]int, bodyFont float64) []float64 {
	var sizes []float64

	// Step 1: find max frequency
	maxCount := 0
	for _, count := range fonts {
		if count > maxCount {
			maxCount = count
		}
	}

	// Step 2: filter meaningful fonts — must be larger than body, with a
	// frequency floor that scales down for fonts much larger than body
	// (chapter titles are rare but still valid headings).
	for fs, count := range fonts {
		// must be strictly larger than body
		if fs <= bodyFont+0.1 {
			continue
		}

		// frequency floor: relax for fonts significantly larger than body
		// so rare-but-large chapter title fonts aren't dropped
		ratio := fs / bodyFont
		var minCount int
		switch {
		case ratio >= 1.8:
			minCount = 3 // very large fonts — allow even rare ones
		case ratio >= 1.3:
			minCount = maxCount / 100
		default:
			minCount = maxCount / 50
		}

		if count < minCount {
			continue
		}

		sizes = append(sizes, fs)
	}

	// Step 3: sort descending
	sort.Sort(sort.Reverse(sort.Float64Slice(sizes)))

	return sizes
}
