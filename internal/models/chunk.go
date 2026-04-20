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

// Store replaces all text chunks for a document: old chunks are deleted first,
// then new ones are inserted in a single batch. This ensures stale chunks from
// a previous ingest (e.g. if chunk count decreased after a layout fix) are removed.
func (m *ChunkModel) Store(ctx context.Context, docID int64, chunks []EnrichedChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	// Delete existing chunks so stale rows from a previous ingest don't linger.
	if _, err := m.DB.Exec(ctx, `DELETE FROM chunks WHERE document_id = $1`, docID); err != nil {
		return fmt.Errorf("delete old chunks for doc %d: %w", docID, err)
	}

	batch := &pgx.Batch{}
	for i, chunk := range chunks {
		batch.Queue(
			`INSERT INTO chunks (document_id, chunk_index, chunk_text, chapter, section_level)
			 VALUES ($1, $2, $3, $4, $5)`,
			docID, i, chunk.Text, chunk.Chapter, chunk.Level,
		)
	}
	br := m.DB.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := range chunks {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store chunk %d for doc %d: %w", i, docID, err)
		}
	}
	logger.Debug(ctx, "Stored chunks",
		slog.Int64("doc_id", docID),
		slog.Int("count", len(chunks)))
	return nil
}

// StoreEmbeddings writes pre-computed embeddings to the chunks table in a single
// batch. The embeddings slice must match chunk_index order.
func (m *ChunkModel) StoreEmbeddings(ctx context.Context, docID int64, embeddings [][]float32) error {
	if len(embeddings) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for i, vec := range embeddings {
		batch.Queue(
			`UPDATE chunks SET embedding = $1::vector
			 WHERE document_id = $2 AND chunk_index = $3`,
			vectorToString(vec), docID, i,
		)
	}
	br := m.DB.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := range embeddings {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store embedding %d for doc %d: %w", i, docID, err)
		}
	}
	logger.Debug(ctx, "Stored embeddings",
		slog.Int64("doc_id", docID),
		slog.Int("count", len(embeddings)))
	return nil
}

// Search performs a vector similarity search and returns the top results.
// If source is non-empty, results are restricted to documents whose source_name
// contains that string (case-insensitive), e.g. "progit" or "lets-go".
func (m *ChunkModel) Search(ctx context.Context, queryVec []float32, limit int, source string) ([]Result, error) {
	var rows pgx.Rows
	var err error

	vec := vectorToString(queryVec)
	if source == "" {
		rows, err = m.DB.Query(ctx, `
			SELECT
				c.document_id,
				c.chunk_index,
				COALESCE(c.section_level, 0),
				d.file_path,
				COALESCE(c.chapter, '') AS chapter,
				c.chunk_text,
				1 - (c.embedding <=> $1::vector) AS similarity
			FROM chunks c
			JOIN documents d ON c.document_id = d.id
			WHERE c.embedding IS NOT NULL
			ORDER BY c.embedding <=> $1::vector
			LIMIT $2`,
			vec, limit,
		)
	} else {
		rows, err = m.DB.Query(ctx, `
			SELECT
				c.document_id,
				c.chunk_index,
				COALESCE(c.section_level, 0),
				d.file_path,
				COALESCE(c.chapter, '') AS chapter,
				c.chunk_text,
				1 - (c.embedding <=> $1::vector) AS similarity
			FROM chunks c
			JOIN documents d ON c.document_id = d.id
			WHERE c.embedding IS NOT NULL
			  AND (d.source_name ILIKE '%' || $3 || '%'
			       OR d.file_path ILIKE '%' || $3 || '%')
			ORDER BY c.embedding <=> $1::vector
			LIMIT $2`,
			vec, limit, source,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.DocumentID, &r.ChunkIndex, &r.Level, &r.File, &r.Chapter, &r.Text, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// FetchContext returns chunks that provide context for the given matched chunk:
// - continuation chunks (same level, same chapter — the section was split across multiple chunks)
// - child chunks (deeper level — subsections)
// Both are bounded by the next sibling or ancestor heading. At most maxChunks are returned.
// Score is set to 0 to distinguish them from direct vector matches.
func (m *ChunkModel) FetchContext(ctx context.Context, parent Result, maxChunks int) ([]Result, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT
			c.document_id,
			c.chunk_index,
			COALESCE(c.section_level, 0),
			d.file_path,
			COALESCE(c.chapter, '') AS chapter,
			c.chunk_text
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		WHERE c.document_id = $1
		  AND c.chunk_index > $2
		  AND c.chunk_index < (
		      SELECT COALESCE(MIN(chunk_index), 2147483647)
		      FROM chunks
		      WHERE document_id = $1
		        AND chunk_index > $2
		        AND section_level <= $3
		        AND chapter != $4
		  )
		ORDER BY c.chunk_index
		LIMIT $5`,
		parent.DocumentID, parent.ChunkIndex, parent.Level, parent.Chapter, maxChunks,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var children []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.DocumentID, &r.ChunkIndex, &r.Level, &r.File, &r.Chapter, &r.Text); err != nil {
			return nil, err
		}
		// Score=0 marks this as a context expansion, not a direct vector match
		children = append(children, r)
	}
	return children, rows.Err()
}

// ListChaptersByDocumentID returns distinct top-level chapter names for a single document.
// If the minimum level has only one distinct chapter (e.g. a document title), it falls
// through to the next level so actual section names are returned instead.
func (m *ChunkModel) ListChaptersByDocumentID(ctx context.Context, docID int64) ([]string, error) {
	rows, err := m.DB.Query(ctx, `
		WITH min_level AS (
			SELECT MIN(section_level) AS lvl
			FROM chunks
			WHERE document_id = $1 AND section_level > 0
		),
		top_count AS (
			SELECT COUNT(DISTINCT chapter) AS cnt
			FROM chunks
			CROSS JOIN min_level
			WHERE document_id = $1
			  AND chapter IS NOT NULL AND chapter != ''
			  AND section_level = min_level.lvl
		),
		effective_level AS (
			SELECT CASE WHEN top_count.cnt <= 1
			            THEN min_level.lvl + 1
			            ELSE min_level.lvl END AS lvl
			FROM min_level, top_count
		)
		SELECT chapter
		FROM chunks
		CROSS JOIN effective_level
		WHERE document_id = $1
		  AND chapter IS NOT NULL AND chapter != ''
		  AND section_level = effective_level.lvl
		GROUP BY chapter
		ORDER BY MIN(chunk_index)`,
		docID,
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

// FindByPathOrChapter returns chunks whose document file path or chapter title
// contains query (case-insensitive LIKE). If source is non-empty, results are
// additionally filtered by source_name or file_path. Results are ordered by
// file path then chunk index so related chunks appear together.
func (m *ChunkModel) FindByPathOrChapter(ctx context.Context, query, source string) ([]Result, error) {
	like := "%" + query + "%"
	args := []any{like}

	sourceFilter := ""
	if source != "" {
		args = append(args, "%"+source+"%")
		sourceFilter = `AND (d.source_name ILIKE $2 OR d.file_path ILIKE $2)`
	}

	rows, err := m.DB.Query(ctx, fmt.Sprintf(`
		SELECT
			c.document_id,
			c.chunk_index,
			COALESCE(c.section_level, 0),
			d.file_path,
			COALESCE(c.chapter, '') AS chapter,
			c.chunk_text
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		WHERE (d.file_path ILIKE $1 OR c.chapter ILIKE $1)
		%s
		ORDER BY d.file_path, c.chunk_index
		LIMIT 100`, sourceFilter),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.DocumentID, &r.ChunkIndex, &r.Level, &r.File, &r.Chapter, &r.Text); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
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
