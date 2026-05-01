package models

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/iamaina/nexus/internal/logger"
	"github.com/jackc/pgx/v5"
)

// DocumentModel handles database operations for ingested documents.
type DocumentModel struct {
	DB *pgx.Conn
}

// IsUpToDate returns true if the file at filePath has already been ingested with the same hash.
func (m *DocumentModel) IsUpToDate(ctx context.Context, filePath, currentHash string) (bool, error) {
	var storedHash string
	err := m.DB.QueryRow(ctx,
		`SELECT file_hash FROM documents WHERE file_path = $1`,
		filePath,
	).Scan(&storedHash)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return storedHash == currentHash, nil
}

// FindDuplicate returns the file_path of an already-ingested document with the
// same hash but a different path. Empty string means no duplicate exists.
func (m *DocumentModel) FindDuplicate(ctx context.Context, path, hash string) (string, error) {
	var existing string
	err := m.DB.QueryRow(ctx,
		`SELECT file_path FROM documents WHERE file_hash = $1 AND file_path != $2 LIMIT 1`,
		hash, path,
	).Scan(&existing)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return existing, err
}

// RePoint updates the file_path for an existing document record and preserves
// the original filename (basename only) in original_name. COALESCE ensures the
// earliest known name is kept across multiple re-points. Chunks and embeddings
// are unchanged since they are keyed by document_id, not file_path.
func (m *DocumentModel) RePoint(ctx context.Context, oldPath, newPath string) error {
	_, err := m.DB.Exec(ctx,
		`UPDATE documents
		    SET file_path     = $1,
		        original_name = COALESCE(original_name, $2)
		  WHERE file_path = $3`,
		newPath, filepath.Base(oldPath), oldPath,
	)
	return err
}

// List returns all ingested documents, optionally filtered by source name.
func (m *DocumentModel) List(ctx context.Context, source string) ([]Document, error) {
	query := `SELECT id, source_name, file_path, chunk_count, char_count,
	                 TO_CHAR(ingest_time, 'YYYY-MM-DD HH24:MI') AS ingest_time
	          FROM documents`
	args := []any{}
	if source != "" {
		query += ` WHERE source_name = $1`
		args = append(args, source)
	}
	query += ` ORDER BY source_name, file_path`

	rows, err := m.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.SourceName, &d.FilePath, &d.ChunkCount, &d.CharCount, &d.IngestTime); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// FindByName returns all documents whose file path contains name (case-insensitive).
func (m *DocumentModel) FindByName(ctx context.Context, name string) ([]Document, error) {
	rows, err := m.DB.Query(ctx,
		`SELECT id, source_name, file_path, chunk_count, char_count,
		        TO_CHAR(ingest_time, 'YYYY-MM-DD HH24:MI') AS ingest_time
		 FROM documents
		 WHERE file_path ILIKE '%' || $1 || '%'
		 ORDER BY source_name, file_path`,
		name,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.SourceName, &d.FilePath, &d.ChunkCount, &d.CharCount, &d.IngestTime); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// Summary returns per-source document and chunk counts, ordered by source name.
func (m *DocumentModel) Summary(ctx context.Context) ([]SourceSummary, error) {
	rows, err := m.DB.Query(ctx,
		`SELECT source_name, COUNT(*) AS doc_count, COALESCE(SUM(chunk_count), 0) AS chunk_count
		 FROM documents
		 GROUP BY source_name
		 ORDER BY source_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceSummary
	for rows.Next() {
		var s SourceSummary
		if err := rows.Scan(&s.SourceName, &s.DocCount, &s.ChunkCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SourceStats returns per-source document count, chunk count, and most recent
// ingest timestamp for every source that has at least one document in the DB.
func (m *DocumentModel) SourceStats(ctx context.Context) ([]SourceStat, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT
			source_name,
			COUNT(*)                                          AS doc_count,
			COALESCE(SUM(chunk_count), 0)                    AS chunk_count,
			TO_CHAR(MAX(ingest_time), 'YYYY-MM-DD HH24:MI')  AS last_ingest
		FROM documents
		GROUP BY source_name
		ORDER BY source_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceStat
	for rows.Next() {
		var s SourceStat
		if err := rows.Scan(&s.SourceName, &s.DocCount, &s.ChunkCount, &s.LastIngest); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteByPath removes a document and all its chunks (via CASCADE) by file path.
// Returns nil if the document does not exist (idempotent).
func (m *DocumentModel) DeleteByPath(ctx context.Context, filePath string) error {
	_, err := m.DB.Exec(ctx, `DELETE FROM documents WHERE file_path = $1`, filePath)
	return err
}

// CountBySource returns the document count and total chunk count for a source.
func (m *DocumentModel) CountBySource(ctx context.Context, sourceName string) (docCount, chunkCount int, err error) {
	err = m.DB.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(chunk_count), 0) FROM documents WHERE source_name = $1`,
		sourceName,
	).Scan(&docCount, &chunkCount)
	return
}

// DeleteBySource removes all documents (and their chunks via CASCADE) for a source.
// Returns the number of documents deleted.
func (m *DocumentModel) DeleteBySource(ctx context.Context, sourceName string) (int64, error) {
	tag, err := m.DB.Exec(ctx, `DELETE FROM documents WHERE source_name = $1`, sourceName)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Insert upserts document metadata and returns the document ID.
// meta may be nil for batch ingestion; when provided the classification
// columns (doc_type, language, institution, doc_date) are stored too.
func (m *DocumentModel) Insert(ctx context.Context, source, path, hash string, charCount, chunkCount int, meta *DocMeta) (int64, error) {
	var docType, language, institution, docDate string
	if meta != nil {
		docType = meta.DocType
		language = meta.Language
		institution = meta.Institution
		docDate = meta.DocDate
	}

	var id int64
	err := m.DB.QueryRow(ctx,
		`INSERT INTO documents (source_name, file_path, file_hash, char_count, chunk_count, doc_type, language, institution, doc_date)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,''), NULLIF($8,''), NULLIF($9,''))
		 ON CONFLICT (file_path) DO UPDATE SET
		     file_hash    = EXCLUDED.file_hash,
		     char_count   = EXCLUDED.char_count,
		     chunk_count  = EXCLUDED.chunk_count,
		     doc_type     = COALESCE(EXCLUDED.doc_type,    documents.doc_type),
		     language     = COALESCE(EXCLUDED.language,    documents.language),
		     institution  = COALESCE(EXCLUDED.institution, documents.institution),
		     doc_date     = COALESCE(EXCLUDED.doc_date,    documents.doc_date),
		     ingest_time  = CURRENT_TIMESTAMP
		 RETURNING id`,
		source, path, hash, charCount, chunkCount, docType, language, institution, docDate,
	).Scan(&id)
	if err != nil {
		logger.Error(ctx, "Document insert failed",
			slog.String("file", path),
			slog.Any("err", err))
		return 0, err
	}
	logger.Debug(ctx, "Stored document metadata",
		slog.Int64("doc_id", id),
		slog.String("file", filepath.Base(path)))
	return id, nil
}
