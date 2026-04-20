.PHONY: help bootstrap setup setup-python reset-db lint build install ingest query layout dev cleanup all test watch-install watch-uninstall watch-restart

help:
	@echo "nexus Makefile"
	@echo ""
	@echo "First-time setup (run in order):"
	@echo "  make bootstrap                     → Install all dependencies (mise tools + brew services + Python venv)"
	@echo "  make setup                         → Configure database, pull AI models, write config.yaml"
	@echo "  make install                       → Build and install nexus to ~/.local/bin"
	@echo "  make ingest                        → Index your documents (run after setup)"
	@echo ""
	@echo "Day-to-day:"
	@echo "  nexus                              → Start an interactive chat session"
	@echo "  nexus --resume <session>           → Continue a saved session (tab-complete)"
	@echo "  make ingest force=1                → Force re-ingest (ignore dedup)"
	@echo "  make query question=\"...\"          → Non-interactive one-off query (for scripts)"
	@echo "  make query question=\"...\" source=progit"
	@echo "  make query question=\"...\" model=llama3.1:8b"
	@echo ""
	@echo "Maintenance:"
	@echo "  make setup reconfigure=1           → Re-run setup and overwrite config.yaml"
	@echo "  make reset-db                      → DROP all tables (loses all ingested data)"
	@echo "  make lint                          → Run golangci-lint"
	@echo "  make build                         → Build binary to ./nexus"
	@echo "  make layout file=<pdf>             → Debug the layout pipeline on a PDF"
	@echo "  make layout file=<pdf> flags=\"--chunks --page-from 1 --page-to 10\""
	@echo "  make watch-install                 → Install nexus watch as a launchd background service"
	@echo "  make watch-restart                 → Restart the background service (after make install)"
	@echo "  make watch-uninstall               → Stop and remove the background service"
	@echo "  make cleanup                       → Delete DB, config, binary, service (fresh start)"
	@echo ""
	@echo "Feedback / issues: https://github.com/iamaina/nexus/issues"
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

	@# Python env for PDF extraction and Google Docs integration (uses mise-managed Python)
	@echo "Setting up Python environment..."
	@mise exec -- python3 -m venv .venv
	@.venv/bin/pip install --quiet pymupdf google-auth-oauthlib google-api-python-client
	@echo "✅ Python environment ready (.venv)"

	@echo ""
	@echo "✅ Bootstrap complete. Next: make setup"

setup-python:
	@echo "=== Setting up Python environment ==="
	@python3 -m venv .venv
	@.venv/bin/pip install pymupdf google-auth-oauthlib google-api-python-client
	@echo "✅ Python environment ready"

reset-db:
	@echo "=== nexus reset-db — WARNING: this deletes all ingested data ==="
	@read -p "Are you sure? This drops all tables (chunks, documents, context_sources, repos, gdocs). (Y/N): " confirm; \
	if [ "$$confirm" != "Y" ] && [ "$$confirm" != "y" ]; then \
		echo "Cancelled."; exit 1; \
	fi
	@USER=$$(whoami); \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS chunks CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS documents CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS context_sources CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS repos CASCADE;" 2>/dev/null || true; \
	psql -U $$USER -h localhost -d opsnexus -c "DROP TABLE IF EXISTS gdocs CASCADE;" 2>/dev/null || true
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

	# Python environment for PDF extraction and Google Docs integration
	@python3 -m venv .venv
	@.venv/bin/pip install --quiet pymupdf google-auth-oauthlib google-api-python-client
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
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		pg_isready -h localhost -p 5432 -q && break; \
		echo "   ($$i/10) not ready yet, retrying in 2s..."; \
		sleep 2; \
	done; \
	pg_isready -h localhost -p 5432 -q || { echo "❌ PostgreSQL did not restart. Check: brew services info postgresql@14"; exit 1; }

	# Ollama models (ollama pull is idempotent — skips if already downloaded)
	@echo "7. Ollama models — choose your generation model:"
	@echo "   1) llama3.2:3b  — fast, ~2.0GB  (good for limited storage/bandwidth)"
	@echo "   2) llama3.1:8b  — better answers, ~4.9GB  (recommended if you have the space)"
	@echo "   Or type any Ollama model name (e.g. llama3.3:70b, mistral:7b)."
	@read -p "   Generation model [llama3.1:8b]: " gen_choice; \
	case "$$gen_choice" in \
		1)   GEN_MODEL=llama3.2:3b ;; \
		2|"") GEN_MODEL=llama3.1:8b ;; \
		*)   GEN_MODEL=$$gen_choice ;; \
	esac; \
	echo "$$GEN_MODEL" > .ollama_gen_model; \
	echo "qwen2.5:7b" > .ollama_class_model
	@echo "   Starting downloads in the background — setup continues while models pull..."
	@chmod +x scripts/pull_model.sh
	@GEN=$$(cat .ollama_gen_model); \
	mkdir -p ~/.config/nexus/models; \
	bash scripts/pull_model.sh "$$GEN" > ~/.config/nexus/models/pull-gen.log 2>&1 & \
	bash scripts/pull_model.sh mxbai-embed-large > ~/.config/nexus/models/pull-emb.log 2>&1 & \
	bash scripts/pull_model.sh qwen2.5:7b > ~/.config/nexus/models/pull-cls.log 2>&1 & \
	true
	@echo "   ✅ Downloads running in background."
	@echo "      nexus shows live progress with ETA on first start."
	@echo "      Pull logs: ~/.config/nexus/models/pull-*.log"

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
		echo "Press Enter to skip optional sources. You can edit config.yaml manually at any time."; \
		echo ""; \
		rm -f config.yaml 2>/dev/null || true; \
		echo "sources:" > config.yaml; \
		read -p "Books / documents folder [~/Documents/knowledge-drop]: " books_path; \
		[ -z "$$books_path" ] && books_path="$$HOME/Documents/knowledge-drop"; \
		echo "  - name: books" >> config.yaml; \
		echo "    path: $$books_path" >> config.yaml; \
		echo "    extensions:" >> config.yaml; \
		echo "      - .pdf" >> config.yaml; \
		echo "      - .md" >> config.yaml; \
		echo "      - .txt" >> config.yaml; \
		read -p "Intelligence / learning notes folder (leave blank to skip): " intel_path; \
		if [ -n "$$intel_path" ]; then \
			echo "  - name: intelligence" >> config.yaml; \
			echo "    path: $$intel_path" >> config.yaml; \
			echo "    extensions:" >> config.yaml; \
			echo "      - .pdf" >> config.yaml; \
			echo "      - .md" >> config.yaml; \
			echo "      - .txt" >> config.yaml; \
		fi; \
		read -p "Work notes / ops notes folder (leave blank to skip): " ops_path; \
		if [ -n "$$ops_path" ]; then \
			echo "  - name: ops-notes" >> config.yaml; \
			echo "    path: $$ops_path" >> config.yaml; \
			echo "    extensions:" >> config.yaml; \
			echo "      - .md" >> config.yaml; \
			echo "      - .txt" >> config.yaml; \
			read -p "  Subdirectories to exclude from ops-notes (comma-separated, leave blank to skip): " ops_excludes; \
			if [ -n "$$ops_excludes" ]; then \
				echo "    exclude:" >> config.yaml; \
				echo "$$ops_excludes" | tr ',' '\n' | while IFS= read -r excl; do \
					excl=$$(echo "$$excl" | xargs); \
					[ -n "$$excl" ] && echo "      - $$excl" >> config.yaml; \
				done; \
			fi; \
		fi; \
		read -p "Runbooks folder (leave blank to skip): " runbooks_path; \
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
		GEN_MODEL=$$(cat .ollama_gen_model 2>/dev/null || echo "llama3.1:8b"); \
		CLASS_MODEL=$$(cat .ollama_class_model 2>/dev/null || echo "qwen2.5:7b"); \
		echo "ollama:" >> config.yaml; \
		echo "  baseURL: http://localhost:11434" >> config.yaml; \
		echo "  embeddingModel: mxbai-embed-large" >> config.yaml; \
		echo "  generationModel: $$GEN_MODEL" >> config.yaml; \
		echo "  classificationModel: $$CLASS_MODEL" >> config.yaml; \
		echo "personal:" >> config.yaml; \
		echo "  watchDirs:" >> config.yaml; \
		echo "    - ~/Downloads" >> config.yaml; \
		echo "    - ~/Desktop" >> config.yaml; \
		echo "  destDir: ~/Documents/PersonalDocs" >> config.yaml; \
		echo "" >> config.yaml; \
		echo "# Workspace OS — roots tell nexus about your full directory structure." >> config.yaml; \
		echo "# Leave blank to skip (you can add this section manually later)." >> config.yaml; \
		read -p "Workspace root — the top-level directory nexus should understand (leave blank to skip): " workspace_root; \
		if [ -n "$$workspace_root" ]; then \
			echo "roots:" >> config.yaml; \
			echo "  workspace: $$workspace_root" >> config.yaml; \
			echo "  repos:" >> config.yaml; \
			echo ""; \
			echo "--- Git repo roots ---"; \
			echo "Add one entry per hosting context (e.g. work, personal-github, personal-bitbucket)."; \
			echo "You can add as many as you like. Press Enter on the name prompt to finish."; \
			while true; do \
				echo ""; \
				read -p "  Repo root name (leave blank to finish): " root_name; \
				[ -z "$$root_name" ] && break; \
				read -p "  Local path for '$$root_name' repos: " root_path; \
				if [ -z "$$root_path" ]; then \
					echo "  Path is required — skipping."; \
					continue; \
				fi; \
				mkdir -p "$$(eval echo $$root_path)"; \
				read -p "  Git host substring(s) for '$$root_name', comma-separated (e.g. github.com, gitlab.com, bitbucket.org): " root_hosts_raw; \
				echo "    - name: $$root_name" >> config.yaml; \
				echo "      path: $$root_path" >> config.yaml; \
				echo "      hosts:" >> config.yaml; \
				if [ -n "$$root_hosts_raw" ]; then \
					echo "$$root_hosts_raw" | tr ',' '\n' | while IFS= read -r h; do \
						h=$$(echo "$$h" | xargs); \
						[ -n "$$h" ] && echo "        - $$h" >> config.yaml; \
					done; \
				fi; \
				echo "      watch: true" >> config.yaml; \
			done; \
		fi; \
		echo "" >> config.yaml; \
		echo "# Google Docs integration (optional — needed for nexus gdoc)" >> config.yaml; \
		read -p "Google Docs credentials.json path (leave blank to skip — you can add it later): " gdoc_creds; \
		if [ -n "$$gdoc_creds" ]; then \
			echo "gdoc:" >> config.yaml; \
			echo "  credentialsPath: $$gdoc_creds" >> config.yaml; \
		fi; \
		echo "" >> config.yaml; \
		echo "relevanceThreshold: 0.70" >> config.yaml; \
		echo "logLevel: info" >> config.yaml; \
		rm -f .ollama_gen_model .ollama_class_model; \
		echo "✅ config.yaml written."; \
	fi

	@echo ""
	@echo "✅ Setup complete!"
	@echo ""
	@echo "Next steps:"
	@echo "   make install               # build and install nexus to ~/.local/bin"
	@echo "   make ingest                # index your documents (skips unchanged files)"
	@echo "   nexus                      # start a chat session (models download in background)"
	@echo "   nexus watch                # auto-file new documents from ~/Downloads"
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
	@mkdir -p ~/.local/bin
	go build $(LDFLAGS) -o ~/.local/bin/nexus .
	@echo "✅ nexus $(VERSION) installed to ~/.local/bin"
	@~/.local/bin/nexus completion zsh > "$$(brew --prefix)/share/zsh/site-functions/_nexus"
	@echo "✅ Zsh completion installed — run: exec zsh"
	@if ! echo "$$PATH" | grep -q "$$HOME/.local/bin"; then \
		echo ""; \
		echo "⚠️  ~/.local/bin is not in your PATH."; \
		echo "   Add this to your ~/.zshrc:"; \
		echo '   export PATH="$$HOME/.local/bin:$$PATH"'; \
		echo "   Then run: source ~/.zshrc"; \
	fi

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
	@if [ -f config.yaml ]; then \
		GEN=$$(grep 'generationModel:' config.yaml | awk '{print $$2}'); \
		CLS=$$(grep 'classificationModel:' config.yaml | awk '{print $$2}'); \
		[ -n "$$GEN" ] && ollama rm "$$GEN" 2>/dev/null || true; \
		[ -n "$$CLS" ] && ollama rm "$$CLS" 2>/dev/null || true; \
	else \
		ollama rm qwen2.5:7b 2>/dev/null || true; \
		ollama rm llama3.1:8b 2>/dev/null || true; \
	fi
	@rm -f ~/.config/nexus/models/*.status ~/.config/nexus/models/pull-*.log 2>/dev/null || true

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
	echo '    <key>WorkingDirectory</key>'; \
	echo '    <string>$(CURDIR)</string>'; \
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