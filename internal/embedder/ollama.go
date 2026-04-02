// Package embedder provides text embedding via Ollama.
package embedder

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/iamaina/nexus/internal/logger"
	"github.com/ollama/ollama/api"
)

// OllamaEmbedder generates text embeddings using an Ollama model.
type OllamaEmbedder struct {
	client *api.Client
	model  string
}

// New creates an OllamaEmbedder connected to the given base URL.
func New(baseURL, model string) (*OllamaEmbedder, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama URL %q: %w", baseURL, err)
	}
	return &OllamaEmbedder{
		client: api.NewClient(u, &http.Client{}),
		model:  model,
	}, nil
}

// Embed generates embeddings for a batch of texts and returns them in the same order.
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	logger.Debug(ctx, "Generating embeddings", slog.Int("count", len(texts)))

	resp, err := e.client.Embed(ctx, &api.EmbedRequest{
		Model: e.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}

	embeddings := make([][]float32, len(resp.Embeddings))
	copy(embeddings, resp.Embeddings)
	return embeddings, nil
}
