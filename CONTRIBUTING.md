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

## Branching Model

```
master          ──●─────────────────────●──────────────────────●──
                 v0.2.0                v0.3.0                 v0.4.0
                    \                     \
stable/v0.3.0        ●──●──●──●──merge     \
                                             stable/v0.4.0    ●──●──...
```

### Rules

- **`master`** — always stable. Only receives merges from a `stable/vX.Y.Z` branch. Never commit directly. Every merge is tagged automatically.
- **`stable/vX.Y.Z`** — the single working branch for all work toward the next release. Created automatically by CI after each tag is pushed. Devs branch off this and open PRs back into it.
- **One working branch at a time.** When `stable/v0.3.0` merges to master and the `v0.3.0` tag is pushed, CI creates `stable/v0.4.0` automatically.
- Short-lived `fix/name` or `feat/name` branches are for isolated changes; open a PR into the current `stable/vX.Y.Z` branch.

### Release cycle

```bash
# 1. All work happens on the current stable branch (created automatically by CI)
git checkout stable/v0.4.0

# 2. For isolated changes, create a short-lived branch and open a PR
git checkout -b feat/my-feature
git add <specific files>
git commit -m "feat(scope): short description"
git push origin feat/my-feature
# → open PR into stable/v0.4.0

# 3. When the milestone is complete and tested:
#    Squash all commits in the stable branch into one conventional commit
git rebase -i master
# → set all but the first to 's', write one clean conventional commit message

# 4. Force-push the squashed branch
git push --force origin stable/v0.4.0

# 5. Open a PR into master
#    The PR title must match your squashed commit message (CI enforces this)

# 6. On merge — CI automatically:
#    a) Creates a version tag based on commit type:
#       feat:     → minor bump  (v0.3.0 → v0.4.0)
#       fix:      → patch bump  (v0.4.0 → v0.4.1)
#       breaking: → major bump  (v0.4.0 → v1.0.0)
#    b) Creates the next stable branch (e.g. stable/v0.5.0) from master
#    c) Protects the new branch (require PR, CI must pass)

# 7. Switch to the new branch — no manual cleanup needed
git checkout master && git pull
git checkout stable/v0.5.0
```

> **Full squash guide:** [docs/commit-conventions.md](docs/commit-conventions.md)

### Tracking progress during development

`git describe --tags` automatically shows `v0.2.0-N-gSHA` on the working branch — N commits since the last release tag. `make build` injects this into the binary. No extra tagging needed during development.

---

## Commit Messages (Conventional Commits)

Format:
```
<type>(<scope>): <subject>        ← max 72 chars, lowercase, no period at end

<body>                            ← explain WHY, not WHAT. Wrap at 72 chars.
                                    Leave blank if subject is self-explanatory.

<footer>                          ← BREAKING CHANGE:, issue refs, Co-Authored-By:
```

### Types

| Type | When | Version impact |
|---|---|---|
| `breaking` / `major` | incompatible change — removed flag, destructive schema change, config format change | major bump (`v0.3.0` → `v1.0.0`) |
| `feat` | new user-facing feature | minor bump (`v0.2.0` → `v0.3.0`) |
| `fix` | bug fix | patch bump (`v0.3.0` → `v0.3.1`) |
| `perf` | performance improvement, no behaviour change | patch bump |
| `refactor` | restructuring, no behaviour change | patch bump |
| `docs` | documentation only | patch bump |
| `chore` | deps, Makefile, build tooling, CI | patch bump |
| `test` | adding or fixing tests | patch bump |

> The CI pipeline enforces this. A non-conforming commit message **fails the build** and blocks the merge. See [docs/commit-conventions.md](docs/commit-conventions.md) for the full guide including how to squash commits.

### Scope (optional)

Use the package or command name: `feat(watch):`, `fix(classifier):`, `perf(models):`, `chore(deps):`

### Examples

```
feat(watch): add 3-second settle delay before processing new files
fix(classifier): strip "Unknown" string from LLM institution field
perf(models): replace N+1 db inserts with pgx.Batch
refactor(config): Load() returns *Config instead of mutating global
docs: rewrite README to reflect v0.2.0 commands and model setup
chore(deps): upgrade fsnotify to v1.9.0
```

### Rules

- Subject: imperative mood ("add" not "added"), lowercase, no period
- Body: blank line after subject; explain the *why* (the what is in the diff)
- `git add <specific files>` — never `git add -A` or `git add .`
- Commit one logical change per commit — don't bundle unrelated fixes

---

## Versioning

nexus follows [Semantic Versioning](https://semver.org/):

```
vMAJOR.MINOR.PATCH
   │     │     └── bug fixes, no new features
   │     └──────── new features, backwards compatible
   └────────────── breaking changes or major mode completions
```

### Version milestones

| Version | Milestone |
|---|---|
| v0.1.x | Core RAG pipeline (stable) |
| v0.2.x | Mode 1 — Personal Document Safe ✅ |
| v0.3.x | Mode 2 — Work Intelligence |
| v0.4.x | Mode 3 — Teacher |
| v1.0.0 | All three modes stable, tests, production-ready |

### Tag rules

- **Always annotated**: `git tag -a v1.2.3 -m "v1.2.3 — one-line summary"`
- **Format**: `vMAJOR.MINOR.PATCH` — strictly semver, always `v`-prefixed
- **Never move or delete a published tag** — cut a new patch instead
- **Tag only on `master`** — never tag on a release branch
- Pre-release suffixes (`-rc.1`, `-beta.1`) are for public release candidates; not used for day-to-day dev work


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
