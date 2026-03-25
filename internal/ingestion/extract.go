// Package ingestion provides utilities for extracting text from various file formats.
package ingestion

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extractText is the single entry point for all supported files
func extractText(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".pdf":
		return extractPDFText(path)
	case ".md", ".txt":
		return extractMarkdownOrText(path)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
}

func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("Warning: failed to close file: %v", err)
		}
	}()

	var sb strings.Builder
	for pageNum := 1; pageNum <= r.NumPage(); pageNum++ {
		p := r.Page(pageNum)
		text, err := p.GetPlainText(nil)
		if err != nil {
			log.Printf("Page %d: %v", pageNum, err)
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n\f\n") // page break
	}

	return sb.String(), nil
}

func extractMarkdownOrText(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Safe: path comes from our configured sources
	if err != nil {
		return "", err
	}
	return string(data), nil
}
