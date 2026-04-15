.PHONY: help bootstrap setup setup-python reset-db lint build install ingest query layout dev cleanup all test watch-install watch-uninstall watch-restart

help:
	@echo "nexus Makefile"
	@echo ""
	@echo "  make bootstrap                     → Install all dependencies (mise tools from .mise.toml + brew services)"
	@echo "  make setup                         → First-time setup (idempotent — safe to re-run)"
	@echo "  make setup reconfigure=1           → Re-run setup and overwrite config.yaml"
	@echo "  make reset-db                      → DROP all tables and re-run migrations (loses all ingested data)"
	@echo "  make lint                          → Run golangci-lint"
	@echo "  make build                         → Build binary to ./nexus"
	@echo "  make install                       → Install binary to ~/.local/bin"
	@echo "  make ingest                        → Ingest documents (skips unchanged files)"
	@echo "  make ingest force=1                → Force re-ingest (ignore dedup)"
	@echo "  make query question=\"...\"                      → Ask a question against the knowledge base"
	@echo "  make query question=\"...\" source=progit         → Restrict to one source"
	@echo "  make query question=\"...\" model=llama3.2:3b     → Use a specific generation model"
	@echo "  make layout file=<pdf>             → Pipeline summary for a PDF"
	@echo "  make layout file=<pdf> flags=\"--chunks --page-from 1 --page-to 10\""
	@echo "  make watch-install                 → Install nexus watch as a launchd background service"
	@echo "  make watch-restart                 → Restart the background service (e.g. after make install)"
	@echo "  make watch-uninstall               → Stop and remove the background service"
	@echo "  make cleanup                       → Delete DB, config, binary, and background service (fresh start)"
	@echo ""

bootstrap:
	@echo "=== nexus bootstrap ==="
	@echo ""
	@echo "Tool versions are pinned in .mise.toml — update there to upgrade."
	@echo ""

	@# mise — single source of truth for all tools (Go, golangci-lint, op, Python, jq)
	@if ! command -v mise >/dev/null 2>&1; then \
		echo "mise not found. Install it first:"; \
		echo "  brew install mise   (or see https://mise.jdx.dev)"; \
		exit 1; \
	fi
	@mise install
	@echo "✅ Tools installed via mise (see .mise.toml for pinned versions)"

	@# Services — PostgreSQL and Ollama are system services managed by brew.
	@# pgvector must match the installed PostgreSQL version so brew manages it too.
	@echo "Checking services (PostgreSQL, Ollama, pgvector)..."
	@for pkg in postgresql@14 ollama pgvector; do \
		if ! brew list $$pkg >/dev/null 2>&1; then \
			echo "  Installing $$pkg via brew..."; \
			brew install $$pkg; \
		else \
			echo "  ✅ $$pkg already installed"; \
		fi; \
	done

	@# Python env for PDF extraction (uses mise-managed Python)
	@echo "Setting up Python environment for PDF extraction..."
	@mise exec -- python3 -m venv .venv
	@.venv/bin/pip install --quiet pymupdf
	@echo "✅ Python environment ready (.venv)"

	@echo ""
	@echo "✅ Bootstrap complete. Next: make setup"

setup-python:
	@echo "=== Setting up Python environment for PDF extraction ==="
	@python3 -m venv .venv
	@.venv/bin/pip install pymupdf
	@echo "✅ Python environment ready for PDF extraction"

reset-db:
	@echo "=== nexus reset-db — WARNING: this deletes all ingested data ==="
	@read -p "Are you sure? This drops chunks, documents, and context_sources. (Y/N): " confirm; \
	if [ "$$confirm" != "Y" ] && [ "$$confirm" != "y" ]; then \
		echo "Cancelled."; exit 1; \
	fi
	@USER=$$(whoami); \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS chunks CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS documents CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS context_sources CASCADE;" 2>/dev/null || true
	@echo "✅ Tables dropped. Run 'make ingest' after nexus restarts to re-populate."

setup:
	@echo "=== nexus setup (idempotent) ==="

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
	@python3 -m venv .venv
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
	@echo "   Waiting for PostgreSQL to be ready..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		pg_isready -h localhost -p 5432 -q && break; \
		echo "   ($$i/10) not ready yet, retrying in 2s..."; \
		sleep 2; \
	done; \
	pg_isready -h localhost -p 5432 -q || { echo "❌ PostgreSQL did not start. Check: brew services info postgresql@14"; exit 1; }

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
	psql -h localhost -U $$USER postgres -c \
		"CREATE ROLE vaultuser WITH LOGIN PASSWORD '$$PASSWORD' CREATEDB;" \
		2>/dev/null || echo "   Role already exists — skipping"; \
	createdb -h localhost -U $$USER -O vaultuser opsnexus 2>/dev/null || echo "   Database already exists — skipping"

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
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		pg_isready -h localhost -p 5432 -q && break; \
		echo "   ($$i/10) not ready yet, retrying in 2s..."; \
		sleep 2; \
	done; \
	pg_isready -h localhost -p 5432 -q || { echo "❌ PostgreSQL did not restart. Check: brew services info postgresql@14"; exit 1; }

	# Ollama models (ollama pull is idempotent — skips if already downloaded)
	@echo "7. Pulling Ollama models (skipped if already present)..."
	@echo "   Embedding model — mxbai-embed-large (multilingual, 1024 dims, ~670MB)..."
	@ollama pull mxbai-embed-large
	@echo "   Classification model — qwen2.5:3b (structured JSON output, ~1.9GB)..."
	@ollama pull qwen2.5:3b
	@echo "   Generation model — llama3.2:3b (query answers, ~2.0GB)..."
	@ollama pull llama3.2:3b
	@echo "llama3.2:3b" > .ollama_gen_model

	# Personal docs directory (mkdir -p is idempotent)
	@echo "8. Creating PersonalDocs directory structure..."
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

	# Config — skip if already exists unless reconfigure=1
	@echo ""
	@if [ -f config.yaml ] && [ "$(reconfigure)" != "1" ]; then \
		echo "✅ config.yaml already exists — skipping (run 'make setup reconfigure=1' to overwrite)."; \
	else \
		echo "=== Configure knowledge sources ==="; \
		echo "Press Enter to accept defaults or enter custom paths."; \
		echo ""; \
		rm -f config.yaml 2>/dev/null || true; \
		echo "sources:" > config.yaml; \
		read -p "Books folder [~/Documents/knowledge-drop]: " books_path; \
		[ -z "$$books_path" ] && books_path="$$HOME/Documents/knowledge-drop"; \
		echo "  - name: books" >> config.yaml; \
		echo "    path: $$books_path" >> config.yaml; \
		echo "    extensions:" >> config.yaml; \
		echo "      - .pdf" >> config.yaml; \
		echo "      - .md" >> config.yaml; \
		echo "      - .txt" >> config.yaml; \
		read -p "Intelligence folder [~/ops-nexus/intelligence]: " intel_path; \
		[ -z "$$intel_path" ] && intel_path="$$HOME/ops-nexus/intelligence"; \
		echo "  - name: intelligence" >> config.yaml; \
		echo "    path: $$intel_path" >> config.yaml; \
		echo "    extensions:" >> config.yaml; \
		echo "      - .pdf" >> config.yaml; \
		echo "      - .md" >> config.yaml; \
		echo "      - .txt" >> config.yaml; \
		read -p "Active ops / notes folder [~/ops-nexus/active-ops]: " ops_path; \
		[ -z "$$ops_path" ] && ops_path="$$HOME/ops-nexus/active-ops"; \
		echo "  - name: ops-notes" >> config.yaml; \
		echo "    path: $$ops_path" >> config.yaml; \
		echo "    extensions:" >> config.yaml; \
		echo "      - .md" >> config.yaml; \
		echo "      - .txt" >> config.yaml; \
		read -p "Subdirectories to exclude from ops-notes (comma-separated, e.g. GitLab_runbooks) [skip]: " ops_excludes; \
		if [ -n "$$ops_excludes" ]; then \
			echo "    exclude:" >> config.yaml; \
			echo "$$ops_excludes" | tr ',' '\n' | while IFS= read -r excl; do \
				excl=$$(echo "$$excl" | xargs); \
				[ -n "$$excl" ] && echo "      - $$excl" >> config.yaml; \
			done; \
		fi; \
		read -p "Runbooks folder path (leave blank to skip): " runbooks_path; \
		if [ -n "$$runbooks_path" ]; then \
			echo "  - name: runbooks" >> config.yaml; \
			echo "    path: $$runbooks_path" >> config.yaml; \
			echo "    extensions:" >> config.yaml; \
			echo "      - .md" >> config.yaml; \
			echo "      - .txt" >> config.yaml; \
		fi; \
		echo "" >> config.yaml; \
		echo "postgres:" >> config.yaml; \
		echo '  dsn: "postgres://vaultuser:$${PG_PASSWORD}@localhost:5432/opsnexus?sslmode=disable"' >> config.yaml; \
		GEN_MODEL=$$(cat .ollama_gen_model 2>/dev/null || echo "llama3.2:3b"); \
		echo "ollama:" >> config.yaml; \
		echo "  baseURL: http://localhost:11434" >> config.yaml; \
		echo "  embeddingModel: mxbai-embed-large" >> config.yaml; \
		echo "  generationModel: $$GEN_MODEL" >> config.yaml; \
		echo "  classificationModel: qwen2.5:3b" >> config.yaml; \
		echo "personal:" >> config.yaml; \
		echo "  watchDirs:" >> config.yaml; \
		echo "    - ~/Downloads" >> config.yaml; \
		echo "    - ~/Desktop" >> config.yaml; \
		echo "  destDir: ~/Documents/PersonalDocs" >> config.yaml; \
		echo "" >> config.yaml; \
		echo "# Workspace OS — roots tell nexus about your full directory structure." >> config.yaml; \
		echo "# Leave blank to skip (you can add this section manually later)." >> config.yaml; \
		read -p "Workspace root (e.g. ~/ops-nexus) [skip]: " workspace_root; \
		if [ -n "$$workspace_root" ]; then \
			echo "roots:" >> config.yaml; \
			echo "  workspace: $$workspace_root" >> config.yaml; \
			echo "  repos:" >> config.yaml; \
			read -p "  Work repos path (e.g. ~/ops-nexus/active-ops/gitlab-work) [skip]: " work_repos; \
			if [ -n "$$work_repos" ]; then \
				mkdir -p "$$(eval echo $$work_repos)"; \
				echo "  Tip: 'gitlab' matches gitlab.com, ops.gitlab.net, dev.gitlab.org, etc."; \
				read -p "  Work git host substring(s), comma-separated [gitlab]: " work_hosts_raw; \
				[ -z "$$work_hosts_raw" ] && work_hosts_raw="gitlab"; \
				echo "    - name: work" >> config.yaml; \
				echo "      path: $$work_repos" >> config.yaml; \
				echo "      hosts:" >> config.yaml; \
				echo "$$work_hosts_raw" | tr ',' '\n' | while IFS= read -r h; do \
					h=$$(echo "$$h" | xargs); \
					[ -n "$$h" ] && echo "        - $$h" >> config.yaml; \
				done; \
				echo "      # no groups — work root is the fallback for all unmatched gitlab repos" >> config.yaml; \
				echo "      watch: true" >> config.yaml; \
			fi; \
			read -p "  Personal GitHub repos path (e.g. ~/ops-nexus/repos/personal/github) [skip]: " gh_repos; \
			if [ -n "$$gh_repos" ]; then \
				mkdir -p "$$(eval echo $$gh_repos)"; \
				read -p "  Your GitHub username (used to identify your repos): " gh_user; \
				echo "    - name: personal-github" >> config.yaml; \
				echo "      path: $$gh_repos" >> config.yaml; \
				echo "      hosts: [github.com]" >> config.yaml; \
				[ -n "$$gh_user" ] && echo "      groups: [$$gh_user]" >> config.yaml; \
				echo "      watch: true" >> config.yaml; \
			fi; \
			read -p "  Personal GitLab repos path (e.g. ~/ops-nexus/repos/personal/gitlab) [skip]: " gl_repos; \
			if [ -n "$$gl_repos" ]; then \
				mkdir -p "$$(eval echo $$gl_repos)"; \
				read -p "  Your GitLab username (used to identify your repos): " gl_user; \
				echo "    - name: personal-gitlab" >> config.yaml; \
				echo "      path: $$gl_repos" >> config.yaml; \
				echo "      hosts: [gitlab.com]" >> config.yaml; \
				[ -n "$$gl_user" ] && echo "      groups: [$$gl_user]" >> config.yaml; \
				echo "      watch: true" >> config.yaml; \
			fi; \
		fi; \
		echo "" >> config.yaml; \
		echo "relevanceThreshold: 0.70" >> config.yaml; \
		echo "logLevel: info" >> config.yaml; \
		rm -f .ollama_gen_model; \
		echo "✅ config.yaml written."; \
	fi

	@echo ""
	@echo "✅ Setup complete!"
	@echo ""
	@echo "Next steps:"
	@echo "   make ingest                                         # ingest your documents (skips unchanged files)"
	@echo "   make query question=\"What is the staging area in Git?\""
	@echo "   make query question=\"...\" model=llama3.2:3b        # use a different model"
	@echo "   nexus watch                                         # auto-file new documents from ~/Downloads"
	@echo ""
	@echo "To reset ingested data (e.g. after changing embedding model): make reset-db"

test:
	go test ./...

lint:
	mise run lint

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/iamaina/nexus/cmd/nexus.buildVersion=$(VERSION)"

build:
	go build $(LDFLAGS) -o nexus .
	@echo "✅ Built nexus $(VERSION)"

install:
	go build $(LDFLAGS) -o ~/.local/bin/nexus .
	@echo "✅ nexus $(VERSION) installed to ~/.local/bin"
	@~/.local/bin/nexus completion zsh > "$$(brew --prefix)/share/zsh/site-functions/_nexus"
	@echo "✅ Zsh completion installed — run: exec zsh"

ingest:
	@if [ "$(force)" = "1" ]; then \
		go run . ingest --force; \
	else \
		go run . ingest; \
	fi

query:
	@if [ -z "$(question)" ]; then \
		echo "Usage: make query question=\"Your question here\" [source=progit] [model=llama3.2:3b]"; \
		exit 1; \
	fi
	@FLAGS=""; \
	[ -n "$(source)" ] && FLAGS="$$FLAGS --source $(source)"; \
	[ -n "$(model)" ]  && FLAGS="$$FLAGS --model $(model)"; \
	go run . query $$FLAGS "$(question)"

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

	@echo "Stopping nexus watch service (if installed)..."
	@launchctl bootout gui/$$(id -u)/$(WATCH_PLIST_LABEL) 2>/dev/null || true
	@rm -f "$(WATCH_PLIST_FILE)"

	@echo "Stopping Ollama service..."
	@brew services stop ollama 2>/dev/null || true

	@echo "Removing Ollama models..."
	@ollama rm mxbai-embed-large 2>/dev/null || true
	@ollama rm qwen2.5:7b 2>/dev/null || true
	@ollama rm llama3.2:3b 2>/dev/null || true

	@echo "✅ Cleanup complete. You can now run 'make setup' for a fresh start."

WATCH_PLIST_LABEL := com.nexus.watch
WATCH_PLIST_FILE  := $(HOME)/Library/LaunchAgents/$(WATCH_PLIST_LABEL).plist
WATCH_LOG         := $(HOME)/Library/Logs/nexus-watch.log

watch-install:
	@echo "=== Installing nexus watch as a launchd background service ==="
	@if [ ! -f "$(HOME)/.local/bin/nexus" ]; then \
		echo "❌ nexus not installed — run 'make install' first."; exit 1; \
	fi
	@if [ -z "$$PG_PASSWORD" ]; then \
		echo "❌ PG_PASSWORD not set — run: source ~/.zshrc"; exit 1; \
	fi
	@mkdir -p "$(HOME)/Library/Logs"
	@{ \
	echo '<?xml version="1.0" encoding="UTF-8"?>'; \
	echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">'; \
	echo '<plist version="1.0">'; \
	echo '<dict>'; \
	echo '    <key>Label</key>'; \
	echo '    <string>$(WATCH_PLIST_LABEL)</string>'; \
	echo '    <key>ProgramArguments</key>'; \
	echo '    <array>'; \
	echo '        <string>$(HOME)/.local/bin/nexus</string>'; \
	echo '        <string>watch</string>'; \
	echo '    </array>'; \
	echo '    <key>RunAtLoad</key><true/>'; \
	echo '    <key>KeepAlive</key><true/>'; \
	echo '    <key>StandardOutPath</key>'; \
	echo '    <string>$(WATCH_LOG)</string>'; \
	echo '    <key>StandardErrorPath</key>'; \
	echo '    <string>$(WATCH_LOG)</string>'; \
	echo '    <key>EnvironmentVariables</key>'; \
	echo '    <dict>'; \
	echo '        <key>PATH</key>'; \
	echo '        <string>$(HOME)/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>'; \
	echo '        <key>HOME</key><string>$(HOME)</string>'; \
	printf '        <key>PG_PASSWORD</key><string>%s</string>\n' "$$PG_PASSWORD"; \
	echo '        <key>NEXUS_LOG_LEVEL</key><string>warn</string>'; \
	echo '    </dict>'; \
	echo '    <key>ThrottleInterval</key><integer>5</integer>'; \
	echo '</dict>'; \
	echo '</plist>'; \
	} > "$(WATCH_PLIST_FILE)"
	@launchctl bootout gui/$$(id -u)/$(WATCH_PLIST_LABEL) 2>/dev/null || true
	@launchctl bootstrap gui/$$(id -u) "$(WATCH_PLIST_FILE)"
	@echo "✅ nexus watch is running in the background"
	@echo "   Logs:    tail -f $(WATCH_LOG)"
	@echo "   Restart: make watch-restart  (run after make install to pick up a new binary)"
	@echo "   Remove:  make watch-uninstall"

watch-uninstall:
	@launchctl bootout gui/$$(id -u)/$(WATCH_PLIST_LABEL) 2>/dev/null || true
	@rm -f "$(WATCH_PLIST_FILE)"
	@echo "✅ nexus watch service removed"

watch-restart:
	@if [ ! -f "$(WATCH_PLIST_FILE)" ]; then \
		echo "❌ Service not installed — run 'make watch-install' first."; exit 1; \
	fi
	@launchctl bootout gui/$$(id -u)/$(WATCH_PLIST_LABEL) 2>/dev/null || true
	@launchctl bootstrap gui/$$(id -u) "$(WATCH_PLIST_FILE)"
	@echo "✅ nexus watch restarted"
	@echo "   Logs: tail -f $(WATCH_LOG)"

all: lint build