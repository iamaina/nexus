// Package logger provides structured logging using slog with JSON output.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Logger is the global logger instance initialized by Init.
var Logger *slog.Logger

// Init initializes the global logger with the specified log level and JSON formatting.
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

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})

	Logger = slog.New(handler)

	// Optional: add default attrs (e.g. app name, version)
	Logger = Logger.With(
		slog.String("app", "nexus"),
		slog.String("version", "0.1.0-dev"),
	)
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
