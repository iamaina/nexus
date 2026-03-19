package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"log/slog"

	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/jackc/pgx/v5"
	"github.com/ollama/ollama/api"
)

type Result struct {
	File  string
	Text  string
	Score float64
}

type Services struct {
	Config *config.Config
	DB     *pgx.Conn
	// Add more services later: Embedder, Retriever, etc.
}

func New() (*Services, error) {
	// 1. Logger first (bootstrapping)
	logger.Init(*config.C.LogLevel)

	// 2. Config
	if err := config.Load(""); err != nil { // empty = default path
		logger.Error(context.Background(), "Config load failed", slog.Any("err", err))
		return nil, err
	}
	if err := config.C.ResolveSecrets(); err != nil {
		logger.Error(context.Background(), "Config secrets failed", slog.Any("err", err))
		return nil, err
	}

	// 3. Storage (DB)
	ctx := context.Background()
	db, err := pgx.Connect(ctx, config.C.Postgres.DSN)
	if err != nil {
		logger.Error(ctx, "DB connect failed", slog.Any("err", err))
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		logger.Error(ctx, "DB ping failed", slog.Any("err", err))
		return nil, err
	}

	logger.Info(ctx, "PostgreSQL connected successfully",
		slog.String("dsn", config.C.Postgres.DSN))

	// 4. Ensure tables
	_, err = db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS documents (
			id BIGSERIAL PRIMARY KEY,
			source_name TEXT NOT NULL,
			file_path TEXT UNIQUE NOT NULL,
			file_hash TEXT NOT NULL,
			char_count INTEGER NOT NULL,
			chunk_count INTEGER NOT NULL,
			ingest_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		logger.Error(ctx, "Table creation failed", slog.Any("err", err))
		return nil, err
	}

	// 5. chunks table
	_, err = db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS chunks (
			id BIGSERIAL PRIMARY KEY,
			document_id BIGINT REFERENCES documents(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			chunk_text TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (document_id, chunk_index)
		);
	`)
	if err != nil {
		logger.Error(ctx, "chunks table creation failed", slog.Any("err", err))
		return nil, err
	}

	return &Services{
		Config: &config.C,
		DB:     db,
	}, nil
}

func (s *Services) InsertDocument(ctx context.Context, source, path, hash string, charCount, chunkCount int) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx,
		`INSERT INTO documents (source_name, file_path, file_hash, char_count, chunk_count)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (file_path) DO UPDATE SET
		     file_hash = EXCLUDED.file_hash,
		     char_count = EXCLUDED.char_count,
		     chunk_count = EXCLUDED.chunk_count,
		     ingest_time = CURRENT_TIMESTAMP
		 RETURNING id`,
		source, path, hash, charCount, chunkCount,
	).Scan(&id)
	if err != nil {
		logger.Error(ctx, "Document insert failed",
			slog.String("file", path),
			slog.Any("err", err))
		return 0, err
	}
	logger.Info(ctx, "Stored document metadata",
		slog.Int64("doc_id", id),
		slog.String("file", filepath.Base(path)))
	return id, nil
}

func (s *Services) IsDocumentUpToDate(ctx context.Context, filePath, currentHash string) (bool, error) {
	var storedHash string
	err := s.DB.QueryRow(ctx,
		`SELECT file_hash FROM documents WHERE file_path = $1`,
		filePath,
	).Scan(&storedHash)
	if err == pgx.ErrNoRows {
		return false, nil // not found → process
	}
	if err != nil {
		return false, err
	}
	return storedHash == currentHash, nil
}

func (s *Services) StoreChunks(ctx context.Context, docID int64, chunks []string) error {
	for i, chunk := range chunks {
		_, err := s.DB.Exec(ctx,
			`INSERT INTO chunks (document_id, chunk_index, chunk_text)
             VALUES ($1, $2, $3)
             ON CONFLICT (document_id, chunk_index) DO UPDATE SET
                 chunk_text = EXCLUDED.chunk_text`,
			docID, i, chunk,
		)
		if err != nil {
			logger.Error(ctx, "Chunk insert failed",
				slog.Int64("doc_id", docID),
				slog.Int("index", i),
				slog.Any("err", err))
			return err
		}
	}
	logger.Info(ctx, "Stored chunks",
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)))
	return nil
}

// EmbedChunks generates embeddings for all chunks of a document using Ollama
func (s *Services) EmbedChunks(ctx context.Context, docID int64, chunks []string) error {
	if len(chunks) == 0 {
		return nil
	}

	logger.Info(ctx, "Starting embedding",
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)))

	baseURL, err := url.Parse("http://localhost:11434")
	if err != nil {
		panic(err)
	}

	httpClient := &http.Client{}

	client := api.NewClient(baseURL, httpClient)

	for i, chunk := range chunks {
		resp, err := client.Embed(ctx, &api.EmbedRequest{
			Model: "nomic-embed-text",
			Input: []string{chunk},
		})
		if err != nil {
			logger.Error(ctx, "Embedding failed for chunk",
				slog.Int64("doc_id", docID),
				slog.Int("chunk_index", i),
				slog.Any("err", err))
			return err
		}

		if len(resp.Embeddings) == 0 {
			continue
		}

		// Build correct Postgres vector string: [0.123,-0.456,...]
		vec := resp.Embeddings[0]
		var sb strings.Builder
		sb.WriteString("[")
		for j, v := range vec {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf("%f", v))
		}
		sb.WriteString("]")

		// Store it
		_, err = s.DB.Exec(ctx,
			`UPDATE chunks 
			 SET embedding = $1::vector 
			 WHERE document_id = $2 AND chunk_index = $3`,
			sb.String(), docID, i,
		)
		if err != nil {
			logger.Error(ctx, "Failed to store embedding", slog.Any("err", err))
			return err
		}
	}

	logger.Info(ctx, "Embeddings stored successfully",
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)))

	return nil
}

// Summarize turns the retrieved chunks into a natural, concise answer using llama3.2
func (s *Services) Summarize(ctx context.Context, question string, results []Result) (string, error) {
	if len(results) == 0 {
		return "I couldn't find any relevant information in your knowledge base.", nil
	}

	// Build context
	var contextBuilder strings.Builder
	for _, r := range results {
		contextBuilder.WriteString(fmt.Sprintf("From %s:\n%s\n\n", r.File, r.Text))
	}

	prompt := fmt.Sprintf(`You are a helpful, concise assistant. 
Answer the question using ONLY the provided context.
If the context doesn't contain enough information, say "I don't have enough information."

Question: %s

Context:
%s

Answer:`, question, contextBuilder.String())

	// Correct way to call Generate (current Ollama Go client)
	baseURL, _ := url.Parse("http://localhost:11434")
	client := api.NewClient(baseURL, &http.Client{})

	var answer strings.Builder

	err := client.Generate(ctx, &api.GenerateRequest{
		Model:  "llama3.2",
		Prompt: prompt,
	}, func(resp api.GenerateResponse) error {
		answer.WriteString(resp.Response)
		return nil
	})
	if err != nil {
		return "", err
	}

	return answer.String(), nil
}

// Close cleans up resources
func (s *Services) Close() {
	if s.DB != nil {
		s.DB.Close(context.Background())
	}
}
