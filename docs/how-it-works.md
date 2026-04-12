# How nexus Works

This document explains the architecture — the pipeline from a file on disk to a cited answer, why each piece exists, and how the code is organized.

---

## The core idea: RAG

nexus implements **Retrieval Augmented Generation (RAG)**. The problem with asking an AI a question about your documents is that the model doesn't know what's in them. Two options:

1. **Fine-tuning**: train the model on your documents. Expensive, slow, requires re-training when documents change.
2. **RAG**: at query time, find the relevant passages in your documents, then give them to the model as context. The model doesn't need to remember your documents — it just reads the excerpts you hand it.

nexus does option 2. The result is cited answers: the model says "According to [1] Pro Git — Git Basics, ..." because it actually read passage [1] to produce that answer.

**Why local?** Privacy. Your financial statements, legal documents, and infrastructure configs stay on your machine. No data leaves over the network. The AI models run on your CPU/GPU via Ollama.

---

## The full pipeline

```
File on disk (.pdf / .md / .txt)
│
├─ layout.Extract(path)
│   ├─ PDF  → scripts/extract_pdf.py    PyMuPDF → []Span (text + font size + position)
│   ├─ .md  → layout.ExtractMarkdown    ATX headings → synthetic font sizes → []Span
│   └─ .txt → layout.ExtractPlainText   all text → []Span
│
├─ layout.GroupSpansIntoLines           merge spans on the same line
├─ layout.AnalyzeFonts                  find body font size, ratio-based heading levels
├─ layout.DetectHeadings                classify lines as headings by font ratio + position
├─ layout.MergeWrappedHeadings          reunite headings broken across two lines
├─ layout.BuildBlocks                   group lines into paragraphs, code blocks, lists, images
├─ layout.MergeLists                    merge consecutive list items into one block
├─ layout.BuildHeadingTree              build a tree from the heading levels
├─ layout.TrimFrontMatter               remove TOC, preface boilerplate before chapter 1
├─ layout.AttachBlocks                  attach body blocks to their closest heading node
├─ layout.BuildSections                 flatten tree into []Section (title + content + children)
│
├─ layout.ChunkSections                 split sections into chunks of ≤5 blocks each
│   └─ flat fallback if 0 chunks: all blocks → one Section{Title: "Document"}
│
├─ embedder.OllamaEmbedder.Embed        mxbai-embed-large → []float32 (1024 dims) per chunk
│
└─ models.DocumentModel / ChunkModel    upsert document row + batch-insert chunk rows + embeddings
```

### Why not just split at 500 characters?

Most RAG implementations split text at fixed character or token counts. nexus splits at structural boundaries: a "chunk" is a section of a document, complete with its heading breadcrumb. When you retrieve chunk [3], you get "Chapter 4 > Rebasing > Interactive rebase" as context, not a random mid-paragraph slice. This dramatically improves answer quality for technical documents.

### Why PyMuPDF for PDFs?

PyMuPDF extracts text at the glyph level — each span has its font size and on-page position. nexus uses font size *ratios* to detect headings: a span at 1.4× the body font size is likely a heading regardless of the absolute point size. This is more robust than pattern matching on text (which breaks for non-English documents and non-standard heading text).

Markdown files have explicit heading syntax (`# H1`, `## H2`) so nexus synthesises equivalent font size ratios in Go directly — no Python needed.

---

## The query pipeline

```
User question (string)
│
├─ embedder.Embed(question)             same model → 1024-dim vector
│
├─ models.ChunkModel.Search(vec, 15)    pgvector cosine similarity top-15
│
├─ threshold filter                     drop chunks below relevance score (default 0.70)
│
├─ models.ChunkModel.FetchContext(r)    for each matched chunk: fetch its structural children
│   (context expansion)                 so a section intro always comes with its body
│
├─ [if live sources registered]
│   └─ live.RunAll(sources, 5s)         run kubectl / terraform / etc. concurrently
│
├─ hard cap: max 12 chunks
│
└─ summarizer.SummarizeWithLive(...)    llama3.1:8b → cited answer in English
```

**Why top-15 then filter?** Casting a wide net and filtering by threshold catches more relevant material than asking for exactly the right number upfront. The 0.70 threshold is conservative; lower it to 0.50 for shorter or less technical documents.

**Why context expansion?** Vector search might return the third paragraph of a section because that's where the specific term appears. But the first paragraph of that section has the conceptual introduction. `FetchContext` walks the heading tree and fetches the sibling and parent chunks, giving the LLM the full picture.

**Why a 12-chunk hard cap?** LLM context windows are finite. 12 chunks at ~500 characters each is roughly 6,000 characters — well within `llama3.1:8b`'s window while keeping latency reasonable.

---

## Code organization

```
nexus/
├── cmd/nexus/              CLI commands — one file per command
│   ├── root.go             Cobra root, version resolution
│   ├── ingest.go           nexus ingest
│   ├── query.go            nexus query
│   ├── file.go             nexus file
│   ├── watch.go            nexus watch
│   ├── context.go          nexus context add|list|rm|run
│   ├── chapters.go         nexus chapters
│   └── layout.go           nexus layout (debug)
│
├── internal/
│   ├── app/app.go          Application struct: all dependencies wired here
│   ├── classifier/         Document type classification (qwen2.5:7b → structured JSON)
│   ├── config/             YAML config loading — Load() returns *Config, no global state
│   ├── embedder/           Text embedding — batched calls to mxbai-embed-large
│   ├── ingestion/          Per-file pipeline + shared FileAndIngest for file/watch
│   ├── layout/             Structure-aware parser — STABLE, do not modify without care
│   ├── live/               Execute shell commands with timeout for live context
│   ├── logger/             Structured logging: coloured on tty, JSON when piped
│   ├── models/             Database access layer (DocumentModel, ChunkModel, ContextModel)
│   └── summarizer/         Answer generation — Summarize, SummarizeWithLive
│
├── scripts/
│   └── extract_pdf.py      PyMuPDF bridge — outputs JSON array of Span objects
│
└── main.go                 Wires app.New() into Cobra context, executes root command
```

### Design principles

**No global state.** `config.Load()` returns `*Config`. All dependencies flow through `*app.Application` which is passed to commands via `context.Value(app.AppKey)`. No package-level variables that mutate after startup.

**Interfaces at the point of use.** `Embedder` and `DocumentClassifier` are interfaces defined in `app.go`, not in the implementing packages. This makes the `Application` struct testable without Ollama running. `Summarizer` is kept as a concrete `*OllamaSummarizer` because its `WithModel()` method returns the same type — abstracting it would create a circular import.

**Errors always have context.** Every `return err` is wrapped: `fmt.Errorf("stage description: %w", err)`. This means a failure bubbles up with a full breadcrumb: `"run migrations: migration CREATE TABLE IF NOT EXISTS documents: ..."`.

**pgx.Batch for bulk writes.** Storing 200 chunks one-by-one is 200 database round-trips. nexus sends all chunk inserts as a single batch — one round-trip per document, regardless of chunk count.

**layout is stable.** The parser in `internal/layout/` is the most complex part of the codebase. It took several iterations to get right. Do not change it unless you understand the full pipeline. Read the canonical pipeline section in [CONTRIBUTING.md](../CONTRIBUTING.md) first.

---

## Key data types

```go
// Span — one piece of text with font metadata, output from extract_pdf.py
type Span struct {
    Text     string
    FontSize float64
    Bold     bool
    Page     int
    X, Y     float64
}

// Block — unified content unit after layout analysis
type Block struct {
    Type    BlockType   // Paragraph, Heading, Code, Image, List
    Text    string
    Items   []string    // for lists
    Page    int
    Y       float64
    Caption string      // for images
}

// Section — one node in the heading tree
type Section struct {
    Title    string
    Content  []Block
    Level    int         // 1 = chapter, 2 = section, 3 = subsection
    Page     int
    Children []Section
}

// EnrichedChunk — rendered text ready for the database
type EnrichedChunk struct {
    Text    string   // heading breadcrumb + all block text
    Chapter string   // top-level heading (for display)
    Level   int
}

// Result — a retrieved chunk from the database
type Result struct {
    DocumentID int64
    ChunkIndex int
    Level      int
    File       string
    Chapter    string
    Text       string
    Score      float64   // cosine similarity 0–1
}

// ContextSource — a registered live context command
type ContextSource struct {
    ID          int64
    Name        string
    Command     string
    Description string
    CreatedAt   string
}
```

---

## The database schema

```sql
CREATE TABLE documents (
    id           BIGSERIAL PRIMARY KEY,
    source_name  TEXT NOT NULL,           -- config sources[].name
    file_path    TEXT UNIQUE NOT NULL,    -- absolute path, dedup key
    file_hash    TEXT NOT NULL,           -- SHA-256, skip if unchanged
    char_count   INT NOT NULL,
    chunk_count  INT NOT NULL,
    ingest_time  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    -- classification metadata (NULL for batch-ingested docs)
    doc_type     TEXT,                    -- invoice, bank_statement, etc.
    language     TEXT,                    -- BCP-47: en, nl, fr
    institution  TEXT,                    -- ING, Canva, etc.
    doc_date     TEXT                     -- YYYY-MM or YYYY-MM-DD
);

CREATE TABLE chunks (
    id             BIGSERIAL PRIMARY KEY,
    document_id    BIGINT REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index    INT NOT NULL,
    chunk_text     TEXT NOT NULL,         -- heading breadcrumb + content
    chapter        TEXT,                  -- top-level heading for display
    section_level  INT DEFAULT 0,
    embedding      vector(1024),          -- mxbai-embed-large dimensions
    UNIQUE (document_id, chunk_index)
);

CREATE TABLE context_sources (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT UNIQUE NOT NULL,     -- e.g. "kubectl", "terraform"
    command     TEXT NOT NULL,            -- shell command string
    description TEXT,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

All tables are created (and columns added) automatically by migrations on startup. Migrations are idempotent — safe to run repeatedly.

---

## How classification works

When you run `nexus file` or `nexus watch`, the classifier:

1. Extracts ~1200 characters of readable text from the file using the same layout pipeline
2. Sends that text + the filename to `qwen2.5:7b` with a structured prompt
3. Parses the JSON response: `{doc_type, language, institution, date, filename, dest_dir}`
4. Sanitises the response (strips "Unknown", removes extensions from filename, sets safe defaults)
5. Moves the file to `~/Documents/PersonalDocs/<dest_dir>/<filename>.<ext>`
6. Ingests with the metadata stored in the `documents` table

`qwen2.5:7b` is used for classification (not `llama3.1:8b`) because it is specifically good at producing valid structured JSON — fewer hallucinations in the output format.

---

## Version handling

The binary version comes from git tags:

```bash
make build   # injects git describe --tags → v0.1.0-38-gABCDEF
go run .     # reads VCS hash from debug.ReadBuildInfo() → dev+abc1234
```

The `buildVersion` variable in `root.go` is the ldflags injection target. It must be a plain string literal — Go's `-X` flag can only replace literals. `Version` is computed from it at startup with a VCS hash fallback for development.

---

## Further reading

- [Live Context](live-context.md) — how nexus context works and when to use it
- [Getting Started](getting-started.md) — setup walkthrough
- [Contributing](../CONTRIBUTING.md) — branching model, commit rules, code quality checklist
