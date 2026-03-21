.PHONY: help setup lint build install ingest query dev clean

help:
	@echo "nexus Makefile"
	@echo ""
	@echo "  make setup     → Interactive first-time setup"
	@echo "  make lint      → Run golangci-lint"
	@echo "  make build     → Build binary"
	@echo "  make install   → Install to ~/.local/bin"
	@echo "  make ingest    → Run ingestion"
	@echo "  make query     → Query the vault"
	@echo "  make dev       → Switch to dev + lint"
	@echo ""

bootstrap:
	@echo "=== nexus bootstrap (tools) ==="
	@mise install
	@echo "✅ Tools installed via mise (Go, golangci-lint, postgres, op)"

setup:
	@echo "=== nexus first-time setup ==="

	@mise install

	# 1Password CLI check
	@if ! command -v op >/dev/null 2>&1; then \
		echo "❌ 1Password CLI (op) is not installed."; \
		echo "   Install with: brew install --cask 1password-cli"; \
		echo "   Full documentation: https://developer.1password.com/docs/cli"; \
		exit 1; \
	fi

	# Sign in check
	@if ! op whoami >/dev/null 2>&1; then \
		echo "❌ You are not signed in to 1Password."; \
		echo "   Please run this command first:"; \
		echo "   eval \"\$$(op signin)\""; \
		echo "   Then run 'make setup' again."; \
		exit 1; \
	fi
	
	# Ollama check
	@if ! command -v ollama >/dev/null 2>&1; then \
		echo "❌ Ollama is not installed."; \
		echo "   Install with: brew install ollama"; \
		exit 1; \
	fi

	# Ollama server interactive check
	@echo ""
	@echo "IMPORTANT: Ollama server must be running."
	@echo "   Open a NEW terminal window and run:"
	@echo "   ollama serve"
	@echo "   Leave it running in the background."
	@echo ""
	@read -p "Is the Ollama server running? (Y/N): " confirm; \
	if [ "$$confirm" != "Y" ] && [ "$$confirm" != "y" ]; then \
		echo "❌ Please start Ollama server first and run 'make setup' again."; \
		exit 1; \
	fi

	# PostgreSQL
	@echo "1. Starting PostgreSQL..."
	@brew services start postgresql@14 || true

	@USER=$$(whoami); \
	echo "2. Creating vaultuser role and opsnexus database..."; \
	psql -U $$USER postgres -c "CREATE ROLE vaultuser WITH LOGIN PASSWORD '$$(op item get "Local Postgres - opsnexus vaultuser" --fields password --reveal)' CREATEDB;" 2>/dev/null || echo "Role already exists"; \
	createdb -U $$USER -O vaultuser opsnexus 2>/dev/null || echo "Database already exists"

	@echo ""
	@echo "IMPORTANT: Export your password for the Go code to connect:"
	@echo "   export PG_PASSWORD=\$$(op item get \"Local Postgres - opsnexus vaultuser\" --fields password --reveal)"
	@echo "   Add this line to your ~/.zshrc for convenience."
	@echo ""

	# pgvector
	@echo "3. Installing pgvector..."
	@brew install pgvector || true
	@brew services restart postgresql@14

	@echo "4. Enabling vector extension..."
	@psql -U vaultuser -d opsnexus -c "CREATE EXTENSION IF NOT EXISTS vector;" || echo "Extension already enabled"

	# Ollama models
	@echo "5. Pulling Ollama models..."
	@ollama pull nomic-embed-text
	@ollama pull llama3.2

	# Interactive sources setup
	@echo ""
	@echo "=== Add your knowledge sources ==="
	@echo "Enter the full paths to your document folders (one per line)."
	@echo "Press Enter on an empty line when done."
	@echo ""

	@rm -f config.yaml 2>/dev/null || true
	@echo "sources:" > config.yaml

	@i=1; \
	while true; do \
		read -p "Path $$i: " path; \
		if [ -z "$$path" ]; then break; fi; \
		read -p "Name for this source (default: source$$i): " name; \
		[ -z "$$name" ] && name="source$$i"; \
		echo "  - name: $$name" >> config.yaml; \
		echo "    path: $$path" >> config.yaml; \
		echo "    extensions:" >> config.yaml; \
		echo "      - .pdf" >> config.yaml; \
		echo "      - .md" >> config.yaml; \
		echo "      - .txt" >> config.yaml; \
		i=$$((i+1)); \
	done

	@echo ""
	@echo "postgres:" >> config.yaml
	@echo '  dsn: "postgres://vaultuser:${PG_PASSWORD}@localhost:5432/opsnexus?sslmode=disable"' >> config.yaml
	@echo "relevanceThreshold: 0.65" >> config.yaml

	@echo ""
	@echo "✅ Setup complete!"
	@echo "Next steps:"
	@echo "   make ingest          # ingest your documents"
	@echo "   make query question=\"What is the staging area in Git?\""

lint:
	mise run lint

build:
	go build -o nexus ./cmd/nexus
	@echo "✅ Built nexus binary"

install:
	go build -o ~/.local/bin/nexus ./cmd/nexus
	@echo "✅ nexus installed to ~/.local/bin"

ingest:
	go run . ingest

query:
	@if [ -z "$(question)" ]; then \
		echo "Usage: make query question=\"Your question here\""; \
		exit 1; \
	fi
	go run . query "$(question)"

dev:
	git checkout dev
	mise run lint

clean:
	rm -f nexus
	@echo "Cleaned build artifacts"

all: lint build