package nexus

// chat_tui.go — bubbletea TUI for the interactive chat interface.
//
// Layout (alt screen):
//
//	┌──────────────────────────────────────────────────────────────┐
//	│  nexus v0.4.0  ·  llama3.2:3b  ·  threshold 0.70  [scope]    │  <- banner
//	│──────────────────────────────────────────────────────────────│  <- separator
//	│                                                              │
//	│  [scrollable viewport — chat history]                        │
//	│                                                              │
//	│──────────────────────────────────────────────────────────────│  <- separator
//	│  [scope] ❯ _                                                 │  <- input / spinner
//	└──────────────────────────────────────────────────────────────┘
//
// Header = 2 visible lines (banner + separator).
// Footer = 4 visible lines (separator + prompt label + 2 textarea rows).
// Viewport height = terminal rows − 6  (accounts for header + footer
// and the two \n separators used in the fmt.Sprintf join in View()).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/gitlab"
	"github.com/iamaina/nexus/internal/live"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/iamaina/nexus/internal/summarizer"
	"github.com/iamaina/nexus/internal/worktrack"
	"github.com/spf13/cobra"
)

// ── TUI message types ─────────────────────────────────────────────────────────

// tuiAnswerMsg carries the result of a full search-and-generate pipeline.
type tuiAnswerMsg struct {
	question    string
	answer      string
	results     []models.Result // filtered, context-expanded results
	rawCount    int             // total vector search candidates before threshold
	liveOutputs []live.Output
	err         error
}

// tuiSlashMsg carries preformatted output from a slash command (e.g. /status, /sources).
type tuiSlashMsg string

// tuiGLMsg carries the result of a /gl fetch + LLM generation.
type tuiGLMsg struct {
	out        live.Output
	syntheticQ string
	answer     string
	err        error
}

// tuiResumeMsg carries a loaded past session for /resume.
type tuiResumeMsg struct {
	path    string
	history []summarizer.ChatMessage
	err     error
}

// pendingTrackState holds a generated work-tracking file that is awaiting
// the user's /track confirm before being written to disk.
type pendingTrackState struct {
	slug    string
	content string
}

// tuiTrackMsg carries the result of an async /track generate call.
// If pending is non-nil the file has NOT been written yet — the user must
// /track confirm to save it. updatedMsgLen is set when an update was written
// so the handler can advance the session's tracking cursor.
type tuiTrackMsg struct {
	out           string
	err           error
	pending       *pendingTrackState
	updatedMsgLen int
}

// tuiTickMsg drives the footer spinner animation.
type tuiTickMsg time.Time

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	tuiHeaderLines = 2 // banner line + separator line
	tuiFooterLines = 4 // separator line + prompt line + 2 textarea content lines
	// tuiMargin is the total rows consumed by header and footer.
	// The two \n separators in View()'s fmt.Sprintf are NOT extra rows —
	// they terminate the last line of each component, contributing 0 extra rows.
	tuiMargin = tuiHeaderLines + tuiFooterLines // = 6
)

var tuiSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sgrMouseRe matches raw SGR mouse sequences that leak through as KeyMsg during
// fast scrolling when mouse tracking is active. Format: [<btn;col;row[Mm]
var sgrMouseRe = regexp.MustCompile(`^\[<\d+;\d+;\d+[Mm]$`)

// ── Session metadata ─────────────────────────────────────────────────────────

// sessionMeta is stored as a JSON sidecar next to each chat session file.
// It persists the work-tracking linkage across session resumes.
type sessionMeta struct {
	TrackingFile   string `json:"tracking_file,omitempty"`
	TrackingMsgLen int    `json:"tracking_msg_len,omitempty"`
}

func metaPath(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".md") + ".meta.json"
}

func loadSessionMeta(sessionPath string) sessionMeta {
	if sessionPath == "" {
		return sessionMeta{}
	}
	data, err := os.ReadFile(metaPath(sessionPath)) //nolint:gosec // path from resolved session
	if err != nil {
		return sessionMeta{}
	}
	var m sessionMeta
	_ = json.Unmarshal(data, &m)
	return m
}

func saveSessionMeta(sessionPath string, m sessionMeta) {
	if sessionPath == "" {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(metaPath(sessionPath), data, 0o600) //nolint:gosec // path from resolved session
}

// ── Track preview helpers ─────────────────────────────────────────────────────

// formatTrackPreview renders a short human-readable summary of a pending
// work-tracking file generation, prompting the user to confirm or discard.
func formatTrackPreview(p *pendingTrackState, c cs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %sPreview:%s %s.md\n\n", c.bold, c.reset, p.slug)
	if title := trackExtractH1(p.content); title != "" {
		fmt.Fprintf(&b, "  %sTitle:%s   %s\n", c.dim, c.reset, title)
	}
	if next := trackExtractNextAction(p.content); next != "" {
		fmt.Fprintf(&b, "  %sNext:%s    %s\n", c.dim, c.reset, next)
	}
	if lines := trackExtractJournal(p.content); len(lines) > 0 {
		fmt.Fprintf(&b, "\n  %sJournal:%s\n", c.dim, c.reset)
		for _, l := range lines {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	fmt.Fprintf(&b, "\n  %s/track confirm%s to save  %s·  /track discard%s to cancel\n\n",
		c.cyan, c.reset, c.dim, c.reset)
	return b.String()
}

func trackExtractH1(content string) string {
	for _, line := range strings.SplitAfter(content, "\n") {
		if trimmed := strings.TrimRight(line, "\n"); strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
	}
	return ""
}

func trackExtractNextAction(content string) string {
	lines := strings.Split(content, "\n")
	inSection := false
	for _, line := range lines {
		if line == "## Next action" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, ">") {
			return strings.TrimSpace(strings.TrimPrefix(line, ">"))
		}
		if inSection && strings.HasPrefix(line, "##") {
			break
		}
	}
	return ""
}

func trackExtractJournal(content string) []string {
	lines := strings.Split(content, "\n")
	inSection, inEntry := false, false
	var result []string
	for _, line := range lines {
		if line == "## Where I left off" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "### ") {
			inEntry = true
			continue
		}
		if inEntry && strings.HasPrefix(line, "- ") {
			result = append(result, line)
			if len(result) >= 4 {
				break
			}
		}
		if inEntry && strings.HasPrefix(line, "##") {
			break
		}
	}
	return result
}

// ── Model ─────────────────────────────────────────────────────────────────────

// chatTUI is the bubbletea model for the interactive chat interface.
// All fields use value or pointer types; the model is copied on every Update call
// as required by the bubbletea framework.
type chatTUI struct {
	// dependencies (pointers — safe to copy)
	ctx context.Context
	a   *app.Application
	c   cs
	sum *summarizer.OllamaSummarizer

	threshold float64

	// terminal layout
	cols  int
	rows  int
	ready bool

	// UI components
	vp    viewport.Model
	input textarea.Model

	// accumulated viewport content (pre-rendered ANSI strings)
	content string

	// spinner
	busy      bool
	spinFrame int

	// glamour renderer — created once before the alt screen opens so that
	// the OSC 11 background-colour query (used by WithAutoStyle) completes
	// before bubbletea takes over stdin. Re-querying inside the alt screen
	// causes the raw OSC response to leak into the viewport output.
	renderer *glamour.TermRenderer

	// session state
	history     []summarizer.ChatMessage
	sessionPath string
	logFile     *os.File
	startTime   time.Time

	// input history — up/down arrow navigates previously sent questions
	inputHistory []string
	historyIdx   int // -1 = not navigating history

	// work-tracking linkage
	pendingTrack   *pendingTrackState // non-nil when a /track create is awaiting /track confirm
	trackingFile   string             // absolute path of the linked work-tracking file for this session
	trackingMsgLen int                // len(history) at the time the file was last synced

	// pre-fetched at program start to avoid a DB query on the event loop
	initialStatus statusInfo
}

// ── Constructor ───────────────────────────────────────────────────────────────

func newChatTUI(
	ctx context.Context,
	a *app.Application,
	c cs,
	sum *summarizer.OllamaSummarizer,
	threshold float64,
) chatTUI {
	ta := textarea.New()
	ta.Placeholder = "Ask anything… (Enter to send · Alt+Enter for newline)"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetHeight(2)
	ta.SetWidth(76)
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	// Move newline insertion to Shift/Alt+Enter so plain Enter submits.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter"))
	// Focus before storing: Focus() has a pointer receiver and sets ta.focus=true
	// on the addressable local. The returned blink cmd is started from Init().
	_ = ta.Focus()

	return chatTUI{
		ctx:        ctx,
		a:          a,
		c:          c,
		sum:        sum,
		threshold:  threshold,
		input:      ta,
		startTime:  time.Now(),
		historyIdx: -1,
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m chatTUI) Init() tea.Cmd {
	return textarea.Blink
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Window resize / first initialisation ──────────────────────────────────
	case tea.WindowSizeMsg:
		m.cols = msg.Width
		m.rows = msg.Height
		m.input.SetWidth(max(msg.Width-4, 20))
		if !m.ready {
			m.vp = viewport.New(msg.Width, msg.Height-tuiMargin)
			m.vp.YPosition = tuiHeaderLines
			m.content = m.buildInitialContent()
			m.vp.SetContent(m.content)
			m.vp.GotoBottom()
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - tuiMargin
		}
		return m, nil

	// ── Mouse wheel — scroll the viewport ────────────────────────────────────
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.vp.ScrollUp(3)
			case tea.MouseButtonWheelDown:
				m.vp.ScrollDown(3)
			}
		}
		return m, nil

	// ── Keyboard ──────────────────────────────────────────────────────────────
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m.quit()

		// Scroll viewport.
		case "pgup", "ctrl+b":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case "pgdown", "ctrl+f":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd

		// Input history — navigate when cursor is on the first/last textarea line.
		case "up":
			if !m.busy && m.input.Line() == 0 && len(m.inputHistory) > 0 {
				if m.historyIdx < 0 {
					m.historyIdx = len(m.inputHistory) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.input.SetValue(m.inputHistory[m.historyIdx])
				m.input.CursorEnd()
				return m, nil
			}
			if !m.busy {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		case "down":
			if !m.busy && m.historyIdx >= 0 && m.input.Line() == m.input.LineCount()-1 {
				if m.historyIdx >= len(m.inputHistory)-1 {
					m.historyIdx = -1
					m.input.Reset()
				} else {
					m.historyIdx++
					m.input.SetValue(m.inputHistory[m.historyIdx])
					m.input.CursorEnd()
				}
				return m, nil
			}
			if !m.busy {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}

		case "tab":
			if !m.busy {
				current := m.input.Value()
				completed, changed, suggestions := tuiComplete(current, m.a.Config)
				if changed {
					m.input.SetValue(completed)
					m.input.CursorEnd()
				}
				// When multiple options remain, show them in the viewport as a hint.
				// Mirrors the readline behaviour of printing completions below the prompt.
				if len(suggestions) > 1 {
					var b strings.Builder
					fmt.Fprintf(&b, "  %s", m.c.dim)
					for i, s := range suggestions {
						if i > 0 {
							b.WriteString("   ")
						}
						b.WriteString(s)
					}
					b.WriteString(m.c.reset + "\n\n")
					m = m.appendContent(b.String())
				}
			}
			return m, nil

		case "enter":
			if m.busy {
				return m, nil
			}
			question := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			m.historyIdx = -1
			if question == "" {
				return m, nil
			}
			// Record in input history (skip exact duplicate of most-recent entry).
			if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != question {
				m.inputHistory = append(m.inputHistory, question)
			}
			if question == "exit" || question == "quit" {
				return m.quit()
			}
			return m.handleQuestion(question)

		default:
			if !m.busy {
				// Drop raw SGR mouse sequences that leak through as KeyMsg
				// during fast scrolling with mouse tracking active.
				if sgrMouseRe.MatchString(msg.String()) {
					return m, nil
				}
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}

	// ── Spinner tick ──────────────────────────────────────────────────────────
	case tuiTickMsg:
		if m.busy {
			m.spinFrame++
			return m, m.tickCmd()
		}
		return m, nil

	// ── Search + generate complete ────────────────────────────────────────────
	case tuiAnswerMsg:
		m.busy = false
		focusCmd := m.input.Focus()

		if msg.err != nil {
			if m.ctx.Err() != nil {
				return m.quit()
			}
			m = m.appendContent("  " + m.c.dim + "✗  " + msg.err.Error() + m.c.reset + "\n\n")
			m = m.appendContent(m.c.sep(m.cols) + "\n\n")
			return m, focusCmd
		}

		// Source attribution
		var srcParts []string
		for _, o := range msg.liveOutputs {
			if o.Err == nil && o.Text != "" {
				srcParts = append(srcParts, "⚡ "+o.Name)
			}
		}
		seenFiles := make(map[string]bool)
		for _, r := range msg.results {
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
				m = m.appendContent("  " + m.c.dim + part + m.c.reset + "\n")
			}
			m = m.appendContent("\n")
		} else if msg.rawCount > 0 {
			m = m.appendContent(fmt.Sprintf("  %s(no context above threshold %.2f)%s\n\n",
				m.c.dim, m.threshold, m.c.reset))
		}

		if msg.answer == "" {
			noCtx := "I don't have relevant information about this in your knowledge base."
			if msg.rawCount > 0 {
				noCtx += fmt.Sprintf("\n(best match scored below threshold %.2f — try lowering with --threshold)", m.threshold)
			}
			m = m.appendContent(m.renderMD(noCtx) + "\n")
		} else {
			m = m.appendContent(m.renderMD(msg.answer) + "\n")
		}

		// "Open to read more" — unique source paths
		home, _ := os.UserHomeDir()
		seenPaths := make(map[string]bool)
		var readMore []string
		for _, r := range msg.results {
			if !seenPaths[r.File] {
				seenPaths[r.File] = true
				readMore = append(readMore, strings.Replace(r.File, home, "~", 1))
			}
		}
		if len(readMore) > 0 {
			m = m.appendContent("  " + m.c.dim + "Open to read more:" + m.c.reset + "\n")
			for _, p := range readMore {
				m = m.appendContent("  " + m.c.dim + "  " + p + m.c.reset + "\n")
			}
			m = m.appendContent("\n")
		}

		m = m.appendContent(m.c.sep(m.cols) + "\n\n")
		m = m.persistExchange(msg.question, msg.answer)
		m.history = append(m.history,
			summarizer.ChatMessage{Role: "user", Content: msg.question},
			summarizer.ChatMessage{Role: "assistant", Content: msg.answer},
		)
		return m, focusCmd

	// ── Slash command output ──────────────────────────────────────────────────
	case tuiSlashMsg:
		m.busy = false
		focusCmd := m.input.Focus()
		m = m.appendContent(string(msg))
		return m, focusCmd

	// ── /gl answer ────────────────────────────────────────────────────────────
	case tuiGLMsg:
		m.busy = false
		focusCmd := m.input.Focus()
		if msg.err != nil {
			m = m.appendContent("  " + m.c.dim + "✗  " + msg.out.Name + ": " + msg.err.Error() + m.c.reset + "\n\n")
			m = m.appendContent(m.c.sep(m.cols) + "\n\n")
			return m, focusCmd
		}
		// ⚡ label rendered here now that fetch is fully async.
		m = m.appendContent("  " + m.c.dim + "⚡ " + msg.out.Name + m.c.reset + "\n\n")
		m = m.appendContent(m.renderMD(msg.out.Text))
		m = m.appendContent(m.renderMD(msg.answer) + "\n")
		m = m.appendContent(m.c.sep(m.cols) + "\n\n")
		m = m.persistExchange(msg.syntheticQ, msg.answer)
		m.history = append(m.history,
			summarizer.ChatMessage{Role: "user", Content: msg.syntheticQ},
			summarizer.ChatMessage{Role: "assistant", Content: msg.answer},
		)
		return m, focusCmd

	// ── /resume session loaded ────────────────────────────────────────────────
	case tuiResumeMsg:
		m.busy = false
		focusCmd := m.input.Focus()
		if msg.err != nil {
			m = m.appendContent("  " + m.c.dim + "✗  " + msg.err.Error() + m.c.reset + "\n\n")
			return m, focusCmd
		}
		if m.logFile != nil {
			_ = m.logFile.Close()
			m.logFile = nil
		}
		f, err := os.OpenFile(msg.path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec
		if err != nil {
			logger.Warn(m.ctx, "could not open resumed session for append", "err", err)
		} else {
			m.logFile = f
		}
		m.sessionPath = msg.path
		m.history = msg.history
		// Restore work-tracking linkage from the sidecar meta file.
		meta := loadSessionMeta(msg.path)
		m.trackingFile = meta.TrackingFile
		m.trackingMsgLen = meta.TrackingMsgLen
		name := strings.TrimSuffix(filepath.Base(msg.path), ".md")
		m = m.appendContent(fmt.Sprintf("  %sSwitched to:%s %s  %s(%d exchange(s))%s\n\n",
			m.c.dim, m.c.reset, name, m.c.dim, len(msg.history)/2, m.c.reset))
		m = m.appendContent(m.renderResumeHistory(msg.history))
		return m, focusCmd

	case tuiTrackMsg:
		m.busy = false
		focusCmd := m.input.Focus()
		if msg.err != nil {
			m = m.appendContent(fmt.Sprintf("  %s✗ track: %v%s\n\n", m.c.dim, msg.err, m.c.reset))
			return m, focusCmd
		}
		if msg.pending != nil {
			// Before showing the create preview, check whether a related file
			// already exists (different slug, same issue ID, or lost linkage).
			if existing := findRelatedTrackFile(msg.pending.slug); existing != "" {
				m.trackingFile = existing
				home, _ := os.UserHomeDir()
				short := strings.Replace(existing, home, "~", 1)
				m = m.appendContent(fmt.Sprintf("  %sFound existing file:%s %s\n  %sUpdating instead of creating a duplicate%s\n\n",
					m.c.dim, m.c.reset, short, m.c.dim, m.c.reset))
				return m.runTrackUpdate()
			}
			m.pendingTrack = msg.pending
			m = m.appendContent(formatTrackPreview(msg.pending, m.c))
			return m, focusCmd
		}
		// Update was written — advance the tracking cursor and write session ID.
		if msg.updatedMsgLen > 0 {
			m.trackingMsgLen = msg.updatedMsgLen
			if m.sessionPath != "" && m.trackingFile != "" {
				sessionName := strings.TrimSuffix(filepath.Base(m.sessionPath), ".md")
				_ = worktrack.UpdateResumeSession(m.trackingFile, sessionName)
			}
			saveSessionMeta(m.sessionPath, sessionMeta{
				TrackingFile:   m.trackingFile,
				TrackingMsgLen: m.trackingMsgLen,
			})
		}
		m = m.appendContent(msg.out)
		return m, focusCmd
	}

	// Pass unhandled messages to the viewport (e.g. smooth scroll)
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// runTrackUpdate fires the async generate-update pipeline for m.trackingFile.
// Callers must ensure m.trackingFile is non-empty and history is newer before calling.
func (m chatTUI) runTrackUpdate() (tea.Model, tea.Cmd) {
	fpath, fileContent, err := worktrack.ReadFilePath(filepath.Base(m.trackingFile))
	if err != nil {
		// trackingFile is an absolute path; fall back to reading directly.
		data, readErr := os.ReadFile(m.trackingFile) //nolint:gosec // path from resolved session meta
		if readErr != nil {
			m = m.appendContent(fmt.Sprintf("  %s✗ %v%s\n\n", m.c.dim, err, m.c.reset))
			return m, nil
		}
		fpath = m.trackingFile
		fileContent = string(data)
	}
	m.busy = true
	capturedHistory := append([]summarizer.ChatMessage(nil), m.history...)
	capturedSum := m.sum
	capturedCtx := m.ctx
	capturedC := m.c
	capturedPath := fpath
	capturedContent := fileContent
	capturedMsgLen := len(m.history)
	return m, tea.Batch(func() tea.Msg {
		nextAction, journalLines, genErr := capturedSum.GenerateWorkTrackUpdate(capturedCtx, capturedHistory, capturedContent)
		if genErr != nil {
			return tuiTrackMsg{err: genErr}
		}
		if writeErr := worktrack.UpdateByPath(capturedPath, nextAction, journalLines); writeErr != nil {
			return tuiTrackMsg{err: writeErr}
		}
		home, _ := os.UserHomeDir()
		short := strings.Replace(capturedPath, home, "~", 1)
		return tuiTrackMsg{
			out:           fmt.Sprintf("  %s✓ Updated:%s %s\n\n", capturedC.bold, capturedC.reset, short),
			updatedMsgLen: capturedMsgLen,
		}
	}, m.tickCmd())
}

// quit closes the session file and returns tea.Quit.
func (m chatTUI) quit() (tea.Model, tea.Cmd) {
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	return m, tea.Quit
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m chatTUI) View() string {
	if !m.ready {
		return "  Initializing…\n"
	}
	return fmt.Sprintf("%s\n%s\n%s", m.headerView(), m.vp.View(), m.footerView())
}

func (m chatTUI) headerView() string {
	scope := m.scopeLabel()
	visLen := len(fmt.Sprintf("nexus %s  ·  %s  ·  threshold %.2f  [%s]",
		Version, m.sum.Model(), m.threshold, scope))
	padStr := pad(visLen, m.cols)
	banner := fmt.Sprintf("%s%snexus %s%s  %s·%s  %s%s%s  %s·%s  threshold %.2f  %s[%s]%s",
		padStr,
		m.c.bold+m.c.cyan, Version, m.c.reset,
		m.c.dim, m.c.reset,
		m.c.bold, m.sum.Model(), m.c.reset,
		m.c.dim, m.c.reset,
		m.threshold,
		m.c.dim, scope, m.c.reset,
	)
	return banner + "\n" + m.c.sep(m.cols)
}

func (m chatTUI) footerView() string {
	sep := m.c.sep(m.cols)
	if m.busy {
		frame := tuiSpinFrames[m.spinFrame%len(tuiSpinFrames)]
		// Pad to tuiFooterLines rows so the viewport height stays constant.
		return sep + "\n  " + m.c.dim + frame + "  generating…" + m.c.reset + "\n\n"
	}
	scope := m.scopeLabel()

	// Left side: scope + optional stale-tracking indicator + prompt glyph.
	trackHint := ""
	trackHintVis := 0
	if m.trackingFile != "" && len(m.history) > m.trackingMsgLen {
		trackHint = " " + m.c.cyan + "[/track update]" + m.c.reset
		trackHintVis = 17 // len(" [/track update]")
	}
	left := "  " + m.c.dim + "[" + scope + "]" + m.c.reset + trackHint + " " + m.c.bold + m.c.green + "❯" + m.c.reset
	// Right side: scroll hint, right-aligned.
	// Visible widths are calculated without ANSI codes.
	leftVis := 4 + len(scope) + 1 + trackHintVis + 3 // "  [scope] ❯ "
	hint := "PgUp/PgDown to scroll  "
	gap := m.cols - leftVis - len(hint)
	if gap < 1 {
		gap = 1
	}
	promptLine := left + strings.Repeat(" ", gap) + m.c.dim + hint + m.c.reset

	return sep + "\n" + promptLine + "\n" + m.input.View()
}

func (m chatTUI) scopeLabel() string {
	var parts []string
	if len(chatSources) > 0 {
		parts = append(parts, strings.Join(chatSources, ","))
	}
	if chatCategory != "" {
		parts = append(parts, chatCategory)
	}
	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, " · ")
}

// ── Content helpers ───────────────────────────────────────────────────────────

// appendContent appends s to the accumulated viewport content, pushes it into
// the viewport model, and scrolls to the bottom. Returns the updated model.
func (m chatTUI) appendContent(s string) chatTUI {
	m.content += s
	m.vp.SetContent(m.content)
	m.vp.GotoBottom()
	return m
}

// renderMD renders markdown for display in the TUI viewport. It uses the
// pre-built renderer stored on the model (created before the alt screen opens)
// so no OSC 11 background-colour query is issued during rendering.
func (m chatTUI) renderMD(text string) string {
	text = normalizeProseNewlines(text)
	if m.renderer == nil {
		// Fallback: plain indented text (renderer creation failed).
		return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\n"
	}
	rendered, err := m.renderer.Render(text)
	if err != nil {
		return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\n"
	}
	return rendered
}

// buildInitialContent generates the content shown in the viewport at startup:
// the status banner, the separator, and (if resuming) the last 3 exchanges.
// Called once inside the first WindowSizeMsg handler after m.cols is set.
func (m chatTUI) buildInitialContent() string {
	var b strings.Builder
	b.WriteString(renderStatusBanner(m.initialStatus, m.c))
	if m.sessionPath != "" && len(m.history) > 0 {
		name := strings.TrimSuffix(filepath.Base(m.sessionPath), ".md")
		fmt.Fprintf(&b, "  %sContinuing:%s %s  %s(%d exchange(s))%s\n\n",
			m.c.dim, m.c.reset, name, m.c.dim, len(m.history)/2, m.c.reset)
		b.WriteString(m.renderResumeHistory(m.history))
	}
	b.WriteString(m.c.sep(m.cols) + "\n\n")
	return b.String()
}

// renderResumeHistory returns the last 3 exchanges as a pre-rendered string
// suitable for the viewport, using the model's own renderer and column width.
func (m chatTUI) renderResumeHistory(history []summarizer.ChatMessage) string {
	var b strings.Builder
	pairs := len(history) / 2
	start := max(pairs-3, 0)
	if start >= pairs {
		return ""
	}
	if start > 0 {
		fmt.Fprintf(&b, "  %s… %d earlier exchange(s) — see session file%s\n\n", m.c.dim, start, m.c.reset)
	}
	for i := start; i < pairs; i++ {
		q := history[i*2].Content
		a := history[i*2+1].Content
		displayQ := q
		if len(displayQ) > 80 {
			displayQ = displayQ[:77] + "…"
		}
		fmt.Fprintf(&b, "  %s❯ %s%s\n", m.c.dim, displayQ, m.c.reset)
		b.WriteString(m.renderMD(a))
		b.WriteString(m.c.sep(m.cols) + "\n\n")
	}
	return b.String()
}

func (m chatTUI) tickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

// ── Question dispatcher ───────────────────────────────────────────────────────

// handleQuestion is called when the user presses Enter with a non-empty question.
// It dispatches slash commands (synchronous or async) and regular questions
// (always async — embed + search + generate).
func (m chatTUI) handleQuestion(question string) (tea.Model, tea.Cmd) {
	// Echo the question so the user sees it scroll into history.
	displayQ := question
	if len(displayQ) > 80 {
		displayQ = displayQ[:77] + "…"
	}
	m = m.appendContent("  " + m.c.dim + "❯ " + displayQ + m.c.reset + "\n\n")

	// ── Synchronous slash commands ────────────────────────────────────────────

	if question == "/help" {
		m = m.appendContent(renderHelp(m.c))
		return m, nil
	}

	if question == "/sessions" {
		m = m.appendContent(renderSessions(m.c, m.cols))
		return m, nil
	}

	// /source and /category modify package-level filter vars.
	// The header and footer prompt re-render automatically on each frame.
	if question == "/sources" {
		// DB query — run in a Cmd goroutine to keep the event loop responsive.
		m.busy = true
		capturedCtx := m.ctx
		capturedA := m.a
		capturedC := m.c
		return m, tea.Batch(func() tea.Msg {
			return tuiSlashMsg(renderSources(capturedCtx, capturedA, capturedC))
		}, m.tickCmd())
	}

	if question == "/status" {
		m.busy = true
		capturedCtx := m.ctx
		capturedA := m.a
		capturedC := m.c
		return m, tea.Batch(func() tea.Msg {
			return tuiSlashMsg(renderStatusFull(gatherStatus(capturedCtx, capturedA), capturedC))
		}, m.tickCmd())
	}

	if question == "/catchup" {
		m.busy = true
		capturedCtx := m.ctx
		capturedA := m.a
		capturedC := m.c
		return m, tea.Batch(func() tea.Msg {
			return tuiSlashMsg(renderCatchup(capturedCtx, capturedA, capturedC))
		}, m.tickCmd())
	}

	if rest, ok := strings.CutPrefix(question, "/track"); ok {
		arg := strings.TrimSpace(rest)

		switch arg {
		case "confirm":
			if m.pendingTrack == nil {
				m = m.appendContent("  " + m.c.dim + "Nothing pending — run /track create first" + m.c.reset + "\n\n")
				return m, nil
			}
			dest, err := worktrack.WriteNew(m.pendingTrack.slug, m.pendingTrack.content+"\n")
			if err != nil {
				m = m.appendContent(fmt.Sprintf("  %s✗ %v%s\n\n", m.c.dim, err, m.c.reset))
				m.pendingTrack = nil
				return m, nil
			}
			m.trackingFile = dest
			m.trackingMsgLen = len(m.history)
			m.pendingTrack = nil
			if m.sessionPath != "" {
				sessionName := strings.TrimSuffix(filepath.Base(m.sessionPath), ".md")
				_ = worktrack.UpdateResumeSession(dest, sessionName)
			}
			home, _ := os.UserHomeDir()
			short := strings.Replace(dest, home, "~", 1)
			m = m.appendContent(fmt.Sprintf("  %s✓ Saved:%s %s\n  %sLinked to this session%s\n\n",
				m.c.bold, m.c.reset, short, m.c.dim, m.c.reset))
			saveSessionMeta(m.sessionPath, sessionMeta{
				TrackingFile:   dest,
				TrackingMsgLen: m.trackingMsgLen,
			})
			return m, nil

		case "discard":
			if m.pendingTrack == nil {
				m = m.appendContent("  " + m.c.dim + "Nothing to discard" + m.c.reset + "\n\n")
				return m, nil
			}
			m.pendingTrack = nil
			m = m.appendContent("  " + m.c.dim + "Discarded" + m.c.reset + "\n\n")
			return m, nil

		case "create":
			if len(m.history) == 0 {
				m = m.appendContent("  " + m.c.dim + "✗ No conversation yet — ask something first" + m.c.reset + "\n\n")
				return m, nil
			}
			// Already linked to a file — redirect to update.
			if m.trackingFile != "" {
				if len(m.history) <= m.trackingMsgLen {
					home, _ := os.UserHomeDir()
					m = m.appendContent(fmt.Sprintf("  %s✓ Already up to date:%s %s\n\n",
						m.c.dim, m.c.reset, strings.Replace(m.trackingFile, home, "~", 1)))
					return m, nil
				}
				return m.runTrackUpdate()
			}
			tmpl, err := worktrack.ReadTemplate()
			if err != nil {
				m = m.appendContent(fmt.Sprintf("  %s✗ %v%s\n\n", m.c.dim, err, m.c.reset))
				return m, nil
			}
			m.busy = true
			capturedHistory := append([]summarizer.ChatMessage(nil), m.history...)
			capturedSum := m.sum
			capturedCtx := m.ctx
			capturedTmpl := tmpl
			return m, tea.Batch(func() tea.Msg {
				slug, content, genErr := capturedSum.GenerateWorkTrack(capturedCtx, capturedHistory, capturedTmpl)
				if genErr != nil {
					return tuiTrackMsg{err: genErr}
				}
				return tuiTrackMsg{pending: &pendingTrackState{slug: slug, content: content}}
			}, m.tickCmd())

		case "update":
			if len(m.history) == 0 {
				m = m.appendContent("  " + m.c.dim + "✗ No conversation yet — ask something first" + m.c.reset + "\n\n")
				return m, nil
			}
			if m.trackingFile == "" {
				m = m.appendContent("  " + m.c.dim + "✗ No linked file — run /track create first" + m.c.reset + "\n\n")
				return m, nil
			}
			if len(m.history) <= m.trackingMsgLen {
				home, _ := os.UserHomeDir()
				m = m.appendContent(fmt.Sprintf("  %s✓ Already up to date:%s %s\n\n",
					m.c.dim, m.c.reset, strings.Replace(m.trackingFile, home, "~", 1)))
				return m, nil
			}
			return m.runTrackUpdate()

		default:
			out := tuiHandleTrack(arg, m.c)
			m = m.appendContent(out)
			return m, nil
		}
	}

	if rest, ok := strings.CutPrefix(question, "/source"); ok {
		arg := strings.TrimSpace(rest)
		switch arg {
		case "", "show":
			if len(chatSources) == 0 {
				m = m.appendContent("  " + m.c.dim + "Active filter: (all default sources)" + m.c.reset + "\n\n")
			} else {
				m = m.appendContent("  " + m.c.dim + "Active filter: " + m.c.reset + strings.Join(chatSources, ", ") + "\n\n")
			}
		case "clear", "off", "none":
			chatSources = nil
			m = m.appendContent("  " + m.c.dim + "Source filter cleared — searching all default sources" + m.c.reset + "\n\n")
		default:
			parts := strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' })
			var sources []string
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" {
					sources = append(sources, p)
				}
			}
			chatSources = sources
			m = m.appendContent("  " + m.c.dim + "Source filter → " + m.c.reset + strings.Join(chatSources, ", ") + "\n\n")
		}
		return m, nil
	}

	if rest, ok := strings.CutPrefix(question, "/category"); ok {
		arg := strings.TrimSpace(rest)
		switch arg {
		case "", "show":
			if chatCategory == "" {
				m = m.appendContent("  " + m.c.dim + "Active category: (all)" + m.c.reset + "\n\n")
			} else {
				m = m.appendContent("  " + m.c.dim + "Active category: " + m.c.reset + chatCategory + "\n\n")
			}
		case "clear", "off", "none":
			chatCategory = ""
			m = m.appendContent("  " + m.c.dim + "Category filter cleared — searching all categories" + m.c.reset + "\n\n")
		default:
			chatCategory = arg
			m = m.appendContent("  " + m.c.dim + "Category filter → " + m.c.reset + chatCategory + "\n\n")
		}
		return m, nil
	}

	// ── /resume — async session load ──────────────────────────────────────────
	if rest, ok := strings.CutPrefix(question, "/resume"); ok {
		arg := strings.TrimSpace(rest)
		if arg == "" {
			m = m.appendContent("  " + m.c.dim + "Usage: /resume <session-name>" + m.c.reset + "\n\n")
			return m, nil
		}
		m.busy = true
		return m, tea.Batch(func() tea.Msg {
			p, err := resolveChatPath(arg)
			if err != nil {
				return tuiResumeMsg{err: fmt.Errorf("session not found: %w", err)}
			}
			history, err := loadChatSession(p)
			if err != nil {
				return tuiResumeMsg{err: fmt.Errorf("loading session: %w", err)}
			}
			return tuiResumeMsg{path: p, history: history}
		}, m.tickCmd())
	}

	// ── /gl — GitLab context + generation ────────────────────────────────────
	if rest, ok := strings.CutPrefix(question, "/gl"); ok {
		arg := strings.TrimSpace(rest)

		// Resolve help/unknown cases synchronously — no network needed.
		switch {
		case arg == "items":
			m = m.appendContent("  Usage: /gl items <group-path|url>\n  e.g.  /gl items gitlab-com/gl-infra/delivery\n        /gl items https://gitlab.com/groups/gitlab-com/gl-infra/-/issues\n\n")
			return m, nil
		case arg == "" || arg == "todos" ||
			strings.HasPrefix(arg, "todos ") ||
			strings.HasPrefix(arg, "items ") ||
			strings.HasPrefix(arg, "read "):
			// known commands — fall through to the async path below
		default:
			m = m.appendContent(fmt.Sprintf("  Unknown /gl command: %q\n  Available:\n    /gl todos [host]       — your pending todos\n    /gl items <group|url>  — open items in a group\n    /gl read <url>         — read a specific issue, MR, or work item\n\n", arg))
			return m, nil
		}

		// All remaining /gl commands require a network call. Move everything —
		// fetch AND generation — into a single goroutine so the event loop
		// never blocks. The ⚡ label is rendered in the tuiGLMsg handler once
		// both the fetch and the LLM answer are ready.
		m.busy = true
		capturedArg := arg
		capturedHistory := append([]summarizer.ChatMessage(nil), m.history...)
		capturedSum := m.sum
		capturedCtx := m.ctx
		capturedCfg := m.a.Config
		return m, tea.Batch(func() tea.Msg {
			var glOut live.Output
			var syntheticQ string
			switch {
			case capturedArg == "" || capturedArg == "todos":
				glOut = gitlab.FetchTodos(capturedCtx, "gitlab.com")
				syntheticQ = "Based on my GitLab todos, what should I prioritise and work on next?"
			case strings.HasPrefix(capturedArg, "todos "):
				host := strings.TrimSpace(strings.TrimPrefix(capturedArg, "todos "))
				glOut = gitlab.FetchTodos(capturedCtx, host)
				syntheticQ = "Based on my GitLab todos, what should I prioritise and work on next?"
			case strings.HasPrefix(capturedArg, "items "):
				groupArg := strings.TrimSpace(strings.TrimPrefix(capturedArg, "items "))
				host, groupPath := gitlab.ParseGroupArg(groupArg)
				glOut = gitlab.FetchGroupItems(capturedCtx, host, groupPath)
				syntheticQ = fmt.Sprintf("What is available to pick up in %s? Summarise the open items by priority and suggest where to start.", groupPath)
			case strings.HasPrefix(capturedArg, "read "):
				urlArg := strings.TrimSpace(strings.TrimPrefix(capturedArg, "read "))
				outputs := gitlab.ExtractAndFetch(capturedCtx, urlArg, gitLabHosts(capturedCfg))
				if len(outputs) == 0 {
					return tuiGLMsg{
						out: live.Output{Name: urlArg},
						err: fmt.Errorf("could not fetch %q: must be a full GitLab issue / MR / work item URL", urlArg),
					}
				}
				glOut = outputs[0]
				syntheticQ = "Summarise this issue or MR in plain language: what is the goal, what work is required, and what context from the description is most useful for getting started?"
			}
			if glOut.Err != nil {
				return tuiGLMsg{out: glOut, syntheticQ: syntheticQ, err: glOut.Err}
			}
			answer, err := capturedSum.StreamChat(capturedCtx, io.Discard, capturedHistory, syntheticQ, nil, []live.Output{glOut})
			return tuiGLMsg{out: glOut, syntheticQ: syntheticQ, answer: answer, err: err}
		}, m.tickCmd())
	}

	// ── Normal question — full search + generate pipeline ─────────────────────
	m.busy = true
	capturedSources := append([]string(nil), chatSources...)
	capturedCategory := chatCategory
	capturedHistory := append([]summarizer.ChatMessage(nil), m.history...)
	capturedQ := question

	return m, tea.Batch(func() tea.Msg {
		return m.searchAndGenerate(capturedQ, capturedSources, capturedCategory, capturedHistory)
	}, m.tickCmd())
}

// ── Search + generate pipeline ────────────────────────────────────────────────

// searchAndGenerate runs the full embed → search → live context → generate
// pipeline and returns the result as a tuiAnswerMsg. It is always called in a
// tea.Cmd goroutine so it does not block the bubbletea event loop.
func (m chatTUI) searchAndGenerate(
	question string,
	sources []string,
	category string,
	history []summarizer.ChatMessage,
) tuiAnswerMsg {
	embeddings, err := m.a.Embedder.Embed(m.ctx, []string{question})
	if err != nil {
		return tuiAnswerMsg{question: question, err: err}
	}

	candidates, err := m.a.Chunks.Search(m.ctx, embeddings[0], 15, buildSearchFilter(m.a.Config, sources, category))
	if err != nil {
		return tuiAnswerMsg{question: question, err: err}
	}

	if len(candidates) == 0 && len(sources) > 0 {
		return tuiAnswerMsg{
			question: question,
			err:      fmt.Errorf("source %q has no indexed content — run: nexus ingest", strings.Join(sources, ", ")),
		}
	}

	var matched []models.Result
	for _, r := range candidates {
		if r.Score >= m.threshold && len(strings.TrimSpace(r.Text)) > 80 {
			matched = append(matched, r)
		}
	}

	// Context expansion: fetch surrounding chunks for each matched result.
	seen := make(map[string]bool)
	var results []models.Result
	for _, r := range matched {
		key := fmt.Sprintf("%d:%d", r.DocumentID, r.ChunkIndex)
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, r)
		children, childErr := m.a.Chunks.FetchContext(m.ctx, r, 5)
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

	// Live context (concurrent with GitLab URL auto-detection)
	var liveOutputs []live.Output
	glCh := make(chan []live.Output, 1)
	go func() {
		glCh <- gitlab.ExtractAndFetch(m.ctx, question, gitLabHosts(m.a.Config))
	}()

	if !chatNoLive {
		liveSources, liveErr := m.a.ContextSources.List(m.ctx)
		if liveErr == nil && len(liveSources) > 0 {
			liveOutputs = live.RunAll(m.ctx, live.FilterByQuery(question, liveSources), 5*time.Second)
		}
	}
	liveOutputs = append(<-glCh, liveOutputs...)

	// Skip the LLM only when there is truly no context of any kind.
	// If conversation history is present (e.g. a prior /gl read), the LLM can
	// answer follow-up questions from that history even with no new search hits.
	if len(results) == 0 && len(liveOutputs) == 0 && len(history) == 0 {
		return tuiAnswerMsg{
			question:    question,
			answer:      "",
			results:     nil,
			rawCount:    len(candidates),
			liveOutputs: nil,
		}
	}

	answer, err := m.sum.StreamChat(m.ctx, io.Discard, history, question, results, liveOutputs)
	return tuiAnswerMsg{
		question:    question,
		answer:      answer,
		results:     results,
		rawCount:    len(candidates),
		liveOutputs: liveOutputs,
		err:         err,
	}
}

// ── Session persistence ───────────────────────────────────────────────────────

// persistExchange writes a completed exchange to the session file, creating the
// file if this is the first exchange of a new session. Returns the updated model.
func (m chatTUI) persistExchange(question, answer string) chatTUI {
	logEntry := fmt.Sprintf("**You:** %s\n\n**nexus:** %s\n\n---\n\n", question, answer)

	if m.logFile == nil && m.sessionPath == "" {
		f, path, err := openNewChatFile(m.startTime, m.sum.Model(), question)
		if err != nil {
			logger.Warn(m.ctx, "could not create chat file", "err", err)
			return m
		}
		m.logFile = f
		m.sessionPath = path
		home, _ := os.UserHomeDir()
		m = m.appendContent("  " + m.c.dim + "Saving to → " + strings.Replace(path, home, "~", 1) + m.c.reset + "\n\n")
	}

	if m.logFile != nil {
		_, _ = m.logFile.WriteString(logEntry)
	}
	return m
}

// ── Tab completion ────────────────────────────────────────────────────────────

// tuiComplete attempts to expand `input` by prefix-matching against known
// slash commands and sub-command arguments.
//
// Returns:
//   - completed  — the (possibly expanded) input string
//   - changed    — true when the input was extended
//   - suggestions — all candidates that matched (len > 1 means ambiguous)
//
// Completion rules (bash-style):
// tuiHandleTrack handles /track subcommands, returning preformatted output
// for the viewport. Kept separate from handleTrackCommand to avoid tty/cols args.
func tuiHandleTrack(arg string, c cs) string {
	var b strings.Builder
	switch {
	case arg == "" || arg == "list":
		names, err := worktrack.List()
		if err != nil {
			fmt.Fprintf(&b, "  %s✗ %v%s\n\n", c.dim, err, c.reset)
			return b.String()
		}
		fmt.Fprintf(&b, "  %sWork-tracking files:%s\n\n", c.bold, c.reset)
		for _, n := range names {
			fmt.Fprintf(&b, "  %s%s%s\n", c.dim, n, c.reset)
		}
		b.WriteString("\n")

	case strings.HasPrefix(arg, "new "):
		rest := strings.TrimSpace(strings.TrimPrefix(arg, "new "))
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			fmt.Fprintf(&b, "  %sUsage: /track new <slug> <title>%s\n  e.g. /track new gl-21998-upgrade upgrade praefect pre to 22.04\n\n", c.dim, c.reset)
			return b.String()
		}
		slug, title := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		dest, err := worktrack.CreateFromTemplate(slug, title)
		if err != nil {
			fmt.Fprintf(&b, "  %s✗ %v%s\n\n", c.dim, err, c.reset)
			return b.String()
		}
		home, _ := os.UserHomeDir()
		short := strings.Replace(dest, home, "~", 1)
		fmt.Fprintf(&b, "  %s✓ Created:%s %s\n\n", c.bold, c.reset, short)

	default:
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			fmt.Fprintf(&b, "  %sUsage: /track <file-fragment> <note>%s\n  e.g. /track refactor-logrus reviewed the approach, will post tomorrow\n\n", c.dim, c.reset)
			return b.String()
		}
		fileFragment, note := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		dest, err := worktrack.AppendNote(fileFragment, note)
		if err != nil {
			fmt.Fprintf(&b, "  %s✗ %v%s\n\n", c.dim, err, c.reset)
			return b.String()
		}
		home, _ := os.UserHomeDir()
		short := strings.Replace(dest, home, "~", 1)
		fmt.Fprintf(&b, "  %s✓ Note appended to:%s %s\n\n", c.bold, c.reset, short)
	}
	return b.String()
}

// - One match   → expand to the full candidate; suggestions has 1 element.
// - Many matches → expand to longest common prefix; suggestions lists them all.
// - No match    → input unchanged; suggestions is nil.
func tuiComplete(input string, cfg *config.Config) (completed string, changed bool, suggestions []string) {
	if !strings.HasPrefix(input, "/") {
		return input, false, nil
	}

	type subSpec struct {
		prefix     string
		candidates func() []string
	}
	subs := []subSpec{
		{"/source ", func() []string { return tuiSourceNames(cfg) }},
		{"/category ", func() []string { return tuiCategoryNames(cfg) }},
		{"/resume ", func() []string { names, _ := chatSessionNames(); return names }},
		{"/gl ", func() []string { return []string{"todos", "items ", "read "} }},
		{"/track ", func() []string {
			names, _ := worktrack.List()
			return append([]string{"list", "create", "update", "confirm", "discard", "new "}, names...)
		}},
	}
	for _, s := range subs {
		if after, ok := strings.CutPrefix(input, s.prefix); ok {
			return tuiCompleteSuffix(s.prefix, after, s.candidates())
		}
	}

	tops := []string{
		"/help", "/status", "/catchup", "/sources", "/sessions",
		"/source ", "/category ", "/resume ", "/gl ", "/track",
	}
	return tuiCompleteSuffix("", input, tops)
}

// tuiCompleteSuffix matches `partial` against `candidates` and returns the
// expanded string, whether it changed, and the full list of matches.
func tuiCompleteSuffix(prefix, partial string, candidates []string) (string, bool, []string) {
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, partial) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return prefix + partial, false, nil
	case 1:
		return prefix + matches[0], matches[0] != partial, matches
	default:
		lcp := tuiLongestCommonPrefix(matches)
		return prefix + lcp, lcp != partial, matches
	}
}

// tuiLongestCommonPrefix returns the longest string that is a prefix of every
// element in strs.
func tuiLongestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func tuiSourceNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Sources)+len(cfg.URLs)+2)
	for _, s := range cfg.Sources {
		names = append(names, s.Name)
	}
	for _, u := range cfg.URLs {
		names = append(names, u.Name)
	}
	return append(names, "clear", "show")
}

func tuiCategoryNames(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var cats []string
	for _, s := range cfg.Sources {
		if s.Category != "" && !seen[s.Category] {
			seen[s.Category] = true
			cats = append(cats, s.Category)
		}
	}
	for _, u := range cfg.URLs {
		if u.Category != "" && !seen[u.Category] {
			seen[u.Category] = true
			cats = append(cats, u.Category)
		}
	}
	return append(cats, "clear", "show")
}

// ── Entry point ───────────────────────────────────────────────────────────────

// runChatTUI starts the bubbletea TUI. Called from root.go when stdout is a TTY.
func runChatTUI(cmd *cobra.Command, resumeSession string) error {
	ctx := cmd.Context()
	a, ok := ctx.Value(app.AppKey).(*app.Application)
	if !ok {
		return fmt.Errorf("application not initialised")
	}

	c := newCS()
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

	m := newChatTUI(ctx, a, c, sum, threshold)

	// Pre-fetch status before entering the alt screen so gatherStatus's DB query
	// does not block the bubbletea event loop during the first WindowSizeMsg.
	m.initialStatus = gatherStatus(ctx, a)

	// Build the glamour renderer NOW — before tea.NewProgram opens the alt screen.
	// glamour.WithAutoStyle calls termenv.HasDarkBackground which sends an OSC 11
	// query to detect the terminal background colour. Inside the bubbletea alt
	// screen the terminal's response arrives as raw bytes that leak into the
	// viewport. Calling it here (normal terminal, blocking read) lets the
	// terminal answer cleanly, and the renderer is then reused for all
	// in-TUI markdown rendering without issuing any further OSC queries.
	cols, _ := termSize()
	if gr, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(max(cols-4, 40)),
	); err == nil {
		m.renderer = gr
	}

	// Load resume session synchronously — it's just a file read, fast enough
	// to do before the program starts. The history is rendered in the viewport
	// during the first WindowSizeMsg (when cols are known).
	if resumeSession != "" {
		if p, err := resolveChatPath(resumeSession); err == nil {
			if history, err := loadChatSession(p); err == nil {
				m.history = history
				m.sessionPath = p
				if f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600); err == nil { //nolint:gosec
					m.logFile = f
				}
			}
		}
	}

	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := prog.Run()

	// Print session path after the alt screen closes.
	if tm, ok := finalModel.(chatTUI); ok {
		if tm.logFile != nil {
			// File wasn't closed in Update (unexpected exit) — close it now.
			_ = tm.logFile.Close()
		}
		if tm.sessionPath != "" {
			home, _ := os.UserHomeDir()
			verb := "saved"
			if resumeSession != "" {
				verb = "updated"
			}
			fmt.Printf("  Session %s → %s\n", verb, strings.Replace(tm.sessionPath, home, "~", 1))
		}
	}

	return err
}
