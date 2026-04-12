# Changelog

All notable changes to nexus are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

---

## [v0.0.1] — 2026-04-10

Initial release. Full local RAG pipeline, personal document filing, live context sources, and CI/CD infrastructure.

### Added

**Core pipeline**
- PDF ingestion via Python/PyMuPDF (`scripts/extract_pdf.py`)
- Layout pipeline: spans → lines → font analysis → heading detection → blocks → heading tree → sections → chunks
- Native Go extractors for Markdown and plain text
- Semantic search via pgvector cosine similarity
- Deduplication by SHA-256 hash

**Commands**
- `nexus ingest` — batch ingestion from configured sources
- `nexus query` — embed → vector search → threshold filter → context expansion → Ollama generate (`--source`, `--model`, `--threshold`, `--sources`, `--no-live` flags)
- `nexus watch` — filesystem daemon watching `personal.watchDirs`, classify → move → ingest
- `nexus file` — classify, move, and ingest a single personal document (`--dry-run` flag)
- `nexus list` — table view of all ingested documents grouped by source
- `nexus context add|list|rm|run` — CRUD for live context sources
- `nexus layout` — full pipeline debug tool
- `nexus chapters` — list top-level chapters for an ingested document

**Mode 1 — Personal Document Safe**
- Document classifier via `qwen2.5:7b` → structured JSON (type, language, institution, date, filename, destDir)
- Auto-file documents to `~/Documents/PersonalDocs/<category>/`
- Metadata columns on ingested documents: `doc_type`, `language`, `institution`, `doc_date`

**Mode 2 — Work Intelligence (pipeline built)**
- Live context sources: shell commands run concurrently at query time, output injected into LLM prompt
- `internal/live/runner.go` — concurrent execution with 5s per-command timeout
- `SummarizeWithLive` — live output injected before static chunks in the prompt

**Infrastructure**
- Three Ollama models: `mxbai-embed-large` (embeddings), `qwen2.5:7b` (classification), `llama3.1:8b` (generation)
- `make setup` — idempotent first-time setup (DB, schema, models, config, directories)
- `make reset-db` — isolated destructive reset with confirmation prompt
- Coloured terminal logging; JSON when piped (Loki/Promtail compatible)
- Ollama health check on startup
- Version injected at build time via `git describe --tags`
- CI: `golangci-lint` on all PRs to `master` and `stable/**`
- Auto-tagging: conventional commit type on merge to master drives semver bump
- Auto stable branch creation after each tag

---

[Unreleased]: https://github.com/iamaina/nexus/compare/v0.0.1...HEAD
[v0.0.1]: https://github.com/iamaina/nexus/releases/tag/v0.0.1
