// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

var queryThreshold float64

var queryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Ask a question against your local knowledge base",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		services, ok := ctx.Value("services").(*app.Services)
		if !ok {
			logger.Error(ctx, "Services not found")
			return
		}

		question := args[0]
		logger.Info(ctx, "Query received", slog.String("question", question))

		threshold := queryThreshold
		if threshold == 0 {
			threshold = float64(services.Config.RelevanceThreshold) // your existing field
		}
		if threshold == 0 {
			threshold = 0.65
		}
		logger.Info(ctx, "Using relevance threshold", slog.Float64("threshold", threshold))

		// Embed question
		baseURL, _ := url.Parse("http://localhost:11434")
		client := api.NewClient(baseURL, &http.Client{})

		resp, err := client.Embed(ctx, &api.EmbedRequest{
			Model: "nomic-embed-text",
			Input: []string{question},
		})
		if err != nil {
			logger.Error(ctx, "Embedding failed", slog.Any("err", err))
			return
		}

		queryVec := resp.Embeddings[0]
		logger.Info(ctx, "Question embedded", slog.Int("dim", len(queryVec)))

		// Convert to Postgres vector string
		vectorStr := vectorToString(queryVec)

		// Retrieve chunks

		var results []app.Result
		rows, err := services.DB.Query(ctx, `
			SELECT 
				d.file_path,
				c.chunk_text,
				1 - (c.embedding <=> $1::vector) as similarity
			FROM chunks c
			JOIN documents d ON c.document_id = d.id
			ORDER BY c.embedding <=> $1::vector
			LIMIT 8`,
			vectorStr,
		)
		if err != nil {
			logger.Error(ctx, "Search failed", slog.Any("err", err))
			return
		}
		defer rows.Close()

		fmt.Printf("\n🔍 Query: %s\n\n", question)

		for rows.Next() {
			var r app.Result
			if err := rows.Scan(&r.File, &r.Text, &r.Score); err != nil {
				logger.Error(ctx, "Failed to scan row", slog.Any("err", err))
				continue
			}
			if r.Score >= threshold {
				results = append(results, r)
			}
		}

		if len(results) == 0 {
			fmt.Println("⚠️  No sufficiently relevant information found.")
			return
		}

		// Generate nice answer
		answer, err := services.Summarize(ctx, question, results)
		if err != nil {
			logger.Error(ctx, "Summarization failed", slog.Any("err", err))
			return
		}

		fmt.Printf("\n🔍 Answer to: %s\n\n%s\n", question, answer)
	},
}

func vectorToString(vec []float32) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%f", v))
	}
	sb.WriteString("]")
	return sb.String()
}

func init() {
	RootCmd.AddCommand(queryCmd)
}
