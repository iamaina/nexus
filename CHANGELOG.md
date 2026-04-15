# Changelog

All notable changes to nexus are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added

**Mode 3 — Workspace OS (Phase 1 & 2)**
- `roots` config section: `workspace` root and typed `repos` roots (`work`, `personal-github`, `personal-gitlab`) with host substring matching and most-specific-wins group routing
- `source.watch: bool` — marks a source for periodic re-ingestion by `nexus watch`
- `internal/workspace` package — generates `dir_structure.md`: annotated tree view + flat Repository Index (name, full path, remote, branch, status per repo)
- `nexus watch` extended to four concurrent modes: personal intake (fsnotify), source re-scan (5-min ticker), workspace structural snapshot (fsnotify + debounce), repo root detection (fsnotify, 10s settle)
- `make watch-install` / `make watch-restart` / `make watch-uninstall` — launchd service management; logs to `~/Library/Logs/nexus-watch.log`

**Bug fixes**
- Body font detection: exclude bold lines from frequency count — fixes chapter detection in code-heavy Markdown documents
- `ListChaptersByDocumentID`: fall through to level+1 when only one chapter at minimum level — fixes documents showing a single title-level chapter
- `ChunkModel.Store`: delete existing chunks before insert — prevents stale chunks when re-ingesting a document with fewer chunks

**Mode 3 — Workspace OS (Phase 3)**
- `nexus organise [path]` — replaces `nexus file`; auto-detects file vs directory from argument; no argument processes all `personal.watchDirs`
- Classifies each file, resolves destination, prints a full plan before touching anything, confirms before executing
- `--dry-run` shows plan without moving or ingesting; `--force` / `-f` re-ingests unchanged files
- `internal/organiser` package: topic-based directory matcher walks source roots to find existing dirs for technical docs (`book`, `article`); all other types route to PersonalDocs with path-traversal sanitisation
- `nexus watch --list` — prints all configured watchers (personal intake dirs, source tickers, workspace root, repo roots) without starting
- `classifier.Classification` gains `topic` field — LLM returns main subject for technical docs, used by organiser to match existing directories
- `make setup` creates repo root directories (`mkdir -p`) when configured, preventing missing-directory warnings on first `nexus watch` start

**`make setup` additions**
- Prompts for ops-notes exclude patterns and optional runbooks source
- Prompts for workspace root, work repos path and host substrings, personal GitHub and GitLab repos and usernames

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
