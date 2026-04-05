package nexus

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var listSource string

// noise patterns to strip from filenames for cleaner display
var reNoise = regexp.MustCompile(`(?i)\s*[\(\[].*?[\)\]]|\s*-\s*PDFDrive\s*`)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all ingested documents",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		docs, err := a.Documents.List(ctx, listSource)
		if err != nil {
			logger.Error(ctx, "Failed to list documents")
			return
		}

		if len(docs) == 0 {
			if listSource != "" {
				fmt.Printf("No documents ingested from source %q.\n", listSource)
			} else {
				fmt.Println("No documents ingested yet. Run: make ingest")
			}
			return
		}

		// Group docs by source
		type row struct{ name, chunks, date string }
		groups := make(map[string][]row)
		order := []string{}
		for _, d := range docs {
			base := strings.TrimSuffix(filepath.Base(d.FilePath), filepath.Ext(d.FilePath))
			name := strings.TrimSpace(reNoise.ReplaceAllString(base, ""))
			if name == "" {
				name = base
			}
			r := row{
				name:   name,
				chunks: fmt.Sprintf("%d", d.ChunkCount),
				date:   d.IngestTime,
			}
			if _, seen := groups[d.SourceName]; !seen {
				order = append(order, d.SourceName)
			}
			groups[d.SourceName] = append(groups[d.SourceName], r)
		}

		const nameCap = 45

		for _, src := range order {
			rows := groups[src]
			fmt.Printf("\n  Source: %s (%d documents)\n", strings.ToUpper(src), len(rows))
			fmt.Printf("  %-*s  %6s  %s\n", nameCap, "NAME", "CHUNKS", "INGESTED")
			fmt.Printf("  %s  %s  %s\n", strings.Repeat("─", nameCap), "──────", "────────────────")
			for _, r := range rows {
				name := r.name
				if len(name) > nameCap {
					name = name[:nameCap-1] + "…"
				}
				fmt.Printf("  %-*s  %6s  %s\n", nameCap, name, r.chunks, r.date)
			}
		}

		fmt.Printf("\n  Total: %d document(s)\n", len(docs))
	},
}

func init() {
	listCmd.Flags().StringVar(&listSource, "source", "", "filter by source name (e.g. books, intelligence)")
	RootCmd.AddCommand(listCmd)
}
