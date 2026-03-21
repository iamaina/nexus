# nexus

**Your personal local knowledge vault** — fully offline RAG for PDFs, Markdown, and text.

Built for SREs who want complete control: no cloud, no subscriptions, no data leaving your machine.

## Quick Start

```bash
make setup
make ingest
make query question="What is the staging area in Git?"
```

## Features

- Ingestion from PDFs, Markdown, and plain text
- Automatic deduplication (by content hash)
- Local embeddings using `nomic-embed-text`
- Semantic search + summarization with `llama3.2`
- Everything runs on your laptop

---

## Full Installation

### 1. Bootstrap tools (once)

```Bash
make bootstrap
```

#### 2. Full setup (database + config + models)

```bash
make setup
```

This will:

- Start `PostgreSQL`
- Create the `opsnexus` database and `vaultuser`
- Install `pgvector`
- Pull Ollama models
- Ask you for your document folders and generate `config.yaml`

#### 3. First ingestion

```bash
make ingest
```

Use `--force` to re-process everything:

```bash
make ingest force=1
```

#### 4. Query your knowledge

```bash
make query question="What is the staging area in Git?"
```

---

## Project Structure

```bash
nexus/
├── cmd
│   └── nexus
│       ├── ingest.go
│       ├── query.go
│       └── root.go
├── internal
│   ├── app
│   │   └── app.go
│   ├── config
│   │   └── config.go
│   ├── ingestion
│   │   ├── chunker.go
│   │   ├── extract.go
│   │   └── ingest_file.go
│   └──logger
│       └── logger.go
├── main.go
├── Makefile
└──  README.md
```

## Common Commands

| Command | Description |
| ------- | ----------- |
| `make setup` | Full first-time setup |
| `make ingest` | Ingest all documents |
| `make query question="..."` | Ask a question |
| `make lint` | Run linter |
| `make build` | Build the binary |
| `make cleanup` | Delete DB + config (fresh start) |
