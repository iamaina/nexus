package layout

import (
	"fmt"
	"strings"
)

// ChunkSections splits a section tree into flat Chunk slices, each holding at most maxBlocks blocks.
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
	current.Level = s.Level
	current.Page = s.Page

	for _, b := range s.Content {

		// flush if full
		if len(current.Blocks) >= maxBlocks {
			chunks = append(chunks, current)
			current = Chunk{Title: s.Title, Level: s.Level, Page: s.Page}
		}

		current.Blocks = append(current.Blocks, b)
	}

	// flush remainder
	if len(current.Blocks) > 0 {
		chunks = append(chunks, current)
	} else if len(s.Children) > 0 {
		// Section has no direct content but has children (e.g. a chapter title
		// whose body is entirely in subsections). Emit a title-only chunk so
		// the chapter name is represented at its level in the database.
		current.Blocks = []Block{{Type: BlockParagraph, Text: s.Title, Page: s.Page}}
		chunks = append(chunks, current)
	}

	// recurse children
	for _, child := range s.Children {
		chunks = append(chunks, chunkSection(child, maxBlocks)...)
	}

	return chunks
}

// ChunkToText renders all blocks in a chunk to a single plain-text string.
func ChunkToText(c Chunk) string {
	var parts []string

	for _, b := range c.Blocks {
		lines := RenderBlock(b, "")
		parts = append(parts, strings.Join(lines, "\n"))
	}

	return strings.Join(parts, "\n")
}

// PrintChunks prints a debug representation of chunks to stdout.
func PrintChunks(chunks []Chunk) {
	for i, c := range chunks {
		fmt.Printf("\n--- CHUNK %d  page:%d  level:%d ---\n", i, c.Page, c.Level)
		fmt.Printf("Title: %s\n", c.Title)
		for _, b := range c.Blocks {
			lines := RenderBlock(b, "  ")
			for _, l := range lines {
				fmt.Println(l)
			}
		}
	}
}
