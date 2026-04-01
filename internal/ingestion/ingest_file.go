package ingestion

import (
	"encoding/json"
	"fmt"

	"github.com/iamaina/nexus/internal/layout"
)

func IngestFile(path string) error {
	// 1. Extract
	data, err := layout.ExtractPDF(path)
	if err != nil {
		return err
	}

	var spans []layout.Span
	if err := json.Unmarshal(data, &spans); err != nil {
		return err
	}

	// 2. Layout pipeline
	lines := layout.GroupSpansIntoLines(spans, 2.0)

	body, freq := layout.AnalyzeFonts(lines)
	fontLevels := layout.BuildFontLevels(freq, body)

	headings := layout.DetectHeadings(lines, body, fontLevels)

	blocks := layout.BuildBlocks(lines, body)
	blocks = layout.MergeLists(blocks)

	// 3. Structure
	tree := layout.BuildHeadingTree(headings)
	tree = layout.TrimFrontMatter(tree)

	layout.AttachBlocks(tree, blocks)

	sections := layout.BuildSections(tree)

	// 4. Chunk
	chunks := layout.ChunkSections(sections, 5)

	// 5. Store (stub for now)
	for _, c := range chunks {
		fmt.Println("CHUNK:", c.Title)
	}

	return nil
}
