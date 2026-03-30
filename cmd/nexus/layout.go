/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package nexus

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/iamaina/nexus/internal/layout"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

// layoutCmd represents the layout command
var layoutCmd = &cobra.Command{
	Use:   "layout",
	Short: "Tests layout extraction on a PDF file",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Println("Please provide a PDF file path")
			return
		}

		ctx := cmd.Context()
		path := args[0]

		// 1. Extract
		data, err := layout.ExtractPDF(path)
		if err != nil {
			logger.Error(ctx, "Failed to extract PDF", slog.Any("error", err))
			return
		}

		// 2. Parse spans
		var spans []layout.Span
		if err := json.Unmarshal(data, &spans); err != nil {
			logger.Error(ctx, "Failed to unmarshal spans", slog.Any("error", err))
			return
		}

		// 3. Build lines
		lines := layout.GroupSpansIntoLines(spans, 2.0)

		// 4. Analyze fonts
		body, freq := layout.AnalyzeFonts(lines)
		fontLevels := layout.BuildFontLevels(freq, body)

		// 5. Detect headings
		headings := layout.DetectHeadings(lines, body, fontLevels)

		// 6. Build blocks
		blocks := layout.BuildBlocks(lines, body)

		// 7. Merge lists
		blocks = layout.MergeLists(blocks)

		var paragraphLines []layout.Line

		for _, b := range blocks {
			if b.Type == layout.BlockParagraph {
				paragraphLines = append(paragraphLines, layout.Line{
					Text: b.Text,
				})
			}
		}

		// 8. Detect document type
		docType := layout.DetectDocumentType(paragraphLines)
		if docType != layout.DocumentBook {
			fmt.Println("❌ Document type not supported yet:", docType)
			return
		}

		// 9. Build heading tree
		tree := layout.BuildHeadingTree(headings)
		tree = layout.TrimFrontMatter(tree)

		// 10. Attach blocks (CORE STEP)
		layout.AttachBlocks(tree, blocks)

		// 11. Build sections
		sections := layout.BuildSections(tree)

		// 12. Output
		layout.PrintSections(sections, 0, 19, 20)
	},
}

func init() {
	RootCmd.AddCommand(layoutCmd)
}
