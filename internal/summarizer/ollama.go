// Package summarizer generates natural language answers from retrieved chunks via Ollama.
package summarizer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/live"
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

// Model returns the generation model name currently configured.
func (s *OllamaSummarizer) Model() string {
	return s.model
}

// WithModel returns a copy of the summarizer using a different generation model.
// The Ollama client is reused so there is no reconnection overhead.
func (s *OllamaSummarizer) WithModel(model string) *OllamaSummarizer {
	return &OllamaSummarizer{client: s.client, model: model}
}

// Summarize produces a concise answer to question using the provided results as context.
func (s *OllamaSummarizer) Summarize(ctx context.Context, question string, results []models.Result) (string, error) {
	return s.SummarizeWithLive(ctx, question, results, nil)
}

// SummarizeWithLive is like Summarize but additionally injects live command
// outputs (kubectl, terraform, etc.) into the prompt as a separate section.
func (s *OllamaSummarizer) SummarizeWithLive(ctx context.Context, question string, results []models.Result, liveOutputs []live.Output) (string, error) {
	if len(results) == 0 && len(liveOutputs) == 0 {
		return "I couldn't find any relevant information in your knowledge base.", nil
	}

	var ctxBuilder strings.Builder
	for i, r := range results {
		book := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
		source := book
		if r.Chapter != "" {
			source = fmt.Sprintf("%s — %s", book, r.Chapter)
		}
		fmt.Fprintf(&ctxBuilder, "[%d] %s\n%s\n\n", i+1, source, r.Text)
	}

	// Build live context section — only include successful outputs.
	var liveBuilder strings.Builder
	for _, o := range liveOutputs {
		if o.Err != nil || o.Text == "" {
			continue
		}
		fmt.Fprintf(&liveBuilder, "[live:%s] $ %s\n%s\n\n", o.Name, o.Command, o.Text)
	}

	liveSection := ""
	if liveBuilder.Len() > 0 {
		liveSection = "\nLive Context (current state of your environment):\n" + liveBuilder.String()
	}

	staticSection := ""
	if ctxBuilder.Len() > 0 {
		staticSection = "\nKnowledge Base:\n" + ctxBuilder.String()
	}

	prompt := fmt.Sprintf(`You are a knowledgeable assistant with access to a personal library and live environment data.
Answer the question using the context below. Always answer in English.
Rules:
- Cite knowledge base sources inline, e.g. "According to [1] Pro Git — Git Basics, ..."
- Reference live context sources as [live:<name>], e.g. "Your cluster currently shows [live:kubectl] ..."
- When multiple sources cover the same topic, synthesise them into one coherent explanation
- Prefer live context over static sources when they conflict — live data is more current
- Include specific details, comparisons, and examples that appear in the context
- Do not invent URLs, page numbers, or any information not present in the context
- If the context genuinely does not contain enough information, say so briefly

Question: %s
%s%s
Answer:`, question, liveSection, staticSection)

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
