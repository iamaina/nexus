# Getting Started

This guide walks you through setting up nexus from scratch. It explains *why* each tool is needed so you understand what you're installing, not just that you need to install it.

---

## What you're setting up

nexus is a pipeline with three parts:

1. **Document extraction** — reads PDFs and text files and turns them into structured data (Python + PyMuPDF)
2. **AI models** — three local AI models running through Ollama. Nothing goes to the cloud.
3. **Database** — PostgreSQL with a vector extension (pgvector) that stores your document chunks and lets you search them by meaning, not just keywords

None of these talk to the internet after initial download.

---

## Prerequisites

### Go 1.22+

The application is written in Go. You need the Go compiler to build it, or `go run` to run it directly during development.

```bash
brew install go
go version   # should show 1.22.x or higher
```

### Python 3.9+

PDF text extraction is done by a small Python script (`scripts/extract_pdf.py`) using PyMuPDF. PyMuPDF provides glyph-level coordinates and font sizes, which nexus uses to detect headings and document structure. There is no Go library that gives this level of detail.

```bash
python3 --version   # should be 3.9+
```

### PostgreSQL 14+

nexus uses PostgreSQL as its only database. It stores document metadata and the text chunks extracted from your files. The key addition is **pgvector** — an extension that adds a `vector` column type and enables cosine similarity search. This is how "find the most relevant passages about X" works: your question and all your chunks are stored as number arrays (embeddings), and the database finds the ones that are mathematically closest.

```bash
brew install postgresql@14
brew services start postgresql@14
```

### Ollama

Ollama runs AI models locally. Think of it as a local server that accepts text and returns generated text, using models stored on your disk. nexus talks to it over HTTP on `localhost:11434`.

```bash
brew install ollama
brew services start ollama
```

After installing, verify it's running:

```bash
curl http://localhost:11434   # should return "Ollama is running"
```

### golangci-lint (contributors only)

Only needed if you're modifying the Go code. The linter is enforced before every commit.

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

---

## First-time setup

Run:

```bash
make setup
```

This is interactive and handles everything. Here's what it does step by step:

1. **Creates a Python virtual environment** at `.venv/` and installs PyMuPDF
2. **Starts Ollama** as a macOS launchd service (auto-starts on login)
3. **Creates a PostgreSQL database** (`opsnexus`) and role (`vaultuser`)
4. **Enables the pgvector extension** in the database
5. **Drops and recreates document tables** — safe on first run; required when the embedding model changes (the vector dimension changes)
6. **Pulls the three Ollama models** — this downloads several gigabytes; expect 10–20 minutes on a first run
7. **Creates `~/Documents/PersonalDocs/`** with a category folder structure
8. **Writes `config.yaml`** from your answers to the interactive prompts

The database password is stored in your shell environment as `PG_PASSWORD` (written to `~/.zshrc`). If you have 1Password CLI installed and signed in, it is stored there instead.

> `config.yaml` is gitignored. It contains your local paths and the database DSN. It stays on your machine.

---

## Ingest your knowledge base

`nexus ingest` reads every file in your configured source directories, extracts its structure, splits it into chunks, embeds each chunk as a vector, and stores everything in PostgreSQL.

```bash
make ingest
```

What "chunk" means: nexus does not split files at fixed character boundaries. It parses the document structure — headings, sections, code blocks, lists — and groups content into semantically coherent units. A chunk is roughly one section of a document, including its heading breadcrumb.

To re-ingest a file that hasn't changed (normally skipped by the dedup check):

```bash
make ingest force=1
```

Progress is logged to stderr. INFO shows file-level milestones; DEBUG shows each pipeline stage. Enable debug logging:

```bash
NEXUS_LOG_LEVEL=debug make ingest
```

---

## Your first query

```bash
nexus query "What is the staging area in Git?"
```

nexus will:
1. Embed your question (same model as the document chunks)
2. Find the 15 most similar chunks in the database
3. Filter out anything below the relevance threshold (default 0.70)
4. Generate a cited answer using `llama3.1:8b`
5. Print the answer with source file names

Example output:

```
🔍 Query: What is the staging area in Git?

  📄 progit — Basic Snapshotting

Answer:

According to [1] progit — Basic Snapshotting, the staging area (also called
the "index") is a file that stores information about what will go into your
next commit. When you run `git add`, you are staging changes — moving them
from your working tree into the staging area. Running `git commit` then takes
everything in the staging area and creates a permanent snapshot.
```

### Tuning the threshold

If you get "No sufficiently relevant information found":

```bash
nexus query "..." --threshold 0.5   # lower = more results, less precise
```

nexus will show the best score it found so you can pick a sensible threshold.

### Restricting to a source

```bash
nexus query "..." --source books       # only search documents from the "books" source
nexus query "..." --source personal    # only search personal documents
```

Source names match the `name:` field in `config.yaml`.

---

## File personal documents

`nexus file` classifies a document, moves it to the right folder, and ingests it — all in one command.

```bash
nexus file ~/Downloads/invoice-april-2026.pdf
```

nexus will:
1. Extract the first ~1200 characters of readable text
2. Send it to `qwen2.5:7b` with a classification prompt
3. Get back: document type, language, institution, date, suggested filename, destination folder
4. Move the file to `~/Documents/PersonalDocs/<category>/YYYY-MM_Institution_Description.pdf`
5. Ingest it into the database with the classification metadata

Preview without moving:

```bash
nexus file ~/Downloads/invoice-april-2026.pdf --dry-run
```

---

## Watch mode

Instead of manually running `nexus file`, watch mode does it automatically for any new file that appears in configured directories.

```bash
nexus watch
```

This watches `personal.watchDirs` from `config.yaml` (default: `~/Downloads` and `~/Desktop`). When a `.pdf`, `.md`, or `.txt` file is created:

1. nexus waits 3 seconds to ensure the file has finished writing (browser downloads, phone transfers)
2. Classifies, moves, and ingests it automatically
3. Prints a one-line confirmation

```
  → Detected: invoice-april-2026.pdf
  ✓ Filed [invoice/en]: 2026-04_Canva_Invoice.pdf
```

Run `nexus watch` in a terminal you leave open, or wire it to a launchd agent to run at login.

---

## Next steps

- [Commands](commands.md) — full reference for every command and flag
- [Live Context](live-context.md) — connect nexus to your running infrastructure
- [Configuration](configuration.md) — all config fields explained
