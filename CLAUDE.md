# Nexus

**Last Updated:** 2026-03-31
**Version:** v0.4.0 (Layout + Chunking Complete, Ingestion Wiring Pending)

## Stack

- Go, Cobra CLI
- PostgreSQL + pgvector
- Ollama (embedding: `nomic-embed-text`, generation: `llama3.2`)
- PDF extraction via Python/pymupdf (`scripts/extract_pdf.py`)

---

### 1. Project Overview

**nexus** is a **local-first RAG (Retrieval-Augmented Generation) CLI tool** that:

- Ingests PDFs
- Reconstructs their **true structure (layout-aware)**
- Converts them into **semantic chunks**
- Stores embeddings in **PostgreSQL (pgvector)**
- Answers natural language questions via **Ollama**

### 2. Stack

- **Go** (Cobra CLI)
- **PostgreSQL + pgvector**
- **Ollama**
  - Embeddings: `nomic-embed-text`
  - Generation: `llama3.2`
- **Python (PyMuPDF)** for PDF extraction (`scripts/extract_pdf.py`)

---

### 3. Core Philosophy

nexus is NOT:

- a naive text extractor ❌
- a regex-based parser ❌
- a string chunker ❌

nexus IS:

```text
A structure-aware document understanding engine
```

---

### 4. Canonical Pipeline (SOURCE OF TRUTH)

```text
PDF
→ Python Extractor (PyMuPDF)
→ []Span
→ Lines
→ Blocks (Paragraph, Code, Image, List)
→ Heading Tree
→ Sections
→ Chunks
→ Embeddings
→ Storage
→ Retrieval
```

---

### 5. Detailed Pipeline

#### 5.1 Extraction (Python)

`scripts/extract_pdf.py`

Outputs:

```json
{
  "type": "text | image",
  "text": "...",
  "x": float,
  "y": float,
  "font_size": float,
  "font_name": string,
  "flags": int,
  "page": int
}
```

---

#### 5.2 Spans → Lines

Grouped by:

- page
- Y proximity
- X ordering

```go
type Line struct {
  Text     string
  Spans    []Span
  Y        float64
  Page     int
  XStart   float64
  XEnd     float64
  FontSize float64
  FontName string
  Flags    int
}
```

---

#### 5.3 Lines → Blocks

```go
type Block struct {
  Type    BlockType
  Text    string
  Items   []string
  Page    int
  Y       float64
  Caption string
}
```

Block types:

- Paragraph
- Code
- Image
- List

---

#### 5.4 Implemented Block Intelligence

##### ✅ Paragraph merging

- Same page
- Y proximity
- No cross-page merge (intentional)

##### ✅ Code blocks

- Multi-line preserved
- Based on spacing + structure

##### ✅ Images

- Extracted from PDF
- Position-aware

##### ✅ Caption binding

- "Figure..." detection
- Attached to image block
- Removed from paragraph flow

##### ✅ Lists

- Bullet (`•`)
- Numbered (`1.`)
- Converted to structured list blocks

---

#### 5.5 Heading Detection

- Based on font size relative to body font
- Builds hierarchy

```go
type Node struct {
  Heading  Heading
  Children []*Node
  Blocks   []Block
}
```

---

#### 5.6 Block Attachment

```go
AttachBlocks(tree, blocks)
```

- Blocks assigned to nearest preceding heading
- Sorted by page + Y

---

#### 5.7 Sections

```go
type Section struct {
  Title    string
  Content  []Block
  Level    int
  Page     int
  Children []Section
}
```

---

#### 5.8 Chunking

```go
type Chunk struct {
  Title  string
  Blocks []Block
}
```

Rules:

- Structure-aware
- No splitting:
  - code
  - lists
  - images

```go
ChunkSections(sections, maxBlocks)
```

---

#### 5.9 Rendering (Unified)

```go
RenderBlock(b Block, prefix string) []string
```

Used by:

- PrintSections
- PrintChunks
- ChunkToText

---

### 6. Current Capabilities

#### ✅ Completed

- PDF extraction (text + images)
- Layout reconstruction
- Font analysis
- Heading hierarchy
- Block system
- Code detection
- List detection
- Image + caption handling
- Section building

---

### 7. Project Status

#### ✅ Done

##### internal/layout/

- Full pipeline: spans → lines → fonts → headings → blocks → tree → sections
- Considered **stable**

---

### 🚧 In Progress

#### in progress/awaiting migration from legacy code

- Pending pipeline: chunks → embed → store

##### internal/app/app.go

- To be migrated to use new extraction
  - DB schema bootstrap
  - Document deduplication
  - Chunk storage
  - Embedding
  - Summarization

##### cmd/nexus/query.go

- To be migrated to use new extraction
  - Rendering
  - Query command

##### internal/ingestion/

###### ingest_file.go

- Pipeline wired:

```bash
  extract → layout → chunk
```

- ❌ Store step is stub:

  ```go
  fmt.Println("CHUNK:", c.Title)
  ```

###### cmd/nexus/ingest.go

- Ingestion call commented out
- Waiting for correct function signature

---

### ❌ Not Started

- Proper ingestion → DB wiring
- Batch embedding
- Efficient storage pipeline
- Tests

---

### Completed Pipeline

```text
PDF → extract (Python) → spans → lines → fonts → headings → blocks → tree → sections → chunks → embed → store
```

---

### 8. Known Issues / Stubs

- `ingest_file.go` → store not implemented
- `ingest.go` → ingestion disabled
- `PrintChunks` → debug only
- `go.mod` → version typo (`1.25.4`)
- `config.yaml` → plaintext (acceptable for dev)

---

### 9. Legacy Code (TO DELETE)

```text
internal/parser/                 ❌
internal/ingestion/extract.go    ❌
internal/ingestion/chunker.go    ❌
```

Rule:

```text
If it does NOT use Block → DELETE
```

---

### 10. Next Phase (CRITICAL)

#### Goal

```bash
nexus ingest file.pdf
```

---

#### Implementation Plan

##### Step 1 — Fix ingestion signature

```go
IngestFile(ctx, services, path, source, force)
```

---

##### Step 2 — Replace stub

```go
chunks := layout.ChunkSections(...)
texts := ChunkToText(chunks)
```

---

##### Step 3 — Store

```go
StoreChunks(chunks)
EmbedChunks(chunks)
```

---

##### Step 4 — Batch processing

- Avoid N+1 embedding calls
- Insert in bulk

---

### 11. Retrieval (Working)

```bash
nexus query "question"
```

Pipeline:

```text
Query → embedding → vector search → chunks → LLM → answer
```

---

### 12. Future Work

#### Near-term

- Token-aware chunking
- Cross-page paragraph merging
- Nested list detection

---

#### Mid-term

- Better embedding pipeline
- Query optimization
- Result ranking

---

#### Long-term

- Image understanding (OCR / vision)
- Multi-document linking
- Knowledge graph

---

### 13. Architecture Notes

- `app.go` is a temporary service container

- Will be split into:

  - store
  - embedder
  - summarizer

- No interfaces yet (intentional)

- Global config (`config.C`) is a known compromise

---

### 14. Constraints

- Fully offline capable
- Go-first architecture
- Python only for extraction
- Deterministic processing

---

### 15. Current Progress

```text
[████████████████░░░░] 80%
```

Completed:

- Layout engine
- Structure
- Chunking
- Query

Pending:

- Ingestion
- Storage wiring optimization

---

### 16. Immediate Next Task

👉 Fix ingestion pipeline:

```text
layout → chunks → embed → store
```

Then:

👉 Delete legacy code

---

### 17. Final Vision

nexus becomes:

```text
A local-first system that converts documents into structured, queryable knowledge
```

Capabilities:

- Ask questions about documents
- Retrieve exact context
- Avoid hallucination
- Work fully offline

---

### 18. Handoff Instructions (CRITICAL)

If continuing:

1. DO NOT modify layout pipeline
2. Use existing:

   - Block
   - Section
   - Chunk
3. Implement ingestion next
4. Then optimize storage
5. Then refine retrieval

Priority:

```text
Ingestion > Storage > Optimization
```

---
