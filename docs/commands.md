# Commands

Complete reference for every nexus CLI command and flag.

---

## Global flags

These work with every command:

```
--config string     path to config.yaml (default: ~/ops-nexus/nexus/config.yaml)
--threshold float   override relevance threshold for this run (default: from config or 0.70)
```

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

**Deduplication:** nexus computes a SHA-256 hash of every file before processing. If the hash matches what's stored in the database, the file is skipped. `--force` bypasses this. If the same file content exists at a different path, nexus logs a warning and skips the duplicate.

**Sources:** configured under `sources:` in `config.yaml`. Each source has a `name`, a `path`, and a list of `extensions`.

---

## `nexus file`

Classifies a single document, moves it to the appropriate subdirectory of `PersonalDocs`, and ingests it.

```bash
nexus file ~/Downloads/statement.pdf
nexus file ~/Downloads/statement.pdf --dry-run
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | false | Classify and print the result without moving or ingesting the file |

**What "classify" means:** nexus extracts the first ~1200 characters of readable text from the document, sends it to `qwen2.5:7b` with a structured prompt, and parses the response to get: document type, language, institution, date, suggested filename, and destination folder inside PersonalDocs.

**Example output:**

```
Classified:
  Type:        invoice
  Language:    en
  Institution: Canva
  Date:        2026-03
  Destination: ~/Documents/PersonalDocs/finance/invoices/2026-03_Canva_Invoice.pdf

→ Moving file...
→ Ingesting...
✓ Done
```

---

## `nexus watch`

Watches configured directories for new files and automatically runs the `nexus file` pipeline on each one.

```bash
nexus watch
```

Watches `personal.watchDirs` from `config.yaml` (default: `~/Downloads` and `~/Desktop`). Supported file types: `.pdf`, `.md`, `.txt`.

nexus waits **3 seconds** after each file event before processing. This settle delay ensures browser downloads and phone transfers have finished writing before the file is read.

**Example output:**

```
  Watching 2 director(ies). Press Ctrl+C to stop.

  → Detected: rabobank-march-2026.pdf
  ✓ Filed [bank_statement/nl]: 2026-03_Rabobank_Bank_Statement.pdf
```

---

## `nexus query`

Embeds your question, searches for relevant chunks, and generates a cited answer.

```bash
nexus query "What is the staging area in Git?"
nexus query "What was the Canva invoice for?"
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--threshold float` | 0 (uses config) | Minimum cosine similarity score to include a chunk |
| `--source string` | "" | Restrict search to documents from one source name or filename fragment |
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

## `nexus version`

Prints the binary version.

```bash
nexus version
nexus --version
```

The version string comes from the git tag at build time (`make build`). Running with `go run` shows a VCS commit hash instead.
