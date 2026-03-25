// Package ingestion provides utilities for ingesting and processing files.
package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"log/slog"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/parser"
)

func computeFileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // Safe: path comes from our configured sources
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logger.Error(ctx, "Failed to close file", slog.String("path", path), slog.Any("err", err))
		}
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// IngestFile processes a single file: hash, dedup check, extract, chunk, store metadata and chunks.
func IngestFile(ctx context.Context, services *app.Services, srcName, path string, force bool) (processed bool, err error) {
	hash, err := computeFileHash(ctx, path)
	if err != nil {
		logger.Error(ctx, "Failed to compute file hash", slog.String("path", path), slog.Any("err", err))
		return false, err
	}

	logger.Info(ctx, "File hash computed",
		slog.String("file", filepath.Base(path)),
		slog.String("hash_prefix", hash[:16]+"..."))
	if !force {
		upToDate, err := services.IsDocumentUpToDate(ctx, path, hash)
		if err != nil {
			logger.Error(ctx, "Dedup check failed", slog.Any("err", err))
			return false, err
		}
		if upToDate {
			logger.Info(ctx, "Skipping unchanged file", slog.String("file", filepath.Base(path)))
			return false, nil
		}
	}

	text, err := extractText(path)
	if err != nil {
		logger.Error(ctx, "Extraction failed", slog.String("path", path), slog.Any("err", err))
		return false, err
	}

	charCount := len(text)
	logger.Info(ctx, "Extracted text",
		slog.String("file", filepath.Base(path)),
		slog.Int("chars", charCount))

	toc := parser.ExtractTOC(text)

	cleanText := text
	pageOffset := 0
	tocEnd := findTOCEnd(text)

	if tocEnd != -1 {
		before := text[:tocEnd]
		pageOffset = len(parser.SplitPages(before))
		cleanText = text[tocEnd:]
	}

	pages := parser.SplitPages(cleanText)

	var chunks []string
	var enriched []app.EnrichedChunk

	for i, pageText := range pages {
		pageNum := i + 1 + pageOffset
		chapter := parser.AssignChapter(pageNum, toc)

		pageChunks := ChunkText(pageText, 0, 0)

		for _, c := range pageChunks {
			chunks = append(chunks, c)

			enriched = append(enriched, app.EnrichedChunk{
				Text:    c,
				Chapter: chapter,
			})
		}
	}

	logger.Info(ctx, "Chunked document",
		slog.String("file", filepath.Base(path)),
		slog.Int("toc_entries", len(toc)),
		slog.Int("chunk_count", len(chunks)),
		slog.Int("avg_chunk_chars", charCount/intMax(1, len(chunks))),
		slog.Int("total_chars", charCount))

	docID, err := services.InsertDocument(ctx, srcName, path, hash, charCount, len(chunks))
	if err != nil {
		logger.Error(ctx, "Metadata insert failed", slog.Any("err", err))
		return false, err
	}

	err = services.StoreChunks(ctx, docID, enriched)
	if err != nil {
		logger.Error(ctx, "Chunk storage failed", slog.Any("err", err))
		return false, err
	}

	err = services.EmbedChunks(ctx, docID, chunks)
	if err != nil {
		logger.Error(ctx, "Embedding failed", slog.Any("err", err))
		// we can continue or fail depending on your preference
	}

	logger.Debug(ctx, "Preview",
		slog.String("file", filepath.Base(path)),
		slog.String("preview", previewText(text, 300)))

	return true, nil
}

func findTOCEnd(text string) int {
	lines := strings.Split(text, "\n")

	for i := 0; i < len(lines)-2; i++ {
		line := strings.TrimSpace(lines[i])

		// Detect real paragraph (not TOC)
		if len(line) > 80 && !strings.Contains(line, ". .") {
			return strings.Index(text, line)
		}
	}

	return -1
}

func previewText(text string, maxLen int) string {
	if len(text) > maxLen {
		return text[:maxLen] + "..."
	}
	return text
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}
