// Package classifier identifies the type, language, and metadata of a document
// using a local LLM via Ollama and returns structured filing instructions.
package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/layout"
	"github.com/ollama/ollama/api"
)

// Classification holds the structured result of classifying a single document.
type Classification struct {
	// DocType is one of the fixed categories defined in the prompt.
	DocType string `json:"doc_type"`
	// Language is the BCP-47 language code of the document's main content (e.g. "en", "nl").
	Language string `json:"language"`
	// Institution is the issuing organisation if detectable. Empty string if unknown.
	Institution string `json:"institution"`
	// Date is the document date in YYYY-MM-DD or YYYY-MM format. Empty string if unknown.
	Date string `json:"date"`
	// Filename is the suggested clean filename WITHOUT extension.
	// Example: "2024-03_ING_Bank_Statement"
	Filename string `json:"filename"`
	// DestDir is the relative sub-directory inside PersonalDocs.
	// Example: "finance/bank-statements"
	DestDir string `json:"dest_dir"`
	// Topic is the main subject of the document — used by nexus organise to match
	// existing directories. E.g. "Kubernetes", "Terraform", "Git". Empty for personal docs.
	Topic string `json:"topic"`
}

// Classifier classifies documents using a local Ollama model.
type Classifier struct {
	client *api.Client
	model  string
}

// New creates a Classifier connected to the Ollama instance at baseURL using the given model.
func New(baseURL, model string) (*Classifier, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama URL %q: %w", baseURL, err)
	}
	return &Classifier{
		client: api.NewClient(u, &http.Client{}),
		model:  model,
	}, nil
}

// maxPreviewChars is the character limit of extracted text sent to the LLM.
// 1200 chars is enough for a document header + first paragraph while staying
// well within the classification model's context window.
const maxPreviewChars = 1200

// Classify extracts readable text from path, sends it to the LLM, and returns
// structured filing metadata. It never returns an error for classification
// failures — callers should fall back to a safe default on error.
func (c *Classifier) Classify(ctx context.Context, path string) (*Classification, error) {
	preview := extractPreview(path)
	prompt := buildPrompt(filepath.Base(path), preview)

	var raw strings.Builder
	err := c.client.Generate(ctx, &api.GenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		Format: json.RawMessage(`"json"`),
	}, func(resp api.GenerateResponse) error {
		raw.WriteString(resp.Response)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ollama classify: %w", err)
	}

	var cl Classification
	if err := json.Unmarshal([]byte(raw.String()), &cl); err != nil {
		return nil, fmt.Errorf("parse classification JSON: %w\nraw: %s", err, raw.String())
	}

	cl.sanitise()
	return &cl, nil
}

// extractPreview uses the existing layout.Extract pipeline to get real document
// text rather than raw bytes (which are binary garbage for PDFs).
// Falls back to an empty string if extraction fails — the LLM will still
// classify based on the filename alone.
func extractPreview(path string) string {
	spans, err := layout.Extract(path)
	if err != nil || len(spans) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, s := range spans {
		t := strings.TrimSpace(s.Text)
		if t == "" {
			continue
		}
		sb.WriteString(t)
		sb.WriteRune('\n')
		if sb.Len() >= maxPreviewChars {
			break
		}
	}
	return sb.String()
}

func buildPrompt(filename, preview string) string {
	previewSection := ""
	if preview != "" {
		previewSection = fmt.Sprintf("\nDocument text (first page):\n\"\"\"\n%s\n\"\"\"", preview)
	}

	return fmt.Sprintf(`You are a document filing assistant. Classify the document below and return a JSON object.

Filename: %s%s

IMPORTANT RULES:
- "dest_dir" must be a SHORT category path like "finance/bank-statements" or "identity/permits".
  Do NOT use the source file path. Do NOT include "downloads", "desktop", or any folder from the filename.
- "filename" must NOT include the file extension. Use format: YYYY-MM_Institution_Description.
  Example: "2024-03_ING_Bank_Statement" or "2024-05_IND_Residence_Permit"
- Output ONLY the JSON object. No markdown, no explanation.

Category guide for dest_dir:
  invoice           → finance/invoices
  bank_statement    → finance/bank-statements
  tax               → finance/tax
  receipt           → finance/receipts
  insurance         → insurance
  contract          → legal/contracts
  id_document       → identity/permits  (passports, residence permits, IDs, DigiD)
  medical           → medical
  letter            → correspondence
  cv                → career
  book / article    → library
  other             → other

Return this exact JSON (use empty string for unknown fields):
{
  "doc_type":    "<invoice|bank_statement|contract|letter|report|book|article|cv|id_document|medical|insurance|tax|receipt|other>",
  "language":    "<BCP-47 code: en, nl, fr, de, ...>",
  "institution": "<issuing organisation, or empty string>",
  "date":        "<YYYY-MM-DD or YYYY-MM or empty string>",
  "filename":    "<clean name WITHOUT extension>",
  "dest_dir":    "<relative path inside PersonalDocs>",
  "topic":       "<main subject for filing — e.g. Kubernetes, Terraform, Git; empty for personal docs>"
}`, filename, previewSection)
}

// sanitise cleans up LLM output quirks in-place.
func (cl *Classification) sanitise() {
	cl.DocType = strings.ToLower(strings.TrimSpace(cl.DocType))
	cl.Language = strings.ToLower(strings.TrimSpace(cl.Language))
	cl.Institution = strings.TrimSpace(cl.Institution)
	if strings.EqualFold(cl.Institution, "unknown") {
		cl.Institution = ""
	}
	cl.Date = strings.TrimSpace(cl.Date)
	cl.DestDir = strings.ToLower(strings.TrimSpace(cl.DestDir))
	cl.Topic = strings.TrimSpace(cl.Topic)

	// Strip file extension from filename if the LLM included it.
	cl.Filename = strings.TrimSpace(cl.Filename)
	if ext := filepath.Ext(cl.Filename); ext != "" {
		cl.Filename = strings.TrimSuffix(cl.Filename, ext)
	}

	// Fallback: if dest_dir or filename came back empty, set safe defaults.
	if cl.DestDir == "" {
		cl.DestDir = "other"
	}
	if cl.Filename == "" {
		cl.Filename = "document"
	}
}
