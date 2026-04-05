.PHONY: help bootstrap setup setup-python lint build install ingest query layout dev cleanup all

help:
	@echo "nexus Makefile"
	@echo ""
	@echo "  make bootstrap                     → Install development tools (mise)"
	@echo "  make setup                         → Interactive first-time setup (DB + config + models)"
	@echo "  make lint                          → Run golangci-lint"
	@echo "  make build                         → Build binary to ./nexus"
	@echo "  make install                       → Install binary to ~/.local/bin"
	@echo "  make ingest                        → Ingest documents from configured sources"
	@echo "  make ingest force=1                → Force re-ingest (ignore dedup)"
	@echo "  make query question=\"...\"                      → Ask a question against the knowledge base"
	@echo "  make query question=\"...\" source=progit         → Restrict to one source"
	@echo "  make query question=\"...\" model=llama3.1:8b     → Use a specific generation model"
	@echo "  make layout file=<pdf>             → Pipeline summary for a PDF"
	@echo "  make layout file=<pdf> flags=\"--chunks --page-from 1 --page-to 10\""
	@echo "  make cleanup                       → Delete DB, config, and binary (fresh start)"
	@echo ""

bootstrap:
	@echo "=== nexus bootstrap (tools) ==="
	@mise install
	@echo "✅ Tools installed via mise (Go, golangci-lint, postgres, op)"

setup-python:
	@echo "=== Setting up Python environment for PDF extraction ==="
	@python -m venv .venv
	@.venv/bin/pip install pymupdf
	@echo "✅ Python environment ready for PDF extraction"

setup:
	@echo "=== nexus first-time setup ==="

	# Step 0: 1Password (optional but preferred)
	@if command -v op >/dev/null 2>&1; then \
		if op whoami >/dev/null 2>&1; then \
			echo "✅ 1Password: signed in as $$(op whoami --format=json 2>/dev/null | grep email | head -1 || echo 'unknown')"; \
		else \
			echo "🔐 1Password CLI is installed but you are not signed in."; \
			read -p "   Sign in now? Passwords will be stored securely. (Y/n): " do_signin; \
			if [ "$$do_signin" != "n" ] && [ "$$do_signin" != "N" ]; then \
				op signin || echo "   Sign-in failed — continuing without 1Password."; \
			else \
				echo "   Skipping 1Password — you will be prompted for a password manually."; \
			fi; \
		fi; \
	else \
		echo "ℹ️  1Password CLI not installed — you will be prompted for a database password."; \
		echo "   To use 1Password later: brew install --cask 1password-cli"; \
	fi

	# Python environment for PDF extraction
	@python -m venv .venv
	@.venv/bin/pip install --quiet pymupdf
	@echo "✅ Python environment ready"

	# Tool checks
	@if ! command -v ollama >/dev/null 2>&1; then \
		echo "❌ Ollama not installed."; \
		echo "   Install: brew install ollama"; \
		exit 1; \
	fi

	# Ollama — register as a launchd service so it starts on login
	@echo "Starting Ollama as a background service..."
	@brew services start ollama || brew services restart ollama
	@echo "Waiting for Ollama to be ready..."
	@for i in 1 2 3 4 5; do \
		curl -sf http://localhost:11434 >/dev/null 2>&1 && break; \
		echo "  ...waiting ($$i/5)"; \
		sleep 2; \
	done; \
	if ! curl -sf http://localhost:11434 >/dev/null 2>&1; then \
		echo "❌ Ollama did not start in time. Check: brew services info ollama"; \
		exit 1; \
	fi
	@echo "✅ Ollama is running (logs: /opt/homebrew/var/log/ollama.log)"

	# PostgreSQL
	@echo "1. Starting PostgreSQL (TCP mode)..."
	@brew services start postgresql@14 || true
	@sleep 3

	@echo "2. Resolving database password..."
	@if command -v op >/dev/null 2>&1 && op whoami >/dev/null 2>&1; then \
		echo "   Using 1Password..."; \
		PASSWORD=$$(op item get "Local Postgres - opsnexus vaultuser" --fields password --reveal 2>/dev/null); \
		if [ -z "$$PASSWORD" ]; then \
			echo "   Item not found in 1Password — creating a new one..."; \
			PASSWORD=$$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 24); \
			op item create --category login --title "Local Postgres - opsnexus vaultuser" --vault Personal \
				username=vaultuser password="$$PASSWORD" 2>/dev/null || true; \
		fi; \
	else \
		if [ -n "$$PG_PASSWORD" ]; then \
			echo "   Using existing PG_PASSWORD from environment."; \
			PASSWORD=$$PG_PASSWORD; \
		else \
			echo "   1Password not available."; \
			read -p "   Enter a password for vaultuser (or press Enter to generate one): " PASSWORD; \
			[ -z "$$PASSWORD" ] && PASSWORD=$$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 24); \
			echo "   Using password: $$PASSWORD"; \
		fi; \
	fi; \
	echo "$$PASSWORD" > .pgpassword; \
	USER=$$(whoami); \
	echo "   Creating vaultuser role and opsnexus database..."; \
	psql -U $$USER postgres -c \
		"CREATE ROLE vaultuser WITH LOGIN PASSWORD '$$PASSWORD' CREATEDB;" \
		2>/dev/null || echo "   Role already exists — skipping"; \
	createdb -U $$USER -O vaultuser opsnexus 2>/dev/null || echo "   Database already exists — skipping"

	@USER=$$(whoami); \
	echo "3. Creating vector extension as superuser..."; \
	psql -U $$USER -h localhost -d opsnexus -c "CREATE EXTENSION IF NOT EXISTS vector;"; \
	echo "4. Granting permissions to vaultuser..."; \
	psql -U $$USER -h localhost -d opsnexus -c "GRANT ALL ON SCHEMA public TO vaultuser;"

	@echo ""
	@echo "5. Exporting PG_PASSWORD for the Go app..."
	@PASSWORD=$$(cat .pgpassword); \
	if ! grep -q "PG_PASSWORD" ~/.zshrc 2>/dev/null; then \
		echo "export PG_PASSWORD=$$PASSWORD" >> ~/.zshrc; \
		echo "   Added PG_PASSWORD to ~/.zshrc"; \
	else \
		sed -i '' "s|^export PG_PASSWORD=.*|export PG_PASSWORD=$$PASSWORD|" ~/.zshrc; \
		echo "   Updated PG_PASSWORD in ~/.zshrc"; \
	fi; \
	export PG_PASSWORD=$$PASSWORD; \
	rm -f .pgpassword

	# pgvector
	@echo "6. Installing pgvector..."
	@brew install pgvector || true
	@brew services restart postgresql@14
	@sleep 3

	# Reset document tables so migrations recreate them with correct vector dimensions.
	# Safe on first run (DROP IF EXISTS is a no-op). Required on re-setup when the
	# embedding model changes (vector dimension changes from 768 → 1024).
	@echo "7. Resetting document tables (correct vector dimensions)..."
	@USER=$$(whoami); \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS chunks CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS documents CASCADE;" 2>/dev/null || true; \
	echo "   Tables will be recreated with vector(1024) on first run."

	# Ollama models
	@echo "8. Pulling Ollama models (this may take a while)..."
	@echo "   Embedding model — mxbai-embed-large (multilingual, 1024 dims)..."
	@ollama pull mxbai-embed-large
	@echo "   Classification model — qwen2.5:7b (structured JSON output)..."
	@ollama pull qwen2.5:7b
	@echo "   Generation model — llama3.1:8b (query answers)..."
	@ollama pull llama3.1:8b
	@echo "llama3.1:8b" > .ollama_gen_model

	# Personal docs directory
	@echo "9. Creating PersonalDocs directory structure..."
	@mkdir -p \
		$$HOME/Documents/PersonalDocs/financial/banking \
		$$HOME/Documents/PersonalDocs/financial/tax \
		$$HOME/Documents/PersonalDocs/financial/mortgage \
		$$HOME/Documents/PersonalDocs/insurance/health \
		$$HOME/Documents/PersonalDocs/insurance/car \
		$$HOME/Documents/PersonalDocs/insurance/home \
		$$HOME/Documents/PersonalDocs/legal \
		$$HOME/Documents/PersonalDocs/medical \
		$$HOME/Documents/PersonalDocs/utilities \
		$$HOME/Documents/PersonalDocs/correspondence \
		$$HOME/Documents/PersonalDocs/books \
		$$HOME/Documents/PersonalDocs/other
	@echo "   ✅ ~/Documents/PersonalDocs/ ready"

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
	@echo '  dsn: "postgres://vaultuser:$${PG_PASSWORD}@localhost:5432/opsnexus?sslmode=disable"' >> config.yaml
	@GEN_MODEL=$$(cat .ollama_gen_model 2>/dev/null || echo "llama3.1:8b"); \
	echo "ollama:" >> config.yaml; \
	echo "  baseURL: http://localhost:11434" >> config.yaml; \
	echo "  embeddingModel: mxbai-embed-large" >> config.yaml; \
	echo "  generationModel: $$GEN_MODEL" >> config.yaml; \
	echo "  classificationModel: qwen2.5:7b" >> config.yaml
	@echo "personal:" >> config.yaml
	@echo "  watchDirs:" >> config.yaml
	@echo "    - ~/Downloads" >> config.yaml
	@echo "    - ~/Desktop" >> config.yaml
	@echo "  destDir: ~/Documents/PersonalDocs" >> config.yaml
	@echo "relevanceThreshold: 0.70" >> config.yaml
	@echo "logLevel: info" >> config.yaml
	@rm -f .ollama_gen_model

	@echo ""
	@echo "✅ Setup complete!"
	@echo ""
	@echo "Next steps:"
	@echo "   make ingest                                       # ingest your documents"
	@echo "   make query question=\"What is the staging area in Git?\""
	@echo "   make query question=\"...\" model=llama3.1:8b      # use a different model"
	@echo ""
	@echo "Personal docs will be watched in ~/Downloads and ~/Desktop"
	@echo "and organised into ~/Documents/PersonalDocs/ (nexus watch — coming soon)"

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
		echo "Usage: make query question=\"Your question here\" [source=progit] [model=llama3.1:8b]"; \
		exit 1; \
	fi
	@ARGS="$(question)"; \
	[ -n "$(source)" ] && ARGS="--source $(source) $$ARGS"; \
	[ -n "$(model)" ]  && ARGS="--model $(model) $$ARGS"; \
	go run . query $$ARGS

layout:
	@if [ -z "$(file)" ]; then \
		echo "Usage: make layout file=<path-to-pdf> [flags=\"--chunks --page-from 1 --page-to 10\"]"; \
		exit 1; \
	fi
	go run . layout $(flags) "$(file)"

dev:
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

	@echo "Stopping Ollama service..."
	@brew services stop ollama 2>/dev/null || true

	@echo "Removing Ollama models..."
	@ollama rm mxbai-embed-large 2>/dev/null || true
	@ollama rm qwen2.5:7b 2>/dev/null || true
	@ollama rm llama3.1:8b 2>/dev/null || true

	@echo "✅ Cleanup complete. You can now run 'make setup' for a fresh start."

all: lint build