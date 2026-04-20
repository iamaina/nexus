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

All tool versions are pinned in `.mise.toml`. Do not install tools manually — `make bootstrap` handles everything so versions stay consistent.

### Step 1 — Install mise

mise is the version manager for all tools (Go, Python, golangci-lint, etc.).

```bash
brew install mise
```

> Only brew is needed before bootstrap. mise takes over from there.

### Step 2 — Run bootstrap

```bash
make bootstrap
```

This installs everything in one shot:

| What | How | Why |
|---|---|---|
| Go, Python, golangci-lint, jq, op | mise (pinned in `.mise.toml`) | Version-controlled, explicit upgrades |
| PostgreSQL 14 | brew (service) | Needs to run as a system daemon |
| Ollama | brew (service) | Needs to run as a system daemon |
| pgvector | brew (PG extension) | Must match the installed PostgreSQL version |
| PyMuPDF | pip (into `.venv/`) | PDF extraction Python library |

To upgrade a tool later: update its version in `.mise.toml`, run `mise install`, verify it works, commit.

---

## First-time setup

Once bootstrap is complete, run:

```bash
make setup
```

This is interactive and handles everything. Here's what it does step by step:

1. **Starts Ollama** as a macOS launchd service (auto-starts on login)
2. **Creates a PostgreSQL database** (`opsnexus`) and role (`vaultuser`)
3. **Enables the pgvector extension** in the database
4. **Runs database migrations** — creates tables on first run; idempotent on subsequent runs
5. **Pulls the three Ollama models** — this downloads several gigabytes; expect 10–20 minutes on a first run
6. **Creates `~/Documents/PersonalDocs/`** with a category folder structure
7. **Writes `config.yaml`** from your answers to the interactive prompts

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

## Your first question

```bash
nexus
```

This starts an interactive chat session. Type any question in plain English and press Enter:

```
❯ What is the staging area in Git?
```

nexus will:
1. Embed your question (same model as the document chunks)
2. Find the 15 most similar chunks in the database
3. Filter out anything below the relevance threshold (default 0.70)
4. Stream a cited answer using `llama3.1:8b`
5. Show source file names and "Open to read more:" paths

Sessions are saved to `~/.config/nexus/chats/` automatically. Resume a session with:

```bash
nexus --resume 2026-04-20_14-32_what-is-staging
```

Type `exit` or press `Ctrl+C` to end.

### One-off questions (non-interactive)

For scripting or piping, use `nexus query` instead:

```bash
nexus query "What is the staging area in Git?"
```

This runs once and exits — no session, no history, same search and answer pipeline.

### Tuning the threshold

If you get "No sufficiently relevant information found":

```bash
nexus --threshold 0.5            # lower = more results, less precise (chat)
nexus query "..." --threshold 0.5   # same for one-off queries
```

nexus will show the best score it found so you can pick a sensible threshold.

### Restricting to a source

```bash
nexus --source books             # only search documents from the "books" source
nexus query "..." --source books    # same for one-off queries
```

Source names match the `name:` field in `config.yaml`.

---

## File personal documents

`nexus organise` classifies documents, shows a plan of where each file will go, and moves + ingests on confirmation.

```bash
nexus organise ~/Downloads                          # batch: all files in a directory
nexus organise ~/Downloads/invoice-april-2026.pdf   # single file
nexus organise                                      # all personal.watchDirs
```

nexus will:
1. Extract the first ~1200 characters of readable text from each file
2. Send it to `qwen2.5:7b` with a classification prompt
3. Get back: document type, language, institution, date, suggested filename, destination folder
4. Show a full plan before moving anything

```
  Plan for ~/Downloads (2 files):

    invoice-april-2026.pdf  →  ~/Documents/PersonalDocs/finance/invoices/2026-04_Canva_Invoice.pdf   [existing]
    k8s-handbook.pdf        →  ~/ops-nexus/intelligence/learnings/Kubernetes/                         [new dir]

  Apply? [Y/n]
```

5. On confirmation: `mkdir -p`, move files, ingest all

Preview without moving or ingesting:

```bash
nexus organise --dry-run ~/Downloads
```

---

## Watch mode

`nexus watch` runs continuously in the background, monitoring multiple directory types at once.

```bash
make watch-install   # install as a launchd service — starts now and on every login
```

What it watches:

- **`personal.watchDirs`** (`~/Downloads`, `~/Desktop` by default) — when a `.pdf`, `.md`, or `.txt` file appears, nexus classifies it, moves it to `PersonalDocs/`, and ingests it automatically
- **Sources with `watch: true`** — re-scans every 5 minutes and ingests any new or changed files
- **`roots.workspace`** (if configured) — regenerates `dir_structure.md` whenever the workspace structure changes, keeping the snapshot queryable
- **`roots.repos`** (if configured) — detects newly cloned repositories

**Example log output:**

```
  → Detected: invoice-april-2026.pdf
  ✓ Filed [invoice/en]: 2026-04_Canva_Invoice.pdf
  ✓ Workspace snapshot updated: ~/ops-nexus/dir_structure.md
```

Check logs and status:

```bash
tail -f ~/Library/Logs/nexus-watch.log
launchctl list | grep nexus
```

---

## Next steps

- [Commands](commands.md) — full reference for every command and flag
- [Live Context](live-context.md) — connect nexus to your running infrastructure
- [Configuration](configuration.md) — all config fields explained
