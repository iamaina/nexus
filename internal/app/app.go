// Package app wires all application dependencies together.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/embedder"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/iamaina/nexus/internal/summarizer"
	"github.com/jackc/pgx/v5"
)

type contextKey string

// AppKey is the context key used to pass the Application instance to commands.
const AppKey contextKey = "app"

// Application holds all wired dependencies.
type Application struct {
	Config     *config.Config
	DB         *pgx.Conn
	Documents  *models.DocumentModel
	Chunks     *models.ChunkModel
	Embedder   *embedder.OllamaEmbedder
	Summarizer *summarizer.OllamaSummarizer
}

// New loads config, connects to the database, runs migrations, and wires all dependencies.
func New() (*Application, error) {
	// 1. Config
	if err := config.Load(""); err != nil {
		return nil, err
	}
	if err := config.C.ResolveSecrets(); err != nil {
		return nil, err
	}

	// 2. Logger
	logLevel := "info"
	if config.C.LogLevel != nil && *config.C.LogLevel != "" {
		logLevel = *config.C.LogLevel
	}
	logger.Init(logLevel)

	ctx := context.Background()

	// 3. Database
	db, err := pgx.Connect(ctx, config.C.Postgres.DSN)
	if err != nil {
		logger.Error(ctx, "DB connect failed", slog.Any("err", err))
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		logger.Error(ctx, "DB ping failed", slog.Any("err", err))
		return nil, err
	}
	logger.Info(ctx, "PostgreSQL connected", slog.String("dsn", maskDSN(config.C.Postgres.DSN)))

	// 4. Migrations
	if err := migrate(ctx, db); err != nil {
		return nil, err
	}

	// 5. Ollama clients
	ollamaURL := config.C.Ollama.BaseURL
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	embModel := config.C.Ollama.EmbeddingModel
	if embModel == "" {
		embModel = "nomic-embed-text"
	}
	genModel := config.C.Ollama.GenerationModel
	if genModel == "" {
		genModel = "llama3.2"
	}

	if err := checkOllama(ctx, ollamaURL); err != nil {
		return nil, err
	}

	emb, err := embedder.New(ollamaURL, embModel)
	if err != nil {
		return nil, err
	}

	sum, err := summarizer.New(ollamaURL, genModel)
	if err != nil {
		return nil, err
	}

	return &Application{
		Config:     &config.C,
		DB:         db,
		Documents:  &models.DocumentModel{DB: db},
		Chunks:     &models.ChunkModel{DB: db},
		Embedder:   emb,
		Summarizer: sum,
	}, nil
}

// Close releases all held resources.
func (a *Application) Close() {
	if a.DB != nil {
		_ = a.DB.Close(context.Background())
	}
}

// migrate ensures the required tables and columns exist.
func migrate(ctx context.Context, db *pgx.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS documents (
			id          BIGSERIAL PRIMARY KEY,
			source_name TEXT NOT NULL,
			file_path   TEXT UNIQUE NOT NULL,
			file_hash   TEXT NOT NULL,
			char_count  INTEGER NOT NULL,
			chunk_count INTEGER NOT NULL,
			ingest_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id          BIGSERIAL PRIMARY KEY,
			document_id BIGINT REFERENCES documents(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			chunk_text  TEXT NOT NULL,
			chapter     TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (document_id, chunk_index)
		)`,
		`ALTER TABLE chunks ADD COLUMN IF NOT EXISTS embedding vector(768)`,
		`ALTER TABLE chunks ADD COLUMN IF NOT EXISTS section_level INTEGER NOT NULL DEFAULT 0`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			logger.Error(ctx, "Migration failed", slog.Any("err", err))
			return err
		}
	}
	return nil
}

// checkOllama verifies that the Ollama server is reachable before any command runs.
func checkOllama(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL) //nolint:noctx
	if err != nil {
		logger.Error(ctx, "Ollama unreachable",
			slog.String("url", baseURL),
			slog.String("hint", "run: brew services start ollama"),
		)
		return fmt.Errorf("ollama is not running at %s — start it with: brew services start ollama", baseURL)
	}
	resp.Body.Close()
	logger.Info(ctx, "Ollama connected", slog.String("url", baseURL))
	return nil
}

func maskDSN(dsn string) string {
	if idx := strings.Index(dsn, "@"); idx != -1 {
		return dsn[:strings.Index(dsn, ":")+1] + "*****" + dsn[idx:]
	}
	return dsn
}
