package nexus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var searchSource string

var searchCmd = &cobra.Command{
	Use:   "search <term>",
	Short: "Find documents and sections by filename or heading (not semantic — use query for that)",
	Long: `Search the index by file path or section heading using a plain substring match.

Use this when you know the name of the file or section you want, e.g.:
  nexus search "change_management"
  nexus search "Reviewer checklist"
  nexus search "praefect.tf"

For meaning-based search ("how does praefect work?"), use nexus query instead.

Since: v0.0.2`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		term := strings.Join(args, " ")
		results, err := a.Chunks.FindByPathOrChapter(ctx, term, searchSource)
		if err != nil {
			logger.Error(ctx, "search failed", "err", err)
			return
		}

		if len(results) == 0 {
			fmt.Printf("No documents or sections matching %q\n", term)
			if searchSource != "" {
				fmt.Printf("(filtered to source %q — try without --source)\n", searchSource)
			}
			return
		}

		home, _ := os.UserHomeDir()
		fmt.Printf("\nSearch: %q  (%d chunk(s))\n\n", term, len(results))

		lastFile := ""
		for _, r := range results {
			if r.File != lastFile {
				p := r.File
				if home != "" {
					p = strings.Replace(r.File, home, "~", 1)
				}
				fmt.Printf("  %s\n", p)
				lastFile = r.File
			}

			ext := filepath.Ext(r.File)
			base := strings.TrimSuffix(filepath.Base(r.File), ext)
			// Only print the chapter if it differs from the filename
			// (flat files use the filename as chapter — no need to repeat it)
			if r.Chapter != "" && r.Chapter != base {
				fmt.Printf("    § %s\n", r.Chapter)
			}

			preview := strings.ReplaceAll(strings.TrimSpace(r.Text), "\n", " ")
			if len(preview) > 120 {
				preview = preview[:120] + "…"
			}
			if preview != "" {
				fmt.Printf("      %s\n", preview)
			}
		}
		fmt.Println()
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchSource, "source", "", "restrict to a source name or path substring")
	RootCmd.AddCommand(searchCmd)
}
