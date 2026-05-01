// Package app wires all application dependencies together.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/classifier"
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

// Embedder is the interface for generating text embeddings.
// Implemented by *embedder.OllamaEmbedder; define your own for tests.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// DocumentClassifier is the interface for classifying a document into filing metadata.
// Implemented by *classifier.Classifier; define your own for tests.
type DocumentClassifier interface {
	Classify(ctx context.Context, path string) (*classifier.Classification, error)
}

// Application holds all wired dependencies.
// Summarizer is kept as a concrete type because its WithModel method returns
// *OllamaSummarizer; abstracting it would require a circular import.
type Application struct {
	Config         *config.Config
	DB             *pgx.Conn
	Documents      *models.DocumentModel
	Chunks         *models.ChunkModel
	ContextSources *models.ContextModel
	Repos          *models.RepoModel
	Gdocs          *models.GdocModel
	Embedder       Embedder
	Summarizer     *summarizer.OllamaSummarizer
	Classifier     DocumentClassifier
	OllamaURL      string
}

// New loads config, connects to the database, runs migrations, and wires all dependencies.
// ctx should be the signal-aware context from main so that Ctrl+C cancels any in-progress
// model downloads during startup.
// verbose overrides the configured log level to "info" when true (--verbose flag).
func New(ctx context.Context, verbose bool) (*Application, error) {
	// 1. Config — Load returns a fully resolved *Config (no global state)
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// 2. Logger — initialised before anything else so all startup logs are formatted.
	// Priority: NEXUS_LOG_LEVEL env var > --verbose flag > config.yaml logLevel > "warn" (silent default).
	logLevel := "warn"
	if cfg.LogLevel != nil && *cfg.LogLevel != "" {
		logLevel = *cfg.LogLevel
	}
	if verbose {
		logLevel = "info"
	}
	if env := os.Getenv("NEXUS_LOG_LEVEL"); env != "" {
		logLevel = env
	}
	logger.Init(logLevel)

	// 3. Database
	db, err := pgx.Connect(ctx, cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	logger.Info(ctx, "PostgreSQL connected", slog.String("dsn", maskDSN(cfg.Postgres.DSN)))

	// 4. Migrations
	if err := migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// 5. Ollama — resolve model names with safe defaults
	ollamaURL := cfg.Ollama.BaseURL
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	embModel := cfg.Ollama.EmbeddingModel
	if embModel == "" {
		embModel = "mxbai-embed-large"
	}
	genModel := cfg.Ollama.GenerationModel
	if genModel == "" {
		genModel = "llama3.2:3b"
	}
	classifyModel := cfg.Ollama.ClassificationModel
	if classifyModel == "" {
		classifyModel = "qwen2.5:3b"
	}

	if err := checkOllama(ctx, ollamaURL); err != nil {
		return nil, err
	}

	if err := checkRequiredModels(ctx, ollamaURL, embModel, genModel, classifyModel); err != nil {
		return nil, err
	}

	emb, err := embedder.New(ollamaURL, embModel)
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	sum, err := summarizer.New(ollamaURL, genModel)
	if err != nil {
		return nil, fmt.Errorf("create summarizer: %w", err)
	}

	clf, err := classifier.New(ollamaURL, classifyModel)
	if err != nil {
		return nil, fmt.Errorf("create classifier: %w", err)
	}

	return &Application{
		Config:         cfg,
		DB:             db,
		Documents:      &models.DocumentModel{DB: db},
		Chunks:         &models.ChunkModel{DB: db},
		ContextSources: &models.ContextModel{DB: db},
		Repos:          &models.RepoModel{DB: db},
		Gdocs:          &models.GdocModel{DB: db},
		Embedder:       emb,
		Summarizer:     sum,
		Classifier:     clf,
		OllamaURL:      ollamaURL,
	}, nil
}

// Close releases all held resources. Safe to call on a nil Application.
func (a *Application) Close() {
	if a == nil || a.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.DB.Close(ctx)
}

// migrate ensures the required tables and columns exist.
// All statements are idempotent (CREATE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS).
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
		`ALTER TABLE chunks    ADD COLUMN IF NOT EXISTS embedding      vector(1024)`,
		`ALTER TABLE chunks    ADD COLUMN IF NOT EXISTS section_level  INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS doc_type       TEXT`,
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS language       TEXT`,
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS institution    TEXT`,
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS doc_date       TEXT`,
		`ALTER TABLE documents DROP COLUMN IF EXISTS original_path`,
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS original_name TEXT`,
		`CREATE TABLE IF NOT EXISTS context_sources (
			id          BIGSERIAL PRIMARY KEY,
			name        TEXT UNIQUE NOT NULL,
			command     TEXT NOT NULL,
			description TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS repos (
			id         BIGSERIAL PRIMARY KEY,
			path       TEXT UNIQUE NOT NULL,
			remote_url TEXT NOT NULL,
			platform   TEXT NOT NULL DEFAULT '',
			repo_type  TEXT NOT NULL DEFAULT '',
			root_name  TEXT NOT NULL DEFAULT '',
			last_seen  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS gdocs (
			id          BIGSERIAL PRIMARY KEY,
			name        TEXT UNIQUE NOT NULL,
			doc_id      TEXT NOT NULL,
			last_synced TIMESTAMP,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			return fmt.Errorf("migration %q: %w", firstLine(s), err)
		}
	}
	return nil
}

// checkOllama verifies that the Ollama server is reachable before any command runs.
func checkOllama(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("ollama is not running at %s — start it with: brew services start ollama", baseURL)
	}
	_ = resp.Body.Close()
	logger.Info(ctx, "Ollama connected", slog.String("url", baseURL))
	return nil
}

func maskDSN(dsn string) string {
	if idx := strings.Index(dsn, "@"); idx != -1 {
		return dsn[:strings.Index(dsn, ":")+1] + "*****" + dsn[idx:]
	}
	return dsn
}

// firstLine returns just the first line of a SQL statement for error messages.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
