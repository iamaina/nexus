package nexus

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var chaptersCmd = &cobra.Command{
	Use:   "chapters <name-or-path-fragment>",
	Short: "Browse chapters of an ingested book or long document",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		query := strings.Join(args, " ")
		docs, err := a.Documents.FindByName(ctx, query)
		if err != nil {
			logger.Error(ctx, "Failed to find document", slog.Any("err", err))
			return
		}

		if len(docs) == 0 {
			fmt.Printf("No ingested document matches %q.\n", query)
			fmt.Println("Run: nexus list --source <name>  to see what's available.")
			return
		}

		// Ambiguous — more than one file matches.
		if len(docs) > 1 {
			home, _ := os.UserHomeDir()
			const maxShown = 10
			fmt.Printf("\n%d documents match %q — be more specific:\n\n", len(docs), query)
			for i, d := range docs {
				if i >= maxShown {
					fmt.Printf("  ... and %d more. Add more path to narrow it down.\n", len(docs)-maxShown)
					break
				}
				p := d.FilePath
				if home != "" {
					p = strings.Replace(p, home, "~", 1)
				}
				fmt.Printf("  nexus chapters %s\n", p)
			}
			fmt.Println()
			return
		}

		// Exactly one match.
		doc := docs[0]
		home, _ := os.UserHomeDir()
		displayPath := doc.FilePath
		if home != "" {
			displayPath = strings.Replace(doc.FilePath, home, "~", 1)
		}
		name := strings.TrimSuffix(filepath.Base(doc.FilePath), filepath.Ext(doc.FilePath))

		chapters, err := a.Chunks.ListChaptersByDocumentID(ctx, doc.ID)
		if err != nil {
			logger.Error(ctx, "Failed to get chapters", slog.Any("err", err))
			return
		}

		fmt.Printf("\n  %s\n  %s\n\n", name, displayPath)

		if len(chapters) == 0 {
			fmt.Println("  No chapters found — document may have a flat structure.")
			return
		}

		for i, ch := range chapters {
			fmt.Printf("  %2d. %s\n", i+1, ch)
		}
		fmt.Println()
	},
}

func init() {
	RootCmd.AddCommand(chaptersCmd)
}
