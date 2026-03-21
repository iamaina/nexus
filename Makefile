.PHONY: help setup lint build install ingest query dev clean

help:
	@echo "nexus Makefile"
	@echo ""
	@echo "  make bootstrap     → Install development tools"
	@echo "  make setup         → Interactive first-time setup"
	@echo "  make lint          → Run golangci-lint"
	@echo "  make build         → Build binary"
	@echo "  make install       → Install to ~/.local/bin"
	@echo "  make ingest        → Run ingestion"
	@echo "  make query     → Query the vault"
	@echo "  make dev       → Switch to dev + lint"
	@echo ""

bootstrap:
	@echo "=== nexus bootstrap (tools) ==="
	@mise install
	@echo "✅ Tools installed via mise (Go, golangci-lint, postgres, op)"

setup:
	@echo "=== nexus first-time setup ==="

	# Tool checks
	@if ! command -v op >/dev/null 2>&1; then \
		echo "❌ 1Password CLI (op) not found."; \
		echo "   Install: brew install --cask 1password-cli"; \
		echo "   Docs: https://developer.1password.com/docs/cli"; \
		exit 1; \
	fi

	@if ! op whoami >/dev/null 2>&1; then \
		echo "❌ Not signed in to 1Password."; \
		echo "   Run: eval \"\$$(op signin)\""; \
		exit 1; \
	fi

	@if ! command -v ollama >/dev/null 2>&1; then \
		echo "❌ Ollama not installed."; \
		echo "   Install: brew install ollama"; \
		exit 1; \
	fi

	# Ollama server check
	@if ! pgrep -x ollama >/dev/null 2>&1; then \
		echo "❌ Ollama server is not running."; \
		echo "   Open a NEW terminal and run: ollama serve"; \
		echo "   Leave it running."; \
		read -p "Is Ollama server running now? (Y/N): " confirm; \
		if [ "$$confirm" != "Y" ] && [ "$$confirm" != "y" ]; then \
			echo "❌ Please start Ollama and run 'make setup' again."; \
			exit 1; \
		fi; \
	fi

	# PostgreSQL
	@echo "1. Starting PostgreSQL (TCP mode)..."
	@brew services start postgresql@14 || true
	@sleep 3

	@USER=$$(whoami); \
	echo "2. Creating vaultuser role and opsnexus database..."; \
	psql -U $$USER postgres -c \
		"CREATE ROLE vaultuser WITH LOGIN PASSWORD '$$(op item get "Local Postgres - opsnexus vaultuser" --fields password --reveal)' CREATEDB;" \
		2>/dev/null || echo "Role already exists"; \
	createdb -U $$USER -O vaultuser opsnexus 2>/dev/null || echo "Database already exists"

	@USER=$$(whoami); \
	echo "3. Creating vector extension as superuser..."; \
	psql -U $$USER -h localhost -d opsnexus -c "CREATE EXTENSION IF NOT EXISTS vector;"; \
	echo "4. Granting permissions to vaultuser..."; \
	psql -U $$USER -h localhost -d opsnexus -c "GRANT ALL ON SCHEMA public TO vaultuser;"

	@echo ""
	@echo "5. IMPORTANT: Exporting PG_PASSWORD(for Go code)..."
	@PASSWORD=$$(op item get "Local Postgres - opsnexus vaultuser" --fields password --reveal); \
	if ! grep -q "PG_PASSWORD" ~/.zshrc 2>/dev/null; then \
		echo "export PG_PASSWORD=$$PASSWORD" >> ~/.zshrc; \
		echo "   Added to ~/.zshrc"; \
	else \
		echo "   PG_PASSWORD already in ~/.zshrc"; \
	fi; \
	export PG_PASSWORD=$$PASSWORD

	# pgvector
	@echo "6. Installing pgvector..."
	@brew install pgvector || true
	@brew services restart postgresql@14
	@sleep 3

	# Ollama models
	@echo "7. Pulling Ollama models..."
	@ollama pull nomic-embed-text
	@ollama pull llama3.2

	# Interactive sources with defaults
	@echo ""
	@echo "=== Add your knowledge sources ==="
	@echo "Press Enter to accept defaults or enter custom paths."
	@echo ""

	@rm -f config.yaml 2>/dev/null || true
	@echo "sources:" > config.yaml

#  we should replace the Default 1 and Default 2 with the loop below, for now we
#  can just do 2 defaults to make it easier to test.
#
#  @i=1; \
# 	while true; do \
# 		read -p "Path $$i: " path; \
# 		if [ -z "$$path" ]; then break; fi; \
# 		read -p "Name (default: source$$i): " name; \
# 		[ -z "$$name" ] && name="source$$i"; \
# 		echo "  - name: $$name" >> config.yaml; \
# 		echo "    path: $$path" >> config.yaml; \
# 		echo "    extensions:" >> config.yaml; \
# 		echo "      - .pdf" >> config.yaml; \
# 		echo "      - .md" >> config.yaml; \
# 		echo "      - .txt" >> config.yaml; \
# 		i=$$((i+1)); \
# 	done

	# Default 1: books
	read -p "Books folder [~/Documents/knowledge-drop]: " books_path; \
	[ -z "$$books_path" ] && books_path="$$HOME/Documents/knowledge-drop"; \
	echo "  - name: books" >> config.yaml; \
	echo "    path: $$books_path" >> config.yaml; \
	echo "    extensions:" >> config.yaml; \
	echo "      - .pdf" >> config.yaml; \
	echo "      - .md" >> config.yaml; \
	echo "      - .txt" >> config.yaml

	# Default 2: intelligence
	read -p "Intelligence folder [~/ops-nexus/intelligence]: " intel_path; \
	[ -z "$$intel_path" ] && intel_path="$$HOME/ops-nexus/intelligence"; \
	echo "  - name: intelligence" >> config.yaml; \
	echo "    path: $$intel_path" >> config.yaml; \
	echo "    extensions:" >> config.yaml; \
	echo "      - .pdf" >> config.yaml; \
	echo "      - .md" >> config.yaml; \
	echo "      - .txt" >> config.yaml

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
	@if [ "$(force)" = "1" ]; then \
		go run . ingest --force; \
	else \
		go run . ingest; \
	fi

query:
	@if [ -z "$(question)" ]; then \
		echo "Usage: make query question=\"Your question here\""; \
		exit 1; \
	fi
	go run . query "$(question)"

dev:
	git checkout dev
	mise run lint

cleanup:
	@echo "=== nexus full cleanup ==="
	@echo "This will delete the database, role, config, and Ollama models."
	@read -p "Are you sure? (Y/N): " confirm; \
	if [ "$$confirm" != "Y" ] && [ "$$confirm" != "y" ]; then \
		echo "Cancelled."; exit 1; \
	fi

	@echo "Dropping database, role, and tables..."
	@psql -U $$(whoami) postgres -c "DROP DATABASE IF EXISTS opsnexus;" 2>/dev/null || true
	@psql -U $$(whoami) postgres -c "DROP ROLE IF EXISTS vaultuser;" 2>/dev/null || true

	@echo "Removing local files..."
	@rm -f ~/.local/bin/nexus
	@rm -f config.yaml

	@echo "Removing Ollama models..."
	@ollama rm nomic-embed-text 2>/dev/null || true
	@ollama rm llama3.2 2>/dev/null || true

	@echo "✅ Cleanup complete. You can now run 'make setup' for a fresh start."

all: lint build