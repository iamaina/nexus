// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/live"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/spf13/cobra"
)

var (
	queryThreshold float64
	querySource    []string
	queryCategory  string
	queryModel     string
	showSources    bool
	noLive         bool
)

var queryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Ask a question in plain English and get a cited answer",
	Long: `Ask a question in plain English and get a cited answer.

Embeds the question, performs a vector search across your ingested sources,
expands results with structural context, and generates an answer via Ollama.

Since: v0.0.1  (--model added v0.0.2; --no-live, --sources added v0.1.0; --category added v0.2.0)`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		queryStart := time.Now()
		question := strings.Join(args, " ")

		threshold := queryThreshold
		if threshold == 0 {
			threshold = a.Config.RelevanceThreshold
		}
		if threshold == 0 {
			threshold = 0.70
		}
		// Resolve summarizer — use --model override if provided
		sum := a.Summarizer
		if queryModel != "" {
			sum = a.Summarizer.WithModel(queryModel)
		}

		logArgs := []any{
			slog.String("component", "query"),
			slog.String("event", "query.start"),
			slog.Float64("threshold", threshold),
			slog.String("model", sum.Model()),
		}
		if len(querySource) > 0 {
			logArgs = append(logArgs, slog.String("source", strings.Join(querySource, ",")))
		}
		logger.Info(ctx, "query.start", logArgs...)

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
		candidates, err := a.Chunks.Search(ctx, queryVec, 15, buildSearchFilter(a.Config, querySource, queryCategory))
		if err != nil {
			logger.Error(ctx, "query.search_failed",
				slog.String("component", "query"),
				slog.String("event", "query.search_failed"),
				slog.Any("err", err))
			return
		}

		// Filter by threshold; drop title-only placeholder chunks (< 80 chars)
		var matched []models.Result
		for _, r := range candidates {
			if r.Score >= threshold && len(strings.TrimSpace(r.Text)) > 80 {
				matched = append(matched, r)
			}
		}

		// Expand each matched chunk with its structural children
		seen := make(map[string]bool)
		var results []models.Result
		for _, r := range matched {
			key := fmt.Sprintf("%d:%d", r.DocumentID, r.ChunkIndex)
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, r)

			children, err := a.Chunks.FetchContext(ctx, r, 5)
			if err != nil {
				logger.Debug(ctx, "context fetch failed",
					slog.String("component", "query"),
					slog.Any("err", err))
				continue
			}
			for _, child := range children {
				ck := fmt.Sprintf("%d:%d", child.DocumentID, child.ChunkIndex)
				if !seen[ck] && len(strings.TrimSpace(child.Text)) > 80 {
					seen[ck] = true
					results = append(results, child)
				}
			}
		}

		// Hard cap: never send more than 12 chunks to the LLM
		if len(results) > 12 {
			results = results[:12]
		}

		logger.Debug(ctx, "query.search_done",
			slog.String("component", "query"),
			slog.String("event", "query.search_done"),
			slog.Int("candidates", len(candidates)),
			slog.Int("matched", len(matched)),
			slog.Int("after_expansion", len(results)),
			slog.Int64("duration_ms", time.Since(t).Milliseconds()),
		)

		// Fetch and run live context sources (skip if --no-live or none registered).
		var liveOutputs []live.Output
		if !noLive {
			liveSources, liveErr := a.ContextSources.List(ctx)
			if liveErr != nil {
				logger.Warn(ctx, "Failed to list context sources", slog.Any("err", liveErr))
			} else if len(liveSources) > 0 {
				logger.Debug(ctx, "query.live_start",
					slog.String("component", "query"),
					slog.Int("sources", len(liveSources)),
				)
				liveOutputs = live.RunAll(ctx, liveSources, 5*time.Second)
				var liveOK int
				for _, o := range liveOutputs {
					if o.Err == nil {
						liveOK++
					} else {
						logger.Warn(ctx, "Live source failed",
							slog.String("name", o.Name),
							slog.Any("err", o.Err))
					}
				}
				logger.Debug(ctx, "query.live_done",
					slog.String("component", "query"),
					slog.Int("ok", liveOK),
					slog.Int("failed", len(liveOutputs)-liveOK),
				)
			}
		}

		fmt.Printf("\n🔍 Query: %s\n\n", question)

		if len(results) == 0 && len(liveOutputs) == 0 {
			fmt.Println("No sufficiently relevant information found.")
			if len(candidates) > 0 {
				best := candidates[0]
				fmt.Printf("(best match scored %.2f — threshold is %.2f; try --threshold %.2f to include it)\n",
					best.Score, threshold, best.Score-0.01)
			} else {
				fmt.Println("(no candidates retrieved — is the source ingested?)")
			}
			return
		}

		// Show live source names when they contributed output.
		for _, o := range liveOutputs {
			if o.Err == nil && o.Text != "" {
				fmt.Printf("  ⚡ %s\n", o.Name)
			}
		}

		// Always show file paths so the user knows where answers came from.
		// --sources additionally shows the chunk preview.
		for _, r := range results {
			if r.Score > 0 {
				file := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
				fmt.Printf("  📄 %s", file)
				if r.Chapter != "" {
					fmt.Printf(" — %s", r.Chapter)
				}
				fmt.Println()
			}
		}
		fmt.Println()

		if showSources {
			fmt.Printf("--- Sources (%d) ---\n", len(results))
			for i, r := range results {
				book := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
				preview := strings.ReplaceAll(strings.TrimSpace(r.Text), "\n", " ")
				if len(preview) > 120 {
					preview = preview[:120] + "…"
				}
				if r.Score > 0 {
					fmt.Printf("  [%d] %.2f  %s — %s\n      %s\n", i+1, r.Score, book, r.Chapter, preview)
				} else {
					fmt.Printf("  [%d]  ↳   %s — %s\n      %s\n", i+1, book, r.Chapter, preview)
				}
			}
			fmt.Println()
		}

		// Generate answer
		t = time.Now()
		answer, err := sum.SummarizeWithLive(ctx, question, results, liveOutputs)
		if err != nil {
			logger.Error(ctx, "query.summarize_failed",
				slog.String("component", "query"),
				slog.String("event", "query.summarize_failed"),
				slog.Any("err", err))
			return
		}

		tty := isTerminal()
		cols, _ := termSize()
		fmt.Print("Answer:\n")
		fmt.Print(renderMarkdown(answer, tty, cols))

		// Deduplicated file paths after the answer — open any of these to read more.
		seenFiles := make(map[string]bool)
		var sourceFiles []string
		for _, r := range results {
			if r.Score > 0 && !seenFiles[r.File] {
				seenFiles[r.File] = true
				sourceFiles = append(sourceFiles, r.File)
			}
		}
		if len(sourceFiles) > 0 {
			home, _ := os.UserHomeDir()
			fmt.Println("\n  Open to read more:")
			for _, f := range sourceFiles {
				p := f
				if home != "" {
					p = strings.Replace(f, home, "~", 1)
				}
				fmt.Printf("    %s\n", p)
			}
		}

		logger.Info(ctx, "query.complete",
			slog.String("component", "query"),
			slog.String("event", "query.complete"),
			slog.String("model", sum.Model()),
			slog.Int("sources", len(results)),
			slog.Int64("summarize_ms", time.Since(t).Milliseconds()),
			slog.Int64("total_ms", time.Since(queryStart).Milliseconds()),
		)
	},
}

func init() {
	queryCmd.Flags().Float64Var(&queryThreshold, "threshold", 0, "relevance threshold (overrides config, default 0.70)")
	queryCmd.Flags().StringSliceVar(&querySource, "source", nil, "restrict search to one or more sources (repeatable: --source a --source b, or comma-separated: --source a,b)")
	queryCmd.Flags().StringVar(&queryCategory, "category", "", "restrict search to sources in this category (e.g. reference, work) (added v0.2.0)")
	queryCmd.Flags().StringVar(&queryModel, "model", "", "generation model to use (overrides config, e.g. llama3.1:8b)")
	queryCmd.Flags().BoolVar(&showSources, "sources", false, "show retrieved source chunks before the answer")
	queryCmd.Flags().BoolVar(&noLive, "no-live", false, "skip running registered live context sources")
	RootCmd.AddCommand(queryCmd)
}
