package models

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/iamaina/nexus/internal/logger"
	"github.com/jackc/pgx/v5"
)

// ChunkModel handles database operations for document chunks.
type ChunkModel struct {
	DB *pgx.Conn
}

// Store inserts or updates the text chunks for a document.
func (m *ChunkModel) Store(ctx context.Context, docID int64, chunks []EnrichedChunk) error {
	for i, chunk := range chunks {
		_, err := m.DB.Exec(ctx,
			`INSERT INTO chunks (document_id, chunk_index, chunk_text, chapter, section_level)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (document_id, chunk_index) DO UPDATE SET
			     chunk_text    = EXCLUDED.chunk_text,
			     chapter       = EXCLUDED.chapter,
			     section_level = EXCLUDED.section_level`,
			docID, i, chunk.Text, chunk.Chapter, chunk.Level,
		)
		if err != nil {
			logger.Error(ctx, "Chunk insert failed",
				slog.Int64("doc_id", docID),
				slog.Int("index", i),
				slog.Any("err", err))
			return err
		}
	}
	logger.Debug(ctx, "Stored chunks",
		slog.Int64("doc_id", docID),
		slog.Int("count", len(chunks)))
	return nil
}

// StoreEmbeddings writes pre-computed embeddings to the chunks table for a given document.
// The embeddings slice must be in the same order as the chunks were stored (chunk_index order).
func (m *ChunkModel) StoreEmbeddings(ctx context.Context, docID int64, embeddings [][]float32) error {
	for i, vec := range embeddings {
		_, err := m.DB.Exec(ctx,
			`UPDATE chunks SET embedding = $1::vector
			 WHERE document_id = $2 AND chunk_index = $3`,
			vectorToString(vec), docID, i,
		)
		if err != nil {
			logger.Error(ctx, "Embedding store failed",
				slog.Int64("doc_id", docID),
				slog.Int("index", i),
				slog.Any("err", err))
			return err
		}
	}
	logger.Debug(ctx, "Stored embeddings",
		slog.Int64("doc_id", docID),
		slog.Int("count", len(embeddings)))
	return nil
}

// Search performs a vector similarity search and returns the top results.
func (m *ChunkModel) Search(ctx context.Context, queryVec []float32, limit int) ([]Result, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT
			d.file_path,
			c.chunk_text,
			1 - (c.embedding <=> $1::vector) AS similarity
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		WHERE c.embedding IS NOT NULL
		ORDER BY c.embedding <=> $1::vector
		LIMIT $2`,
		vectorToString(queryVec), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.File, &r.Text, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListChaptersByBook returns distinct non-empty chapter names for documents whose
// file path contains bookName (case-insensitive).
func (m *ChunkModel) ListChaptersByBook(ctx context.Context, bookName string) ([]string, error) {
	rows, err := m.DB.Query(ctx, `
		WITH min_level AS (
			SELECT MIN(c.section_level) AS lvl
			FROM chunks c
			JOIN documents d ON c.document_id = d.id
			WHERE d.file_path ILIKE '%' || $1 || '%'
			  AND c.section_level > 0
		)
		SELECT c.chapter
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		CROSS JOIN min_level
		WHERE d.file_path ILIKE '%' || $1 || '%'
		  AND c.chapter IS NOT NULL
		  AND c.chapter != ''
		  AND c.section_level = min_level.lvl
		GROUP BY c.chapter
		ORDER BY MIN(c.chunk_index)`,
		bookName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chapters []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		chapters = append(chapters, ch)
	}
	return chapters, rows.Err()
}

// vectorToString converts a float32 slice into a Postgres vector literal e.g. "[0.1,0.2,...]".
func vectorToString(vec []float32) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "%f", v)
	}
	sb.WriteString("]")
	return sb.String()
}
