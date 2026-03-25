// Package parser contains functions for assigning chapters to pages based on a table of contents.
package parser //nolint:revive

// AssignChapter takes a page number and a table of contents (TOC) and returns the chapter title that corresponds to that page. It iterates through the TOC entries to find the correct chapter based on the page number.
func AssignChapter(page int, toc []TOCEntry) string {
	current := "Unknown"

	for i := 0; i < len(toc); i++ {
		entry := toc[i]

		// If last entry
		if i == len(toc)-1 {
			if page >= entry.Page {
				return entry.Title
			}
			continue
		}

		next := toc[i+1]

		if page >= entry.Page && page < next.Page {
			return entry.Title
		}
	}

	return current
}
