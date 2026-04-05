// Package logger provides structured logging using slog.
// When stderr is a terminal it emits coloured human-readable lines;
// when it is piped/redirected it falls back to JSON for log aggregators.
package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Logger is the global logger instance initialized by Init.
var Logger *slog.Logger

// ANSI colour codes.
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiGray   = "\033[90m"
	ansiDim    = "\033[2m"
)

// Version is set at build time via -ldflags from cmd/nexus.Version.
// It is exported so app.go can forward the value here during initialisation.
var Version = "dev"

// Init initializes the global logger. If stderr is a terminal, a coloured
// text handler is used; otherwise a JSON handler is used (suitable for Loki).
func Init(levelStr string) {
	var lvl slog.Level
	if levelStr == "" {
		if env := os.Getenv("NEXUS_LOG_LEVEL"); env != "" {
			levelStr = env
		} else {
			levelStr = "info"
		}
	}
	switch strings.ToLower(levelStr) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
		Warn(context.Background(), "Invalid log level, defaulting to info", slog.String("given", levelStr))
	}

	opts := &slog.HandlerOptions{Level: lvl}

	if isTerminal(os.Stderr) {
		Logger = slog.New(&colorHandler{level: lvl, out: os.Stderr})
	} else {
		h := slog.NewJSONHandler(os.Stderr, opts)
		Logger = slog.New(h).With(
			slog.String("app", "nexus"),
			slog.String("version", Version),
		)
	}
}

// isTerminal reports whether f is connected to an interactive terminal.
func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// colorHandler is a slog.Handler that writes coloured lines to a writer.
type colorHandler struct {
	level    slog.Level
	out      io.Writer
	mu       sync.Mutex
	preAttrs []slog.Attr
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *colorHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// timestamp (dim)
	fmt.Fprintf(&buf, "%s%s%s ", ansiDim, r.Time.Format("15:04:05"), ansiReset)

	// level badge
	switch r.Level {
	case slog.LevelDebug:
		fmt.Fprintf(&buf, "%s[DEBUG]%s ", ansiBlue, ansiReset)
	case slog.LevelInfo:
		fmt.Fprintf(&buf, "%s[INFO ]%s ", ansiGreen, ansiReset)
	case slog.LevelWarn:
		fmt.Fprintf(&buf, "%s[WARN ]%s ", ansiYellow, ansiReset)
	case slog.LevelError:
		fmt.Fprintf(&buf, "%s[ERROR]%s ", ansiRed, ansiReset)
	default:
		fmt.Fprintf(&buf, "[%s] ", r.Level)
	}

	// message
	buf.WriteString(r.Message)

	// pre-built attrs (from WithAttrs)
	for _, a := range h.preAttrs {
		fmt.Fprintf(&buf, " %s%s%s=%v", ansiGray, a.Key, ansiReset, a.Value)
	}

	// record attrs
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&buf, " %s%s%s=%v", ansiGray, a.Key, ansiReset, a.Value)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(buf.Bytes())
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, len(h.preAttrs)+len(attrs))
	copy(merged, h.preAttrs)
	copy(merged[len(h.preAttrs):], attrs)
	return &colorHandler{level: h.level, out: h.out, preAttrs: merged}
}

func (h *colorHandler) WithGroup(_ string) slog.Handler {
	return h
}

// Debug logs a debug message with context and structured arguments.
func Debug(ctx context.Context, msg string, args ...any) {
	Logger.DebugContext(ctx, msg, args...)
}

// Info logs an info message with context and structured arguments.
func Info(ctx context.Context, msg string, args ...any) {
	Logger.InfoContext(ctx, msg, args...)
}

// Warn logs a warning message with context and structured arguments.
func Warn(ctx context.Context, msg string, args ...any) {
	Logger.WarnContext(ctx, msg, args...)
}

// Error logs an error message with context and structured arguments.
func Error(ctx context.Context, msg string, args ...any) {
	Logger.ErrorContext(ctx, msg, args...)
}
