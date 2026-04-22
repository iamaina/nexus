// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/iamaina/nexus/internal/layout"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var (
	showFonts    bool
	showHeadings bool
	showTree     bool
	showBlocks   bool
	showSections bool
	showChunks   bool
	pageFrom     int
	pageTo       int
)

var layoutCmd = &cobra.Command{
	Use:   "layout <pdf>",
	Short: "Debug the layout pipeline on a PDF file",
	Long: `Runs the full layout pipeline on a PDF and prints debug output.

With no flags, prints a summary of what each pipeline stage produced.
Use flags to inspect specific stages:

  --fonts     Font distribution and detected body/heading sizes
  --headings  All detected headings (raw, before tree building)
  --tree      Heading tree structure
  --blocks    All blocks with type and page
  --sections  Section hierarchy
  --chunks    Final chunks (title, level, block count)

Use --page-from / --page-to to narrow output to a page range.

Since: v0.0.1`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		path := args[0]

		// 1. Extract
		data, err := layout.ExtractPDF(path)
		if err != nil {
			logger.Error(ctx, "Failed to extract PDF", slog.Any("error", err))
			return
		}

		var spans []layout.Span
		if err := json.Unmarshal(data, &spans); err != nil {
			logger.Error(ctx, "Failed to unmarshal spans", slog.Any("error", err))
			return
		}

		// 2. Lines
		lines := layout.GroupSpansIntoLines(spans, 2.0)

		// 3. Fonts
		body, freq := layout.AnalyzeFonts(lines)
		fontLevels := layout.BuildFontLevels(freq, body)

		// 4. Headings
		headings := layout.DetectHeadings(lines, body, fontLevels)
		headings = layout.MergeWrappedHeadings(headings)

		// 5. Blocks
		blocks := layout.BuildBlocks(lines, body)
		blocks = layout.MergeLists(blocks)

		// 6. Doc type
		var paraLines []layout.Line
		for _, b := range blocks {
			if b.Type == layout.BlockParagraph {
				paraLines = append(paraLines, layout.Line{Text: b.Text})
			}
		}
		docType := layout.DetectDocumentType(paraLines)

		// 7. Tree + sections + chunks
		tree := layout.BuildHeadingTree(headings)
		tree = layout.TrimFrontMatter(tree)
		layout.AttachBlocks(tree, blocks)
		sections := layout.BuildSections(tree)
		chunks := layout.ChunkSections(sections, 5)

		// --- Output ---

		anyFlag := showFonts || showHeadings || showTree || showBlocks || showSections || showChunks
		if !anyFlag {
			printStats(path, docType, spans, lines, body, fontLevels, headings, blocks, sections, chunks)
			return
		}

		if showFonts {
			printFonts(body, fontLevels, freq)
		}
		if showHeadings {
			printHeadings(headings, pageFrom, pageTo)
		}
		if showTree {
			fmt.Println("\n--- Heading Tree ---")
			layout.PrintTree(tree, 0, pageFrom, pageTo)
		}
		if showBlocks {
			printBlocks(blocks, pageFrom, pageTo)
		}
		if showSections {
			fmt.Println("\n--- Sections ---")
			layout.PrintSections(sections, 0, pageFrom, pageTo)
		}
		if showChunks {
			layout.PrintChunks(filterChunks(chunks, pageFrom, pageTo))
		}
	},
}

func printStats(path string, docType layout.DocumentType, spans []layout.Span, lines []layout.Line, body float64, fontLevels []float64, headings []layout.Heading, blocks []layout.Block, sections []layout.Section, chunks []layout.Chunk) {
	blockCounts := map[layout.BlockType]int{}
	for _, b := range blocks {
		blockCounts[b.Type]++
	}

	levelCounts := map[int]int{}
	for _, h := range headings {
		levelCounts[h.Level]++
	}

	chunkLevelCounts := map[int]int{}
	for _, c := range chunks {
		chunkLevelCounts[c.Level]++
	}

	fmt.Printf("\n=== Layout Pipeline: %s ===\n\n", path)
	fmt.Printf("  Document type : %s\n", docType)
	fmt.Printf("  Spans         : %d\n", len(spans))
	fmt.Printf("  Lines         : %d\n", len(lines))
	fmt.Printf("  Body font     : %.2fpt\n", body)
	fmt.Printf("  Heading fonts : %v\n", fontLevels)
	fmt.Printf("\n  Headings      : %d\n", len(headings))
	for _, lvl := range []int{1, 2, 3} {
		if n := levelCounts[lvl]; n > 0 {
			fmt.Printf("    H%d          : %d\n", lvl, n)
		}
	}
	fmt.Printf("\n  Blocks        : %d\n", len(blocks))
	fmt.Printf("    paragraphs  : %d\n", blockCounts[layout.BlockParagraph])
	fmt.Printf("    code        : %d\n", blockCounts[layout.BlockCode])
	fmt.Printf("    lists       : %d\n", blockCounts[layout.BlockList])
	fmt.Printf("    images      : %d\n", blockCounts[layout.BlockImage])
	fmt.Printf("\n  Sections      : %d\n", len(sections))
	fmt.Printf("  Chunks        : %d\n", len(chunks))
	for _, lvl := range []int{1, 2, 3} {
		if n := chunkLevelCounts[lvl]; n > 0 {
			fmt.Printf("    level %d     : %d chunks\n", lvl, n)
		}
	}
	fmt.Printf("\nUse flags to inspect stages: --fonts --headings --tree --blocks --sections --chunks\n")
}

func printFonts(body float64, fontLevels []float64, freq map[float64]int) {
	// Sort by frequency descending
	type entry struct {
		size  float64
		count int
	}
	var entries []entry
	for fs, count := range freq {
		entries = append(entries, entry{fs, count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})

	fmt.Printf("\n--- Font Distribution ---\n\n")
	fmt.Printf("  %-10s  %-8s  %s\n", "size (pt)", "count", "role")
	fmt.Printf("  %-10s  %-8s  %s\n", "---------", "-----", "----")
	for _, e := range entries {
		role := ""
		if e.size == body {
			role = "← body"
		}
		for i, lvl := range fontLevels {
			if e.size == lvl {
				role = fmt.Sprintf("← H%d candidate", i+1)
				break
			}
		}
		fmt.Printf("  %-10.2f  %-8d  %s\n", e.size, e.count, role)
	}
}

func printHeadings(headings []layout.Heading, from, to int) {
	fmt.Printf("\n--- Detected Headings (%d) ---\n\n", len(headings))
	fmt.Printf("  %-4s  %-6s  %-8s  %s\n", "lvl", "page", "font(pt)", "text")
	fmt.Printf("  %-4s  %-6s  %-8s  %s\n", "---", "----", "--------", "----")
	for _, h := range headings {
		if from > 0 && h.Page < from {
			continue
		}
		if to > 0 && h.Page > to {
			continue
		}
		text := h.Text
		if len(text) > 70 {
			text = text[:70] + "…"
		}
		fmt.Printf("  H%-3d  %-6d  %-8.2f  %s\n", h.Level, h.Page, h.FontSize, text)
	}
}

func printBlocks(blocks []layout.Block, from, to int) {
	fmt.Printf("\n--- Blocks (%d) ---\n\n", len(blocks))
	fmt.Printf("  %-10s  %-6s  %s\n", "type", "page", "preview")
	fmt.Printf("  %-10s  %-6s  %s\n", "----", "----", "-------")
	for _, b := range blocks {
		if from > 0 && b.Page < from {
			continue
		}
		if to > 0 && b.Page > to {
			continue
		}
		preview := b.Text
		if b.Type == layout.BlockList && len(b.Items) > 0 {
			preview = fmt.Sprintf("[%d items] %s", len(b.Items), b.Items[0])
		}
		if len(preview) > 70 {
			preview = preview[:70] + "…"
		}
		fmt.Printf("  %-10s  %-6d  %s\n", b.Type, b.Page, preview)
	}
}

func filterChunks(chunks []layout.Chunk, from, to int) []layout.Chunk {
	if from == 0 && to == 0 {
		return chunks
	}
	var out []layout.Chunk
	for _, c := range chunks {
		if from > 0 && c.Page < from {
			continue
		}
		if to > 0 && c.Page > to {
			continue
		}
		out = append(out, c)
	}
	return out
}

func init() {
	layoutCmd.Flags().BoolVar(&showFonts, "fonts", false, "Show font distribution and heading size candidates")
	layoutCmd.Flags().BoolVar(&showHeadings, "headings", false, "Show all detected headings with level, font size, and page")
	layoutCmd.Flags().BoolVar(&showTree, "tree", false, "Show the heading tree structure")
	layoutCmd.Flags().BoolVar(&showBlocks, "blocks", false, "Show all blocks with type and page")
	layoutCmd.Flags().BoolVar(&showSections, "sections", false, "Show section hierarchy")
	layoutCmd.Flags().BoolVar(&showChunks, "chunks", false, "Show final chunks")
	layoutCmd.Flags().IntVar(&pageFrom, "page-from", 0, "Filter output to pages >= N")
	layoutCmd.Flags().IntVar(&pageTo, "page-to", 0, "Filter output to pages <= N")
	RootCmd.AddCommand(layoutCmd)
}
