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
	Long: `This command is used for testing the layout extraction process on a
	PDF file. It extracts text and layout information, groups spans into lines,
	analyzes font usage to identify the body font, and detects headings based on
	font size and other features. The results are printed to the console for
	verification.`, Run: func(cmd *cobra.Command, args []string) {
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

		// DEBUG: print results
		// fmt.Println("\n--- Detected Headings ---")
		// for _, h := range headings {
		// 	fmt.Printf("Level: %d Font_name: %s FontSize: %.2f Text: %s\n", h.Level, h.FontName, h.FontSize, h.Text)
		// }
		// fmt.Printf("\nBody Font: %.2f\n", body)

		// 6. Build paragraphs
		paragraphs := layout.BuildParagraphs(lines, body)

		// 7. Detect document type
		docType := layout.DetectDocumentType(paragraphs)

		if docType != layout.DocumentBook {
			fmt.Println("❌ Document type not supported yet:", docType)
			return
		}

		// 8. Build heading tree
		tree := layout.BuildHeadingTree(headings)
		tree = layout.TrimFrontMatter(tree)

		// 9. Convert paragraphs → blocks
		var blocks []layout.Block
		for _, p := range paragraphs {
			blocks = append(blocks, layout.Block{
				Type: "paragraph",
				Text: p.Text,
				Page: p.Page,
				Y:    p.Y,
			})
		}

		// 10. Attach blocks (NEW CORE)
		layout.AttachBlocks(tree, blocks)

		// 11. Build sections
		sections := layout.BuildSections(tree)

		debugTree := false

		// 11. Print results
		if debugTree {
			layout.PrintTree(tree, 0, 0, 20)
		} else {
			// sections := layout.BuildSections(tree)
			layout.PrintSections(sections, 0, 0, 20)
		}

	},
}

func init() {
	RootCmd.AddCommand(layoutCmd)
}
