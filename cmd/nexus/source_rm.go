package nexus

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/spf13/cobra"
)

var sourceRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a source and all its ingested documents from the index",
	Long: `Remove all documents and chunks for a named source from the nexus
index. The source entry in config.yaml is not touched — only the database
records are deleted. Run "nexus ingest" afterwards if you want to re-index.

Since: v0.3.0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			return fmt.Errorf("application not available")
		}

		docCount, chunkCount, err := a.Documents.CountBySource(ctx, name)
		if err != nil {
			return fmt.Errorf("count source %q: %w", name, err)
		}
		if docCount == 0 {
			fmt.Printf("  Source %q not found in the index (nothing to remove).\n", name)
			return nil
		}

		fmt.Printf("\n  Source:  %s\n", name)
		fmt.Printf("  Docs:    %d\n", docCount)
		fmt.Printf("  Chunks:  %s\n\n", formatChunks(chunkCount))
		fmt.Printf("  This will permanently delete all indexed content for %q.\n", name)
		fmt.Print("  Continue? [y/N] ")

		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Println("  Aborted.")
			return nil
		}

		deleted, err := a.Documents.DeleteBySource(ctx, name)
		if err != nil {
			return fmt.Errorf("delete source %q: %w", name, err)
		}

		fmt.Printf("  ✓ Removed %d doc(s) and their chunks for source %q.\n\n", deleted, name)
		return nil
	},
}

func init() {
	sourceCmd.AddCommand(sourceRmCmd)
}
