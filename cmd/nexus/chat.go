package nexus

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/chzyer/readline"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/gitlab"
	"github.com/iamaina/nexus/internal/live"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/iamaina/nexus/internal/summarizer"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ── ANSI colour codes ────────────────────────────────────────────────────────

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiGreen = "\033[32m"
	ansiCyan  = "\033[36m"
	ansiGray  = "\033[90m"
)

// cs holds the active colour sequences.
// All fields are empty strings when stdout is not a TTY (piped/redirected).
type cs struct{ reset, bold, dim, green, cyan, gray string }

// isTerminal returns true when stdout is an interactive terminal.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func newCS() cs {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return cs{}
	}
	return cs{ansiReset, ansiBold, ansiDim, ansiGreen, ansiCyan, ansiGray}
}

// termSize returns the current terminal dimensions, falling back to 80×24.
func termSize() (cols, rows int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd())) //nolint:gosec
	if err != nil || w <= 0 {
		w = 80
	}
	if err != nil || h <= 0 {
		h = 24
	}
	return w, h
}

// pad returns leading spaces to center text of visLen characters in cols columns.
func pad(visLen, cols int) string {
	return strings.Repeat(" ", max((cols-visLen)/2, 0))
}

func (c cs) sep(cols int) string {
	return c.gray + strings.Repeat("─", cols) + c.reset
}

// ── Spinner ──────────────────────────────────────────────────────────────────

// startSpinner prints a braille spinner on the current line.
// Returns a stop func that clears the line (must be called exactly once).
// No-op when tty is false.
func startSpinner(label string, tty bool) func() {
	if !tty {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-done:
				return
			case <-time.After(80 * time.Millisecond):
				fmt.Printf("\r  %s  %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return func() {
		close(done)
		fmt.Print("\r\033[K")
	}
}

// ── Markdown renderer ────────────────────────────────────────────────────────

// renderMarkdown renders markdown text with glamour when tty is true.
// Falls back to plain indented text on non-tty (piped) output.
func renderMarkdown(text string, tty bool, cols int) string {
	if !tty {
		// Plain indented text for piped output
		var sb strings.Builder
		for line := range strings.SplitSeq(text, "\n") {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	width := max(cols-4, 40) // leave margin
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\n"
	}
	rendered, err := r.Render(text)
	if err != nil {
		return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\n"
	}
	return rendered
}

// ── Command vars ─────────────────────────────────────────────────────────────

var (
	chatModel    string
	chatNoLive   bool
	chatSources  []string
	chatCategory string
)

// gitLabHosts returns all GitLab hostnames from the repo roots config so the
// gitlab fetcher recognises private instances (ops.gitlab.net, pre.gitlab.com…).
func gitLabHosts(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var hosts []string
	for _, repo := range cfg.Roots.Repos {
		for _, h := range repo.Hosts {
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// ── Shared completions helper ─────────────────────────────────────────────────

// chatSessionNames returns saved session names (without .md) for tab completion.
func chatSessionNames() ([]string, cobra.ShellCompDirective) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := os.ReadDir(filepath.Join(home, ".config", "nexus", "chats"))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// ── Core chat function ────────────────────────────────────────────────────────

// runChatSession is the shared implementation for both `nexus` and `nexus chat`.
// resumeSession is the session name to continue; empty starts a new session.
func runChatSession(cmd *cobra.Command, resumeSession string) error {
	ctx := cmd.Context()

	a, ok := ctx.Value(app.AppKey).(*app.Application)
	if !ok {
		return fmt.Errorf("application not initialised")
	}

	c := newCS()
	tty := c.reset != ""
	cols, _ := termSize()

	threshold := queryThreshold
	if threshold == 0 {
		threshold = a.Config.RelevanceThreshold
	}
	if threshold == 0 {
		threshold = 0.70
	}

	sum := a.Summarizer
	if chatModel != "" {
		sum = a.Summarizer.WithModel(chatModel)
	}

	startTime := time.Now()
	var history []summarizer.ChatMessage
	var sessionPath string
	var logFile *os.File

	// ── Header ───────────────────────────────────────────────────────────────
	// Clear the screen on startup so chat content begins at the top.
	// No alternate screen or scroll region — both cause terminal-specific
	// scroll conflicts. The header is printed once and scrolls away naturally
	// as the session grows; scrolling up shows previous chat turns.
	if tty {
		fmt.Print("\033[2J\033[H") // clear screen, cursor home
	}

	// redrawHeader prints a compact context line after /source changes so the
	// active filter is always visible without a pinned header.
	redrawHeader := func() {
		if !tty || len(chatSources) == 0 {
			return
		}
		fmt.Printf("  %s● source: %s%s\n\n",
			c.dim, strings.Join(chatSources, ", "), c.reset)
	}

	srcLabel := ""
	if len(chatSources) > 0 {
		srcLabel = "  ·  source: " + strings.Join(chatSources, ",")
	}
	headerVis := fmt.Sprintf("nexus %s  ·  %s  ·  threshold %.2f%s", Version, sum.Model(), threshold, srcLabel)
	fmt.Printf("\n%s%snexus %s%s  %s·%s  %s%s%s  %s·%s  threshold %.2f%s\n",
		pad(len(headerVis), cols),
		c.bold+c.cyan, Version, c.reset,
		c.dim, c.reset,
		c.bold, sum.Model(), c.reset,
		c.dim, c.reset,
		threshold,
		func() string {
			if len(chatSources) > 0 {
				return c.dim + "  ·  " + c.reset + c.bold + "source: " + strings.Join(chatSources, ",") + c.reset
			}
			return ""
		}(),
	)

	// ── Load existing session if resuming ────────────────────────────────────
	if resumeSession != "" {
		p, err := resolveChatPath(resumeSession)
		if err != nil {
			return fmt.Errorf("session not found: %w", err)
		}
		loaded, err := loadChatSession(p)
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}
		sessionPath = p
		history = loaded

		// Open file for appending immediately so Ctrl+C doesn't lose turns
		f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec
		if err != nil {
			logger.Warn(ctx, "could not open session for append", "err", err)
		} else {
			logFile = f
		}

		name := strings.TrimSuffix(filepath.Base(p), ".md")
		contVis := fmt.Sprintf("Continuing: %s  (%d exchange(s))", name, len(history)/2)
		fmt.Printf("%s%sContinuing:%s %s  %s(%d exchange(s))%s\n",
			pad(len(contVis), cols),
			c.dim, c.reset, name, c.dim, len(history)/2, c.reset)
	}

	fmt.Println(c.sep(cols))
	fmt.Println()

	// ── Main loop ─────────────────────────────────────────────────────────────
	// readline provides proper line editing: arrow keys, Ctrl+A/E/W/K, history.
	// \001...\002 wraps invisible ANSI sequences so readline measures prompt
	// width correctly when positioning the cursor during arrow-key navigation.
	rlPrompt := "❯ "
	if tty {
		rlPrompt = "\001" + c.bold + c.green + "\002❯\001" + c.reset + "\002 "
	}
	rl, rlErr := readline.NewEx(&readline.Config{
		Prompt:       rlPrompt,
		HistoryLimit: 200,
		AutoComplete: buildCompleter(a.Config),
	})
	if rlErr != nil {
		return fmt.Errorf("readline: %w", rlErr)
	}
	defer func() { _ = rl.Close() }()

	// Close readline when context is cancelled so any blocking Readline()
	// call in the goroutine below unblocks immediately.
	go func() {
		<-ctx.Done()
		_ = rl.Close()
	}()

	type scanLine struct {
		text string
		ok   bool
	}

loop:
	for {
		// One goroutine per Readline call: the prompt appears only when we are
		// ready for input, not during embedding / streaming / file listing.
		scanCh := make(chan scanLine, 1)
		go func() {
			line, err := rl.Readline()
			if err != nil {
				scanCh <- scanLine{ok: false}
				return
			}
			scanCh <- scanLine{text: line, ok: true}
		}()

		select {
		case <-ctx.Done():
			fmt.Println()
			break loop

		case sl := <-scanCh:
			if !sl.ok {
				fmt.Println()
				break loop
			}
			question := strings.TrimSpace(sl.text)
			if question == "" {
				continue
			}
			if question == "exit" || question == "quit" {
				fmt.Println()
				break loop
			}

			// ── Slash commands ───────────────────────────────────────────────
			if question == "/help" {
				printHelp(c)
				continue
			}

			// /sources — list all configured sources with indexed status.
			// Must be checked before /source to avoid prefix collision.
			if question == "/sources" {
				printSources(ctx, a, c)
				continue
			}

			if rest, ok := strings.CutPrefix(question, "/source"); ok {
				arg := strings.TrimSpace(rest)
				switch arg {
				case "", "show":
					if len(chatSources) == 0 {
						fmt.Printf("  %sActive filter: (all default sources)%s\n\n", c.dim, c.reset)
					} else {
						fmt.Printf("  %sActive filter:%s %s\n\n", c.dim, c.reset, strings.Join(chatSources, ", "))
					}
				case "clear", "off", "none":
					chatSources = nil
					fmt.Printf("  %sSource filter cleared — searching all default sources%s\n\n", c.dim, c.reset)
					redrawHeader()
				default:
					// Accept space- or comma-separated names: "/source a b" or "/source a,b"
					parts := strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' })
					var sources []string
					for _, p := range parts {
						if p = strings.TrimSpace(p); p != "" {
							sources = append(sources, p)
						}
					}
					chatSources = sources
					fmt.Printf("  %sSource filter →%s %s\n\n", c.dim, c.reset, strings.Join(chatSources, ", "))
					redrawHeader()
				}
				continue
			}

			// ── /gl command — on-demand GitLab context ───────────────────────
			if rest, ok := strings.CutPrefix(question, "/gl"); ok {
				arg := strings.TrimSpace(rest)

				var glOut live.Output
				var syntheticQ string

				switch {
				case arg == "" || arg == "todos":
					glOut = gitlab.FetchTodos(ctx, "gitlab.com")
					syntheticQ = "Based on my GitLab todos, what should I prioritise and work on next?"
				case strings.HasPrefix(arg, "todos "):
					host := strings.TrimSpace(strings.TrimPrefix(arg, "todos "))
					glOut = gitlab.FetchTodos(ctx, host)
					syntheticQ = "Based on my GitLab todos, what should I prioritise and work on next?"
				case strings.HasPrefix(arg, "items "):
					groupArg := strings.TrimSpace(strings.TrimPrefix(arg, "items "))
					host, groupPath := gitlab.ParseGroupArg(groupArg)
					glOut = gitlab.FetchGroupItems(ctx, host, groupPath)
					syntheticQ = fmt.Sprintf("What is available to pick up in %s? Summarise the open items by priority and suggest where to start.", groupPath)
				default:
					fmt.Printf("  Unknown /gl command: %q\n  Available:\n    /gl todos [host]       — your pending todos\n    /gl items <group|url>  — open items in a group\n\n", arg)
					continue
				}

				if glOut.Err != nil {
					fmt.Printf("  %s✗ %s: %v%s\n\n", c.dim, glOut.Name, glOut.Err, c.reset)
					continue
				}

				fmt.Printf("  %s⚡ %s%s\n\n", c.dim, glOut.Name, c.reset)
				// Print the raw list first — URLs are always visible here regardless
				// of what the LLM chooses to include in its summary.
				fmt.Print(renderMarkdown(glOut.Text, tty, cols))

				genStop := startSpinner("generating…", tty)
				answer, genErr := sum.StreamChat(ctx, io.Discard, history, syntheticQ, nil, []live.Output{glOut})
				genStop()
				if genErr != nil {
					if ctx.Err() != nil {
						break loop
					}
					fmt.Printf("  generate error: %v\n\n", genErr)
					continue
				}
				fmt.Print(renderMarkdown(answer, tty, cols))
				fmt.Println(c.sep(cols))
				fmt.Println()

				history = append(history,
					summarizer.ChatMessage{Role: "user", Content: syntheticQ},
					summarizer.ChatMessage{Role: "assistant", Content: answer},
				)
				continue
			}

			fmt.Println()

			// Embed + search
			stop := startSpinner("searching...", tty)

			embeddings, err := a.Embedder.Embed(ctx, []string{question})
			if err != nil {
				stop()
				if ctx.Err() != nil {
					break loop
				}
				fmt.Printf("  embed error: %v\n\n", err)
				continue
			}

			candidates, err := a.Chunks.Search(ctx, embeddings[0], 15, buildSearchFilter(a.Config, chatSources, chatCategory))
			if err != nil {
				stop()
				if ctx.Err() != nil {
					break loop
				}
				fmt.Printf("  search error: %v\n\n", err)
				continue
			}

			// If --source was given but the vector search returned nothing, the
			// source almost certainly has no indexed content. Warn and skip the
			// LLM call — otherwise the model hallucinates from training data.
			if len(candidates) == 0 && len(chatSources) > 0 {
				stop()
				fmt.Printf("  %s⚠  Source %q has no indexed content — run: nexus ingest%s\n\n",
					c.dim, strings.Join(chatSources, ", "), c.reset)
				continue
			}

			var matched []models.Result
			for _, r := range candidates {
				if r.Score >= threshold && len(strings.TrimSpace(r.Text)) > 80 {
					matched = append(matched, r)
				}
			}

			// Context expansion
			seen := make(map[string]bool)
			var results []models.Result
			for _, r := range matched {
				key := fmt.Sprintf("%d:%d", r.DocumentID, r.ChunkIndex)
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, r)
				children, childErr := a.Chunks.FetchContext(ctx, r, 5)
				if childErr != nil {
					continue
				}
				for _, child := range children {
					ck := fmt.Sprintf("%d:%d", child.DocumentID, child.ChunkIndex)
					if !seen[ck] && len(strings.TrimSpace(child.Text)) > 80 {
						seen[ck] = true
						results = append(results, child)
					}
				}
			}
			if len(results) > 12 {
				results = results[:12]
			}

			stop() // clear spinner before live sources so their output doesn't bleed onto the spinner line

			// Live context + GitLab URL auto-detection (run concurrently)
			var liveOutputs []live.Output
			glCh := make(chan []live.Output, 1)
			go func() {
				glCh <- gitlab.ExtractAndFetch(ctx, question, gitLabHosts(a.Config))
			}()

			if !chatNoLive {
				liveSources, liveErr := a.ContextSources.List(ctx)
				if liveErr == nil && len(liveSources) > 0 {
					liveOutputs = live.RunAll(ctx, liveSources, 5*time.Second)
				}
			}
			liveOutputs = append(<-glCh, liveOutputs...)

			// Source attribution
			var srcParts []string
			for _, o := range liveOutputs {
				if o.Err == nil && o.Text != "" {
					srcParts = append(srcParts, "⚡ "+o.Name)
				}
			}
			seenFiles := make(map[string]bool)
			for _, r := range results {
				if r.Score > 0 && !seenFiles[r.File] {
					seenFiles[r.File] = true
					label := strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))
					if r.Chapter != "" {
						label += " — " + r.Chapter
					}
					srcParts = append(srcParts, "📄 "+label)
				}
			}
			if len(srcParts) > 0 {
				for _, part := range srcParts {
					fmt.Printf("  %s%s%s\n", c.dim, part, c.reset)
				}
				fmt.Println()
			} else if len(candidates) > 0 {
				fmt.Printf("  %s(no context above threshold %.2f)%s\n\n",
					c.dim, threshold, c.reset)
			}

			// Generate response — stream to discard, then glamour-render the full answer
			genStop := startSpinner("generating…", tty)
			answer, err := sum.StreamChat(ctx, io.Discard, history, question, results, liveOutputs)
			genStop()
			if err != nil {
				if ctx.Err() != nil {
					break loop
				}
				fmt.Printf("  generate error: %v\n\n", err)
				continue
			}
			fmt.Print(renderMarkdown(answer, tty, cols))
			fmt.Println()

			// "Open to read more:" — unique file paths from matched results
			home, _ := os.UserHomeDir()
			seenPaths := make(map[string]bool)
			var readMore []string
			for _, r := range results {
				if !seenPaths[r.File] {
					seenPaths[r.File] = true
					p := strings.Replace(r.File, home, "~", 1)
					readMore = append(readMore, p)
				}
			}
			if len(readMore) > 0 {
				fmt.Printf("  %sOpen to read more:%s\n", c.dim, c.reset)
				for _, p := range readMore {
					fmt.Printf("  %s  %s%s\n", c.dim, p, c.reset)
				}
				fmt.Println()
			}

			fmt.Println(c.sep(cols))
			fmt.Println()

			// ── Persist this exchange immediately ────────────────────────────
			// Writing after each exchange means Ctrl+C only loses the in-progress
			// answer, not the entire session.
			logEntry := fmt.Sprintf("**You:** %s\n\n**nexus:** %s\n\n---\n\n", question, answer)

			if logFile == nil && sessionPath == "" {
				// First exchange of a new session — create the file now
				f, path, createErr := openNewChatFile(startTime, sum.Model(), question)
				if createErr != nil {
					logger.Warn(ctx, "could not create chat file", "err", createErr)
				} else {
					logFile = f
					sessionPath = path
					home, _ := os.UserHomeDir()
					fmt.Printf("  %sSaving to → %s%s\n\n",
						c.dim, strings.Replace(path, home, "~", 1), c.reset)
				}
			}

			if logFile != nil {
				_, _ = logFile.WriteString(logEntry)
			}

			history = append(history,
				summarizer.ChatMessage{Role: "user", Content: question},
				summarizer.ChatMessage{Role: "assistant", Content: answer},
			)
		}
	}

	// ── Close and report ─────────────────────────────────────────────────────
	if logFile != nil {
		name := logFile.Name()
		_ = logFile.Close()
		home, _ := os.UserHomeDir()
		verb := "saved"
		if resumeSession != "" {
			verb = "updated"
		}
		fmt.Printf("  %sSession %s → %s%s\n",
			c.dim, verb, strings.Replace(name, home, "~", 1), c.reset)
	}

	return nil
}

func init() {
	// Chat flags live on RootCmd — bare `nexus` IS the chat interface.
	RootCmd.Flags().StringVar(&chatModel, "model", "", "generation model (overrides config)")
	RootCmd.Flags().BoolVar(&chatNoLive, "no-live", false, "skip live context sources")
	RootCmd.Flags().StringSliceVar(&chatSources, "source", nil, "restrict search to one or more sources (repeatable: --source a --source b, or comma-separated: --source a,b)")
	RootCmd.Flags().StringVar(&chatCategory, "category", "", "restrict search to sources in this category (e.g. reference, work) (added v0.2.0)")
}

// ── Chat slash-command helpers ────────────────────────────────────────────────

// buildCompleter returns a readline AutoCompleter that tab-completes slash
// commands and source names. Source names are read from the config at session
// start — dynamic enough for typical use without polling the DB on every tab.
func buildCompleter(cfg *config.Config) readline.AutoCompleter {
	var srcItems []readline.PrefixCompleterInterface
	for _, s := range cfg.Sources {
		srcItems = append(srcItems, readline.PcItem(s.Name))
	}
	for _, u := range cfg.URLs {
		srcItems = append(srcItems, readline.PcItem(u.Name))
	}
	srcItems = append(srcItems,
		readline.PcItem("clear"),
		readline.PcItem("show"),
	)

	return readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/sources"),
		readline.PcItem("/source", srcItems...),
		readline.PcItem("/gl",
			readline.PcItem("todos"),
			readline.PcItem("items"),
		),
	)
}

// printHelp prints the in-chat slash command reference.
func printHelp(c cs) {
	cmds := [][2]string{
		{"/help", "print this reference"},
		{"/sources", "list all configured sources with indexed status"},
		{"/source <name>", "restrict search to a source (comma-sep for multiple: a,b)"},
		{"/source clear", "remove source filter — search all default sources"},
		{"/gl todos [host]", "fetch your GitLab todos and get prioritisation advice"},
		{"/gl items <group|url>", "list open items in a group"},
	}

	fmt.Printf("  %sSlash commands%s\n\n", c.bold, c.reset)
	for _, cmd := range cmds {
		fmt.Printf("  %s%-26s%s %s%s%s\n",
			c.cyan, cmd[0], c.reset,
			c.dim, cmd[1], c.reset)
	}
	fmt.Printf("\n  %sType 'exit' or 'quit' to end the session.%s\n\n", c.dim, c.reset)
}

// printSources lists all configured sources with their type, category, and
// indexed document count. Sources with zero documents are flagged.
func printSources(ctx context.Context, a *app.Application, c cs) {
	// Count indexed documents per source name.
	counts := make(map[string]int)
	rows, err := a.DB.Query(ctx, `SELECT source_name, COUNT(*) FROM documents GROUP BY source_name`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var n int
			if rows.Scan(&name, &n) == nil {
				counts[name] = n
			}
		}
	}

	totalSources := len(a.Config.Sources) + len(a.Config.URLs)
	indexed := 0
	for _, s := range a.Config.Sources {
		if counts[s.Name] > 0 {
			indexed++
		}
	}
	for _, u := range a.Config.URLs {
		if counts[u.Name] > 0 {
			indexed++
		}
	}

	fmt.Printf("  %sSources%s  %s(%d configured · %d indexed)%s\n\n",
		c.bold, c.reset, c.dim, totalSources, indexed, c.reset)

	printSourceRow := func(name, kind, category string, docCount int) {
		status := fmt.Sprintf("%s%s docs%s", c.dim, fmtCount(docCount), c.reset)
		if docCount == 0 {
			status = c.dim + "not indexed" + c.reset + "  — run: nexus ingest"
		}
		cat := category
		if cat == "" {
			cat = "—"
		}
		fmt.Printf("  %s%-24s%s  %s%-6s%s  %s%s\n",
			c.cyan, name, c.reset,
			c.dim, kind, c.reset,
			c.dim+"cat:"+c.reset+" "+cat+"  ",
			status)
	}

	if len(a.Config.Sources) > 0 {
		fmt.Printf("  %sFILE SOURCES%s\n", c.dim, c.reset)
		for _, s := range a.Config.Sources {
			printSourceRow(s.Name, "file", s.Category, counts[s.Name])
		}
		fmt.Println()
	}

	if len(a.Config.URLs) > 0 {
		fmt.Printf("  %sURL SOURCES%s\n", c.dim, c.reset)
		for _, u := range a.Config.URLs {
			printSourceRow(u.Name, "url", u.Category, counts[u.Name])
		}
		fmt.Println()
	}

	fmt.Printf("  %sUse /source <name> to filter your search.%s\n\n", c.dim, c.reset)
}

// fmtCount formats an integer with comma separators (e.g. 1234 → "1,234").
func fmtCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	start := len(s) % 3
	if start > 0 {
		b.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// ── File helpers ──────────────────────────────────────────────────────────────

// openNewChatFile creates the session file and writes the header.
// Returns the open file (ready for appending) and its path.
func openNewChatFile(start time.Time, model, firstQuestion string) (*os.File, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(home, ".config", "nexus", "chats")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, "", err
	}

	slug := chatSlug(firstQuestion)
	name := fmt.Sprintf("%s_%s.md", start.Format("2006-01-02_15-04"), slug)
	path := filepath.Join(dir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return nil, "", err
	}

	header := fmt.Sprintf("# %s\n**Started:** %s  **Model:** %s\n\n---\n\n",
		slug, start.Format("2006-01-02 15:04:05"), model)
	if _, err := f.WriteString(header); err != nil {
		_ = f.Close()
		return nil, "", err
	}

	return f, path, nil
}

// resolveChatPath finds the full path for a session name (with or without .md).
func resolveChatPath(arg string) (string, error) {
	if filepath.IsAbs(arg) {
		return arg, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "nexus", "chats")
	for _, candidate := range []string{
		filepath.Join(dir, arg),
		filepath.Join(dir, arg+".md"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q not found in %s", arg, dir)
}

// loadChatSession parses a saved session and returns the conversation history.
func loadChatSession(path string) ([]summarizer.ChatMessage, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}

	_, body, found := strings.Cut(string(data), "\n---\n\n")
	if !found {
		return nil, nil
	}

	const userPrefix = "**You:** "
	const nexusMark = "\n\n**nexus:** "

	var history []summarizer.ChatMessage
	for turn := range strings.SplitSeq(body, "\n\n---\n\n") {
		turn = strings.TrimSpace(turn)
		if turn == "" || !strings.HasPrefix(turn, userPrefix) {
			continue
		}
		question, answer, ok := strings.Cut(strings.TrimPrefix(turn, userPrefix), nexusMark)
		if !ok {
			continue
		}
		history = append(history,
			summarizer.ChatMessage{Role: "user", Content: question},
			summarizer.ChatMessage{Role: "assistant", Content: answer},
		)
	}
	return history, nil
}

// chatSlug converts a string into a filename-safe slug (max 50 chars).
func chatSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevHyphen := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if len(slug) > 50 {
		slug = strings.TrimRight(slug[:50], "-")
	}
	if slug == "" {
		slug = "chat"
	}
	return slug
}
