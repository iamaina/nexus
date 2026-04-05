package layout

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// ExtractPDF runs the Python extractor on path and returns raw span JSON.
// Kept for callers that need the raw bytes (e.g. layout debug command).
func ExtractPDF(path string) ([]byte, error) {
	if _, err := os.Stat(".venv/bin/python"); os.IsNotExist(err) {
		return nil, fmt.Errorf("python environment not set up — run: make setup-python")
	}
	cmd := exec.Command(".venv/bin/python", "scripts/extract_pdf.py", path) //nolint:gosec
	return cmd.CombinedOutput()
}

// extractPDFSpans calls ExtractPDF and unmarshals the result into []Span.
func extractPDFSpans(path string) ([]Span, error) {
	data, err := ExtractPDF(path)
	if err != nil {
		return nil, err
	}
	var spans []Span
	if err := json.Unmarshal(data, &spans); err != nil {
		return nil, fmt.Errorf("unmarshal spans: %w", err)
	}
	return spans, nil
}
