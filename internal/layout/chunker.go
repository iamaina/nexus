package layout

import "strings"

func ChunkSections(sections []Section, maxBlocks int) []Chunk {
	var chunks []Chunk

	for _, s := range sections {
		chunks = append(chunks, chunkSection(s, maxBlocks)...)
	}

	return chunks
}

func chunkSection(s Section, maxBlocks int) []Chunk {
	var chunks []Chunk
	var current Chunk

	current.Title = s.Title

	for _, b := range s.Content {

		// flush if full
		if len(current.Blocks) >= maxBlocks {
			chunks = append(chunks, current)
			current = Chunk{Title: s.Title}
		}

		current.Blocks = append(current.Blocks, b)
	}

	// flush remainder
	if len(current.Blocks) > 0 {
		chunks = append(chunks, current)
	}

	// recurse children
	for _, child := range s.Children {
		chunks = append(chunks, chunkSection(child, maxBlocks)...)
	}

	return chunks
}

func ChunkToText(c Chunk) string {
	var parts []string

	for _, b := range c.Blocks {
		lines := RenderBlock(b, "")
		parts = append(parts, strings.Join(lines, "\n"))
	}

	return strings.Join(parts, "\n")
}

func PrintChunks(chunks []Chunk) {
	for i, c := range chunks {
		
		println("\n--- CHUNK", i, "---")
		println("Title:", c.Title)

		for _, b := range c.Blocks {
			lines := RenderBlock(b, "  ")
			for _, l := range lines {
				println(l)
			}
		}
	}
}
