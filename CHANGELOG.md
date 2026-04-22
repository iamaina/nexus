# Changelog

All notable changes to nexus are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added

**Web page ingestion (`nexus ingest-url`)**
- `nexus ingest-url <url>` — fetch a web page, extract its content, and ingest it into the search index; the URL is the document identity for dedup (unchanged pages skipped on re-run)
- `--recursive` — crawl all pages within the same URL path prefix; each page fetched once and reused for both ingestion and link discovery (no double-fetch)
- `--depth <n>` — limit crawl depth (0 = unlimited); depth 1 = seed + directly linked pages, depth 2 = one level further
- `--delay <duration>` — pause between requests (e.g. `300ms`, `1s`) for polite crawling; respects `ctx.Done()` so Ctrl+C exits cleanly mid-crawl
- `--dry-run` — show every URL that would be ingested without touching the database
- `--source <name>` — custom source name for query filtering; defaults to the URL host
- `--force` — re-ingest even when content hash is unchanged
- `internal/layout/html.go` — `ExtractHTML(r io.Reader)` walks the HTML tree, maps `h1–h6` to synthetic font sizes (same convention as Markdown), skips `nav`/`header`/`footer`/`aside`/`script`; `ExtractLinks` returns all `<a href>` values for the crawler
- `internal/layout/text_extractor.go` — `.html` / `.htm` dispatch added to `Extract()` so locally-saved HTML files are also ingestable via `nexus ingest`
- Malformed link guard: paths containing `//` after the first character are rejected — prevents broken doc-site hrefs (e.g. `https//github.com/...`) from producing junk URLs
- `urls:` config section — declare web sources in `config.yaml` with `name`, `url`, `recursive`, `depth`, `watch`, `interval`, `delay`; `nexus ingest` processes all configured URL sources; `nexus watch` polls each `watch: true` URL source on its interval (default 24h)

**Chat interface**
- `nexus` (bare command) now starts an interactive chat session — no subcommand needed
- Sessions stream token-by-token with a braille spinner during retrieval
- Session files saved to `~/.config/nexus/chats/<date>_<slug>.md` after each exchange, so `Ctrl+C` only loses the answer in progress
- `nexus --resume <session>` continues a saved session with full conversation history; tab-completion lists available sessions
- `--model`, `--no-live`, `--source` flags now live on the root command (chat flags)
- Sticky header: `nexus vX.Y.Z · model · threshold · pid` pinned via terminal scroll region — stays visible as answers scroll beneath it; version shown inline
- `nexus search <term>` — path/title substring lookup for when semantic search falls short (templates, READMEs, structured documents)
- Readline line editing via `github.com/chzyer/readline`: arrow keys move cursor, `↑`/`↓` navigate history, `Ctrl+A`/`E`/`W`/`K`/`U` work as expected; one readline call per question so the prompt only appears when nexus is ready for input
- "Open to read more:" file path list printed after each answer in chat mode, matching `nexus query` output
- Sources displayed one per line in chat mode (was a single wrapped line)

**Background model downloads**
- `make setup` now starts all three Ollama model pulls in the background — setup continues immediately instead of blocking for up to 10 GB of downloads
- `scripts/pull_model.sh` — retry wrapper (3 attempts with backoff) that writes a status file (`~/.config/nexus/models/<model>.status`) after each attempt
- `nexus` detects missing models on startup, reads the status file, and streams a live progress bar with ETA directly from the Ollama pull API — no separate command needed
- Ctrl+C during the wait cancels the display but the background download continues; running `nexus` again resumes the progress view
- `make cleanup` reads the generation and classification model names from `config.yaml` instead of using hardcoded defaults

**Signal handling & process tracing**
- `signal.NotifyContext` replaces manual signal goroutine — SIGINT, SIGTERM, SIGHUP, SIGQUIT all cancel the root context and unwind cleanly through all in-flight DB, embedding, and LLM calls
- Scanner runs in a goroutine with `select { case <-ctx.Done() }` so chat input loop exits promptly on signal without blocking on `bufio.Scan()`
- PID file written to `~/.config/nexus/nexus.pid` on startup, removed on clean exit; survives `kill -9` (stale file overwritten on next start)
- Panic recovery: application-level panics print `nexus [pid N]: unexpected panic:` + stack trace and exit 2 (hardware faults handled by Go runtime)

**Bug fixes**
- `DetectHeadings`: change trailing-digit filter from `\d+$` to `\s\d+$` — was silently dropping all date headings (e.g. `## 2026-04-14`) because dates end in digits; now only filters page-number-style lines ("Introduction 15")
- `scripts/fetch_gdoc.py`: add `_element_text()` helper to extract text from all ParagraphElement types (textRun, dateElement, person, richLinkElement); fix heading/bullet priority so `HEADING_1` list items render as `##` not `1.`
- Pipeline: move `AttachBlocks` before list merging; replace flat `MergeLists` call with `MergeNodeLists` (walks the node tree and merges lists per-section). Previously, all list-item paragraphs across section boundaries were merged into a single block before attachment, leaving all but the first section in each run with zero blocks (153 chunks → was 33)
- Embeddings: prepend section title to the text embedded for each chunk so title-based queries (e.g. dates, chapter names) resolve via vector search. `chunk_text` in DB stores content only; the title prefix is embedding-only

**Google Docs integration**
- `nexus gdoc add <url> --name <name>` — register a Google Doc, fetch it immediately as Markdown, and ingest it into the search index
- `nexus gdoc auth` — one-time OAuth2 consent flow; token saved to `~/.config/nexus/gdoc_token.json`
- `nexus gdoc sync [name]` — re-fetch and re-ingest one or all registered docs; nexus watch calls this every 30 minutes automatically
- `nexus gdoc list` / `nexus gdoc rm` — manage registered docs; `rm` now fully purges: removes from registry, deletes the local `.md` file, and cascades the document + chunks out of the search index
- `scripts/fetch_gdoc.py` — Google Docs API client: handles OAuth token refresh, converts document structure (headings, lists, tables) to Markdown
- `nexus watch` now starts a 30-min gdoc sync ticker when credentials are configured and docs are registered
- `make setup` prompts for `credentials.json` path; `setup-python` installs `google-auth-oauthlib` and `google-api-python-client` into the venv
- `WorkingDirectory` added to the launchd plist so `.venv/bin/python` resolves correctly when running as a background service

**Mode 3 — Workspace OS (Phase 1 & 2)**
- `roots` config section: `workspace` root and typed `repos` roots (`work`, `personal-github`, `personal-gitlab`) with host substring matching and most-specific-wins group routing
- `source.watch: bool` — marks a source for periodic re-ingestion by `nexus watch`
- `internal/workspace` package — generates `dir_structure.md`: annotated tree view + flat Repository Index (name, full path, remote, branch, status per repo)
- `nexus watch` extended to four concurrent modes: personal intake (fsnotify), source re-scan (5-min ticker), workspace structural snapshot (fsnotify + debounce), repo root detection (fsnotify, 10s settle)
- `make watch-install` / `make watch-restart` / `make watch-uninstall` — launchd service management; logs to `~/Library/Logs/nexus-watch.log`

**Bug fixes**
- `logger`: initialise `Logger` to a safe warn-level default so callers are safe before `Init` is called — prevents nil pointer dereference when `app.New` fails early (e.g. missing config on first run)
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

**`nexus source status` — ingestion status command**
- `nexus source status` — shows all configured sources (file and URL) with per-source doc count, chunk count, last ingest timestamp, watch interval, and `opt-in` visibility flag
- Sources in `config.yaml` that have not yet been ingested appear in the table with `—` counts so you can see at a glance what still needs indexing
- Summary line: total docs · total chunks · count of sources not yet ingested with reminder to run `nexus ingest`
- `SourceStat` type in `internal/models/types.go` — carries per-source stats with a nullable `*string LastIngest` (nil = never ingested)
- `DocumentModel.SourceStats(ctx)` — aggregates doc count, chunk count, and most-recent ingest time per source in one SQL query

**URL ticker progress logging**
- `nexus watch` now logs `⟳ Crawling "<name>" (<url>)…` before each URL poll begins — previously the ticker was silent until completion
- Shows `✓ "<name>": up to date (no changes)` when a crawl completes with zero new or updated pages (was previously silent)

**SRE reference library in config.yaml**
- 17 reference documentation URL sources added to `config.yaml`: `kubernetes`, `docker`, `terraform`, `prometheus`, `helm`, `golang`, `postgresql`, `nginx`, `vault`, `ansible`, `grafana`, `gitlab-ci`, `ruby`, `bash`, `linux-commands`, `opentelemetry`, `consul`
- All set `search_by_default: false` and `category: reference` — opt-in via `--category reference` or `--source <name>`
- `linux-commands` (GeeksforGeeks) includes `delay: 500ms` for polite crawling
- Depths tuned per site: kubernetes depth 3, all others depth 1–2

**Version annotations in command help**
- Every command's `--help` output now shows `Since: vX.Y.Z` so it's clear which version introduced each command or flag
- Flags added after a command's debut are annotated with `(added vX.Y.Z)` in their description
- Enables backport targeting: a bug in `--category` (added v0.2.0) only needs backporting to `maint/v0.2.0`, not `maint/v0.1.0`

**Source categories — setup and reconfigure integration**
- `make setup` now prompts for URL sources during initial setup: URL, name, category, `search_by_default` (default yes), recursive crawl; writes a `urls:` section to `config.yaml`
- `nexus setup-reconfigure` Sources menu [2] rewritten: shows all sources (file + URL) with their `category` and `search_by_default` values; select a number to edit these fields or prefix with `r` to remove; editing toggles `search_by_default` or sets a category without re-running setup

**Source categories and default search control**
- `search_by_default: false` on any `sources:` or `urls:` entry — that source is excluded from all queries unless explicitly requested with `--source <name>`; use this for large reference sources like Wikipedia that would otherwise dominate results
- `category: <name>` on sources — logical group label (e.g. `reference`, `work`, `personal`)
- `--category <name>` flag on `nexus query` and `nexus` (chat) — restrict search to sources in the named category
- `SearchFilter` type in `internal/models` — replaces the bare `source string` parameter on `ChunkModel.Search`; carries source substring, include list (category), and exclude list (default exclusions) in a single struct; SQL built dynamically with positional placeholders — no interpolation of user values

**Setup and configuration (Phase 5)**
- `nexus source scan` — reads `dir_structure.md`, groups repos by parent directory, proposes each group as a nexus source; interactive: prompts for name per group, confirms before writing config.yaml; `--dry-run` shows groups without modifying anything
- `nexus setup-reconfigure` — menu-driven config editor: [1] Models (Balanced/Recommended/Large/Custom tier selection), [2] Sources (list + remove), [3] Database (DSN update); runs without DB or Ollama
- `make setup-reconfigure` — Makefile shortcut for `nexus setup-reconfigure`
- `make setup` model tier selection updated: Balanced (~3.5 GB), Recommended (~4.6 GB), Large (~10 GB) — generation and classification model both configurable per tier
- App.go fallback defaults updated to Recommended tier (`llama3.2:3b` + `qwen2.5:3b`)
- `config.Save()` — new method that marshals the in-memory `*Config` back to the file it was loaded from
- `workspace.ParseRepos` / `workspace.GroupByDirectory` — parse `dir_structure.md` to extract repo entries and group them by parent directory

**Mode 3 — Workspace OS (Phase 4)**
- `nexus repo scan` — walks all configured repo roots, discovers git repositories, and upserts them into a new `repos` table; run once after setup, then `nexus watch` keeps it current
- `nexus repo list` — lists all registered repositories grouped by root with live branch and dirty status
- `nexus repo check <url>` — finds an existing clone (DB lookup → workspace fallback scan → auto-register) or infers a placement from existing repo patterns and offers to clone; handles URL namespace mismatches with a corrective suggestion
- Pattern inference uses substring org matching so nested GitLab namespaces (e.g. `gitlab-com/gl-infra/*`) map correctly to their top-level subdirectory (`infrastructure/`)
- `nexus watch` wires `checkNewRepo` to upsert newly detected repos into the DB immediately on clone
- `config.FindRepoRoot` — exported method for most-specific-wins host+group routing, shared by `nexus repo check` and `nexus watch`
- `repos` table migration added to auto-migration sequence in `app.go`

**Code file ingestion**
- `Extract`: route `.tf`, `.hcl`, `.go`, `.rb`, `.py`, `.rs`, `.js`, `.ts`, `.sh`, `.yaml`, `.yml`, `.json`, `.toml`, `.sql` through `ExtractPlainText` instead of the PDF extractor — previously these extensions would silently fail
- Flat fallback section title changed from hardcoded `"Document"` to the source filename (without extension) — code files now cite `praefect.tf` in query results instead of `"Document"`

**`make setup` additions (model selection)**
- `make setup` now prompts for generation model choice: `llama3.2:3b` (fast/lightweight) or `llama3.1:8b` (recommended default, better answers); accepts any Ollama model name for custom installs
- Default generation model raised back to `llama3.1:8b` (was reduced to `llama3.2:3b` for bandwidth; selectable at setup time now)
- Classification model default raised to `qwen2.5:7b`; stored in `.ollama_class_model` alongside `.ollama_gen_model` so both persist across re-runs

**`make setup` additions (other)**
- Prompts for ops-notes exclude patterns and optional runbooks source
- Prompts for workspace root and repo roots via a generic loop — any number of roots on any platform (GitHub, GitLab, Bitbucket, Gitea, self-hosted); no hardcoded platform names or paths
- Intelligence and ops-notes sources are now optional (leave blank to skip) — no default paths assumed

**Model size reduction**
- Default generation model: `llama3.1:8b` → `llama3.2:3b` (~4.7GB → ~2.0GB)
- Default classification model: `qwen2.5:7b` → `qwen2.5:3b` (~4.7GB → ~1.9GB)
- Embedding model unchanged: `mxbai-embed-large` (~670MB) — dimension (1024) is baked into the DB schema
- Total download reduced from ~10GB to ~4.6GB

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
