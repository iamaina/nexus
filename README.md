# nexus

Personal local knowledge vault with RAG (Retrieval-Augmented Generation).

Built for SREs and power users who want full control over their knowledge base — no cloud, no subscriptions, no data leaving your machine.

## Features

- Ingestion from PDFs, Markdown, and plain text
- Automatic deduplication
- Local embeddings using `nomic-embed-text`
- Semantic search + summarization with `llama3.2`
- Fully offline after initial setup

---

## Prerequisites

- macOS (Homebrew)
- [Ollama](https://ollama.com) (for embeddings and summarization)
- PostgreSQL 14 (via Homebrew)

---

## 1. Install Ollama

```bash
# Install Ollama
brew install ollama

# Start the service
brew services start ollama

# Pull required models
ollama pull nomic-embed-text
ollama pull llama3.2
```

Keep `ollama serve` running in the background. (This can be automated)

## 2. Setup PostgreSQL + pgvector

### A. Create Database and User

```Bash
brew install postgresql
brew services restart postgresql
psql -U <root_username> postgres -c "CREATE ROLE <preferred_username> WITH LOGIN PASSWORD 'password' CREATEDB;"
createdb -U anganga -O <your user> opsnexus
```

### B. Install pgvector

```bash
brew install pgvector
brew services restart postgresql
```

#### Then enable the extension

```bash
psql -U preferred_username -d opsnexus -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

### If you are stuck on PostgreSQL@14 (common case)

you can get this error:

```text
ERROR: could not access file "$libdir/vector": No such file or directory
```

This happens because `brew install pgvector` builds against whatever PostgreSQL version Homebrew considers “current”

So:

- `brew` installs `vector.so` into something like:

    `/opt/homebrew/Cellar/postgresql@17/...`

- But your Postgres@14 is 14, is in:

  `/opt/homebrew/Cellar/postgresql@14/...`

#### Use the manual installation method

```Bash
git clone https://github.com/pgvector/pgvector.git
cd pgvector

# Important: Use your PostgreSQL@14 path
export PATH="/opt/homebrew/opt/postgresql@14/bin:$PATH"

make
sudo make install

brew services restart postgresql@14

# Then enable the extension:
SQLCREATE EXTENSION IF NOT EXISTS vector;
```

## 3. Install nexus

```bash
git clone https://github.com/iamaina/nexus.git
cd nexus

go build -o ~/.local/bin/nexus ./...
```

Add to PATH (if not already):

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

## 4. Configuration

Copy the example config:

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` with your paths.

## 5. First Ingestion

```Bash
nexus ingest
```

Use `--force` to re-process everything:

```Bash
nexus ingest --force
```

## Basic Usage

```Bash
nexus query "What is the staging area in Git?"
nexus query "How does Git store commits as snapshots?" --threshold 0.7
```
