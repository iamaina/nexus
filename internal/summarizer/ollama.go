// Package summarizer generates natural language answers from retrieved chunks via Ollama.
package summarizer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/iamaina/nexus/internal/models"
	"github.com/ollama/ollama/api"
)

// OllamaSummarizer answers questions using context chunks and an Ollama LLM.
type OllamaSummarizer struct {
	client *api.Client
	model  string
}

// New creates an OllamaSummarizer connected to the given base URL.
func New(baseURL, model string) (*OllamaSummarizer, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama URL %q: %w", baseURL, err)
	}
	return &OllamaSummarizer{
		client: api.NewClient(u, &http.Client{}),
		model:  model,
	}, nil
}

// Summarize produces a concise answer to question using the provided results as context.
func (s *OllamaSummarizer) Summarize(ctx context.Context, question string, results []models.Result) (string, error) {
	if len(results) == 0 {
		return "I couldn't find any relevant information in your knowledge base.", nil
	}

	var ctxBuilder strings.Builder
	for _, r := range results {
		fmt.Fprintf(&ctxBuilder, "From %s:\n%s\n\n", r.File, r.Text)
	}

	prompt := fmt.Sprintf(`You are a helpful, concise assistant.
Answer the question using ONLY the provided context.
If the context doesn't contain enough information, say "I don't have enough information."

Question: %s

Context:
%s

Answer:`, question, ctxBuilder.String())

	var answer strings.Builder
	err := s.client.Generate(ctx, &api.GenerateRequest{
		Model:  s.model,
		Prompt: prompt,
	}, func(resp api.GenerateResponse) error {
		answer.WriteString(resp.Response)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ollama generate: %w", err)
	}

	return answer.String(), nil
}
