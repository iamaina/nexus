// Package summarizer generates natural language answers from retrieved chunks via Ollama.
package summarizer

import (
	"context"
	"fmt"
	"io"
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

// ChatMessage is a single turn in a conversation (role: "user" or "assistant").
type ChatMessage struct {
	Role    string
	Content string
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

	prompt := buildPrompt(question, results, liveOutputs)

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

// SummarizeChat is like SummarizeWithLive but prepends conversation history so the
// model can answer follow-up questions. Only the last 6 exchanges are included to
// keep the prompt size bounded.
func (s *OllamaSummarizer) SummarizeChat(ctx context.Context, history []ChatMessage, question string, results []models.Result, liveOutputs []live.Output) (string, error) {
	// Cap history to last 6 exchanges (12 messages: 6 user + 6 assistant)
	const maxMessages = 12
	if len(history) > maxMessages {
		history = history[len(history)-maxMessages:]
	}

	prompt := buildChatPrompt(history, question, results, liveOutputs)

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

// buildPrompt constructs the full LLM prompt from the question, retrieved chunks,
// and any live context outputs. Extracted as a package-level function for testability.
func buildPrompt(question string, results []models.Result, liveOutputs []live.Output) string {
	var ctxBuilder strings.Builder
	for _, r := range results {
		book := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
		source := book
		if r.Chapter != "" {
			source = fmt.Sprintf("%s — %s", book, r.Chapter)
		}
		fmt.Fprintf(&ctxBuilder, "[%s]\n%s\n\n", source, r.Text)
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

	return fmt.Sprintf(`You are a knowledgeable assistant with access to a personal library and live environment data.
Answer the question using the context below. Always answer in English.
Rules:
- Cite knowledge base sources inline using the label in brackets, e.g. "According to [progit — Git Basics], ..."
- Reference live context sources as [live:<name>], e.g. "Your cluster currently shows [live:work-status] ..."
- When multiple sources cover the same topic, synthesise them into one coherent explanation
- Prefer live context over static sources when they conflict — live data is more current
- Include specific details, comparisons, and examples that appear in the context
- Do not invent URLs, page numbers, or any information not present in the context
- If the context genuinely does not contain enough information, say so briefly

Question: %s
%s%s
Answer:`, question, liveSection, staticSection)
}

// StreamChat generates a response like SummarizeChat but streams each token
// directly to w as it arrives. Returns the complete response text for history.
func (s *OllamaSummarizer) StreamChat(ctx context.Context, w io.Writer, history []ChatMessage, question string, results []models.Result, liveOutputs []live.Output) (string, error) {
	const maxMessages = 12
	if len(history) > maxMessages {
		history = history[len(history)-maxMessages:]
	}

	prompt := buildChatPrompt(history, question, results, liveOutputs)

	var full strings.Builder
	err := s.client.Generate(ctx, &api.GenerateRequest{
		Model:  s.model,
		Prompt: prompt,
	}, func(resp api.GenerateResponse) error {
		_, _ = fmt.Fprint(w, resp.Response)
		full.WriteString(resp.Response)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ollama generate: %w", err)
	}
	return full.String(), nil
}

// buildChatPrompt is like buildPrompt but prepends conversation history so the
// model can interpret follow-up questions correctly.
func buildChatPrompt(history []ChatMessage, question string, results []models.Result, liveOutputs []live.Output) string {
	var ctxBuilder strings.Builder
	for _, r := range results {
		book := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
		source := book
		if r.Chapter != "" {
			source = fmt.Sprintf("%s — %s", book, r.Chapter)
		}
		fmt.Fprintf(&ctxBuilder, "[%s]\n%s\n\n", source, r.Text)
	}

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

	var histBuilder strings.Builder
	if len(history) > 0 {
		histBuilder.WriteString("\nConversation so far:\n")
		for _, msg := range history {
			role := "User"
			if msg.Role == "assistant" {
				role = "Assistant"
			}
			fmt.Fprintf(&histBuilder, "%s: %s\n\n", role, msg.Content)
		}
	}

	return fmt.Sprintf(`You are a knowledgeable assistant with access to a personal library and live environment data.
Answer the question using the context below. Always answer in English.
Rules:
- Cite knowledge base sources inline using the label in brackets, e.g. "According to [progit — Git Basics], ..."
- Reference live context sources as [live:<name>], e.g. "Your cluster currently shows [live:work-status] ..."
- When multiple sources cover the same topic, synthesise them into one coherent explanation
- Prefer live context over static sources when they conflict — live data is more current
- Include specific details, comparisons, and examples that appear in the context
- Do not invent URLs, page numbers, or any information not present in the context
- If the context genuinely does not contain enough information, say so briefly
- Use the conversation history to understand follow-up questions and pronouns like "it", "that", "this"
%s%s%s
Question: %s

Answer:`, histBuilder.String(), liveSection, staticSection, question)
}
