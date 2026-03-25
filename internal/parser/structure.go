// Extracts the structure of the document from the text. It looks for headings and their corresponding content.
package parser

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
