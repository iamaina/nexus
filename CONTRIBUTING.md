# Contributing to nexus

nexus is a local-first personal intelligence layer. It runs entirely on your machine — no cloud, no subscriptions. This guide explains how to work on it effectively.

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Development Setup](#development-setup)
3. [Project Structure](#project-structure)
4. [Development Workflow](#development-workflow)
5. [Versioning](#versioning)
6. [Code Standards](#code-standards)
7. [Architecture Decisions](#architecture-decisions)

---

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.22+ | Primary language |
| Python | 3.9+ | PDF extraction via PyMuPDF |
| PostgreSQL | 15+ | Storage + pgvector |
| Ollama | latest | LLM inference (local) |
| golangci-lint | latest | Lint enforcement |

Install dependencies:
```bash
brew install go postgresql@15 ollama
pip install pymupdf
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

---

## Development Setup

`make setup` handles everything end-to-end:

```bash
make setup
```

This:
1. Creates the PostgreSQL database and user
2. Creates `config.yaml` from the template (gitignored — your local config)
3. Drops and recreates tables (required when switching embedding model dimensions)
4. Pulls the three Ollama models: `mxbai-embed-large`, `qwen2.5:7b`, `llama3.1:8b`
5. Creates `~/Documents/PersonalDocs/` directory tree

To ingest your documents after setup:
```bash
make ingest
```

To run a query:
```bash
go run main.go query "your question here"
```

---

## Project Structure

```
nexus/
├── cmd/nexus/          CLI commands (one file per command)
├── internal/
│   ├── app/            Dependency wiring (Application struct)
│   ├── classifier/     Document classification via qwen2.5:7b
│   ├── config/         YAML config loading
│   ├── embedder/       Text embedding via mxbai-embed-large
│   ├── ingestion/      Per-file ingestion pipeline
│   ├── layout/         ✅ STABLE — structure-aware document parser
│   ├── logger/         Coloured terminal / JSON logging
│   ├── models/         Database access layer
│   └── summarizer/     Answer generation via llama3.1:8b
├── scripts/
│   └── extract_pdf.py  PyMuPDF bridge
├── main.go
├── Makefile
└── config.yaml         (gitignored — created by make setup)
```

The `internal/layout/` package is the most complex. It implements the full document structure pipeline: raw spans → lines → font analysis → heading detection → blocks → heading tree → sections → chunks. Read `CLAUDE.md` section 3 before touching it.

---

## Development Workflow

Every feature follows this cycle:

### 1. Branch off `v0.1.x-dev`
```bash
git checkout v0.1.1-dev   # or current dev branch
```

### 2. Build and verify
```bash
go build ./...
golangci-lint run ./...
```

### 3. Test manually
Run the specific command affected. See `CLAUDE.md` section 16 for the current feature under development and its test commands.

### 4. Commit
```bash
git add <specific files>    # never git add -A
git commit -m "feat: description"
git push origin v0.1.x-dev
```

Commit message prefixes:
- `feat:` — new feature
- `fix:` — bug fix
- `refactor:` — code restructuring, no behaviour change
- `chore:` — tooling, config, CI

### 5. Merge to master only when tested
```bash
git checkout master
git merge v0.1.x-dev --no-ff -m "Merge: feature description"
git push origin master
git checkout v0.1.x-dev
```

**Rule:** master is always working and tested. No partial features on master.

### 6. Tag releases
See [Versioning](#versioning) below.

---

## Versioning

nexus follows [Semantic Versioning](https://semver.org/):

```
MAJOR.MINOR.PATCH
  │     │     └── bug fixes, no new features
  │     └──────── new features, backwards compatible
  └────────────── breaking changes or major mode completions
```

### Version milestones

| Version | Milestone |
|---|---|
| v0.1.x | Core pipeline + Mode 1 (Personal Document Safe) |
| v0.2.x | Mode 2 (Work Intelligence — live infra context) |
| v0.3.x | Mode 3 (Teacher — adaptive learning) |
| v1.0.0 | All three modes stable |

### Creating a release

When a milestone is complete and tested on master:

```bash
# 1. Update CHANGELOG.md — move [Unreleased] to the new version with today's date
# 2. Commit the changelog
git add CHANGELOG.md
git commit -m "chore: release v0.1.2"

# 3. Tag it
git tag -a v0.1.2 -m "v0.1.2 — brief description of what this version delivers"
git push origin master --tags

# 4. Open the next dev branch
git checkout -b v0.1.2-dev
git push origin v0.1.2-dev
```

Tags are annotated (`-a`) so they carry a message visible in `git tag -n`.

---

## Code Standards

### The short version
- `golangci-lint run ./...` must pass before every commit (enforced by pre-commit hook)
- `fmt.Print*` → stdout only; `logger.*` → stderr only — never mix
- `logger.Info` for user-visible milestones; `logger.Debug` for internal steps
- No global state — all dependencies flow through `*app.Application` via context
- Every model used must be pulled in `make setup` — no manual steps

### Packages and imports
- One responsibility per package — `classifier` classifies, `embedder` embeds, `layout` parses
- Import the `layout` package via `layout.Extract(path)` — never call `ExtractPDF` directly from outside `layout/`
- Circular imports are a build error; use interfaces or move shared types to `models/`

### Error handling
- Wrap errors with context: `fmt.Errorf("stage description: %w", err)`
- Validation only at system boundaries (user input, external APIs, file system)
- Classification failures must never block ingestion — fall back gracefully

### Logging
```go
logger.Info(ctx, "event.name",
    slog.String("component", "ingestion"),
    slog.String("file_path", path),
)
```
Event names follow the `component.event` pattern used throughout.

---

## Architecture Decisions

**Why local-only?**
Privacy. Your documents, your data, your machine. No API keys, no subscriptions, no data leaving your network.

**Why three separate Ollama models?**
Each model is optimised for its job:
- `mxbai-embed-large` — multilingual embeddings; Dutch and English land in the same vector space so cross-language queries work
- `qwen2.5:7b` — reliable structured JSON output for classification
- `llama3.1:8b` — better reasoning for fluent, cited query answers

**Why PyMuPDF for PDFs and native Go for Markdown/text?**
PyMuPDF gives us precise glyph-level coordinates and font sizes for PDFs — essential for heading detection based on font size ratios. Markdown has explicit heading syntax (`#`, `##`) so we synthesise equivalent font sizes in Go directly.

**Why block-count chunking instead of token-aware chunking?**
Simplicity while the pipeline was being established. Token-aware chunking is on the roadmap — the `maxBlocks=5` limit will be replaced once the layout pipeline is stable.

**Why pgvector over a dedicated vector DB?**
Keeps the stack minimal (one database) and lets us join vector results with document metadata in a single query. At personal-library scale (thousands of chunks) pgvector is more than sufficient.
