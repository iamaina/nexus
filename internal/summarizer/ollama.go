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
	"time"

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

// SummarizeCatchup generates a work-status summary from work-tracking documents.
// It uses a focused prompt that surfaces open/in-progress items, next actions,
// and blockers. Completed items are intentionally excluded from the output.
func (s *OllamaSummarizer) SummarizeCatchup(ctx context.Context, results []models.Result) (string, error) {
	if len(results) == 0 {
		return "No work-tracking files found in your knowledge base.\nAdd files under a `work-tracking` directory and re-ingest them.", nil
	}

	var ctxBuilder strings.Builder
	seen := make(map[string]bool)
	for _, r := range results {
		key := fmt.Sprintf("%d", r.DocumentID)
		label := filepath.Base(r.File)
		if r.Chapter != "" {
			label = fmt.Sprintf("%s — %s", label, r.Chapter)
		}
		header := ""
		if !seen[key] {
			seen[key] = true
			header = fmt.Sprintf("=== %s ===\n", filepath.Base(r.File))
		}
		fmt.Fprintf(&ctxBuilder, "%s[%s]\n%s\n\n", header, label, r.Text)
	}

	prompt := fmt.Sprintf(`You are reviewing personal work-tracking notes.

Rules:
- Only include items whose status is explicitly "Open" or "In Progress". Skip any item with status Closed, Done, Complete, or Merged.
- Group by status: In Progress first, then Open.
- For each item state: what it is (one sentence), the next concrete action, and what is blocking it if anything.
- When referencing MRs, issues, or tickets always include the full project path or URL alongside the number. Never write a bare reference like "!4968" — write "gl-infra/delivery!4968" or the full URL as found in the notes.
- Include any relevant URLs exactly as they appear in the notes.
- Be concise — one to two sentences per item.
- Do not invent information not present in the notes.
- If all items are closed or done, say so explicitly instead of listing anything.

Work tracking notes:
%s

Summary:`, ctxBuilder.String())

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

// GenerateWorkTrack creates a filled work-tracking document from conversation history
// and TEMPLATE.md content. Returns the suggested filename slug and complete file content.
func (s *OllamaSummarizer) GenerateWorkTrack(ctx context.Context, history []ChatMessage, template string) (slug, content string, err error) {
	if len(history) == 0 {
		return "", "", fmt.Errorf("no conversation history")
	}

	var conv strings.Builder
	for _, msg := range history {
		role := "User"
		if msg.Role == "assistant" {
			role = "Nexus"
		}
		fmt.Fprintf(&conv, "%s: %s\n\n", role, msg.Content)
	}

	prompt := fmt.Sprintf(`Fill in this work-tracking template from the conversation below.
Today's date: %s.

Rules:
- Output ONLY the filled template. No preamble, no explanation, no extra text.
- Replace every YYYY-MM-DD with today's date.
- Replace XXXXX with the issue number extracted from any URL or reference in the conversation.
- Status: "In Progress" unless the conversation says otherwise.
- Priority, Blocking, Repos: copy the placeholder text EXACTLY as-is if not explicitly mentioned.
- Duration: set to "%s → ongoing".
- Next action: one sentence — most concrete next step based on where the conversation ended.
- Where I left off: 3–6 bullet points covering what was discussed and what was learned.
- Knowledge gaps: use format "- [ ] Topic — why it matters for this work". No other format.
- Context: 2–3 sentences on what this is and why it matters. Include any GitLab issue, work item, or MR URL from the conversation verbatim.
- Key learnings: concrete facts only. No questions, no intentions, no placeholders.
- MRs/PRs table: leave the header row only if no MRs are mentioned. Do not add placeholder rows.
- Changes section: leave empty if no code changes are mentioned. Do not add placeholder paths.
- Do not invent anything not in the conversation.

Conversation:
%s

Template:
%s

Output:`,
		time.Now().Format("2006-01-02"),
		time.Now().Format("2006-01-02"),
		conv.String(),
		template,
	)

	var out strings.Builder
	if genErr := s.client.Generate(ctx, &api.GenerateRequest{
		Model:  s.model,
		Prompt: prompt,
	}, func(resp api.GenerateResponse) error {
		out.WriteString(resp.Response)
		return nil
	}); genErr != nil {
		return "", "", fmt.Errorf("ollama generate: %w", genErr)
	}

	return parseWorkTrackCreate(strings.TrimSpace(out.String()))
}

func parseWorkTrackCreate(raw string) (slug, content string, err error) {
	// Strip markdown code fences.
	raw = strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(raw, "```"); ok {
		if nl := strings.Index(after, "\n"); nl >= 0 {
			raw = strings.TrimSpace(after[nl+1:])
		}
	}
	raw = strings.TrimSuffix(strings.TrimRight(raw, "\n "), "```")

	lines := strings.Split(raw, "\n")

	// First pass: find a proper "# " H1 line (the model usually outputs one).
	h1Idx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			h1Idx = i
			break
		}
	}

	// Second pass: if no H1 found, the model may have continued from our
	// trailing "Output:" without including the "#". Find the first non-empty
	// line and treat it as the title.
	if h1Idx < 0 {
		for i, line := range lines {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				// Reconstruct as a proper H1.
				lines[i] = "# " + strings.TrimLeft(line, " \t")
				h1Idx = i
				break
			}
		}
	}

	if h1Idx < 0 {
		preview := raw
		if len(preview) > 400 {
			preview = preview[:400] + "…"
		}
		return "", "", fmt.Errorf("could not find H1 heading in LLM output\n\nRaw output:\n%s", preview)
	}

	content = strings.TrimSpace(strings.Join(lines[h1Idx:], "\n"))
	title := strings.TrimPrefix(lines[h1Idx], "# ")
	slug = workTrackSlugify(title)

	if slug == "" || content == "" {
		return "", "", fmt.Errorf("LLM returned empty slug or content")
	}
	return slug, content, nil
}

// workTrackSlugify converts a heading like "Issue 1755 — Decouple post-deploy migrations"
// into a filename slug like "gl-1755-decouple-post-deploy-migrations".
func workTrackSlugify(title string) string {
	s := strings.ToLower(title)
	// Normalise common separators to spaces.
	for _, sep := range []string{"—", "–", ":", "/", "_"} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	// Map known prefixes to short slug prefixes.
	s = strings.ReplaceAll(s, "issue ", "gl-")
	s = strings.ReplaceAll(s, "mr ", "mr-")

	// Build a kebab slug from letters, digits, and existing hyphens.
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	result := strings.TrimRight(b.String(), "-")

	// Truncate at ~50 chars, cutting at the last word boundary.
	if len(result) > 50 {
		result = result[:50]
		if idx := strings.LastIndex(result, "-"); idx > 20 {
			result = result[:idx]
		}
	}
	return result
}

// GenerateWorkTrackUpdate reads conversation history and existing file content and
// returns the new next-action sentence and journal bullet points to add.
func (s *OllamaSummarizer) GenerateWorkTrackUpdate(ctx context.Context, history []ChatMessage, existingContent string) (nextAction string, journalLines []string, err error) {
	if len(history) == 0 {
		return "", nil, fmt.Errorf("no conversation history")
	}

	var conv strings.Builder
	for _, msg := range history {
		role := "User"
		if msg.Role == "assistant" {
			role = "Nexus"
		}
		fmt.Fprintf(&conv, "%s: %s\n\n", role, msg.Content)
	}

	prompt := fmt.Sprintf(`You are updating a work-tracking file with new learnings from a conversation.

Output exactly this format, nothing else:
NEXT_ACTION: <one concrete sentence — what to do next>
JOURNAL:
- <bullet 1>
- <bullet 2>

Rules:
- NEXT_ACTION: write a concrete next step IF the conversation reveals one more specific than what is already in the file. If the existing next action is already specific and accurate, output exactly: NEXT_ACTION: (keep)
- JOURNAL: 3–6 bullet points, each a concrete fact or realisation from the conversation
- Only include NEW information not already captured in the existing file
- Do not repeat what is already in the file
- No headers, explanations, or extra text outside the format above

Existing file:
%s

New conversation:
%s

NEXT_ACTION:`,
		existingContent,
		conv.String(),
	)

	var out strings.Builder
	if genErr := s.client.Generate(ctx, &api.GenerateRequest{
		Model:  s.model,
		Prompt: prompt,
	}, func(resp api.GenerateResponse) error {
		out.WriteString(resp.Response)
		return nil
	}); genErr != nil {
		return "", nil, fmt.Errorf("ollama generate: %w", genErr)
	}

	return parseWorkTrackUpdate(strings.TrimSpace(out.String()))
}

func parseWorkTrackUpdate(raw string) (nextAction string, journalLines []string, err error) {
	inJournal := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "NEXT_ACTION:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "NEXT_ACTION:"))
			// "(keep)" means the LLM decided the existing next action is already good.
			if v != "(keep)" {
				nextAction = v
			}
		} else if trimmed == "JOURNAL:" {
			inJournal = true
		} else if inJournal && strings.HasPrefix(trimmed, "-") {
			journalLines = append(journalLines, trimmed)
		}
	}
	if nextAction == "" && len(journalLines) == 0 {
		return "", nil, fmt.Errorf("could not parse update output")
	}
	return nextAction, journalLines, nil
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

	return fmt.Sprintf(`You are an assistant with access to a personal knowledge base and live environment data.
Answer the question using ONLY the context provided below. Always answer in English.
Rules:
- Answer only from the provided context — never use your pre-trained knowledge
- The ONLY sources that exist are those explicitly labeled in the sections below. Do not cite any source not listed there.
- Cite knowledge base sources inline using the exact label shown in brackets, e.g. "According to [SRE-handbook — Patch releases], ..."
- Reference live context sources as [live:<name>], e.g. "Your cluster currently shows [live:work-status] ..."
- When multiple sources cover the same topic, synthesise them into one coherent explanation
- Prefer live context over static sources when they conflict — live data is more current
- Include specific details, comparisons, and examples that appear in the context
- Do not invent URLs, citations, statistics, page numbers, or any information not present in the context
- If the context does not contain enough information to answer, say "The provided sources do not contain sufficient information to answer this." Do not fill the gap with general knowledge.

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

	return fmt.Sprintf(`You are an assistant with access to a personal knowledge base and live environment data.
Answer the question using ONLY the context provided below. Always answer in English.
Rules:
- Answer only from the provided context — never use your pre-trained knowledge
- The ONLY sources that exist are those explicitly labeled in the sections below. Do not cite any source not listed there.
- Cite knowledge base sources inline using the exact label shown in brackets, e.g. "According to [SRE-handbook — Patch releases], ..."
- Reference live context sources as [live:<name>], e.g. "Your cluster currently shows [live:work-status] ..."
- When multiple sources cover the same topic, synthesise them into one coherent explanation
- Prefer live context over static sources when they conflict — live data is more current
- Include specific details, comparisons, and examples that appear in the context
- Do not invent URLs, citations, statistics, page numbers, or any information not present in the context
- If the context does not contain enough information to answer, say "The provided sources do not contain sufficient information to answer this." Do not fill the gap with general knowledge.
- Use the conversation history to understand follow-up questions and pronouns like "it", "that", "this"
%s%s%s
Question: %s

Answer:`, histBuilder.String(), liveSection, staticSection, question)
}
