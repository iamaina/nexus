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
	Short: "List real chapters from a book and summarize them",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		services, ok := ctx.Value(app.ServicesKey).(*app.Services)
		if !ok {
			logger.Error(ctx, "Services not found")
			return
		}

		book := args[0]

		// 1. Get distinct chapters from this book only
		rows, err := services.DB.Query(ctx, `
			SELECT DISTINCT c.chapter
			FROM chunks c
			JOIN documents d ON c.document_id = d.id
			WHERE d.file_path ILIKE '%' || $1 || '%'
			AND c.chapter IS NOT NULL
			AND c.chapter != ''
			ORDER BY c.chapter
		`, book)
		if err != nil {
			logger.Error(ctx, "Failed to get chapters", slog.Any("err", err))
			return
		}
		defer rows.Close()

		fmt.Printf("\n📖 Chapters in: %s\n\n", book)

		i := 1
		for rows.Next() {
			var chapter string
			if err := rows.Scan(&chapter); err != nil {
				logger.Error(ctx, "Failed to scan row", slog.Any("err", err))
				continue
			}

			fmt.Printf("%2d. %s\n", i, chapter)
			i++
		}

		if i == 1 {
			fmt.Println("⚠️ No chapters found.")
		}

		// fmt.Println("\nEnter number to summarize that chapter, or 'all' for full book:")
		// var input string
		// fmt.Print("> ")
		// fmt.Scanln(&input)

		// if input == "all" {
		// 	answer, _ := services.Summarize(ctx, book+" (full book)", nil)
		// 	fmt.Println("\n" + answer)
		// 	return
		// }

		// num, _ := strconv.Atoi(input)
		// if num > 0 && num <= len(chapters) {
		// 	selectedChapter := chapters[num-1]
		// 	fmt.Printf("\nSummarizing chapter: %s\n\n", selectedChapter)

		// 	answer, _ := services.Summarize(ctx, selectedChapter, nil)
		// 	fmt.Println(answer)
		// }
	},
}

func init() {
	RootCmd.AddCommand(chaptersCmd)
}
