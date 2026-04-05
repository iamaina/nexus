// Package ingestion handles the full pipeline for ingesting a document into nexus.
package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/layout"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
)

// IngestFile runs the full pipeline for a single file:
// extract → layout → chunk → store text → embed → store embeddings.
// Returns true if the file was ingested, false if it was skipped (up to date).
func IngestFile(ctx context.Context, a *app.Application, path, source string, force bool) (bool, error) {
	start := time.Now()

	// 1. Hash the file for deduplication
	hash, err := hashFile(path)
	if err != nil {
		return false, fmt.Errorf("hash %s: %w", path, err)
	}

	// 2. Skip if already up to date or duplicate (unless forced)
	if !force {
		upToDate, err := a.Documents.IsUpToDate(ctx, path, hash)
		if err != nil {
			return false, fmt.Errorf("dedup check: %w", err)
		}
		if upToDate {
			logger.Info(ctx, "file.skipped",
				slog.String("component", "ingestion"),
				slog.String("event", "file.skipped"),
				slog.String("source", source),
				slog.String("file_path", path),
				slog.String("reason", "up_to_date"),
			)
			return false, nil
		}

		// Same content already ingested from a different path — warn and skip.
		duplicate, err := a.Documents.FindDuplicate(ctx, path, hash)
		if err != nil {
			return false, fmt.Errorf("duplicate check: %w", err)
		}
		if duplicate != "" {
			logger.Warn(ctx, "file.duplicate",
				slog.String("component", "ingestion"),
				slog.String("event", "file.duplicate"),
				slog.String("source", source),
				slog.String("file_path", path),
				slog.String("already_ingested_at", duplicate),
			)
			return false, nil
		}
	}

	logger.Info(ctx, "file.start",
		slog.String("component", "ingestion"),
		slog.String("event", "file.start"),
		slog.String("source", source),
		slog.String("file_path", path),
	)

	// 3. Extract spans
	t := time.Now()
	spans, err := layout.Extract(path)
	if err != nil {
		return false, fmt.Errorf("extract %s: %w", path, err)
	}
	logger.Debug(ctx, "file.extracted",
		slog.String("component", "ingestion"),
		slog.String("event", "file.extracted"),
		slog.String("file_path", path),
		slog.Int("span_count", len(spans)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	// 4. Layout pipeline
	t = time.Now()
	lines := layout.GroupSpansIntoLines(spans, 2.0)
	body, freq := layout.AnalyzeFonts(lines)
	fontLevels := layout.BuildFontLevels(freq, body)
	headings := layout.DetectHeadings(lines, body, fontLevels)
	headings = layout.MergeWrappedHeadings(headings)
	blocks := layout.BuildBlocks(lines, body)
	blocks = layout.MergeLists(blocks)
	tree := layout.BuildHeadingTree(headings)
	tree = layout.TrimFrontMatter(tree)
	layout.AttachBlocks(tree, blocks)
	sections := layout.BuildSections(tree)
	logger.Debug(ctx, "file.layout_done",
		slog.String("component", "ingestion"),
		slog.String("event", "file.layout_done"),
		slog.String("file_path", path),
		slog.Int("line_count", len(lines)),
		slog.Int("heading_count", len(headings)),
		slog.Int("block_count", len(blocks)),
		slog.Int("section_count", len(sections)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	// 5. Chunk
	chunks := layout.ChunkSections(sections, 5)
	if len(chunks) == 0 {
		if len(blocks) == 0 {
			logger.Info(ctx, "file.skipped",
				slog.String("component", "ingestion"),
				slog.String("event", "file.skipped"),
				slog.String("source", source),
				slog.String("file_path", path),
				slog.String("reason", "no_content"),
			)
			return false, nil
		}
		// No heading structure detected — treat the entire document as one flat section.
		logger.Info(ctx, "file.flat_fallback",
			slog.String("component", "ingestion"),
			slog.String("event", "file.flat_fallback"),
			slog.String("file_path", path),
			slog.String("reason", "no_sections_detected"),
		)
		flat := layout.Section{Title: "Document", Level: 1, Content: blocks}
		chunks = layout.ChunkSections([]layout.Section{flat}, 5)
	}

	// 6. Render chunks to enriched text
	enriched := make([]models.EnrichedChunk, len(chunks))
	texts := make([]string, len(chunks))
	charCount := 0
	for i, c := range chunks {
		text := layout.ChunkToText(c)
		enriched[i] = models.EnrichedChunk{Text: text, Chapter: c.Title, Level: c.Level}
		texts[i] = text
		charCount += len(text)
	}
	logger.Debug(ctx, "file.chunked",
		slog.String("component", "ingestion"),
		slog.String("event", "file.chunked"),
		slog.String("file_path", path),
		slog.Int("chunk_count", len(chunks)),
		slog.Int("char_count", charCount),
	)

	// 7. Store document metadata
	docID, err := a.Documents.Insert(ctx, source, path, hash, charCount, len(chunks))
	if err != nil {
		return false, fmt.Errorf("store document: %w", err)
	}

	// 8. Store chunk text
	t = time.Now()
	if err := a.Chunks.Store(ctx, docID, enriched); err != nil {
		return false, fmt.Errorf("store chunks: %w", err)
	}
	logger.Debug(ctx, "file.chunks_stored",
		slog.String("component", "ingestion"),
		slog.String("event", "file.chunks_stored"),
		slog.String("file_path", path),
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	// 9. Generate embeddings (batched — one HTTP call for all chunks)
	t = time.Now()
	embeddings, err := a.Embedder.Embed(ctx, texts)
	if err != nil {
		return false, fmt.Errorf("embed chunks: %w", err)
	}
	logger.Debug(ctx, "file.embedded",
		slog.String("component", "ingestion"),
		slog.String("event", "file.embedded"),
		slog.String("file_path", path),
		slog.Int64("doc_id", docID),
		slog.Int("embed_count", len(embeddings)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	// 10. Store embeddings
	t = time.Now()
	if err := a.Chunks.StoreEmbeddings(ctx, docID, embeddings); err != nil {
		return false, fmt.Errorf("store embeddings: %w", err)
	}
	logger.Debug(ctx, "file.embeddings_stored",
		slog.String("component", "ingestion"),
		slog.String("event", "file.embeddings_stored"),
		slog.String("file_path", path),
		slog.Int64("doc_id", docID),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	logger.Info(ctx, "file.done",
		slog.String("component", "ingestion"),
		slog.String("event", "file.done"),
		slog.String("source", source),
		slog.String("file_path", path),
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)),
		slog.Int("char_count", charCount),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)

	return true, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
