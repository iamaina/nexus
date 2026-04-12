package nexus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var listSource string

// reNoise strips PDF metadata noise from filenames for cleaner display.
var reNoise = regexp.MustCompile(`(?i)\s*[\(\[].*?[\)\]]|\s*-\s*PDFDrive\s*`)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show everything in your knowledge base",
	Long: `Without --source: show a summary of each source (document and chunk counts).
With --source: list all documents ingested from that source.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		if listSource != "" {
			// Drill-down: list documents for one source.
			docs, err := a.Documents.List(ctx, listSource)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("list failed: %v", err))
				return
			}
			if len(docs) == 0 {
				fmt.Printf("No documents ingested from source %q.\n", listSource)
				return
			}

			home, _ := os.UserHomeDir()
			const nameCap = 28
			fmt.Printf("\n  Source: %s (%d documents)\n\n", strings.ToUpper(listSource), len(docs))
			fmt.Printf("  %-*s  %-s\n", nameCap, "NAME", "PATH")
			fmt.Printf("  %s  %s\n", strings.Repeat("─", nameCap), strings.Repeat("─", 52))
			for _, d := range docs {
				base := strings.TrimSuffix(filepath.Base(d.FilePath), filepath.Ext(d.FilePath))
				name := strings.TrimSpace(reNoise.ReplaceAllString(base, ""))
				if name == "" {
					name = base
				}
				if len(name) > nameCap {
					name = name[:nameCap-1] + "…"
				}
				path := d.FilePath
				if home != "" {
					path = strings.Replace(path, home, "~", 1)
				}
				fmt.Printf("  %-*s  %s\n", nameCap, name, path)
			}
			fmt.Println()
			return
		}

		// Default: per-source summary.
		summaries, err := a.Documents.Summary(ctx)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("list failed: %v", err))
			return
		}
		if len(summaries) == 0 {
			fmt.Println("No documents ingested yet. Run: make ingest")
			return
		}

		var totalDocs, totalChunks int
		fmt.Printf("\n  %-16s  %5s  %s\n", "SOURCE", "DOCS", "CHUNKS")
		fmt.Printf("  %-16s  %5s  %s\n", "────────────────", "─────", "──────")
		for _, s := range summaries {
			fmt.Printf("  %-16s  %5d  %d\n", s.SourceName, s.DocCount, s.ChunkCount)
			totalDocs += s.DocCount
			totalChunks += s.ChunkCount
		}
		fmt.Printf("\n  Total: %d documents, %d chunks\n", totalDocs, totalChunks)
		fmt.Printf("  Tip: nexus list --source <name> to see documents in a source\n\n")
	},
}

func init() {
	listCmd.Flags().StringVar(&listSource, "source", "", "show documents from a specific source (e.g. books, intelligence)")
	RootCmd.AddCommand(listCmd)
}
