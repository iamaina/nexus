# Commands

Complete reference for every nexus CLI command and flag.

---

## `nexus` — interactive chat (default)

Running `nexus` with no subcommand starts an interactive chat session. Ask anything in
plain English. Answers are cited and streamed token by token. Sessions are saved to
`~/.config/nexus/chats/` automatically after each exchange.

```bash
nexus                                              # start a new session
nexus --resume 2026-04-20_14-32_praefect           # continue a saved session (tab-complete)
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--resume string` | "" | Continue a saved session (tab-complete with session names) |
| `--model string` | "" | Override generation model for this session |
| `--no-live` | false | Skip live context sources (kubectl, terraform, etc.) |
| `--source string` | "" | Restrict search to one or more sources (repeatable: `--source a --source b`, or comma-separated: `--source a,b`) |
| `--category string` | "" | Restrict search to sources in this category (e.g. `reference`, `work`) |
| `--threshold float` | 0 (uses config) | Minimum cosine similarity score to include a chunk |

Inside a session, type `exit` or `quit` to end, or press `Ctrl+C`. The session file is
updated after every exchange so `Ctrl+C` only loses the answer that was in progress.

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| `←` / `→` | Move cursor within the line |
| `↑` / `↓` | Navigate session history |
| `Ctrl+A` / `Ctrl+E` | Jump to start / end of line |
| `Ctrl+W` | Delete word backwards |
| `Ctrl+K` | Delete to end of line |
| `Ctrl+U` | Clear the whole line |
| `Backspace` | Delete character |

The header line (`nexus vX.Y.Z · model · threshold · pid`) is pinned — it stays visible as
answers scroll. The PID is shown for easy signal tracing:
```
kill $(cat ~/.config/nexus/nexus.pid)
```

**In-session slash commands:**

| Command | Description |
|---|---|
| `/source <name>` | Restrict search to one or more sources (space- or comma-separated) |
| `/source clear` | Remove source restriction |
| `/source` or `/source show` | Show current active filter |
| `/gl todos` | Fetch your pending GitLab todos and get a prioritised recommendation |
| `/gl todos <host>` | Same but for a specific GitLab instance (e.g. `ops.gitlab.net`) |
| `/gl items <group-path\|url>` | List open work items / issues in a GitLab group |

GitLab URLs pasted anywhere in your question are **auto-fetched** — no slash command needed:
```
What is this issue about? https://gitlab.com/namespace/project/-/issues/123
What changed in https://gitlab.com/namespace/project/-/merge_requests/456?
```

---

## Global flags

These work with every command:

```
--config string     path to config.yaml (default: ~/ops-nexus/nexus/config.yaml)
--threshold float   override relevance threshold for this run (default: from config or 0.70)
-v, --verbose       show connection and pipeline logs (INFO level)
```

---

## `nexus search`

Finds documents and sections by file path or heading title using a plain substring match.
Use this when you know the name of a file or section. For meaning-based search, use `nexus query`.

```bash
nexus search "change_management"
nexus search "Reviewer checklist"
nexus search praefect --source runbooks
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--source string` | "" | Restrict to a source name or path substring |

Results are grouped by file with chapter labels and 120-character text previews.

---

## `nexus ingest`

Reads all configured source directories, extracts structure, chunks, embeds, and stores documents.

```bash
nexus ingest
nexus ingest --force
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--force` | false | Re-ingest every file, ignoring the SHA-256 dedup check |
| `--background` | false | Run ingest detached from the terminal; returns immediately and logs to `~/.config/nexus/logs/ingest.log` |

**Deduplication:** nexus computes a SHA-256 hash of every file before processing. If the hash matches what's stored in the database, the file is skipped. `--force` bypasses this. If the same file content exists at a different path, nexus logs a warning and skips the duplicate.

**Sources:** configured under `sources:` in `config.yaml`. Each source has a `name`, a `path`, and a list of `extensions`.

---

## `nexus ingest-url`

Fetches a web page (or an entire docs site), extracts structured content, and ingests it into the search index. The URL is the document identity — unchanged pages are skipped automatically on re-run.

```bash
nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/
nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive
nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --dry-run
nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --source chef-knife-docs --delay 300ms
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--recursive` | false | Follow all links within the same URL path prefix |
| `--depth int` | 0 | Max crawl depth (0 = unlimited; 1 = seed + directly linked pages) |
| `--delay duration` | none | Pause between requests, e.g. `200ms`, `1s` — polite crawling |
| `--source string` | derived from host | Source name for query filtering (e.g. `--source chef-knife-docs`) |
| `--dry-run` | false | Show every URL that would be ingested without touching the database |
| `--force` | false | Re-ingest even when content hash is unchanged |
| `--save` | false | Persist this source to `config.yaml` so `nexus ingest` and `nexus watch` pick it up automatically |
| `--watch` | false | When used with `--save`, set `watch: true` so `nexus watch` polls this source on its interval |
| `--background` | false | Run the crawl detached; returns immediately and logs to `~/.config/nexus/logs/ingest-url-<name>.log` |

**Config-based URL sources** — add to `config.yaml` and they run with `nexus ingest` and `nexus watch`:

```yaml
urls:
  - name: chef-knife-docs
    url: https://docs.chef.io/workstation/26/tools/knife/
    recursive: true
    depth: 0          # unlimited within the path prefix
    watch: true       # nexus watch re-checks on interval
    interval: 24h     # polling interval (default: 24h)
    delay: 300ms      # pause between requests
```

**Querying ingested web sources:**

```bash
nexus query "how do I use knife ssh to run a command on a node" --source chef-knife-docs
```

---

## `nexus organise`

Classifies documents, shows a plan of where each file will go, and moves + ingests on confirmation. Replaces `nexus file`.

```bash
nexus organise                                      # process all personal.watchDirs
nexus organise ~/Downloads                          # process a directory
nexus organise ~/Downloads/invoice.pdf              # process a single file
nexus organise --dry-run ~/Downloads                # show plan without moving anything
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | false | Show the plan without moving or ingesting |
| `--force` / `-f` | false | Re-ingest even if file content is unchanged |

**How path resolution works:**

- If the argument is a **file** → single-file mode
- If the argument is a **directory** → batch mode, all supported files (`.pdf`, `.md`, `.txt`) inside it
- If **no argument** → batch mode on all `personal.watchDirs`

**Routing:**

- `book` and `article` types search configured source directories for an existing directory matching the document topic; if none found, suggests a new directory under the first source root
- All other types route to `PersonalDocs/{dest_dir}/` as classified by the LLM

**Example output:**

```
  Classifying 3 file(s)...

  Classifying invoice-april-2026.pdf ...
  Classifying kubernetes-handbook.pdf ...
  Classifying bank-statement.pdf ...

  Plan for ~/Downloads (3 file(s)):

    invoice-april-2026.pdf    →  ~/Documents/PersonalDocs/finance/invoices/2026-04_Canva_Invoice.pdf              [existing]
    kubernetes-handbook.pdf   →  ~/ops-nexus/intelligence/learnings/Kubernetes/Kubernetes_In_Action.pdf           [existing]
    bank-statement.pdf        →  ~/Documents/PersonalDocs/finance/bank-statements/2026-03_ABNAMRO_Statement.pdf   [new dir] 

  Apply? [Y/n]
```

---

## `nexus workspace`

Commands for generating and inspecting the workspace structure map.

### `nexus workspace scan`

Walks `roots.workspace`, detects all git repos, and writes `dir_structure.md` at the workspace root. Also ingests the file as source `workspace-structure` so it is immediately queryable.

This is the bootstrap command — run it once on first setup, or any time you want to force an immediate refresh without waiting for the next `nexus watch` cycle.

**`nexus organise` requires this file to exist before it will proceed.**

```bash
nexus workspace scan
```

`nexus watch` keeps the map current automatically (regenerated on startup, every 24 h, and whenever a new repo is detected). `nexus workspace scan` is only needed for the initial run or manual refreshes.

---

## `nexus watch`

Monitors multiple directory types concurrently. Designed to run as a background service via `make watch-install`.

```bash
nexus watch
```

Four watch modes run in parallel:

| Mode | Trigger | Action |
|---|---|---|
| Personal intake | `personal.watchDirs` — file created/written | Classify → move → ingest (3s settle delay) |
| Source re-scan | Sources with `watch: true` | Re-ingest new/changed files every 5 minutes |
| Workspace snapshot | `roots.workspace` — directory created/removed | Regenerate and ingest `dir_structure.md` |
| Repo detection | `roots.repos[watch: true]` — new directory | Detect newly cloned repositories (10s settle) |

Supported personal file types: `.pdf`, `.md`, `.txt`.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--list` | false | Print all configured watchers without starting |

**Running as a background service (recommended):**

```bash
make watch-install     # install as launchd agent, starts at login
make watch-restart     # reload after make install (new binary)
make watch-uninstall   # remove the service
tail -f ~/Library/Logs/nexus-watch.log   # logs
```

**Example output (foreground):**

```
  Watching 4 director(ies), 2 source ticker(s). Press Ctrl+C to stop.

  → Detected: rabobank-march-2026.pdf
  ✓ Filed [bank_statement/nl]: 2026-03_Rabobank_Bank_Statement.pdf
  ✓ Workspace snapshot updated: ~/ops-nexus/dir_structure.md
```

---

## `nexus query`

`nexus query` was the original interface to nexus before the interactive chat session became the default. It performs the same semantic search and LLM answer pipeline as the chat, but exits immediately after printing the answer — no session, no history, no streaming UI. It exists for automation and scripting: results can be piped, redirected, or called from cron jobs. Use `nexus` (bare) for day-to-day interactive work; use `nexus query` when you need a single answer in a script or want to chain it with other tools.

Embeds your question, searches for relevant chunks, and generates a cited answer.

```bash
nexus query "What is the staging area in Git?"
nexus query "What was the Canva invoice for?"
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--threshold float` | 0 (uses config) | Minimum cosine similarity score to include a chunk |
| `--source string` | "" | Restrict search to one or more sources (repeatable or comma-separated) |
| `--category string` | "" | Restrict search to sources in this category (e.g. `reference`, `work`) |
| `--model string` | "" | Override the generation model for this query |
| `--sources` | false | Print retrieved source chunks before the answer |
| `--no-live` | false | Skip running registered live context sources |

**Threshold guidance:**

| Document type | Suggested threshold |
|---|---|
| Technical books | 0.70 (default) |
| Short personal docs (invoices, letters) | 0.50–0.60 |
| Mixed sources | 0.65 |

If nexus says "No sufficiently relevant information found", it will print the best score it found:
```
(best match scored 0.63 — threshold is 0.70; try --threshold 0.62 to include it)
```

**Source filter:** `--source progit` matches documents whose file path contains "progit". Useful for restricting a query to one book or one category.

**Live context:** If you have registered context sources (see [`nexus context`](#nexus-context)), they run automatically before generating the answer. Live outputs are shown as `⚡ name` lines above the answer. Use `--no-live` to skip them for a particular query.

---

## `nexus context`

Manages **live context sources** — shell commands whose output is injected into the query prompt alongside your static documents.

See [Live Context](live-context.md) for a full explanation of what this is for.

### `nexus context add`

Register a new live context source.

```bash
nexus context add <name> "<command>" [--description "..."]
```

```bash
nexus context add kubectl  "kubectl get pods -A"            --description "all pods across namespaces"
nexus context add nodes    "kubectl get nodes -o wide"      --description "node status and IP addresses"
nexus context add tf       "terraform show | head -80"      --description "current Terraform state summary"
nexus context add prom     "curl -s http://prometheus:9090/api/v1/query?query=up | jq '.data.result'"
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--description string` | "" | Human-readable description of what this source provides |

The name must be unique. The command runs in a shell (`sh -c`) so pipes, redirects, and environment variables all work.

### `nexus context list`

Show all registered sources.

```bash
nexus context list
```

```
  NAME              COMMAND                                   ADDED
  ────────────────  ────────────────────────────────────────  ───────────────────
  kubectl           kubectl get pods -A                       2026-04-06 21:41
                    all pods across namespaces
  nodes             kubectl get nodes -o wide                 2026-04-06 21:41
```

### `nexus context run`

Execute a single source and print its output. Use this to test a source before relying on it in queries.

```bash
nexus context run kubectl
```

```
  $ kubectl get pods -A

NAMESPACE     NAME                         READY   STATUS    RESTARTS   AGE
kube-system   coredns-5d78c9869d-abc12     1/1     Running   0          3d
...
```

### `nexus context rm`

Remove a registered source.

```bash
nexus context rm kubectl
```

---

## `nexus list`

Lists all ingested documents, grouped by source.

```bash
nexus list
nexus list --source books
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--source string` | "" | Filter by source name |

---

## `nexus chapters`

Lists the top-level chapter headings detected in an ingested document. Useful for understanding how nexus parsed a specific file.

```bash
nexus chapters progit
nexus chapters "2026-03_Canva_Invoice"
```

The argument is matched against document file paths as a substring.

---

## `nexus layout`

Debug tool for inspecting the layout pipeline on a specific file. Shows the output of each stage.

```bash
nexus layout ~/Documents/progit.pdf
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--fonts` | false | Show font analysis (body size, heading levels) |
| `--headings` | false | Show detected headings |
| `--blocks` | false | Show all blocks (paragraphs, code, lists, images) |
| `--tree` | false | Show the heading tree structure |
| `--chunks` | false | Show the final chunks as they would be stored |
| `--page-from int` | 0 | Only process pages from this number |
| `--page-to int` | 0 | Only process pages up to this number |

**Common workflows:**

```bash
# Is heading detection working?
nexus layout --headings mybook.pdf

# Are chunks good quality?
nexus layout --chunks --page-from 1 --page-to 20 mybook.pdf

# What font sizes are in this document?
nexus layout --fonts mybook.pdf
```

---

## `nexus source`

Manage document sources — directories whose files nexus indexes for search.

### `nexus source status`

Shows all configured sources (file and URL) with per-source ingestion statistics. Sources that have not yet been ingested appear in the table with `—` counts so you can see at a glance what still needs indexing.

```bash
nexus source status
```

**Example output:**

```
  Source             Type  Docs      Chunks    Last Ingest       Watch   Visibility
  ─────────────────  ────  ───────   ─────────  ────────────────  ──────  ──────────
  books              file       47       1,832  2026-04-20 12:10  —
  intelligence       file       12         398  2026-04-19 08:45  —
  ops-notes          file        —          —   never             5m
  runbooks           file       89       2,104  2026-04-21 10:30  5m
  wikipedia          url        22         541  2026-04-18 14:00  —       opt-in
  kubernetes         url         —          —   never             —       opt-in

  Total: 170 docs · 4,875 chunks  ·  2 source(s) not yet ingested — run: nexus ingest
```

**Columns:**

| Column | Description |
|---|---|
| Source | Source name as configured in `config.yaml` |
| Type | `file` (local directory) or `url` (web source) |
| Docs | Number of ingested documents (`—` = not yet ingested) |
| Chunks | Total chunks stored in the vector index |
| Last Ingest | Timestamp of most recent ingest (format: `YYYY-MM-DD HH:MI`) |
| Watch | Re-ingest interval if `watch: true` (e.g. `5m`, `24h`); `—` if not watched |
| Visibility | `opt-in` if `search_by_default: false`; blank if included in default search |

### `nexus source rm`

Removes all ingested documents and chunks for a named source from the database. The source entry in `config.yaml` is not touched — only the indexed data is deleted.

```bash
nexus source rm Wikipedia
```

```
  Source:  Wikipedia
  Docs:    493
  Chunks:  15,246

  This will permanently delete all indexed content for "Wikipedia".
  Continue? [y/N] y
  ✓ Removed 493 doc(s) and their chunks for source "Wikipedia".
```

---

### `nexus source scan`

Reads `dir_structure.md` from the workspace root, groups git repositories by their parent
directory, and proposes each group as a nexus source. This solves the "everything in one
big source" problem by splitting a workspace into per-directory sources (e.g. `infrastructure`,
`release-deployment`, `docs-tasks`).

```bash
nexus source scan             # interactive — prompts for names, then applies
nexus source scan --dry-run   # show discovered groups without modifying config.yaml
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | false | Show discovered groups without modifying config.yaml |

**How it works:**

1. Reads `{roots.workspace}/dir_structure.md` (generated by `nexus watch`)
2. Groups repos by their immediate parent directory
3. Filters out directories already configured as sources
4. Shows each new group with its repo count and a preview of repos
5. Prompts for a source name per group (Enter = use directory name, `-` = skip)
6. Shows the full plan and asks for confirmation before writing config.yaml

**Example output:**

```
  Discovered 4 repo group(s) not yet in config.yaml:

  [1] ~/ops-nexus/active-ops/gitlab-work/infrastructure/  (28 repos)
        - delivery                      [gitlab.com/gitlab-com/gl-infra/delivery]
        - charts                        [gitlab.com/gitlab-com/gl-infra/charts]
        ... and 26 more

  [2] ~/ops-nexus/active-ops/gitlab-work/release-deployment/  (5 repos)
        - deployer                      [ops.gitlab.net/gitlab-com/gl-infra/deployer]
        ...

  For each group, enter a source name (Enter = use directory name, '-' = skip):

  ~/ops-nexus/active-ops/gitlab-work/infrastructure/ → [infrastructure]:
  ~/ops-nexus/active-ops/gitlab-work/release-deployment/ → [release-deployment]:
  ...

  Apply? [Y/n]
  ✅  Added 4 source(s) to config.yaml.
      Run 'nexus ingest' to index the new sources.
```

**Prerequisites:** `roots.workspace` must be set in config.yaml and `nexus watch` must have run at least once to generate `dir_structure.md`.

---

## `nexus setup-reconfigure`

Interactively update individual sections of `config.yaml` without re-running the full
`make setup`. Useful for changing models, removing a source, or updating the database DSN.

Does not require the database or Ollama to be running.

```bash
nexus setup-reconfigure
make setup-reconfigure    # equivalent Makefile shortcut
```

**Menu sections:**

| Section | What you can do |
|---|---|
| [1] Models | Choose a model tier (Balanced/Recommended/Large/Custom) |
| [2] Sources | List current sources, remove one by number |
| [3] Database | Update the Postgres DSN |

**Model tiers:**

| Tier | Generation | Classification | Total size |
|---|---|---|---|
| Balanced | llama3.2:3b | qwen2.5:1.5b | ~3.5 GB |
| Recommended | llama3.2:3b | qwen2.5:3b | ~4.6 GB |
| Large | llama3.1:8b | qwen2.5:7b | ~10 GB |

After updating models, the command prints the exact `ollama pull` commands needed to download
the new models.

---

## `nexus repo`

Manage and locate git repositories across your workspace.

### `nexus repo scan`

Walks all configured `roots.repos` directories, discovers git repositories, and registers them in the nexus database. Run once after setup; `nexus watch` keeps the database current as new repos are cloned.

```bash
nexus repo scan
```

### `nexus repo list`

Lists all registered repositories grouped by root, with live branch and dirty status.

```bash
nexus repo list
```

### `nexus repo check <url>`

Finds an existing clone or suggests where to put a new one.

```bash
nexus repo check git@gitlab.com:gl-infra/delivery.git
nexus repo check https://github.com/iamaina/nexus.git
```

**Lookup order:**
1. DB lookup by normalised remote URL (instant)
2. Workspace root scan if not found in DB (auto-registers on match)
3. Pattern inference from existing repos in the matching root

**If found:**
```
  ✅  ~/ops-nexus/active-ops/gitlab-work/infrastructure/delivery
      Branch: main  |  clean
      Last commit: fix(ci): update pipeline config (2 days ago)
```

**If not found — suggests placement:**
```
  ❌  gitlab.com/gl-infra/delivery not found in any registered root.

  Suggested location (work root):
    ~/ops-nexus/active-ops/gitlab-work/infrastructure/delivery  [inferred from gl-infra/* pattern]

  Clone here? [Y/n]
```

**If a different repo exists at the suggested path:**
```
  ⚠️   A different repository already exists at that path:
      remote: gitlab.com/gitlab-com/gl-infra/delivery
  Did you mean: nexus repo check git@gitlab.com:gitlab-com/gl-infra/delivery.git ?
```

---

## `nexus gdoc`

Manage Google Doc sources — register shared docs so nexus can search them and keep them current.

### One-time setup

1. Create a Google Cloud project, enable the **Google Docs API**, and create OAuth 2.0 credentials (Desktop app type). Download `credentials.json`.
2. Add to `config.yaml`:
   ```yaml
   gdoc:
     credentialsPath: ~/path/to/credentials.json
   ```
3. Authenticate once:
   ```bash
   nexus gdoc auth
   ```
   A browser window opens. Grant access. The token is saved and reused automatically.

### `nexus gdoc add`

Register a Google Doc and ingest it immediately.

```bash
nexus gdoc add https://docs.google.com/document/d/<id>/edit --name manager-1on1
nexus gdoc add https://docs.google.com/document/d/<id>/edit --name mentor-notes
```

**Flags:**

| Flag | Required | Description |
|---|---|---|
| `--name string` | yes | Short name to identify the doc |

The doc is saved as `~/.local/share/nexus/gdocs/{name}.md` and ingested as source `gdocs`. The sync directory can be changed with `gdoc.syncDir` in config.

### `nexus gdoc sync`

Re-fetch and re-ingest one or all registered docs. nexus watch does this automatically every 30 minutes.

```bash
nexus gdoc sync                  # sync all
nexus gdoc sync manager-1on1     # sync one
```

### `nexus gdoc list`

Show all registered docs with their last sync time.

```bash
nexus gdoc list
```

```
  NAME                      DOC ID                                        LAST SYNCED
  ────────────────────────  ────────────────────────────────────────────  ────────────────
  manager-1on1              1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2up   2026-04-16 09:15
  mentor-notes              1aB2cD3eF4gH5iJ6kL7mN8oP9qR0sT1uV2wX3yZ4a    2026-04-16 09:15
```

### `nexus gdoc rm`

Remove a registered doc. The ingested content stays in the search index until the document file is deleted and the index rebuilt.

```bash
nexus gdoc rm manager-1on1
```

---

## `nexus index`

Monitor and rebuild the IVFFlat vector index on the `chunks` table.

The index partitions 1024-dimensional chunk vectors into buckets at build time. Queries probe the nearest buckets instead of scanning all rows — roughly 400× faster at 4M+ chunks with the default `probes=10` setting. The index degrades as data grows because the original bucket centroids no longer reflect the current distribution. These commands tell you when to act and do the rebuild safely.

### `nexus index status`

Show index health and the exact command to run if a rebuild is needed. Read-only.

```bash
nexus index status
```

**Example output — healthy:**
```
  Index:  chunks_embedding_idx  (IVFFlat, lists=4000)
  Built:  2026-04-26 with 4,195,592 chunks
  Now:    4,195,592 chunks  (+0%)

  ✅  Index is healthy. No action needed.
      Rebuild recommended when chunks reach ~6,293,388.
```

**Example output — reindex recommended:**
```
  Index:  chunks_embedding_idx  (IVFFlat, lists=4000)
  Built:  2026-04-26 with 4,195,592 chunks
  Now:    6,800,000 chunks  (+62%)

  ⚠️   Chunk count has grown 62% since the index was built.
      Bucket centroids are becoming stale. REINDEX recommended.

  Run:  nexus index rebuild
```

**Example output — resize recommended:**
```
  ⚠️   lists=4000 is significantly below optimal (9000).
      Bucket centroids no longer reflect the current data distribution.

  Run:  nexus index rebuild --resize   ← drop + recreate with lists=9000
```

### `nexus index rebuild`

Rebuild the index to restore full search performance.

```bash
nexus index rebuild           # REINDEX CONCURRENTLY — same lists, queries stay live
nexus index rebuild --resize  # drop + recreate with optimal lists value
```

**Decision guide:**

| Condition | Recommendation |
|---|---|
| Growth < 50% from build count | Nothing — `nexus index status` shows ✅ |
| Growth 50–100%, lists within 30% of optimal | `nexus index rebuild` |
| Growth > 100% or lists > 30% below optimal | `nexus index rebuild --resize` |

**`maintenance_work_mem` is handled automatically:**

Both modes check the current setting. If it is below 2 GB, `ALTER SYSTEM SET maintenance_work_mem = '2GB'` is run once to fix it permanently, then 5 GB is set for the current session. No manual `PGOPTIONS` workaround needed.

**Flags:**

| Flag | Description |
|---|---|
| `--resize` | Drop and recreate with `lists = current_count / 1000` instead of REINDEX |

**Background:** `nexus watch` runs an index health check every 24 h and logs a `[WARN]` message if a rebuild is recommended. No automatic rebuild — the command is always user-driven.

---

## `nexus version`

Prints the binary version.

```bash
nexus version
nexus --version
```

The version string comes from the git tag at build time (`make build`). Running with `go run` shows a VCS commit hash instead.
