// This file contains the logic for analyzing font usage in a PDF document to
// identify the body font. The AnalyzeFonts function takes a list of lines and
// counts the frequency of each font size (excluding code fonts) to determine
// which font size is most likely used for the main text content. This
// information is crucial for accurately detecting headings and understanding
// the document's structure.
package layout

import (
	"math"
	"sort"
	"strings"
)

func AnalyzeFonts(lines []Line) (float64, map[float64]int) {
	freq := make(map[float64]int)

	for _, l := range lines {
		// skip code fonts
		if strings.Contains(l.FontName, "Mono") ||
			strings.Contains(l.FontName, "Courier") {
			continue
		}
		freq[l.FontSize]++
	}

	// DEBUG: print distribution
	// fmt.Println("\n--- Font Distribution ---")
	// for fs, count := range freq {
	// 	fmt.Printf("fs=%.2f → %d\n", fs, count)
	// }

	// find most frequent font (body)
	var body float64
	max := 0

	for fs, count := range freq {
		if count > max {
			max = count
			body = fs
		}
	}

	// fmt.Printf("\nDetected Body Font: %.2f\n", body)

	return body, freq
}

func BuildFontLevels(fonts map[float64]int, bodyFont float64) []float64 {
	var sizes []float64

	// Step 1: find max frequency
	maxCount := 0
	for _, count := range fonts {
		if count > maxCount {
			maxCount = count
		}
	}

	// Step 2: filter meaningful fonts
	for fs, count := range fonts {
		// skip body
		if math.Abs(fs-bodyFont) < 0.1 {
			continue
		}

		if count < maxCount/50 {
			continue
		}

		sizes = append(sizes, fs)
	}

	// Step 3: sort descending
	sort.Sort(sort.Reverse(sort.Float64Slice(sizes)))

	return sizes
}
