package nexus

import (
	"fmt"
	"log/slog"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var chaptersCmd = &cobra.Command{
	Use:   "chapters [book-name]",
	Short: "List chapters found in an ingested book",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		chapters, err := a.Chunks.ListChaptersByBook(ctx, args[0])
		if err != nil {
			logger.Error(ctx, "Failed to get chapters", slog.Any("err", err))
			return
		}

		fmt.Printf("\n📖 Chapters in: %s\n\n", args[0])

		if len(chapters) == 0 {
			fmt.Println("⚠️ No chapters found.")
			return
		}

		for i, ch := range chapters {
			fmt.Printf("%2d. %s\n", i+1, ch)
		}
	},
}

func init() {
	RootCmd.AddCommand(chaptersCmd)
}
