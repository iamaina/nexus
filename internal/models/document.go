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

// Insert upserts document metadata and returns the document ID.
func (m *DocumentModel) Insert(ctx context.Context, source, path, hash string, charCount, chunkCount int) (int64, error) {
	var id int64
	err := m.DB.QueryRow(ctx,
		`INSERT INTO documents (source_name, file_path, file_hash, char_count, chunk_count)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (file_path) DO UPDATE SET
		     file_hash    = EXCLUDED.file_hash,
		     char_count   = EXCLUDED.char_count,
		     chunk_count  = EXCLUDED.chunk_count,
		     ingest_time  = CURRENT_TIMESTAMP
		 RETURNING id`,
		source, path, hash, charCount, chunkCount,
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
