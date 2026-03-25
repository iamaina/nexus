// Package parser contains functions for processing and extracting structured information from raw text documents, such as splitting text into pages and extracting table of contents entries.
package parser //nolint:revive

import (
	"strconv"
	"strings"
)

// TOCEntry represents a single entry in the table of contents, including the title, page number, and hierarchy level.
type TOCEntry struct {
	Title string
	Page  int
	Level int
}

// ExtractTOC takes the full text of a document and attempts to extract the table of contents (TOC) entries. It looks for lines that contain a title followed by dots and a page number, while applying various heuristics to filter out noise and irrelevant lines.
func ExtractTOC(text string) []TOCEntry {
	start := strings.Index(text, "Table of Contents")
	if start == -1 {
		return nil
	}

	lines := strings.Split(text[start:], "\n")
	lines = lines[:min(len(lines), 200)]
	var entries []TOCEntry

	for i := 0; i < len(lines); i++ {
		title := strings.TrimSpace(lines[i])
		clean := strings.ReplaceAll(title, ".", "")
		clean = strings.TrimSpace(clean)

		if clean == "" {
			continue
		}

		if _, err := strconv.Atoi(title); err == nil {
			continue
		}

		if len(title) < 3 {
			continue
		}

		if title == "" {
			continue
		}

		if strings.EqualFold(title, "Table of Contents") {
			continue
		}

		// Look ahead for dots + page
		for j := i + 1; j < i+6 && j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])

			// Skip NBSP and empty lines
			if line == "" || line == "\u00a0" {
				continue
			}

			// Check for dots
			if strings.Contains(line, ".") {
				continue
			}

			// Try parsing page number
			page, err := strconv.Atoi(line)
			if err != nil {
				continue
			}

			// Found valid entry
			if isNoise(title) {
				break
			}

			// Skip numeric titles
			if _, err := strconv.Atoi(title); err == nil {
				continue
			}

			// Keep only major sections (chapters)
			if len(entries) > 0 {
				prev := entries[len(entries)-1]
				if page-prev.Page < 5 {
					continue
				}
			}

			entries = append(entries, TOCEntry{
				Title: title,
				Page:  page,
				Level: 0,
			})

			break
		}
	}

	return entries
}

func isNoise(title string) bool {
	skip := []string{
		"Preface",
		"Contributors",
		"Licence",
		"Dedications",
		"Copyright",
		"Table of Contents",
	}

	for _, s := range skip {
		if strings.Contains(title, s) {
			return true
		}
	}
	return false
}
