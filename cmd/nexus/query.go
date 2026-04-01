// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/spf13/cobra"
)

var queryThreshold float64

var queryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Ask a question against your local knowledge base",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		queryStart := time.Now()
		question := args[0]

		threshold := queryThreshold
		if threshold == 0 {
			threshold = float64(a.Config.RelevanceThreshold)
		}
		if threshold == 0 {
			threshold = 0.65
		}
		logger.Info(ctx, "query.start",
			slog.String("component", "query"),
			slog.String("event", "query.start"),
			slog.Float64("threshold", threshold),
		)

		// Embed the question
		t := time.Now()
		embeddings, err := a.Embedder.Embed(ctx, []string{question})
		if err != nil {
			logger.Error(ctx, "query.embed_failed",
				slog.String("component", "query"),
				slog.String("event", "query.embed_failed"),
				slog.Any("err", err))
			return
		}
		queryVec := embeddings[0]
		logger.Debug(ctx, "query.embedded",
			slog.String("component", "query"),
			slog.String("event", "query.embedded"),
			slog.Int("dim", len(queryVec)),
			slog.Int64("duration_ms", time.Since(t).Milliseconds()),
		)

		// Vector search
		t = time.Now()
		candidates, err := a.Chunks.Search(ctx, queryVec, 8)
		if err != nil {
			logger.Error(ctx, "query.search_failed",
				slog.String("component", "query"),
				slog.String("event", "query.search_failed"),
				slog.Any("err", err))
			return
		}

		// Filter by threshold
		var results []models.Result
		for _, r := range candidates {
			if r.Score >= threshold {
				results = append(results, r)
			}
		}
		logger.Debug(ctx, "query.search_done",
			slog.String("component", "query"),
			slog.String("event", "query.search_done"),
			slog.Int("candidates", len(candidates)),
			slog.Int("results_above_threshold", len(results)),
			slog.Int64("duration_ms", time.Since(t).Milliseconds()),
		)

		fmt.Printf("\n🔍 Query: %s\n\n", question)

		if len(results) == 0 {
			fmt.Println("⚠️  No sufficiently relevant information found.")
			return
		}

		// Generate answer
		t = time.Now()
		answer, err := a.Summarizer.Summarize(ctx, question, results)
		if err != nil {
			logger.Error(ctx, "query.summarize_failed",
				slog.String("component", "query"),
				slog.String("event", "query.summarize_failed"),
				slog.Any("err", err))
			return
		}
		logger.Info(ctx, "query.complete",
			slog.String("component", "query"),
			slog.String("event", "query.complete"),
			slog.Int64("summarize_ms", time.Since(t).Milliseconds()),
			slog.Int64("total_ms", time.Since(queryStart).Milliseconds()),
		)

		fmt.Printf("Answer:\n\n%s\n", answer)
	},
}

func init() {
	queryCmd.Flags().Float64Var(&queryThreshold, "threshold", 0, "relevance threshold (overrides config)")
	RootCmd.AddCommand(queryCmd)
}
