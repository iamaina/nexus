package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

var Logger *slog.Logger

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

// Convenience wrappers if you want (optional)
func Debug(ctx context.Context, msg string, args ...any) {
	Logger.DebugContext(ctx, msg, args...)
}

func Info(ctx context.Context, msg string, args ...any) {
	Logger.InfoContext(ctx, msg, args...)
}

func Warn(ctx context.Context, msg string, args ...any) {
	Logger.WarnContext(ctx, msg, args...)
}

func Error(ctx context.Context, msg string, args ...any) {
	Logger.ErrorContext(ctx, msg, args...)
}
