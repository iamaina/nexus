# nexus

> **Retired, 2026-10-05.** This repository is archived and read-only.
>
> The one capability worth keeping — `nexus repo check`, which finds a local
> clone or works out where a new one belongs — lives on as `ops repo check`
> in [opsmith](https://github.com/iamaina/opsmith). The port drops the
> PostgreSQL dependency this version needed just to answer the question, and
> infers placement from the clones already on disk instead.
>
> Everything else was retired on evidence rather than on a whim. `watch` had
> failed every ingest since 2026-09-17, and its launchd agent spent weeks
> restarting a binary that died at startup every five seconds, writing a log
> that reached 8.1 GB before anyone noticed. It was marked "keep, verify" and
> was never verified; verifying it is what retired it.
>
> Kept public because the decision record is the useful part. See `CHANGELOG.md`
> and `docs/`.

---
**Local-first personal intelligence layer** — fully offline document filing, search, and Q&A.

No cloud. No subscriptions. No data leaving your machine.

---

## What it does

nexus runs three AI models entirely on your laptop. It reads your documents, understands their structure, and lets you query them in plain English. Drop a PDF, get a cited answer.

| Mode | Status | Description |
|---|---|---|
| **Personal Document Safe** | ✅ Complete | Auto-classify, auto-file, and query your documents |
| **Work Intelligence** | 🚧 Active | Your running infrastructure + your technical books |
| **Teacher** | 📋 Planned | Build a course from your own ingested material |

---

## Quick start

```bash
make setup     # one-time: database, AI models, config, directories
make ingest    # index your knowledge base
nexus watch    # auto-file new documents from ~/Downloads and ~/Desktop
nexus          # start a chat session — ask anything in plain English
```

---

## Documentation

| Page | What it covers |
|---|---|
| [Getting Started](docs/getting-started.md) | Prerequisites, setup, first query — includes explanations for newcomers to the stack |
| [How It Works](docs/how-it-works.md) | The full pipeline, architecture decisions, and code organization |
| [Commands](docs/commands.md) | Every command and flag with examples |
| [Live Context](docs/live-context.md) | What `nexus context` is, why it exists, and SRE use cases |
| [Configuration](docs/configuration.md) | Every config field explained |
| [Contributing](CONTRIBUTING.md) | Branching model, commit rules, code quality checklist |
| [Commit Conventions](docs/commit-conventions.md) | Accepted commit types, version bumps, how to squash commits before a PR |
| [Changelog](CHANGELOG.md) | What changed in each release |

---

## How a question is answered

```
nexus (bare) → interactive chat session
  Your question
  → embed with mxbai-embed-large (1024-dim vector)
  → cosine similarity search in pgvector (top 15 chunks)
  → filter by relevance threshold (default 0.70)
  → expand with structural children
  → [if live sources registered] run kubectl / terraform / etc.
  → llama3.1:8b streams a cited answer in English
  → session saved to ~/.config/nexus/chats/

nexus --resume <session>  → continue a previous session with full history
nexus query "..."         → single one-off question (no session, no history)
nexus search "..."        → path/title substring lookup (no embedding needed)
```

---

## Models

Three local Ollama models, each chosen for its job:

| Model | Job | Why |
|---|---|---|
| `mxbai-embed-large` | Embeddings | Multilingual — Dutch and English land in the same vector space |
| `qwen2.5:7b` | Classification | Reliable structured JSON output |
| `llama3.1:8b` | Query answers | Better reasoning for fluent, cited answers |

All pulled automatically by `make setup`. No API keys.

---

## Make targets

| Target | Description |
|---|---|
| `make setup` | Full first-time setup |
| `make ingest` | Ingest all configured sources |
| `make ingest force=1` | Force re-ingest (ignore dedup) |
| `make query question="..."` | Ask a question |
| `make build` | Build binary (version from git tag) |
| `make install` | Install to `~/.local/bin` |
| `make lint` | Run golangci-lint |
| `make cleanup` | Delete DB, config, and binary (fresh start) |
