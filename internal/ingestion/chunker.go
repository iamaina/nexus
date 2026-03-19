package ingestion

import (
	"strings"
)

const (
	DefaultChunkSize    = 600   // characters; roughly 150–250 tokens for most models
	DefaultChunkOverlap = 150   // context overlap to avoid cutting mid-sentence
	MinChunkLength      = 50    // skip tiny fragments
)

func ChunkText(text string, size, overlap int) []string {
	if size <= 0 {
		size = DefaultChunkSize
	}
	if overlap < 0 {
		overlap = DefaultChunkOverlap
	}

	var chunks []string
	textLen := len(text)
	start := 0

	for start < textLen {
		end := start + size
		if end > textLen {
			end = textLen
		}

		// Try to end at a natural boundary (sentence > space)
		if end < textLen {
			// Prefer period (sentence end)
			if lastPeriod := strings.LastIndexByte(text[start:end], '.'); lastPeriod >= 50 {
				end = start + lastPeriod + 1
			} else if lastSpace := strings.LastIndexByte(text[start:end], ' '); lastSpace >= 50 {
				end = start + lastSpace
			}
		}

		chunk := text[start:end]
		if len(chunk) >= MinChunkLength {
			chunks = append(chunks, chunk)
		}

		// Slide window with overlap
		start = end - overlap
		if start < 0 {
			start = 0
		}
		if start >= textLen {
			break
		}
	}

	return chunks
}