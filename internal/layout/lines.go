// This file contains the logic for grouping spans into lines based on their Y
// coordinates and page numbers. The GroupSpansIntoLines function takes a list
// of spans and a Y tolerance value to determine which spans belong to the same
// line. It sorts the spans by page, Y coordinate, and X coordinate, then
// iterates through them to group them into lines. Each line is represented by a
// Line struct that contains the combined text, font information, and other
// features derived from the spans that belong to that line.
package layout

import (
	"math"
	"sort"
)

func GroupSpansIntoLines(spans []Span, yTolerance float64) []Line {
	// Sort spans: page → y → x
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Page != spans[j].Page {
			return spans[i].Page < spans[j].Page
		}
		if math.Abs(spans[i].Y-spans[j].Y) > yTolerance {
			return spans[i].Y < spans[j].Y
		}
		return spans[i].X < spans[j].X
	})

	var lines []Line

	for _, span := range spans {
		placed := false

		for i := range lines {
			line := &lines[i]

			// Same page + close Y = same line
			if span.Page == line.Page && math.Abs(span.Y-line.Y) < yTolerance {
				line.Spans = append(line.Spans, span)
				placed = true
				break
			}
		}

		if !placed {
			lines = append(lines, Line{
				Spans: []Span{span},
				Y:     span.Y,
				Page:  span.Page,
			})
		}
	}

	// Sort spans inside each line by X and build text + features
	for i := range lines {
		line := &lines[i]

		sort.Slice(line.Spans, func(a, b int) bool {
			return line.Spans[a].X < line.Spans[b].X
		})

		text := ""
		minX := line.Spans[0].X
		maxX := line.Spans[0].X
		totalFont := 0.0
		fontCount := make(map[string]int)
		flagCount := make(map[int]int)

		for _, s := range line.Spans {
			if text != "" {
				text += " "
			}
			text += s.Text

			if s.X < minX {
				minX = s.X
			}
			if s.X > maxX {
				maxX = s.X
			}

			totalFont += s.FontSize
			fontCount[s.FontName]++
			flagCount[s.Flags]++
		}

		line.Text = text
		line.XStart = minX
		line.XEnd = maxX
		line.FontSize = totalFont / float64(len(line.Spans))
		line.FontName = mostCommonFont(fontCount)
		line.Flags = mostCommonFlag(flagCount)
	}

	return lines
}

func mostCommonFont(m map[string]int) string {
	var maxFont string
	maxCount := 0

	for f, c := range m {
		if c > maxCount {
			maxFont = f
			maxCount = c
		}
	}
	return maxFont
}

func mostCommonFlag(m map[int]int) int {
	var maxFlag int
	maxCount := 0

	for f, c := range m {
		if c > maxCount {
			maxFlag = f
			maxCount = c
		}
	}
	return maxFlag
}
