# Changelog

All notable changes to nexus are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — v0.1.1-dev

### In Progress
- `nexus watch` — filesystem watcher (fsnotify) for automatic filing
- DB columns for personal document metadata (`doc_type`, `language`, `institution`, `doc_date`)
- Summarizer prompt update: always answer in English regardless of document language
- File path always shown in query results (not gated behind `--sources`)

---

## [v0.1.1] — 2026-04-05

### Added
- **`nexus file <path>`** — classify, move, and ingest a personal document in one command
  - Reads actual document text (via PyMuPDF / layout pipeline) for accurate classification
  - Moves file to `~/Documents/PersonalDocs/<category>/<clean-name>.<ext>`
  - `--dry-run` flag: show classification without touching the file
- **`nexus list`** — table view of all ingested documents grouped by source
  - `--source <name>` flag to filter by source
  - Strips noise patterns from filenames (PDFDrive suffixes, bracketed text)
- **`internal/classifier`** — document classification via `qwen2.5:7b`
  - Returns structured `Classification{DocType, Language, Institution, Date, Filename, DestDir}`
  - Falls back to `other/` category on LLM failure — never blocks ingestion
- **Three-model architecture**
  - `mxbai-embed-large` — multilingual embeddings (Dutch + English in same vector space)
  - `qwen2.5:7b` — structured JSON classification
  - `llama3.1:8b` — fluent query answers
  - All three pulled automatically by `make setup`
- **Multi-format ingestion** — native Go extractors for Markdown and plain text
  - ATX headings in `.md` files map to synthetic font sizes for heading detection
  - Fenced code blocks detected and typed correctly
- **Flat fallback** — documents with no heading structure are ingested as a single chunk instead of silently dropped
- **Duplicate detection** — same content ingested from multiple paths logs a `WARN` and skips gracefully
- **`personal:` config section** — `watchDirs` and `destDir` with `~` expansion
- **Development workflow documented** in CLAUDE.md — build → explain → test → commit → merge

### Changed
- `layout.Extract(path)` is now the single entry point for all file types; commands never call `ExtractPDF` directly
- `extractPDFSpans` uses `CombinedOutput` so Python errors surface in Go error messages
- Embedding truncated at 800 chars (safe limit for code-dense technical content at ~2 chars/token)
- Vector dimension changed from 768 (`nomic-embed-text`) to 1024 (`mxbai-embed-large`)
- `make setup` drops and recreates chunk/document tables when switching embedding models
- `make setup` creates full `PersonalDocs` directory tree
- Generation model default changed from `llama3.2` to `llama3.1:8b`
- Embedding model default changed from `nomic-embed-text` to `mxbai-embed-large`
- `.gitignore`: `/nexus` (binary only) instead of `nexus` (which matched `cmd/nexus/`)

### Fixed
- All `.md` and `.txt` files were failing with `exit status 1` (PyMuPDF called on non-PDF files)
- PDFs with no heading structure produced zero chunks and hard-failed ingestion
- `scripts/extract_pdf.py` had unresolved merge conflict markers causing `IndentationError`
- `ollama embed: the input length exceeds the context length` on code-heavy books
- `vector(768)` dimension mismatch after switching to `mxbai-embed-large` (1024-dim)
- `resp.Body.Close()` return value unchecked in `app.go`

---

## [v0.1.0] — 2026-03-21

First stable release. Local RAG pipeline running entirely on your machine.

### Added
- PDF ingestion via Python/PyMuPDF (`scripts/extract_pdf.py`)
- Layout pipeline: spans → lines → font analysis → heading detection → blocks → heading tree → sections → chunks
- Line-wrap heading merging (`MergeWrappedHeadings`)
- Semantic search via pgvector cosine similarity
- `nexus ingest` — batch ingestion from configured sources
- `nexus query` — embed → vector search → threshold filter → context expansion → Ollama generate
  - `--source`, `--model`, `--threshold`, `--sources` flags
- `nexus layout` — full pipeline debug tool with per-stage inspection flags
- `nexus chapters` — list top-level chapters for an ingested PDF
- Deduplication by SHA-256 hash (skip unchanged files on re-ingest)
- Coloured terminal logging; JSON output when piped (Loki/Promtail compatible)
- Ollama health check on startup
- `make setup` — end-to-end setup: DB, schema, Ollama models, config generation
- PostgreSQL + pgvector schema with `documents` and `chunks` tables

---

[Unreleased]: https://github.com/iamaina/nexus/compare/v0.1.1...HEAD
[v0.1.1]: https://github.com/iamaina/nexus/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/iamaina/nexus/releases/tag/v0.1.0
